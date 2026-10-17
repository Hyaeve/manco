package telegram

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/hyaeve/manco/internal/model"
)

const (
	maxGlobalSources  = 3
	maxPerSourceItems = 3
	maxDirectItems    = 5
	titleLimit        = 24
	summaryLimit      = 160
)

var htmlTagPattern = regexp.MustCompile(`(?s)<[^>]+>`)

func (s *Service) handleText(ctx context.Context, chatID int64, text string) {
	if text == "" {
		return
	}
	chat := strconv.FormatInt(chatID, 10)
	command, argument := splitCommand(text)
	switch command {
	case "/start", "开始", "start":
		s.sendMessage(ctx, chat, helpText(), nil)
	case "/subscriptions", "订阅", "subscriptions":
		s.replySubscriptions(ctx, chat)
	case "/check", "检查全部", "check":
		go s.replyCheckAll(ctx, chat)
	case "/sources", "内容源", "sources":
		s.replySources(ctx, chat)
	case "/s", "搜索", "s":
		s.replySearch(ctx, chatID, argument, "")
	default:
		if query, sourceName, ok := parseSearchText(text); ok {
			s.replySearch(ctx, chatID, query, sourceName)
			return
		}
		if action, title, ok := parseSubscriptionAction(text); ok {
			s.replySubscriptionAction(ctx, chat, action, title)
			return
		}
		s.sendMessage(ctx, chat, "未识别的指令。\n\n"+helpText(), nil)
	}
}

// splitCommand normalizes a leading slash command and returns its argument.
func splitCommand(text string) (string, string) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", ""
	}
	head := fields[0]
	if strings.HasPrefix(head, "/") {
		if index := strings.Index(head, "@"); index > 0 {
			head = head[:index]
		}
		head = strings.ToLower(head)
	}
	return head, strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
}

// parseSearchText recognises "搜索漫画 xxx" and "<内容源>搜索 xxx".
func parseSearchText(text string) (query, sourceName string, ok bool) {
	trimmed := strings.TrimSpace(text)
	for _, prefix := range []string{"搜索漫画", "搜索资源", "搜索"} {
		if strings.HasPrefix(trimmed, prefix) {
			query = strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			return query, "", query != ""
		}
	}
	if index := strings.Index(trimmed, "搜索"); index > 0 {
		sourceName = strings.TrimSpace(trimmed[:index])
		query = strings.TrimSpace(trimmed[index+len("搜索"):])
		if sourceName != "" && query != "" {
			return query, sourceName, true
		}
	}
	return "", "", false
}

var actionAliases = map[string]string{
	"检查": "check",
	"归档": "archive",
	"补全": "fill",
	"刮削": "scrape",
	"启用": "enable",
	"禁用": "disable",
	"全量": "full",
	"删除": "delete",
}

// parseSubscriptionAction recognises "检查 作品名" style commands.
func parseSubscriptionAction(text string) (action, title string, ok bool) {
	for prefix, actionName := range actionAliases {
		for _, separator := range []string{" ", "　"} {
			if strings.HasPrefix(text, prefix+separator) {
				title = strings.TrimSpace(strings.TrimPrefix(text, prefix+separator))
				if title != "" {
					return actionName, title, true
				}
			}
		}
	}
	return "", "", false
}

func helpText() string {
	return strings.Join([]string{
		"<b>Manco 指令</b>",
		"/start 开始",
		"/subscriptions 查看订阅清单",
		"/check 检查全部订阅更新",
		"/sources 查看已添加内容源",
		"/s 关键词 全局搜索",
		"搜索漫画 关键词",
		"包子搜索 关键词",
		"检查 作品名",
		"补全 作品名",
		"刮削 作品名",
		"启用 作品名",
		"禁用 作品名",
		"全量 作品名",
		"归档 作品名",
		"删除 作品名",
	}, "\n")
}

