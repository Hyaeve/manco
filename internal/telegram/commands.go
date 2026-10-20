package telegram

import (
	"context"
	"fmt"
	"html"
	"net/url"
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
	maxSearchResultsPerKind = 10
	searchPageSize          = 10
	searchSourceFetchLimit  = 20
	maxSearchConcurrency    = 6
	titleLimit              = 24
	summaryLimit            = 160
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
	case "/subscriptions", "订阅", "查看订阅", "subscriptions":
		s.replySubscriptions(ctx, chat, argument)
	case "/delete", "删除订阅", "delete":
		s.replyDeleteSubscriptions(ctx, chatID, argument)
	case "/check", "检查全部", "检查", "check":
		go s.replyCheck(ctx, chat, argument)
	case "/downloading", "下载中", "正在下载", "downloading":
		s.replyDownloading(ctx, chat)
	case "/view", "查看榜单", "榜单", "view":
		s.replyBrowse(ctx, chatID, argument)
	case "/sources", "内容源", "查看内容源", "sources":
		s.replySources(ctx, chat, argument)
	case "/sh", "搜索", "sh":
		s.replySearch(ctx, chatID, argument, "")
	default:
		if sourceName, ok := parseViewText(text); ok {
			s.replyBrowse(ctx, chatID, sourceName)
			return
		}
		if query, sourceName, ok := parseSearchText(text); ok {
			s.replySearch(ctx, chatID, query, sourceName)
			return
		}
		if action, title, ok := parseSubscriptionAction(text); ok {
			if action == "delete" {
				s.replyDeleteSubscriptions(ctx, chatID, title)
				return
			}
			s.replySubscriptionAction(ctx, chat, action, title)
			return
		}
		s.sendMessage(ctx, chat, "未识别的指令。\n\n"+helpText(), nil)
	}
}

// parseViewText accepts natural Chinese source-ranking commands such as
// "查看包子榜单" and "查看包子漫画榜单" while keeping the older
// "查看榜单 包子" command supported.
func parseViewText(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "查看") || !strings.HasSuffix(trimmed, "榜单") {
		return "", false
	}
	sourceName := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "查看"), "榜单"))
	return sourceName, sourceName != ""
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

// parseSearchText recognises global, source-type and concrete-source searches.
func parseSearchText(text string) (query, scope string, ok bool) {
	trimmed := strings.TrimSpace(text)
	prefixes := []struct {
		value string
		scope string
	}{
		{value: "搜索漫画", scope: "漫画"},
		{value: "搜索书籍", scope: "书籍"},
		{value: "搜索视频", scope: "视频"},
		{value: "搜索其他", scope: "其他"},
		{value: "搜索资源", scope: ""},
		{value: "搜索", scope: ""},
	}
	for _, prefix := range prefixes {
		if !strings.HasPrefix(trimmed, prefix.value) {
			continue
		}
		query = strings.TrimSpace(strings.TrimPrefix(trimmed, prefix.value))
		return query, prefix.scope, query != ""
	}
	if index := strings.Index(trimmed, "搜索"); index > 0 {
		scope = strings.TrimSpace(trimmed[:index])
		query = strings.TrimSpace(trimmed[index+len("搜索"):])
		if scope != "" && query != "" {
			return query, scope, true
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
		"/subscriptions [下载父目录] 查看全部订阅或指定父目录",
		"删除订阅 [作品名] 打开订阅删除确认",
		"/check [作品名或下载父目录] 检查全部、目录或单项",
		"/downloading 查看正在下载与暂停状态",
		"/view [内容源] 查看榜单",
		"/sources [漫画源|书籍源|视频源] 查看全部或指定类型内容源",
		"/sh 关键词 全部来源分类型搜索",
		"查看订阅 [下载父目录]",
		"删除订阅 作品名（名称至少输入前四个字）",
		"查看包子榜单 / 查看包子漫画榜单",
		"搜索 关键词 全局搜索",
		"漫画搜索 关键词",
		"书籍搜索 关键词",
		"视频搜索 关键词",
		"包子搜索 关键词",
		"同名源使用完整名称+序号，例如 AA2搜索 关键词",
		"检查 [作品名或下载父目录]",
		"补全 作品名",
		"刮削 作品名",
		"启用 作品名",
		"禁用 作品名",
		"全量 作品名",
		"归档 作品名",
		"删除 作品名（打开删除确认）",
	}, "\n")
}

func (s *Service) replySources(ctx context.Context, chat, filter string) {
	items, err := s.app.BotSources(ctx)
	if err != nil {
		s.sendMessage(ctx, chat, "读取内容源失败："+err.Error(), nil)
		return
	}
	if len(items) == 0 {
		s.sendMessage(ctx, chat, "还没有添加内容源。", nil)
		return
	}
	kind := sourceKindFilter(filter)
	if strings.TrimSpace(filter) != "" && kind == "" {
		s.sendMessage(ctx, chat, "未知内容源类型，请使用漫画源、书籍源或视频源。", nil)
		return
	}
	filtered := make([]BotSource, 0, len(items))
	for _, item := range items {
		if kind != "" && normalizedSourceKind(item.Kind) != kind {
			continue
		}
		filtered = append(filtered, item)
	}
	if len(filtered) == 0 {
		s.sendMessage(ctx, chat, fmt.Sprintf("还没有添加%s。", kindLabel(kind)), nil)
		return
	}
	title := "内容源"
	if kind != "" {
		title = kindLabel(kind)
	}
	lines := []string{"<b>" + title + "</b>"}
	for _, item := range filtered {
		lines = append(lines, fmt.Sprintf("· %s（%s）", item.Name, kindLabel(item.Kind)))
	}
	s.sendMessage(ctx, chat, strings.Join(lines, "\n"), nil)
}

func sourceKindFilter(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, "内容源")
	value = strings.TrimSuffix(value, "源")
	switch value {
	case "", "全部", "all":
		return ""
	case "漫画", "comic", "manga":
		return "comic"
	case "书籍", "小说", "book", "books":
		return "book"
	case "视频", "其他", "other", "video", "videos":
		return "other"
	default:
		return ""
	}
}

func (s *Service) replySubscriptions(ctx context.Context, chat, directory string) {
	items, err := s.app.BotSubscriptions(ctx)
	if err != nil {
		s.sendMessage(ctx, chat, "读取订阅清单失败："+err.Error(), nil)
		return
	}
	if len(items) == 0 {
		s.sendMessage(ctx, chat, "订阅清单是空的。", nil)
		return
	}
	directory = strings.TrimSpace(directory)
	if directory != "" {
		filtered := subscriptionsInDownloadDirectory(items, directory)
		if len(filtered) == 0 {
			s.sendMessage(ctx, chat, fmt.Sprintf("没有找到下载父目录为「%s」的订阅。", directory), nil)
			return
		}
		items = filtered
	}
	s.sendMessage(ctx, chat, renderSubscriptionList(items, directory), nil)
}

func subscriptionsInDownloadDirectory(items []BotSubscription, directory string) []BotSubscription {
	query := normalizeDirectory(directory)
	if query == "" {
		return nil
	}
	result := make([]BotSubscription, 0, len(items))
	for _, item := range items {
		parent := normalizeDirectory(item.ParentDir)
		full := normalizeDirectory(item.DownloadDir)
		if parent == query || full == query || strings.HasSuffix(full, "/"+query) {
			result = append(result, item)
		}
	}
	return result
}