func (s *Service) replySources(ctx context.Context, chat string) {
	items, err := s.app.BotSources(ctx)
	if err != nil {
		s.sendMessage(ctx, chat, "读取内容源失败："+err.Error(), nil)
		return
	}
	if len(items) == 0 {
		s.sendMessage(ctx, chat, "还没有添加内容源。", nil)
		return
	}
	lines := []string{"<b>内容源</b>"}
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("· %s（%s）", item.Name, kindLabel(item.Kind)))
	}
	s.sendMessage(ctx, chat, strings.Join(lines, "\n"), nil)
}

func (s *Service) replySubscriptions(ctx context.Context, chat string) {
	items, err := s.app.BotSubscriptions(ctx)
	if err != nil {
		s.sendMessage(ctx, chat, "读取订阅清单失败："+err.Error(), nil)
		return
	}
	if len(items) == 0 {
		s.sendMessage(ctx, chat, "订阅清单是空的。", nil)
		return
	}
	s.sendMessage(ctx, chat, renderSubscriptionList(items), nil)
}

type subscriptionCheckOutcome struct {
	Subscription BotSubscription
	Result       BotCheckResult
	Err          error
}

// replyCheckAll 检查全部订阅卡片，并复用同一条消息展示汇总结果。
func (s *Service) replyCheckAll(ctx context.Context, chat string) {
	items, err := s.app.BotSubscriptions(ctx)
	if err != nil {
		s.sendMessage(ctx, chat, "读取订阅清单失败："+err.Error(), nil)
		return
	}
	if len(items) == 0 {
		s.sendMessage(ctx, chat, "订阅清单是空的。", nil)
		return
	}
	messageID, err := s.callMethod(ctx, "sendMessage", map[string]any{
		"chat_id":    chat,
		"text":       fmt.Sprintf("<b>正在检查订阅更新</b>\n共 %d 个订阅，请稍候。", len(items)),
		"parse_mode": "HTML",
	})
	if err != nil {
		s.logger.Printf("telegram: send check progress: %v", err)
	}
	checkCtx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	outcomes := make([]subscriptionCheckOutcome, len(items))
	work := make(chan int)
	workers := 4
	if len(items) < workers {
		workers = len(items)
	}
	var wg sync.WaitGroup
	wg.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wg.Done()
			for index := range work {
				item := items[index]
				result, checkErr := s.app.BotCheck(checkCtx, item.ID)
				outcomes[index] = subscriptionCheckOutcome{Subscription: item, Result: result, Err: checkErr}
			}
		}()
	}
	for index := range items {
		work <- index
	}
	close(work)
	wg.Wait()

	report := renderCheckReport(outcomes, checkCtx.Err())
	if messageID != 0 {
		if err := s.editMessage(ctx, chat, messageID, false, report, nil); err == nil {
			return
		} else {
			s.logger.Printf("telegram: edit check report: %v", err)
		}
	}
	s.sendMessage(ctx, chat, report, nil)
}

func (s *Service) replySubscriptionAction(ctx context.Context, chat, action, title string) {
	items, err := s.app.BotSubscriptions(ctx)
	if err != nil {
		s.sendMessage(ctx, chat, "读取订阅清单失败："+err.Error(), nil)
		return
	}
	var matches []BotSubscription
	for _, item := range items {
		if strings.Contains(item.Title, title) {
			matches = append(matches, item)
		}
	}
	switch len(matches) {
	case 0:
		s.sendMessage(ctx, chat, fmt.Sprintf("订阅清单里没有找到「%s」。", title), nil)
		return
	case 1:
		s.runAction(ctx, chat, action, matches[0])
		return
	default:
		lines := []string{fmt.Sprintf("「%s」匹配到 %d 个订阅，请用更完整的名称：", title, len(matches))}
		for _, item := range matches {
			lines = append(lines, "· "+item.Title)
		}
		s.sendMessage(ctx, chat, strings.Join(lines, "\n"), nil)
	}
}

func (s *Service) runAction(ctx context.Context, chat, action string, subscription BotSubscription) {
	switch action {
	case "check":
		result, err := s.app.BotCheck(ctx, subscription.ID)
		if err != nil {
			s.sendMessage(ctx, chat, "检查失败："+err.Error(), nil)
			return
		}
		message := fmt.Sprintf("《%s》检查完成：新章节 %d，已入队 %d", result.Title, result.New, result.Queued)
		if result.Retried > 0 {
			message += fmt.Sprintf("，重试失败章节 %d", result.Retried)
		}
		s.sendMessage(ctx, chat, message+"。", nil)
	case "archive":
		if err := s.app.BotArchive(ctx, subscription.ID); err != nil {
			s.sendMessage(ctx, chat, "归档失败："+err.Error(), nil)
			return
		}
		s.sendMessage(ctx, chat, fmt.Sprintf("《%s》已归档。", subscription.Title), nil)
	case "fill":
		queued, skipped, err := s.app.BotFill(ctx, subscription.ID)
		if err != nil {
			s.sendMessage(ctx, chat, "补全失败："+err.Error(), nil)
			return
		}
		if queued == 0 {
			s.sendMessage(ctx, chat, fmt.Sprintf("《%s》已经完整，无需补全。", subscription.Title), nil)
			return
		}
		s.sendMessage(ctx, chat, fmt.Sprintf("《%s》缺 %d 章，已全部入队（已存在 %d 章）。", subscription.Title, queued, skipped), nil)
	case "enable", "disable":
		enabled := action == "enable"
		if err := s.app.BotSetSubscriptionEnabled(ctx, subscription.ID, enabled); err != nil {
			s.sendMessage(ctx, chat, "设置失败："+err.Error(), nil)
			return
		}
		s.sendMessage(ctx, chat, fmt.Sprintf("《%s》已%s。", subscription.Title, map[bool]string{true: "启用", false: "禁用"}[enabled]), nil)
	case "scrape":
		updated, err := s.app.BotScrape(ctx, subscription.ID)
		if err != nil {
			s.sendMessage(ctx, chat, "刮削失败："+err.Error(), nil)
			return
		}
		if !updated {
			s.sendMessage(ctx, chat, fmt.Sprintf("《%s》还没有可刮削的本地文件。", subscription.Title), nil)
			return
		}
		s.sendMessage(ctx, chat, fmt.Sprintf("《%s》已重新刮削元数据，ComicInfo.xml 与 series.json 已覆盖。", subscription.Title), nil)
	case "full":
		queued, err := s.app.BotFullDownload(ctx, subscription.ID)
		if err != nil {
			s.sendMessage(ctx, chat, "全量下载失败："+err.Error(), nil)
			return
		}
		s.sendMessage(ctx, chat, fmt.Sprintf("《%s》已清理旧记录与文件，重新入队 %d 章。", subscription.Title, queued), nil)
	case "delete":
		if err := s.app.BotDelete(ctx, subscription.ID); err != nil {
			s.sendMessage(ctx, chat, "删除失败："+err.Error(), nil)
			return
		}
		s.sendMessage(ctx, chat, fmt.Sprintf("《%s》订阅已删除。", subscription.Title), nil)
	}
}