func normalizeDirectory(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.TrimRight(value, "/")
	if value == "" {
		return ""
	}
	return strings.ToLower(value)
}

type subscriptionCheckOutcome struct {
	Subscription BotSubscription
	Result       BotCheckResult
	Err          error
}

func (s *Service) replyDownloading(ctx context.Context, chat string) {
	snapshot, err := s.app.BotDownloading(ctx)
	if err != nil {
		s.sendMessage(ctx, chat, "读取下载状态失败："+err.Error(), nil)
		return
	}
	s.sendMessage(ctx, chat, renderDownloading(snapshot), nil)
}

func renderDownloading(snapshot BotDownloadingSnapshot) string {
	if snapshot.CooldownMinutes > 0 {
		lines := []string{
			"<b>处于暂停期</b>",
			fmt.Sprintf("还有 %d 分钟后开始下载", snapshot.CooldownMinutes),
		}
		if snapshot.NextQueued != nil {
			lines[1] += fmt.Sprintf(
				"《%s》· %s",
				html.EscapeString(snapshot.NextQueued.ComicTitle),
				html.EscapeString(snapshot.NextQueued.ChapterTitle),
			)
		} else {
			lines[1] += "；当前没有排队任务。"
		}
		if len(snapshot.Running) > 0 {
			lines = append(lines, "", "<b>当前仍在下载</b>")
			for index, item := range snapshot.Running {
				lines = append(lines, fmt.Sprintf(
					"%d. 《%s》· %s",
					index+1,
					html.EscapeString(item.ComicTitle),
					html.EscapeString(item.ChapterTitle),
				))
			}
		}
		return strings.Join(lines, "\n")
	}
	if len(snapshot.Running) == 0 {
		return "当前没有正在下载的任务。"
	}
	lines := []string{fmt.Sprintf("<b>正在下载（%d）</b>", len(snapshot.Running))}
	for index, item := range snapshot.Running {
		lines = append(lines, fmt.Sprintf(
			"%d. 《%s》· %s",
			index+1,
			html.EscapeString(item.ComicTitle),
			html.EscapeString(item.ChapterTitle),
		))
	}
	return strings.Join(lines, "\n")
}

// replyCheck checks every subscription by default. A supplied exact title
// checks one subscription; an exact download parent directory checks every
// subscription below that directory.
func (s *Service) replyCheck(ctx context.Context, chat, query string) {
	allItems, err := s.app.BotSubscriptions(ctx)
	if err != nil {
		s.sendMessage(ctx, chat, "读取订阅清单失败："+err.Error(), nil)
		return
	}
	query = strings.TrimSpace(query)
	targetDirectory := checkTargetDirectory(allItems, query)
	items := checkSubscriptionTargets(allItems, query)
	if len(items) == 0 {
		if query == "" {
			s.sendMessage(ctx, chat, "订阅清单是空的。", nil)
		} else {
			s.sendMessage(ctx, chat, fmt.Sprintf("订阅清单里没有找到「%s」。", query), nil)
		}
		return
	}
	if query != "" && len(items) > 1 && targetDirectory == "" {
		lines := []string{fmt.Sprintf("「%s」匹配到 %d 个订阅，请使用更完整的作品名称或下载父目录：", query, len(items))}
		for _, item := range items {
			lines = append(lines, "· "+item.Title)
		}
		s.sendMessage(ctx, chat, strings.Join(lines, "\n"), nil)
		return
	}
	progress := fmt.Sprintf("<b>正在检查全部订阅</b>\n共 %d 个订阅，请稍候。", len(items))
	if targetDirectory != "" {
		progress = fmt.Sprintf(
			"<b>正在检查下载目录「%s」</b>\n共 %d 个订阅，请稍候。",
			html.EscapeString(targetDirectory),
			len(items),
		)
	} else if query != "" && len(items) == 1 {
		progress = fmt.Sprintf("<b>正在检查《%s》</b>", html.EscapeString(items[0].Title))
	}
	messageID, err := s.callMethod(ctx, "sendMessage", map[string]any{
		"chat_id":    chat,
		"text":       progress,
		"parse_mode": "HTML",
	})
	if err != nil {
		s.logger.Printf("telegram: send check progress: %v", err)
	} else {
		s.trackTransientMessageForChat(chat, messageID)
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

// checkSubscriptionTargets returns every subscription for a default check.
// Exact title matches win over directory matches; an exact parent directory
// selects every subscription below it. Remaining matches are partial title or
// parent-directory matches.
func checkSubscriptionTargets(items []BotSubscription, query string) []BotSubscription {
	query = strings.TrimSpace(query)
	if query == "" {
		return append([]BotSubscription(nil), items...)
	}
	var exact []BotSubscription
	var partial []BotSubscription
	needle := strings.ToLower(query)
	for _, item := range items {
		title := strings.ToLower(strings.TrimSpace(item.Title))
		switch {
		case title == needle:
			exact = append(exact, item)
		case strings.Contains(title, needle) ||
			strings.Contains(normalizeDirectory(item.ParentDir), normalizeDirectory(query)) ||
			strings.Contains(normalizeDirectory(item.DownloadDir), normalizeDirectory(query)):
			partial = append(partial, item)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	if directoryMatches := subscriptionsInDownloadDirectory(items, query); len(directoryMatches) > 0 {
		return directoryMatches
	}
	return partial
}

func checkTargetDirectory(items []BotSubscription, query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}
	needle := strings.ToLower(query)
	for _, item := range items {
		if strings.ToLower(strings.TrimSpace(item.Title)) == needle {
			return ""
		}
	}
	matches := subscriptionsInDownloadDirectory(items, query)
	if len(matches) == 0 {
		return ""
	}
	if parent := strings.TrimSpace(matches[0].ParentDir); parent != "" {
		return parent
	}
	return query
}

func normalizeSubscriptionSearch(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), ""))
}

func subscriptionDeleteTitleMatches(title, query string) bool {
	title = normalizeSubscriptionSearch(title)
	query = normalizeSubscriptionSearch(query)
	if query == "" {
		return false
	}
	titleRunes := []rune(title)
	queryRunes := []rune(query)
	required := 4
	if len(titleRunes) < required {
		required = len(titleRunes)
	}
	if required == 0 || len(queryRunes) < required {
		return false
	}
	return strings.HasPrefix(title, query)
}

func subscriptionDeleteMatches(items []BotSubscription, query string) []BotSubscription {
	query = strings.TrimSpace(query)
	if query == "" {
		return append([]BotSubscription(nil), items...)
	}
	matches := make([]BotSubscription, 0, len(items))
	for _, item := range items {
		if subscriptionDeleteTitleMatches(item.Title, query) {
			matches = append(matches, item)
		}
	}
	return matches
}

func renderSubscriptionDeleteDetail(item BotSubscription, index, total int) string {
	lines := []string{
		fmt.Sprintf(
			"<b>%s</b>（%s）",
			html.EscapeString(item.Title),
			html.EscapeString(shortSourceName(fallback(item.Source, "未知"))),
		),
		"作者：" + html.EscapeString(fallback(item.Author, "未知作者")),
	}
	if total > 1 {
		lines = append(lines, fmt.Sprintf("匹配结果：第 %d/%d 个", index+1, total))
	}
	summary := cleanComicSummary(item.Title, item.Description)
	lines = append(lines, "", "简介："+html.EscapeString(fallback(truncate(summary, 240), "暂无简介")))
	return strings.Join(lines, "\n")
}

func subscriptionDeleteCardKeyboard(sessionID string, total int) any {
	rows := [][]map[string]string{
		{
			{"text": "取消", "callback_data": fmt.Sprintf("subdel:cancel:%s", sessionID)},
			{"text": "删除订阅", "callback_data": fmt.Sprintf("subdel:delete:%s", sessionID)},
		},
	}
	if total > 1 {
		rows = append(rows, []map[string]string{
			{"text": "上一个", "callback_data": fmt.Sprintf("subdel:prev:%s", sessionID)},
			{"text": "下一个", "callback_data": fmt.Sprintf("subdel:next:%s", sessionID)},
		})
	}
	return map[string]any{"inline_keyboard": rows}
}

func (s *Service) replyDeleteSubscriptions(ctx context.Context, chatID int64, query string) {
	chat := strconv.FormatInt(chatID, 10)
	items, err := s.app.BotSubscriptions(ctx)
	if err != nil {
		s.sendMessage(ctx, chat, "读取订阅清单失败："+err.Error(), nil)
		return
	}
	matches := subscriptionDeleteMatches(items, query)
	if len(matches) == 0 {
		s.sendMessage(ctx, chat, "没有相关作品的订阅计划。", nil)
		return
	}
	sessionID := s.allocateSession(chatID, &session{
		subscriptionDelete: &subscriptionDeletePager{
			Query: strings.TrimSpace(query),
			Items: matches,
			Index: 0,
		},
	})
	if s.sendSubscriptionDeleteCard(ctx, chat, sessionID, 0) == 0 {
		s.deleteSession(chatID, sessionID)
		s.sendMessage(ctx, chat, "删除确认消息发送失败，请稍后重试。", nil)
	}
}

func (s *Service) sendSubscriptionDeleteCard(ctx context.Context, chat, sessionID string, index int) int64 {
	s.mu.RLock()
	current := s.sessions[sessionID]
	if current == nil || current.subscriptionDelete == nil {
		s.mu.RUnlock()
		return 0
	}
	chatID := current.ChatID
	pager := *current.subscriptionDelete
	pager.Items = append([]BotSubscription(nil), current.subscriptionDelete.Items...)
	s.mu.RUnlock()
	if len(pager.Items) == 0 {
		return 0
	}
	index = (index%len(pager.Items) + len(pager.Items)) % len(pager.Items)
	item := pager.Items[index]
	if detailed, err := s.app.BotSubscriptionDetail(ctx, item.ID); err == nil {
		item = detailed
	} else {
		s.logger.Printf("telegram: subscription delete detail %d: %v", item.ID, err)
	}
	caption := renderSubscriptionDeleteDetail(item, index, len(pager.Items))
	keyboard := subscriptionDeleteCardKeyboard(sessionID, len(pager.Items))
	if path := strings.TrimSpace(item.CoverFile); path != "" {
		photoCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		id, err := s.sendPhotoFileWithKeyboard(photoCtx, chat, path, caption, item.CoverBlur, keyboard)
		cancel()
		if err == nil && id != 0 {
			s.trackTransientMessageForChat(chat, id)
			s.mutateSession(chatID, sessionID, func(value *session) {
				if value.subscriptionDelete != nil {
					value.subscriptionDelete.Index = index
				}
			})
			return id
		}
		s.logger.Printf("telegram: send subscription delete cover file: %v", err)
	}
	if cover := telegramPhotoURL(item.CoverURL); cover != "" {
		payload := map[string]any{
			"chat_id":      chat,
			"photo":        cover,
			"caption":      caption,
			"parse_mode":   "HTML",
			"reply_markup": keyboard,
		}
		if item.CoverBlur {
			payload["has_spoiler"] = true
		}
		if id, err := s.callMethod(ctx, "sendPhoto", payload); err == nil && id != 0 {
			s.trackTransientMessageForChat(chat, id)
			s.mutateSession(chatID, sessionID, func(value *session) {
				if value.subscriptionDelete != nil {
					value.subscriptionDelete.Index = index
				}
			})
			return id
		}
	}
	id := s.sendMessage(ctx, chat, caption, keyboard)
	if id != 0 {
		s.mutateSession(chatID, sessionID, func(value *session) {
			if value.subscriptionDelete != nil {
				value.subscriptionDelete.Index = index
			}
		})
	}
	return id
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

// replySearch runs a global, source-type or concrete-source search.
func (s *Service) replySearch(ctx context.Context, chatID int64, query, scope string) {
	chat := strconv.FormatInt(chatID, 10)
	query = strings.TrimSpace(query)
	if query == "" {
		s.sendMessage(ctx, chat, "请给出搜索关键词，例如：搜索 凡人修仙传、漫画搜索 凡人修仙传、包子搜索 凡人修仙传", nil)
		return
	}
	scope = strings.TrimSpace(scope)
	kindScope := searchKindScope(scope)
	if scope == "" || kindScope != "" {
		s.rememberGlobalSearch(ctx, query)
	}
	sources, err := s.app.BotSources(ctx)
	if err != nil {
		s.sendMessage(ctx, chat, "读取内容源失败："+err.Error(), nil)
		return
	}
	if scope == "" {
		s.replyGlobalSearch(ctx, chatID, chat, query, sources)
		return
	}
	if kindScope != "" {
		filtered := make([]BotSource, 0, len(sources))
		for _, source := range sources {
			if normalizedSourceKind(source.Kind) == kindScope {
				filtered = append(filtered, source)
			}
		}
		if len(filtered) == 0 {
			s.sendMessage(ctx, chat, fmt.Sprintf("还没有添加%s。", kindLabel(kindScope)), nil)
			return
		}
		sessionID := s.allocateSession(chatID, &session{search: &sourceSearchPager{
			Query:      query,
			ScopeLabel: kindLabel(kindScope),
			Sources:    filtered,
			Page:       1,
			SourcePage: 0,
			HasMore:    true,
		}})
		found, sendErr := s.showSourceSearchPage(ctx, chatID, chat, sessionID, 1, 0)
		if sendErr != nil {
			s.logger.Printf("telegram: send type search results: %v", sendErr)
		}
		if !found {
			s.deleteSession(chatID, sessionID)
			s.sendMessage(ctx, chat, fmt.Sprintf("%s里没有搜到「%s」的结果。", kindLabel(kindScope), query), nil)
		}
		return
	}

	source, ok := matchSource(sources, scope)
	if !ok {
		s.sendMessage(ctx, chat, fmt.Sprintf("没有找到内容源或搜索类型「%s」。", scope), nil)
		return
	}
	sessionID := s.allocateSession(chatID, &session{search: &sourceSearchPager{
		Query:      query,
		ScopeLabel: source.Name,
		Sources:    []BotSource{source},
		Page:       1,
		SourcePage: 0,
		HasMore:    true,
	}})
	found, sendErr := s.showSourceSearchPage(ctx, chatID, chat, sessionID, 1, 0)
	if sendErr != nil {
		s.logger.Printf("telegram: send source search results: %v", sendErr)
	}
	if !found {
		s.deleteSession(chatID, sessionID)
		s.sendMessage(ctx, chat, fmt.Sprintf("%s里没有搜到「%s」的结果。", source.Name, query), nil)
	}
}

func (s *Service) rememberGlobalSearch(ctx context.Context, query string) {
	s.mu.RLock()
	app := s.app
	s.mu.RUnlock()
	if app == nil {
		return
	}
	if err := app.BotRememberGlobalSearch(ctx, query); err != nil {
		s.logger.Printf("telegram: remember global search: %v", err)
	}
}

type sourceSearchResult struct {
	Source  BotSource
	Entries []resultEntry
}

func (s *Service) searchSources(ctx context.Context, sources []BotSource, query string, page int) ([]sourceSearchResult, bool) {
	results := make([]sourceSearchResult, len(sources))
	hasMore := make([]bool, len(sources))
	semaphore := make(chan struct{}, maxSearchConcurrency)
	var wait sync.WaitGroup
	for index, source := range sources {
		index, source := index, source
		results[index].Source = source
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}
			sourceCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			pageResult, err := s.app.BotSearch(sourceCtx, source.ID, query, page, searchSourceFetchLimit)
			if err != nil {
				s.logger.Printf("telegram: search source %s: %v", source.Name, err)
				return
			}
			hasMore[index] = pageResult.HasMore
			for _, item := range pageResult.Items {
				results[index].Entries = append(results[index].Entries, newResultEntry(source, item))
			}
		}()
	}
	wait.Wait()
	more := false
	for _, value := range hasMore {
		more = more || value
	}
	return results, more
}