// replySearch runs a global or per-source search and returns the aggregated
// result list with inline buttons.
func (s *Service) replySearch(ctx context.Context, chatID int64, query, sourceName string) {
	chat := strconv.FormatInt(chatID, 10)
	query = strings.TrimSpace(query)
	if query == "" {
		s.sendMessage(ctx, chat, "请给出搜索关键词，例如：搜索漫画 凡人修仙传", nil)
		return
	}
	sources, err := s.app.BotSources(ctx)
	if err != nil {
		s.sendMessage(ctx, chat, "读取内容源失败："+err.Error(), nil)
		return
	}
	var entries []resultEntry
	if sourceName != "" {
		source, ok := matchSource(sources, sourceName)
		if !ok {
			s.sendMessage(ctx, chat, fmt.Sprintf("没有找到内容源「%s」。", sourceName), nil)
			return
		}
		items, err := s.app.BotSearch(ctx, source.ID, query, maxDirectItems)
		if err != nil {
			s.sendMessage(ctx, chat, fmt.Sprintf("%s 搜索失败：%s", source.Name, err.Error()), nil)
			return
		}
		for _, item := range items {
			entries = append(entries, newResultEntry(source, item))
		}
	} else {
		// Global search keeps resource-extension order and takes the top hits
		// from the first few content sources.
		used := 0
		resultCh := make(chan []resultEntry, len(sources))
		for _, source := range sources {
			if used >= maxGlobalSources {
				break
			}
			used++
			source := source
			go func() {
				items, err := s.app.BotSearch(ctx, source.ID, query, maxPerSourceItems)
				if err != nil {
					resultCh <- nil
					return
				}
				out := make([]resultEntry, 0, len(items))
				for _, item := range items {
					out = append(out, newResultEntry(source, item))
				}
				resultCh <- out
			}()
		}
		for index := 0; index < used; index++ {
			entries = append(entries, (<-resultCh)...)
		}
	}
	if len(entries) == 0 {
		s.sendMessage(ctx, chat, fmt.Sprintf("没有搜到「%s」的结果。", query), nil)
		return
	}
	// 记下搜索结果消息的 message_id：订阅成功后要把这条消息删掉。
	text := renderResults(query, entries)
	messageID, err := s.callMethod(ctx, "sendMessage", map[string]any{
		"chat_id":      chat,
		"text":         text,
		"parse_mode":   "HTML",
		"reply_markup": resultKeyboard(entries),
	})
	if err != nil {
		s.logger.Printf("telegram: send search results: %v", err)
	}
	s.mu.Lock()
	s.sessions[chatID] = &session{results: entries, resultsText: text, resultsMessageID: messageID}
	s.mu.Unlock()
}

func (s *Service) handleCallback(ctx context.Context, query *CallbackQuery) {
	if query == nil || query.Message == nil || query.Message.Chat == nil {
		return
	}
	chatID := query.Message.Chat.ID
	chat := strconv.FormatInt(chatID, 10)
	s.answerCallbackAsync(query.ID)
	if !s.allowed(chatID) {
		return
	}
	data := strings.TrimSpace(query.Data)
	switch {
	case strings.HasPrefix(data, "open:"):
		index, err := strconv.Atoi(strings.TrimPrefix(data, "open:"))
		if err != nil {
			return
		}
		// 打开详情时先收起搜索结果，聊天里同时只留一个消息框。
		s.mu.RLock()
		resultsMessageID := int64(0)
		if current := s.sessions[chatID]; current != nil {
			resultsMessageID = current.resultsMessageID
		}
		s.mu.RUnlock()
		s.deleteMessageAsync(chat, resultsMessageID)
		s.mu.Lock()
		if current := s.sessions[chatID]; current != nil {
			current.resultsMessageID = 0
		}
		s.mu.Unlock()
		s.replyResultDetail(ctx, chatID, chat, index)
	case strings.HasPrefix(data, "subscribe:"):
		index, err := strconv.Atoi(strings.TrimPrefix(data, "subscribe:"))
		if err != nil {
			return
		}
		s.replySubscribe(ctx, chatID, chat, index, query.Message.MessageID)
	case data == "back":
		// 收起详情，并把搜索结果重新发一份，供用户重新选择。
		s.deleteMessageAsync(chat, query.Message.MessageID)
		results := s.sessionResults(chatID)
		if len(results) == 0 {
			s.sendMessage(ctx, chat, "没有可返回的搜索结果。", nil)
			return
		}
		resultsText := s.sessionResultsText(chatID)
		if resultsText == "" {
			resultsText = renderResults("", results)
		}
		messageID, err := s.callMethod(ctx, "sendMessage", map[string]any{
			"chat_id":      chat,
			"text":         resultsText,
			"parse_mode":   "HTML",
			"reply_markup": resultKeyboard(results),
		})
		if err != nil {
			s.logger.Printf("telegram: resend search results: %v", err)
			return
		}
		s.mu.Lock()
		if current := s.sessions[chatID]; current != nil {
			current.resultsMessageID = messageID
			current.detailMessageID = 0
			current.detailPhoto = false
			current.detailIndex = 0
		}
		s.mu.Unlock()
	}
}