func (s *Service) showSourceSearchPage(ctx context.Context, chatID int64, chat, sessionID string, page int, messageID int64) (bool, error) {
	s.mu.RLock()
	current := s.sessions[sessionID]
	if current == nil || current.ChatID != chatID || current.search == nil {
		s.mu.RUnlock()
		return false, fmt.Errorf("搜索会话已过期")
	}
	pager := *current.search
	pager.Sources = append([]BotSource(nil), current.search.Sources...)
	pager.Pool = append([]resultEntry(nil), current.search.Pool...)
	s.mu.RUnlock()
	if page < 1 {
		page = 1
	}

	for {
		for pager.HasMore && len(pager.Pool) < page*searchPageSize {
			pager.SourcePage++
			groups, more := s.searchSources(ctx, pager.Sources, pager.Query, pager.SourcePage)
			pager.HasMore = more
			seen := make(map[string]bool, len(pager.Pool))
			for _, entry := range pager.Pool {
				seen[entry.SourceID+"\x00"+entry.ComicID] = true
			}
			added := 0
			for _, entry := range rankSearchEntries(pager.Query, groups) {
				key := entry.SourceID + "\x00" + entry.ComicID
				if seen[key] {
					continue
				}
				seen[key] = true
				pager.Pool = append(pager.Pool, entry)
				added++
			}
			if added == 0 && !pager.HasMore {
				break
			}
		}
		maxPage := max(1, (len(pager.Pool)+searchPageSize-1)/searchPageSize)
		if page <= maxPage || page == 1 {
			break
		}
		page = maxPage
	}

	start := (page - 1) * searchPageSize
	if start >= len(pager.Pool) {
		return false, nil
	}
	end := min(len(pager.Pool), start+searchPageSize)
	entries := append([]resultEntry(nil), pager.Pool[start:end]...)
	hasNext := end < len(pager.Pool) || pager.HasMore
	text := renderScopedSearchResults(pager.Query, pager.ScopeLabel, page, entries)
	keyboard := browseKeyboard(entries, page, hasNext, sessionID)
	if messageID != 0 {
		s.deleteMessageAsync(chat, messageID)
	}
	newID, _, err := s.sendResultList(ctx, chat, text, entries, keyboard)
	if err != nil {
		return false, err
	}
	pager.Page = page
	s.mutateSession(chatID, sessionID, func(current *session) {
		current.results = entries
		current.resultsText = text
		current.resultsMessageID = newID
		current.search = &pager
	})
	return true, nil
}

func (s *Service) replyGlobalSearch(ctx context.Context, chatID int64, chat, query string, sources []BotSource) {
	searched, _ := s.searchSources(ctx, sources, query, 1)
	byKind := map[string][]sourceSearchResult{
		"comic": nil,
		"book":  nil,
		"other": nil,
	}
	for _, result := range searched {
		kind := normalizedSourceKind(result.Source.Kind)
		byKind[kind] = append(byKind[kind], result)
	}
	pages := make([]globalSearchPage, 0, 3)
	total := 0
	for _, item := range []struct {
		Kind  string
		Label string
	}{
		{Kind: "comic", Label: "漫画源"},
		{Kind: "book", Label: "书籍源"},
		{Kind: "other", Label: "视频源"},
	} {
		entries := rankSearchEntries(query, byKind[item.Kind])
		if len(entries) > maxSearchResultsPerKind {
			entries = entries[:maxSearchResultsPerKind]
		}
		total += len(entries)
		pages = append(pages, globalSearchPage{Kind: item.Kind, Label: item.Label, Entries: entries})
	}
	if total == 0 {
		s.sendMessage(ctx, chat, fmt.Sprintf("没有搜到「%s」的结果。", query), nil)
		return
	}
	sessionID := s.allocateSession(chatID, &session{globalSearch: &globalSearchPager{
		Query: query,
		Page:  1,
		Pages: pages,
	}})
	if err := s.showGlobalSearchPage(ctx, chatID, chat, sessionID, 1, 0); err != nil {
		s.sendMessage(ctx, chat, "全局搜索结果发送失败："+err.Error(), nil)
	}
}

func (s *Service) showGlobalSearchPage(ctx context.Context, chatID int64, chat, sessionID string, page int, messageID int64) error {
	s.mu.RLock()
	current := s.sessions[sessionID]
	if current == nil || current.ChatID != chatID || current.globalSearch == nil || len(current.globalSearch.Pages) == 0 {
		s.mu.RUnlock()
		return fmt.Errorf("全局搜索会话已过期")
	}
	pager := *current.globalSearch
	pager.Pages = append([]globalSearchPage(nil), current.globalSearch.Pages...)
	s.mu.RUnlock()
	if page < 1 {
		page = 1
	}
	if page > len(pager.Pages) {
		page = len(pager.Pages)
	}
	resultPage := pager.Pages[page-1]
	entries := append([]resultEntry(nil), resultPage.Entries...)
	text := renderGlobalSearchResults(pager.Query, resultPage, page, len(pager.Pages))
	keyboard := globalSearchKeyboard(entries, page, len(pager.Pages), sessionID)
	if messageID != 0 {
		s.deleteMessageAsync(chat, messageID)
	}
	newID, _, err := s.sendResultList(ctx, chat, text, entries, keyboard)
	if err != nil {
		return err
	}
	s.mutateSession(chatID, sessionID, func(current *session) {
		if current.globalSearch != nil {
			current.results = entries
			current.resultsText = text
			current.resultsMessageID = newID
			current.globalSearch.Page = page
		}
	})
	return nil
}

type rankedSearchEntry struct {
	Entry       resultEntry
	Weight      int
	SourceOrder int
	ResultOrder int
}

func rankSearchEntries(query string, groups []sourceSearchResult) []resultEntry {
	ranked := make([]rankedSearchEntry, 0)
	for sourceOrder, group := range groups {
		for resultOrder, entry := range group.Entries {
			ranked = append(ranked, rankedSearchEntry{
				Entry:       entry,
				Weight:      searchTitleWeight(query, entry.Title),
				SourceOrder: sourceOrder,
				ResultOrder: resultOrder,
			})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Weight != ranked[j].Weight {
			return ranked[i].Weight > ranked[j].Weight
		}
		if ranked[i].SourceOrder != ranked[j].SourceOrder {
			return ranked[i].SourceOrder < ranked[j].SourceOrder
		}
		return ranked[i].ResultOrder < ranked[j].ResultOrder
	})
	seen := map[string]bool{}
	entries := make([]resultEntry, 0, len(ranked))
	for _, item := range ranked {
		key := item.Entry.SourceID + "\x00" + item.Entry.ComicID
		if seen[key] {
			continue
		}
		seen[key] = true
		entries = append(entries, item.Entry)
	}
	return entries
}