func (s *Service) sessionResults(chatID int64) []resultEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	current := s.sessions[chatID]
	if current == nil {
		return nil
	}
	return append([]resultEntry(nil), current.results...)
}

func (s *Service) sessionResultsText(chatID int64) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	current := s.sessions[chatID]
	if current == nil {
		return ""
	}
	return current.resultsText
}

func (s *Service) replyResultDetail(ctx context.Context, chatID int64, chat string, index int) {
	results := s.sessionResults(chatID)
	if index < 0 || index >= len(results) {
		s.sendMessage(ctx, chat, "结果已过期，请重新搜索。", nil)
		return
	}
	entry := results[index]
	// 先用搜索结果里的缓存数据立刻建框，再异步拉详情并原位更新。
	caption := renderResultDetail(entry, true)
	keyboard := resultDetailKeyboard(index)
	messageID, photo := s.sendResultDetail(ctx, chat, caption, entry.Cover, keyboard)
	if messageID == 0 {
		s.sendMessage(ctx, chat, "作品详情发送失败，请稍后重试。", nil)
		return
	}
	s.mu.Lock()
	if current := s.sessions[chatID]; current != nil {
		current.detailMessageID = messageID
		current.detailPhoto = photo
		current.detailIndex = index
	}
	s.mu.Unlock()
	if resultNeedsDetail(entry) {
		go s.enrichResultDetail(ctx, chatID, chat, index, messageID, photo)
	}
}

func resultDetailKeyboard(index int) any {
	return map[string]any{
		"inline_keyboard": [][]map[string]string{
			{
				{"text": "返回", "callback_data": "back"},
				{"text": "订阅", "callback_data": fmt.Sprintf("subscribe:%d", index)},
			},
		},
	}
}

func resultNeedsDetail(entry resultEntry) bool {
	return strings.TrimSpace(entry.Author) == "" ||
		strings.TrimSpace(entry.Latest) == "" ||
		strings.TrimSpace(entry.Summary) == "" ||
		entry.ChapterCount <= 0
}

func renderResultDetail(entry resultEntry, loading bool) string {
	author := fallback(entry.Author, "未知")
	latest := fallback(entry.Latest, "未知")
	if loading {
		if strings.TrimSpace(entry.Author) == "" {
			author = "获取中…"
		}
		if strings.TrimSpace(entry.Latest) == "" {
			latest = "获取中…"
		}
	}
	lines := []string{
		fmt.Sprintf("<b>%s</b>（%s）", html.EscapeString(entry.Title), html.EscapeString(entry.SourceName)),
		"作者：" + html.EscapeString(author),
		"最新章节：" + html.EscapeString(latest),
		"章节数量：" + chapterCountLabel(entry.ChapterCount, loading),
	}
	caption := strings.Join(lines, "\n")
	if summary := cleanComicSummary(entry.Title, entry.Summary); summary != "" {
		caption += "\n\n" + html.EscapeString(truncate(summary, summaryLimit))
	}
	return caption
}

func chapterCountLabel(count int, loading bool) string {
	if count > 0 {
		return fmt.Sprintf("共 %d 章", count)
	}
	if loading {
		return "获取中…"
	}
	return "未知"
}

func (s *Service) sendResultDetail(ctx context.Context, chat, caption, cover string, keyboard any) (int64, bool) {
	if cover = strings.TrimSpace(cover); cover != "" {
		photoCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		id, err := s.callMethod(photoCtx, "sendPhoto", map[string]any{
			"chat_id":      chat,
			"photo":        cover,
			"caption":      caption,
			"parse_mode":   "HTML",
			"reply_markup": keyboard,
		})
		cancel()
		if err == nil && id != 0 {
			return id, true
		}
		s.logger.Printf("telegram: send detail photo: %v", err)
	}
	id, err := s.callMethod(ctx, "sendMessage", map[string]any{
		"chat_id":      chat,
		"text":         caption,
		"parse_mode":   "HTML",
		"reply_markup": keyboard,
	})
	if err != nil {
		s.logger.Printf("telegram: send detail text: %v", err)
		return 0, false
	}
	return id, false
}