func searchTitleWeight(query, title string) int {
	query = strings.ToLower(strings.Join(strings.Fields(query), ""))
	title = strings.ToLower(strings.Join(strings.Fields(title), ""))
	switch {
	case query == "":
		return 0
	case title == query:
		return 10000
	case strings.HasPrefix(title, query):
		return 8000
	case strings.Contains(title, query):
		return 6000
	}
	matched := 0
	for _, r := range []rune(query) {
		if strings.ContainsRune(title, r) {
			matched++
		}
	}
	return matched * 100
}

func searchKindScope(scope string) string {
	value := strings.ToLower(strings.TrimSpace(scope))
	value = strings.TrimSuffix(value, "内容源")
	value = strings.TrimSuffix(value, "源")
	switch value {
	case "漫画", "漫画源", "comic", "manga":
		return "comic"
	case "书籍", "书籍源", "小说", "book", "books":
		return "book"
	case "视频", "视频源", "其他", "其他源", "video", "videos", "other":
		return "other"
	default:
		return ""
	}
}

func normalizedSourceKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "book":
		return "book"
	case "other":
		return "other"
	default:
		return "comic"
	}
}

// sendResultList attaches the result list to the first work's cover so the
// Telegram inline keyboard appears directly below that cover. Sources without
// an absolute cover URL fall back to the existing text message.
func (s *Service) sendResultList(ctx context.Context, chat, text string, entries []resultEntry, keyboard any) (int64, bool, error) {
	cover := ""
	coverBlur := false
	if len(entries) > 0 {
		cover = telegramPhotoURL(entries[0].Cover)
		coverBlur = entries[0].CoverBlur
	}
	if cover != "" {
		photoCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		payload := map[string]any{
			"chat_id":      chat,
			"photo":        cover,
			"caption":      text,
			"parse_mode":   "HTML",
			"reply_markup": keyboard,
		}
		if coverBlur {
			payload["has_spoiler"] = true
		}
		id, err := s.callMethod(photoCtx, "sendPhoto", payload)
		cancel()
		if err == nil && id != 0 {
			s.trackTransientMessageForChat(chat, id)
			return id, true, nil
		}
		s.logger.Printf("telegram: send result cover: %v", err)
	}
	id, err := s.callMethod(ctx, "sendMessage", map[string]any{
		"chat_id":      chat,
		"text":         text,
		"parse_mode":   "HTML",
		"reply_markup": keyboard,
	})
	if err == nil && id != 0 {
		s.trackTransientMessageForChat(chat, id)
	}
	return id, false, err
}

func telegramPhotoURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	return parsed.String()
}

const browsePageSize = 10

func (s *Service) replyBrowse(ctx context.Context, chatID int64, sourceQuery string) {
	chat := strconv.FormatInt(chatID, 10)
	sourceQuery = strings.TrimSpace(sourceQuery)
	if sourceQuery == "" {
		s.sendMessage(ctx, chat, "请加入具体内容源，例如：查看包子榜单、查看包子漫画榜单 或 /view 包子。", nil)
		return
	}
	sources, err := s.app.BotSources(ctx)
	if err != nil {
		s.sendMessage(ctx, chat, "读取内容源失败："+err.Error(), nil)
		return
	}
	source, ok := matchViewSource(sources, sourceQuery)
	if !ok {
		s.sendMessage(ctx, chat, fmt.Sprintf(
			"没有找到内容源「%s」。中文源至少输入前两个字，英文源至少输入前四个字母（短名称请输入全名），不区分大小写；同名源可用完整名称加序号，例如 AA2。",
			sourceQuery,
		), nil)
		return
	}
	firstPage, err := s.app.BotBrowse(ctx, source.ID, 1)
	if err != nil {
		s.sendMessage(ctx, chat, fmt.Sprintf("%s 榜单加载失败：%s", source.Name, err.Error()), nil)
		return
	}
	pool := make([]resultEntry, 0, len(firstPage.Items))
	for _, item := range firstPage.Items {
		pool = append(pool, newResultEntry(source, item))
	}
	if len(pool) == 0 {
		s.sendMessage(ctx, chat, fmt.Sprintf("%s 当前没有榜单内容。", source.Name), nil)
		return
	}
	sessionID := s.allocateSession(chatID, &session{browse: &browseSession{
		SourceID:        source.ID,
		SourceName:      source.Name,
		SourceCoverBlur: source.CoverBlur,
		SourcePage:      1,
		Page:            1,
		HasMore:         firstPage.HasMore,
		Pool:            pool,
	}})
	if err := s.showBrowsePage(ctx, chatID, chat, sessionID, 1, 0); err != nil {
		s.sendMessage(ctx, chat, "榜单发送失败："+err.Error(), nil)
	}
}

func matchViewSource(sources []BotSource, value string) (BotSource, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return BotSource{}, false
	}
	if source, ok := matchSourceByExactOrOrdinal(sources, value); ok {
		return source, true
	}
	for _, source := range sources {
		name := strings.ToLower(strings.TrimSpace(source.Name))
		if name == "" {
			continue
		}
		if sourceQueryEligible(name, value) && strings.HasPrefix(name, value) {
			return source, true
		}
	}
	return BotSource{}, false
}