func (s *Service) enrichResultDetail(ctx context.Context, chatID int64, chat string, index int, messageID int64, photo bool) {
	results := s.sessionResults(chatID)
	if index < 0 || index >= len(results) {
		return
	}
	entry := results[index]
	detailCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	detail, err := s.app.BotComicDetail(detailCtx, entry.SourceID, entry.ComicID)
	if err != nil {
		s.logger.Printf("telegram: comic detail %s/%s: %v", entry.SourceID, entry.ComicID, err)
		return
	}
	if strings.TrimSpace(detail.Author) != "" {
		entry.Author = detail.Author
	}
	if strings.TrimSpace(detail.LatestChapterTitle) != "" {
		entry.Latest = detail.LatestChapterTitle
	}
	if strings.TrimSpace(detail.Cover) != "" && strings.TrimSpace(entry.Cover) == "" {
		entry.Cover = detail.Cover
	}
	if detail.ChapterCount > 0 {
		entry.ChapterCount = detail.ChapterCount
	}
	if summary := cleanComicSummary(entry.Title, detail.Description); summary != "" {
		entry.Summary = summary
	}

	s.mu.Lock()
	current := s.sessions[chatID]
	if current == nil || current.detailMessageID != messageID || current.detailIndex != index {
		s.mu.Unlock()
		return
	}
	current.results[index] = entry
	s.mu.Unlock()

	caption := renderResultDetail(entry, false)
	editCtx, editCancel := context.WithTimeout(ctx, 10*time.Second)
	defer editCancel()
	if err := s.editMessage(editCtx, chat, messageID, photo, caption, resultDetailKeyboard(index)); err != nil {
		s.logger.Printf("telegram: edit detail %s/%s: %v", entry.SourceID, entry.ComicID, err)
	}
}

func (s *Service) replySubscribe(ctx context.Context, chatID int64, chat string, index int, detailMessageID int64) {
	results := s.sessionResults(chatID)
	if index < 0 || index >= len(results) {
		s.sendMessage(ctx, chat, "结果已过期，请重新搜索。", nil)
		return
	}
	entry := results[index]
	subscription, queued, err := s.app.BotSubscribe(ctx, entry.SourceID, entry.ComicID)
	if err != nil {
		s.sendMessage(ctx, chat, fmt.Sprintf("《%s》订阅失败：%s", entry.Title, err.Error()), nil)
		return
	}
	// 订阅完成后收起搜索结果与作品详情，只留一条结果消息。
	s.mu.RLock()
	resultsMessageID := int64(0)
	if current := s.sessions[chatID]; current != nil {
		resultsMessageID = current.resultsMessageID
	}
	s.mu.RUnlock()
	s.deleteMessage(ctx, chat, resultsMessageID)
	s.deleteMessage(ctx, chat, detailMessageID)
	s.mu.Lock()
	delete(s.sessions, chatID)
	s.mu.Unlock()

	// 订阅回执：封面 → 作品名 → 作者（内容源）→ cron → 完整下载位置 → 待下载章节数。
	lines := []string{
		fmt.Sprintf("<b>%s</b>", subscription.Title),
		fmt.Sprintf("%s（%s）", fallback(subscription.Author, "未知作者"), entry.SourceName),
		"Cron：" + fallback(subscription.CronExpr, "未设置"),
		"下载位置：" + fallback(subscription.DownloadDir, "未设置"),
	}
	if queued > 0 {
		lines = append(lines, fmt.Sprintf("共 %d 个章节待下载", queued))
	} else {
		lines = append(lines, "暂时没有需要下载的章节")
	}
	caption := strings.Join(lines, "\n")
	cover := strings.TrimSpace(subscription.Cover)
	if cover == "" {
		cover = strings.TrimSpace(entry.Cover)
	}
	if cover != "" {
		if _, err := s.callMethod(ctx, "sendPhoto", map[string]any{
			"chat_id":    chat,
			"photo":      cover,
			"caption":    caption,
			"parse_mode": "HTML",
		}); err == nil {
			return
		}
	}
	s.sendMessage(ctx, chat, caption, nil)
}