func asciiSourceName(value string) bool {
	for _, r := range value {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

func (s *Service) showBrowsePage(ctx context.Context, chatID int64, chat, sessionID string, page int, messageID int64) error {
	if page < 1 {
		page = 1
	}
	s.mu.RLock()
	current := s.sessions[sessionID]
	if current == nil || current.ChatID != chatID || current.browse == nil {
		s.mu.RUnlock()
		return fmt.Errorf("榜单会话已过期")
	}
	browse := *current.browse
	browse.Pool = append([]resultEntry(nil), current.browse.Pool...)
	s.mu.RUnlock()

	for {
		start := (page - 1) * browsePageSize
		if start < len(browse.Pool) || !browse.HasMore {
			break
		}
		browse.SourcePage++
		next, err := s.app.BotBrowse(ctx, browse.SourceID, browse.SourcePage)
		if err != nil {
			return err
		}
		browse.HasMore = next.HasMore
		if len(next.Items) == 0 {
			browse.HasMore = false
			break
		}
		source := BotSource{
			ID:        browse.SourceID,
			Name:      browse.SourceName,
			CoverBlur: browse.SourceCoverBlur,
		}
		for _, item := range next.Items {
			browse.Pool = append(browse.Pool, newResultEntry(source, item))
		}
	}
	start := (page - 1) * browsePageSize
	if start >= len(browse.Pool) {
		page = max(1, (len(browse.Pool)+browsePageSize-1)/browsePageSize)
		start = (page - 1) * browsePageSize
	}
	end := min(len(browse.Pool), start+browsePageSize)
	entries := append([]resultEntry(nil), browse.Pool[start:end]...)
	hasNext := end < len(browse.Pool) || browse.HasMore
	text := renderBrowseResults(browse.SourceName, page, start, entries)
	keyboard := browseKeyboard(entries, page, hasNext, sessionID)
	if messageID != 0 {
		s.deleteMessageAsync(chat, messageID)
	}
	newID, _, err := s.sendResultList(ctx, chat, text, entries, keyboard)
	if err != nil {
		return err
	}
	messageID = newID
	browse.Page = page
	browse.HasNext = hasNext
	s.mutateSession(chatID, sessionID, func(existing *session) {
		if existing.browse != nil {
			existing.results = entries
			existing.resultsText = text
			existing.resultsMessageID = messageID
			existing.browse = &browse
		}
	})
	return nil
}

func renderBrowseResults(sourceName string, page, start int, entries []resultEntry) string {
	lines := []string{
		fmt.Sprintf("<b>%s · 榜单</b>", html.EscapeString(sourceName)),
		fmt.Sprintf("第 %d 页", page),
	}
	for index, entry := range entries {
		lines = append(lines, fmt.Sprintf(
			"%d. %s",
			start+index+1,
			html.EscapeString(truncate(entry.Title, titleLimit)),
		))
	}
	return strings.Join(lines, "\n")
}

func browseKeyboard(entries []resultEntry, page int, hasNext bool, sessionID string) any {
	rows := resultRowsFrom(entries, (page-1)*browsePageSize, sessionID)
	navigation := make([]map[string]string, 0, 2)
	if page > 1 {
		navigation = append(navigation, map[string]string{"text": "上一页", "callback_data": fmt.Sprintf("browse:%s:prev", sessionID)})
	}
	if hasNext {
		navigation = append(navigation, map[string]string{"text": "下一页", "callback_data": fmt.Sprintf("browse:%s:next", sessionID)})
	}
	if len(navigation) > 0 {
		rows = append(rows, navigation)
	}
	return map[string]any{"inline_keyboard": rows}
}

func globalSearchKeyboard(entries []resultEntry, page, pageCount int, sessionID string) any {
	rows := resultRowsFrom(entries, 0, sessionID)
	navigation := make([]map[string]string, 0, 2)
	if page > 1 {
		navigation = append(navigation, map[string]string{"text": "上一页", "callback_data": fmt.Sprintf("browse:%s:prev", sessionID)})
	}
	if page < pageCount {
		navigation = append(navigation, map[string]string{"text": "下一页", "callback_data": fmt.Sprintf("browse:%s:next", sessionID)})
	}
	if len(navigation) > 0 {
		rows = append(rows, navigation)
	}
	return map[string]any{"inline_keyboard": rows}
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
	s.touchChatSessions(chatID)
	s.refreshTransientChat(chatID)
	data := strings.TrimSpace(query.Data)
	switch {
	case strings.HasPrefix(data, "open:"):
		sessionID, index, ok := parseIndexCallback(data, "open:")
		if !ok || s.session(chatID, sessionID) == nil {
			return
		}
		// 只收起当前交互消息，其他指令产生的消息框保持可用。
		resultsMessageID := int64(0)
		s.mu.RLock()
		if current := s.sessions[sessionID]; current != nil && current.ChatID == chatID {
			resultsMessageID = current.resultsMessageID
		}
		s.mu.RUnlock()
		s.deleteMessageAsync(chat, resultsMessageID)
		s.mutateSession(chatID, sessionID, func(current *session) {
			current.resultsMessageID = 0
		})
		s.replyResultDetail(ctx, chatID, chat, sessionID, index)
	case strings.HasPrefix(data, "subdel:"):
		s.handleSubscriptionDeleteCallback(ctx, chatID, chat, query.Message.MessageID, data)
	case strings.HasPrefix(data, "subscribe:"):
		sessionID, index, ok := parseIndexCallback(data, "subscribe:")
		if !ok || s.session(chatID, sessionID) == nil {
			return
		}
		s.replySubscribe(ctx, chatID, chat, sessionID, index, query.Message.MessageID)
	case strings.HasPrefix(data, "browse:"):
		parts := strings.Split(data, ":")
		if len(parts) != 3 {
			return
		}
		sessionID, action := parts[1], parts[2]
		current := s.session(chatID, sessionID)
		if current == nil {
			s.sendMessage(ctx, chat, "搜索或榜单会话已过期，请重新发起。", nil)
			return
		}
		s.mu.RLock()
		page := 0
		globalSearch := false
		sourceSearch := false
		if current.browse != nil {
			page = current.browse.Page
		} else if current.globalSearch != nil {
			page = current.globalSearch.Page
			globalSearch = true
		} else if current.search != nil {
			page = current.search.Page
			sourceSearch = true
		}
		s.mu.RUnlock()
		if page == 0 {
			s.sendMessage(ctx, chat, "搜索或榜单会话已过期，请重新发起。", nil)
			return
		}
		if action == "prev" {
			page--
		} else if action == "next" {
			page++
		} else {
			return
		}
		if globalSearch {
			if err := s.showGlobalSearchPage(ctx, chatID, chat, sessionID, page, query.Message.MessageID); err != nil {
				s.logger.Printf("telegram: global search page: %v", err)
			}
		} else if sourceSearch {
			found, err := s.showSourceSearchPage(ctx, chatID, chat, sessionID, page, query.Message.MessageID)
			if err != nil {
				s.logger.Printf("telegram: source search page: %v", err)
			} else if !found {
				s.sendMessage(ctx, chat, "没有更多搜索结果。", nil)
			}
		} else if err := s.showBrowsePage(ctx, chatID, chat, sessionID, page, query.Message.MessageID); err != nil {
			s.logger.Printf("telegram: browse page: %v", err)
		}
	case strings.HasPrefix(data, "back:"):
		sessionID := strings.TrimSpace(strings.TrimPrefix(data, "back:"))
		if s.session(chatID, sessionID) == nil {
			return
		}
		// 收起详情，并把搜索结果重新发一份，供用户重新选择。
		s.deleteMessageAsync(chat, query.Message.MessageID)
		results := s.sessionResults(chatID, sessionID)
		if len(results) == 0 {
			s.sendMessage(ctx, chat, "没有可返回的搜索结果。", nil)
			return
		}
		resultsText := s.sessionResultsText(chatID, sessionID)
		if resultsText == "" {
			resultsText = renderResults("", results)
		}
		keyboard := s.sessionResultsKeyboard(chatID, sessionID, results)
		messageID, _, err := s.sendResultList(ctx, chat, resultsText, results, keyboard)
		if err != nil {
			s.logger.Printf("telegram: resend search results: %v", err)
			return
		}
		s.mutateSession(chatID, sessionID, func(current *session) {
			current.resultsMessageID = messageID
			current.detailMessageID = 0
			current.detailPhoto = false
			current.detailIndex = 0
		})
	}
}

func (s *Service) handleSubscriptionDeleteCallback(
	ctx context.Context,
	chatID int64,
	chat string,
	messageID int64,
	data string,
) {
	parts := strings.Split(data, ":")
	if len(parts) != 3 || strings.TrimSpace(parts[2]) == "" {
		return
	}
	action := strings.TrimSpace(parts[1])
	sessionID := strings.TrimSpace(parts[2])
	s.mu.RLock()
	current := s.sessions[sessionID]
	if current == nil || current.ChatID != chatID || current.subscriptionDelete == nil {
		s.mu.RUnlock()
		s.sendMessage(ctx, chat, "删除订阅会话已过期，请重新发起。", nil)
		return
	}
	pager := *current.subscriptionDelete
	pager.Items = append([]BotSubscription(nil), current.subscriptionDelete.Items...)
	s.mu.RUnlock()
	if len(pager.Items) == 0 {
		s.deleteSession(chatID, sessionID)
		s.sendMessage(ctx, chat, "没有相关作品的订阅计划。", nil)
		return
	}
	switch action {
	case "cancel":
		s.deleteMessage(ctx, chat, messageID)
		s.deleteSession(chatID, sessionID)
	case "prev", "next":
		index := pager.Index
		if action == "prev" {
			index--
		} else {
			index++
		}
		s.deleteMessageAsync(chat, messageID)
		if s.sendSubscriptionDeleteCard(ctx, chat, sessionID, index) == 0 {
			s.sendMessage(ctx, chat, "切换删除确认作品失败，请重新发起。", nil)
		}
	case "delete":
		index := pager.Index
		if index < 0 || index >= len(pager.Items) {
			s.sendMessage(ctx, chat, "删除订阅会话已过期，请重新发起。", nil)
			return
		}
		item := pager.Items[index]
		if err := s.app.BotDelete(ctx, item.ID); err != nil {
			s.sendMessage(ctx, chat, "删除失败："+err.Error(), nil)
			return
		}
		s.deleteMessage(ctx, chat, messageID)
		s.deleteSession(chatID, sessionID)
		s.sendMessage(ctx, chat, fmt.Sprintf("《%s》订阅已删除。", html.EscapeString(item.Title)), nil)
	}
}

func parseIndexCallback(data, prefix string) (string, int, bool) {
	parts := strings.Split(strings.TrimPrefix(data, prefix), ":")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
		return "", 0, false
	}
	index, err := strconv.Atoi(parts[1])
	if err != nil || index < 0 {
		return "", 0, false
	}
	return parts[0], index, true
}

func (s *Service) sessionResults(chatID int64, sessionID string) []resultEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	current := s.sessions[sessionID]
	if current == nil || current.ChatID != chatID {
		return nil
	}
	return append([]resultEntry(nil), current.results...)
}

func (s *Service) sessionResultsText(chatID int64, sessionID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	current := s.sessions[sessionID]
	if current == nil || current.ChatID != chatID {
		return ""
	}
	return current.resultsText
}

func (s *Service) sessionResultsKeyboard(chatID int64, sessionID string, results []resultEntry) any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	current := s.sessions[sessionID]
	if current == nil || current.ChatID != chatID {
		return resultKeyboard(results, sessionID)
	}
	if current != nil && current.browse != nil {
		return browseKeyboard(results, current.browse.Page, current.browse.HasNext, sessionID)
	}
	if current != nil && current.globalSearch != nil {
		return globalSearchKeyboard(results, current.globalSearch.Page, len(current.globalSearch.Pages), sessionID)
	}
	if current != nil && current.search != nil {
		hasNext := current.search.Page*searchPageSize < len(current.search.Pool) || current.search.HasMore
		return browseKeyboard(results, current.search.Page, hasNext, sessionID)
	}
	return resultKeyboard(results, sessionID)
}

func (s *Service) replyResultDetail(ctx context.Context, chatID int64, chat, sessionID string, index int) {
	results := s.sessionResults(chatID, sessionID)
	if index < 0 || index >= len(results) {
		s.sendMessage(ctx, chat, "结果已过期，请重新搜索。", nil)
		return
	}
	entry := results[index]
	// 先用搜索结果里的缓存数据立刻建框，再异步拉详情并原位更新。
	caption := renderResultDetail(entry, true)
	keyboard := resultDetailKeyboard(index, sessionID)
	messageID, photo := s.sendResultDetail(ctx, chat, caption, entry.Cover, entry.CoverBlur, keyboard)
	if messageID == 0 {
		s.sendMessage(ctx, chat, "作品详情发送失败，请稍后重试。", nil)
		return
	}
	s.mutateSession(chatID, sessionID, func(current *session) {
		current.detailMessageID = messageID
		current.detailPhoto = photo
		current.detailIndex = index
	})
	if resultNeedsDetail(entry) {
		go s.enrichResultDetail(ctx, chatID, chat, sessionID, index, messageID, photo)
	}
}