func newResultEntry(source BotSource, comic model.Comic) resultEntry {
	return resultEntry{
		SourceID:     source.ID,
		SourceName:   shortSourceName(source.Name),
		ComicID:      comic.ID,
		Title:        comic.Title,
		Author:       comic.Author,
		Latest:       comic.LatestChapterTitle,
		ChapterCount: comic.ChapterCount,
		Cover:        comic.Cover,
		Summary:      cleanComicSummary(comic.Title, comic.Description),
	}
}

func renderResults(query string, entries []resultEntry) string {
	lines := []string{}
	if query != "" {
		lines = append(lines, fmt.Sprintf("<b>搜索：%s</b>", html.EscapeString(query)))
	}
	for index, entry := range entries {
		lines = append(lines, fmt.Sprintf(
			"%d. %s（%s）",
			index+1,
			html.EscapeString(truncate(entry.Title, titleLimit)),
			html.EscapeString(entry.SourceName),
		))
	}
	return strings.Join(lines, "\n")
}

func renderSubscriptionList(items []BotSubscription) string {
	lines := []string{fmt.Sprintf("<b>订阅清单（%d）</b>", len(items))}
	for index, item := range items {
		if index >= 40 {
			lines = append(lines, fmt.Sprintf("… 其余 %d 项请在网页端查看", len(items)-40))
			break
		}
		lines = append(lines, fmt.Sprintf(
			"%d. %s（%s）%s",
			index+1,
			html.EscapeString(truncate(item.Title, titleLimit)),
			html.EscapeString(shortSourceName(item.Source)),
			subscriptionStateDot(item.Enabled),
		))
	}
	return strings.Join(lines, "\n")
}

func subscriptionStateDot(enabled bool) string {
	if enabled {
		return "🟢"
	}
	return "⚪"
}

func renderCheckReport(outcomes []subscriptionCheckOutcome, ctxErr error) string {
	success := 0
	failed := 0
	newChapters := 0
	queued := 0
	retried := 0
	for _, outcome := range outcomes {
		if outcome.Err != nil {
			failed++
			continue
		}
		success++
		newChapters += outcome.Result.New
		queued += outcome.Result.Queued
		retried += outcome.Result.Retried
	}
	lines := []string{
		fmt.Sprintf("<b>检查更新完成（%d）</b>", len(outcomes)),
		fmt.Sprintf("成功 %d · 失败 %d · 新章节 %d · 入队 %d · 重试 %d", success, failed, newChapters, queued, retried),
	}
	if ctxErr != nil {
		lines = append(lines, "检查已超时，未完成的订阅按失败计。")
	}
	limit := len(outcomes)
	if limit > 20 {
		limit = 20
	}
	for index := 0; index < limit; index++ {
		outcome := outcomes[index]
		item := outcome.Subscription
		prefix := fmt.Sprintf(
			"%d. %s %s（%s）",
			index+1,
			subscriptionStateDot(item.Enabled),
			html.EscapeString(truncate(item.Title, titleLimit)),
			html.EscapeString(shortSourceName(item.Source)),
		)
		if outcome.Err != nil {
			lines = append(lines, prefix+" · 失败："+html.EscapeString(truncate(outcome.Err.Error(), 60)))
			continue
		}
		detail := []string{}
		if outcome.Result.New > 0 {
			detail = append(detail, fmt.Sprintf("新 %d", outcome.Result.New))
		}
		if outcome.Result.Queued > 0 {
			detail = append(detail, fmt.Sprintf("入队 %d", outcome.Result.Queued))
		}
		if outcome.Result.Retried > 0 {
			detail = append(detail, fmt.Sprintf("重试 %d", outcome.Result.Retried))
		}
		if len(detail) == 0 {
			detail = append(detail, "无更新")
		}
		lines = append(lines, prefix+" · "+strings.Join(detail, " / "))
	}
	if len(outcomes) > limit {
		lines = append(lines, fmt.Sprintf("… 其余 %d 项已计入上方汇总", len(outcomes)-limit))
	}
	return strings.Join(lines, "\n")
}