func resultDetailKeyboard(index int, sessionID string) any {
	return map[string]any{
		"inline_keyboard": [][]map[string]string{
			{
				{"text": "返回", "callback_data": fmt.Sprintf("back:%s", sessionID)},
				{"text": "订阅", "callback_data": fmt.Sprintf("subscribe:%s:%d", sessionID, index)},
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

func (s *Service) sendResultDetail(ctx context.Context, chat, caption, cover string, coverBlur bool, keyboard any) (int64, bool) {
	if cover = strings.TrimSpace(cover); cover != "" {
		photoCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		payload := map[string]any{
			"chat_id":      chat,
			"photo":        cover,
			"caption":      caption,
			"parse_mode":   "HTML",
			"reply_markup": keyboard,
		}
		if coverBlur {
			payload["has_spoiler"] = true
		}
		id, err := s.callMethod(photoCtx, "sendPhoto", payload)
		cancel()
		if err == nil && id != 0 {
			s.trackTransientMessageForChat(chat, id)
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
	if id != 0 {
		s.trackTransientMessageForChat(chat, id)
	}
	return id, false
}

func (s *Service) enrichResultDetail(ctx context.Context, chatID int64, chat, sessionID string, index int, messageID int64, photo bool) {
	results := s.sessionResults(chatID, sessionID)
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
	current := s.sessions[sessionID]
	if current == nil || current.ChatID != chatID || current.detailMessageID != messageID || current.detailIndex != index {
		s.mu.Unlock()
		return
	}
	current.results[index] = entry
	current.UpdatedAt = time.Now()
	s.mu.Unlock()

	caption := renderResultDetail(entry, false)
	editCtx, editCancel := context.WithTimeout(ctx, 10*time.Second)
	defer editCancel()
	if err := s.editMessage(editCtx, chat, messageID, photo, caption, resultDetailKeyboard(index, sessionID)); err != nil {
		s.logger.Printf("telegram: edit detail %s/%s: %v", entry.SourceID, entry.ComicID, err)
	}
}

func (s *Service) replySubscribe(ctx context.Context, chatID int64, chat, sessionID string, index int, detailMessageID int64) {
	results := s.sessionResults(chatID, sessionID)
	if index < 0 || index >= len(results) {
		s.sendMessage(ctx, chat, "结果已过期，请重新搜索。", nil)
		return
	}
	entry := results[index]
	if existing, err := s.app.BotSubscriptionVisual(ctx, entry.SourceID, entry.ComicID); err == nil && existing.InPlan {
		title := fallback(existing.Title, entry.Title)
		s.sendMessage(
			ctx,
			chat,
			fmt.Sprintf("<b>《%s》</b>已在订阅计划中。", html.EscapeString(title)),
			nil,
		)
		return
	}
	subscription, queued, err := s.app.BotSubscribe(ctx, entry.SourceID, entry.ComicID)
	if err != nil {
		s.sendMessage(ctx, chat, fmt.Sprintf("《%s》订阅失败：%s", entry.Title, err.Error()), nil)
		return
	}
	// 订阅完成后收起搜索结果与作品详情，只留一条结果消息。
	s.mu.RLock()
	resultsMessageID := int64(0)
	if current := s.sessions[sessionID]; current != nil && current.ChatID == chatID {
		resultsMessageID = current.resultsMessageID
	}
	s.mu.RUnlock()
	s.deleteMessage(ctx, chat, resultsMessageID)
	s.deleteMessage(ctx, chat, detailMessageID)
	s.deleteSession(chatID, sessionID)

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
		payload := map[string]any{
			"chat_id":    chat,
			"photo":      cover,
			"caption":    caption,
			"parse_mode": "HTML",
		}
		if subscription.CoverBlur {
			payload["has_spoiler"] = true
		}
		if id, err := s.callMethod(ctx, "sendPhoto", payload); err == nil {
			s.trackTransientMessageForChat(chat, id)
			return
		}
	}
	s.sendMessage(ctx, chat, caption, nil)
}

func newResultEntry(source BotSource, comic model.Comic) resultEntry {
	cover := strings.TrimSpace(comic.Cover)
	if cover == "" {
		cover = strings.TrimSpace(comic.CachedCover)
	}
	return resultEntry{
		SourceID:     source.ID,
		SourceName:   shortSourceName(source.Name),
		ComicID:      comic.ID,
		Title:        comic.Title,
		Author:       comic.Author,
		Latest:       comic.LatestChapterTitle,
		ChapterCount: comic.ChapterCount,
		Cover:        cover,
		CoverBlur:    source.CoverBlur,
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

func renderScopedSearchResults(query, scopeLabel string, page int, entries []resultEntry) string {
	lines := []string{
		fmt.Sprintf("<b>搜索：%s</b>", html.EscapeString(query)),
		fmt.Sprintf("%s · 第 %d 页 · %d 个候选", html.EscapeString(scopeLabel), page, len(entries)),
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

func renderGlobalSearchResults(query string, page globalSearchPage, pageNumber, pageCount int) string {
	lines := []string{
		fmt.Sprintf("<b>搜索：%s</b>", html.EscapeString(query)),
		fmt.Sprintf("<b>%s · 第 %d/%d 页</b>", html.EscapeString(page.Label), pageNumber, pageCount),
	}
	if len(page.Entries) == 0 {
		lines = append(lines, "本分类没有搜索结果")
		return strings.Join(lines, "\n")
	}
	for index, entry := range page.Entries {
		lines = append(lines, fmt.Sprintf(
			"%d. %s（%s）",
			index+1,
			html.EscapeString(truncate(entry.Title, titleLimit)),
			html.EscapeString(entry.SourceName),
		))
	}
	return strings.Join(lines, "\n")
}

func renderSubscriptionList(items []BotSubscription, directory ...string) string {
	header := fmt.Sprintf("<b>订阅清单（%d）</b>", len(items))
	if len(directory) > 0 && strings.TrimSpace(directory[0]) != "" {
		header = fmt.Sprintf(
			"<b>订阅清单 · %s（%d）</b>",
			html.EscapeString(strings.TrimSpace(directory[0])),
			len(items),
		)
	}
	lines := []string{header}
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

func resultKeyboard(entries []resultEntry, sessionID string) any {
	return map[string]any{"inline_keyboard": resultRows(entries, sessionID)}
}

func resultRows(entries []resultEntry, sessionID string) [][]map[string]string {
	return resultRowsFrom(entries, 0, sessionID)
}

func resultRowsFrom(entries []resultEntry, offset int, sessionID string) [][]map[string]string {
	const resultsPerRow = 5
	rows := make([][]map[string]string, 0, (len(entries)+resultsPerRow-1)/resultsPerRow)
	row := make([]map[string]string, 0, resultsPerRow)
	for index := range entries {
		row = append(row, map[string]string{
			"text":          fmt.Sprintf("%d", offset+index+1),
			"callback_data": fmt.Sprintf("open:%s:%d", sessionID, index),
		})
		if len(row) == resultsPerRow {
			rows = append(rows, row)
			row = make([]map[string]string, 0, resultsPerRow)
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}

// matchSource resolves a user supplied source name such as "包子" to a source.
func matchSource(sources []BotSource, value string) (BotSource, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return BotSource{}, false
	}
	if source, ok := matchSourceByExactOrOrdinal(sources, value); ok {
		return source, true
	}
	if base, _, ok := splitSourceOrdinal(value); ok && hasSourceName(sources, base) {
		return BotSource{}, false
	}
	candidates := append([]BotSource(nil), sources...)
	if len(candidates) == 0 {
		return BotSource{}, false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return sourceMatchScore(candidates[i], value) > sourceMatchScore(candidates[j], value)
	})
	if score := sourceMatchScore(candidates[0], value); score > 0 {
		return candidates[0], true
	}
	return BotSource{}, false
}

func matchSourceByExactOrOrdinal(sources []BotSource, value string) (BotSource, bool) {
	for _, item := range sources {
		name := strings.ToLower(strings.TrimSpace(item.Name))
		id := strings.ToLower(strings.TrimSpace(item.ID))
		if name == value || id == value {
			return item, true
		}
	}
	base, ordinal, ok := splitSourceOrdinal(value)
	if !ok {
		return BotSource{}, false
	}
	matches := make([]BotSource, 0, 2)
	for _, item := range sources {
		if strings.ToLower(strings.TrimSpace(item.Name)) == base {
			matches = append(matches, item)
		}
	}
	if ordinal > 0 && ordinal <= len(matches) {
		return matches[ordinal-1], true
	}
	return BotSource{}, false
}

func hasSourceName(sources []BotSource, value string) bool {
	for _, item := range sources {
		if strings.ToLower(strings.TrimSpace(item.Name)) == value {
			return true
		}
	}
	return false
}

func splitSourceOrdinal(value string) (string, int, bool) {
	runes := []rune(strings.TrimSpace(value))
	end := len(runes)
	for end > 0 && runes[end-1] >= '0' && runes[end-1] <= '9' {
		end--
	}
	if end == len(runes) || end == 0 {
		return "", 0, false
	}
	ordinal, err := strconv.Atoi(string(runes[end:]))
	if err != nil || ordinal <= 0 {
		return "", 0, false
	}
	return strings.TrimSpace(string(runes[:end])), ordinal, true
}

func sourceMatchScore(source BotSource, value string) int {
	name := strings.ToLower(source.Name)
	id := strings.ToLower(source.ID)
	switch {
	case name == value || id == value:
		return 100
	case sourceQueryEligible(name, value) && strings.HasPrefix(name, value):
		return 90
	case sourceQueryEligible(name, value) && strings.Contains(name, value):
		return 80
	case strings.Contains(value, name):
		return 70
	case len([]rune(value)) >= 4 && strings.Contains(id, value):
		return 60
	default:
		return 0
	}
}

func sourceQueryEligible(name, value string) bool {
	nameRunes := []rune(name)
	valueRunes := []rune(value)
	if len(valueRunes) == 0 {
		return false
	}
	if asciiSourceName(name) {
		if len(nameRunes) < 4 {
			return value == name
		}
		return len(valueRunes) >= 4
	}
	return len(valueRunes) >= 2
}

// shortSourceName keeps at most four ASCII letters and the first two
// characters for localized names. Consecutive uppercase segments are softened
// so names such as GoDA are displayed as GoDa.
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
		if len(runes) < 4 {
			return name
		}
		if len(runes) > 4 {
			runes = runes[:4]
		}
		original := append([]rune(nil), runes...)
		for index := 1; index < len(runes); index++ {
			if unicode.IsUpper(original[index]) && unicode.IsUpper(original[index-1]) {
				runes[index] = unicode.ToLower(runes[index])
			}
		}
		return string(runes)
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
		return "视频"
	default:
		return "漫画"
	}
}