func resultKeyboard(entries []resultEntry) any {
	rows := make([][]map[string]string, 0, len(entries))
	for index, entry := range entries {
		rows = append(rows, []map[string]string{
			{"text": fmt.Sprintf("%d. %s", index+1, truncate(entry.Title, 18)), "callback_data": fmt.Sprintf("open:%d", index)},
		})
	}
	return map[string]any{"inline_keyboard": rows}
}

// matchSource resolves a user supplied source name such as "包子" to a source.
func matchSource(sources []BotSource, value string) (BotSource, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return BotSource{}, false
	}
	candidates := append([]BotSource(nil), sources...)
	sort.SliceStable(candidates, func(i, j int) bool {
		return sourceMatchScore(candidates[i], value) > sourceMatchScore(candidates[j], value)
	})
	if score := sourceMatchScore(candidates[0], value); score > 0 {
		return candidates[0], true
	}
	return BotSource{}, false
}

func sourceMatchScore(source BotSource, value string) int {
	name := strings.ToLower(source.Name)
	id := strings.ToLower(source.ID)
	switch {
	case name == value || id == value:
		return 100
	case strings.Contains(name, value):
		return 80
	case strings.Contains(value, name):
		return 70
	case strings.Contains(id, value):
		return 60
	default:
		return 0
	}
}

// shortSourceName keeps the full name for ASCII sources and the first two
// characters for localized names, matching the requested display style.
func shortSourceName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "未知"
	}
	ascii := true
	for _, r := range name {
		if r > unicode.MaxASCII {
			ascii = false
			break
		}
	}
	if ascii {
		runes := []rune(name)
		if len(runes) > 4 {
			return string(runes[:4])
		}
		return name
	}
	runes := []rune(name)
	if len(runes) <= 2 {
		return name
	}
	return string(runes[:2])
}

func cleanComicSummary(title, summary string) string {
	summary = htmlTagPattern.ReplaceAllString(summary, " ")
	summary = html.UnescapeString(summary)
	summary = strings.ReplaceAll(summary, "\u00a0", " ")
	summary = strings.Join(strings.Fields(summary), " ")
	if summary == "" {
		return ""
	}
	title = strings.TrimSpace(title)
	if title != "" {
		for _, marker := range []string{
			"作品讲述了", "作品讲述", "作品简介", "内容简介", "故事简介", "故事讲述", "讲述了", "简介：", "简介:",
		} {
			pos := strings.Index(summary, marker)
			if pos <= 0 || pos > 240 {
				continue
			}
			prefix := summary[:pos]
			if strings.Contains(prefix, title) || strings.Contains(prefix, "全集") || strings.Contains(prefix, "最新章节") {
				summary = summary[pos:]
				break
			}
		}
	}
	summary = strings.TrimLeft(summary, " \t\r\n,，:：;；。.!！?？-—")
	for _, prefix := range []string{"作品简介：", "作品简介:", "内容简介：", "内容简介:", "简介：", "简介:"} {
		if strings.HasPrefix(summary, prefix) {
			summary = strings.TrimSpace(strings.TrimPrefix(summary, prefix))
			break
		}
	}
	return strings.TrimSpace(summary)
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func fallback(value, fallbackValue string) string {
	if strings.TrimSpace(value) == "" {
		return fallbackValue
	}
	return value
}

func kindLabel(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "book":
		return "书籍"
	case "other":
		return "其他"
	default:
		return "漫画"
	}
}
