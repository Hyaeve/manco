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
	maxSearchResultsPerKind = 5
	maxDirectItems          = 5
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
	case "/check", "检查全部", "检查", "check":
		go s.replyCheck(ctx, chat, argument)
	case "/downloading", "下载中", "正在下载", "downloading":
		s.replyDownloading(ctx, chat)
	case "/view", "查看榜单", "榜单", "view":
		s.replyBrowse(ctx, chatID, argument)
	case "/sources", "内容源", "sources":
		s.replySources(ctx, chat)
	case "/sh", "搜索", "sh":
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

// parseSearchText recognises global, source-type and concrete-source searches.
func parseSearchText(text string) (query, scope string, ok bool) {
	trimmed := strings.TrimSpace(text)
	prefixes := []struct {
		value string
		scope string
	}{
		{value: "搜索漫画", scope: "漫画"},
		{value: "搜索书籍", scope: "书籍"},
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
		"/check [作品名或下载父目录] 检查全部、目录或单项",
		"/downloading 查看正在下载与暂停状态",
		"/view [内容源] 查看内容源榜单",
		"/sources 查看已添加内容源",
		"/sh 关键词 全部来源分类型搜索",
		"查看订阅 [下载父目录]",
		"查看榜单 包子",
		"搜索 关键词 全局搜索",
		"漫画搜索 关键词",
		"书籍搜索 关键词",
		"其他搜索 关键词",
		"包子搜索 关键词",
		"检查 [作品名或下载父目录]",
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
	sources, err := s.app.BotSources(ctx)
	if err != nil {
		s.sendMessage(ctx, chat, "读取内容源失败："+err.Error(), nil)
		return
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		s.replyGlobalSearch(ctx, chatID, chat, query, sources)
		return
	}
	if kind := searchKindScope(scope); kind != "" {
		filtered := make([]BotSource, 0, len(sources))
		for _, source := range sources {
			if normalizedSourceKind(source.Kind) == kind {
				filtered = append(filtered, source)
			}
		}
		if len(filtered) == 0 {
			s.sendMessage(ctx, chat, fmt.Sprintf("还没有添加%s。", kindLabel(kind)), nil)
			return
		}
		entries := rankSearchEntries(query, s.searchSources(ctx, filtered, query))
		if len(entries) > maxSearchResultsPerKind {
			entries = entries[:maxSearchResultsPerKind]
		}
		if len(entries) == 0 {
			s.sendMessage(ctx, chat, fmt.Sprintf("%s里没有搜到「%s」的结果。", kindLabel(kind), query), nil)
			return
		}
		text := renderScopedSearchResults(query, kindLabel(kind), entries)
		messageID, _, sendErr := s.sendResultList(ctx, chat, text, entries, resultKeyboard(entries))
		if sendErr != nil {
			s.logger.Printf("telegram: send type search results: %v", sendErr)
		}
		s.mu.Lock()
		s.sessions[chatID] = &session{results: entries, resultsText: text, resultsMessageID: messageID}
		s.mu.Unlock()
		return
	}

	source, ok := matchSource(sources, scope)
	if !ok {
		s.sendMessage(ctx, chat, fmt.Sprintf("没有找到内容源或搜索类型「%s」。", scope), nil)
		return
	}
	items, err := s.app.BotSearch(ctx, source.ID, query, maxDirectItems)
	if err != nil {
		s.sendMessage(ctx, chat, fmt.Sprintf("%s 搜索失败：%s", source.Name, err.Error()), nil)
		return
	}
	entries := make([]resultEntry, 0, len(items))
	for _, item := range items {
		entries = append(entries, newResultEntry(source, item))
	}
	if len(entries) == 0 {
		s.sendMessage(ctx, chat, fmt.Sprintf("%s里没有搜到「%s」的结果。", source.Name, query), nil)
		return
	}
	text := renderScopedSearchResults(query, source.Name, entries)
	messageID, _, err := s.sendResultList(ctx, chat, text, entries, resultKeyboard(entries))
	if err != nil {
		s.logger.Printf("telegram: send source search results: %v", err)
	}
	s.mu.Lock()
	s.sessions[chatID] = &session{results: entries, resultsText: text, resultsMessageID: messageID}
	s.mu.Unlock()
}

type sourceSearchResult struct {
	Source  BotSource
	Entries []resultEntry
}

func (s *Service) searchSources(ctx context.Context, sources []BotSource, query string) []sourceSearchResult {
	results := make([]sourceSearchResult, len(sources))
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
			items, err := s.app.BotSearch(sourceCtx, source.ID, query, maxDirectItems)
			if err != nil {
				s.logger.Printf("telegram: search source %s: %v", source.Name, err)
				return
			}
			for _, item := range items {
				results[index].Entries = append(results[index].Entries, newResultEntry(source, item))
			}
		}()
	}
	wait.Wait()
	return results
}

func (s *Service) replyGlobalSearch(ctx context.Context, chatID int64, chat, query string, sources []BotSource) {
	searched := s.searchSources(ctx, sources, query)
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
		{Kind: "other", Label: "其他源"},
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
	s.mu.Lock()
	s.sessions[chatID] = &session{globalSearch: &globalSearchPager{
		Query: query,
		Page:  1,
		Pages: pages,
	}}
	s.mu.Unlock()
	if err := s.showGlobalSearchPage(ctx, chatID, chat, 1, 0); err != nil {
		s.sendMessage(ctx, chat, "全局搜索结果发送失败："+err.Error(), nil)
	}
}

func (s *Service) showGlobalSearchPage(ctx context.Context, chatID int64, chat string, page int, messageID int64) error {
	s.mu.RLock()
	current := s.sessions[chatID]
	if current == nil || current.globalSearch == nil || len(current.globalSearch.Pages) == 0 {
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
	keyboard := globalSearchKeyboard(entries, page, len(pager.Pages))
	if messageID != 0 {
		s.deleteMessageAsync(chat, messageID)
	}
	newID, _, err := s.sendResultList(ctx, chat, text, entries, keyboard)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if session := s.sessions[chatID]; session != nil && session.globalSearch != nil {
		session.results = entries
		session.resultsText = text
		session.resultsMessageID = newID
		session.globalSearch.Page = page
	}
	s.mu.Unlock()
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
	case "其他", "其他源", "other":
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
	if len(entries) > 0 {
		cover = telegramPhotoURL(entries[0].Cover)
	}
	if cover != "" {
		photoCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		id, err := s.callMethod(photoCtx, "sendPhoto", map[string]any{
			"chat_id":      chat,
			"photo":        cover,
			"caption":      text,
			"parse_mode":   "HTML",
			"reply_markup": keyboard,
		})
		cancel()
		if err == nil && id != 0 {
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
		s.sendMessage(ctx, chat, "请加入具体内容源，例如：查看榜单 包子、/view 包子 或 查看榜单 拷贝。", nil)
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
			"没有找到内容源「%s」。中文源请输入前两个字或全名，英文源至少输入前六个字母，不区分大小写。",
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
	s.mu.Lock()
	s.sessions[chatID] = &session{browse: &browseSession{
		SourceID:   source.ID,
		SourceName: source.Name,
		SourcePage: 1,
		Page:       1,
		HasMore:    firstPage.HasMore,
		Pool:       pool,
	}}
	s.mu.Unlock()
	if err := s.showBrowsePage(ctx, chatID, chat, 1, 0); err != nil {
		s.sendMessage(ctx, chat, "榜单发送失败："+err.Error(), nil)
	}
}

func matchViewSource(sources []BotSource, value string) (BotSource, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return BotSource{}, false
	}
	for _, source := range sources {
		if strings.ToLower(strings.TrimSpace(source.Name)) == value {
			return source, true
		}
	}
	for _, source := range sources {
		name := strings.ToLower(strings.TrimSpace(source.Name))
		if name == "" {
			continue
		}
		if asciiSourceName(name) {
			prefix := 6
			runes := []rune(name)
			if len(runes) < prefix {
				prefix = len(runes)
			}
			if len([]rune(value)) >= prefix && strings.HasPrefix(name, value) {
				return source, true
			}
			continue
		}
		runes := []rune(name)
		if len(runes) >= 2 && value == string(runes[:2]) {
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

func (s *Service) showBrowsePage(ctx context.Context, chatID int64, chat string, page int, messageID int64) error {
	if page < 1 {
		page = 1
	}
	s.mu.RLock()
	current := s.sessions[chatID]
	if current == nil || current.browse == nil {
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
		source := BotSource{ID: browse.SourceID, Name: browse.SourceName}
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
	keyboard := browseKeyboard(entries, page, hasNext)
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
	s.mu.Lock()
	if existing := s.sessions[chatID]; existing != nil && existing.browse != nil {
		existing.results = entries
		existing.resultsText = text
		existing.resultsMessageID = messageID
		existing.browse = &browse
	}
	s.mu.Unlock()
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

func browseKeyboard(entries []resultEntry, page int, hasNext bool) any {
	rows := resultRowsFrom(entries, (page-1)*browsePageSize)
	navigation := make([]map[string]string, 0, 2)
	if page > 1 {
		navigation = append(navigation, map[string]string{"text": "上一页", "callback_data": "browse:prev"})
	}
	if hasNext {
		navigation = append(navigation, map[string]string{"text": "下一页", "callback_data": "browse:next"})
	}
	if len(navigation) > 0 {
		rows = append(rows, navigation)
	}
	return map[string]any{"inline_keyboard": rows}
}

func globalSearchKeyboard(entries []resultEntry, page, pageCount int) any {
	rows := resultRowsFrom(entries, 0)
	navigation := make([]map[string]string, 0, 2)
	if page > 1 {
		navigation = append(navigation, map[string]string{"text": "上一页", "callback_data": "browse:prev"})
	}
	if page < pageCount {
		navigation = append(navigation, map[string]string{"text": "下一页", "callback_data": "browse:next"})
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
	case data == "browse:prev" || data == "browse:next":
		s.mu.RLock()
		current := s.sessions[chatID]
		page := 0
		globalSearch := false
		if current != nil && current.browse != nil {
			page = current.browse.Page
		} else if current != nil && current.globalSearch != nil {
			page = current.globalSearch.Page
			globalSearch = true
		}
		s.mu.RUnlock()
		if page == 0 {
			s.sendMessage(ctx, chat, "搜索或榜单会话已过期，请重新发起。", nil)
			return
		}
		if data == "browse:prev" {
			page--
		} else {
			page++
		}
		if globalSearch {
			if err := s.showGlobalSearchPage(ctx, chatID, chat, page, query.Message.MessageID); err != nil {
				s.logger.Printf("telegram: global search page: %v", err)
			}
		} else if err := s.showBrowsePage(ctx, chatID, chat, page, query.Message.MessageID); err != nil {
			s.logger.Printf("telegram: browse page: %v", err)
		}
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
		keyboard := s.sessionResultsKeyboard(chatID, results)
		messageID, _, err := s.sendResultList(ctx, chat, resultsText, results, keyboard)
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

func (s *Service) sessionResultsKeyboard(chatID int64, results []resultEntry) any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	current := s.sessions[chatID]
	if current != nil && current.browse != nil {
		return browseKeyboard(results, current.browse.Page, current.browse.HasNext)
	}
	if current != nil && current.globalSearch != nil {
		return globalSearchKeyboard(results, current.globalSearch.Page, len(current.globalSearch.Pages))
	}
	return resultKeyboard(results)
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

func renderScopedSearchResults(query, scopeLabel string, entries []resultEntry) string {
	lines := []string{
		fmt.Sprintf("<b>搜索：%s</b>", html.EscapeString(query)),
		fmt.Sprintf("%s · %d 个候选", html.EscapeString(scopeLabel), len(entries)),
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

func resultKeyboard(entries []resultEntry) any {
	return map[string]any{"inline_keyboard": resultRows(entries)}
}

func resultRows(entries []resultEntry) [][]map[string]string {
	return resultRowsFrom(entries, 0)
}

func resultRowsFrom(entries []resultEntry, offset int) [][]map[string]string {
	rows := make([][]map[string]string, 0, len(entries))
	for index, entry := range entries {
		rows = append(rows, []map[string]string{
			{"text": fmt.Sprintf("%d. %s", offset+index+1, truncate(entry.Title, 18)), "callback_data": fmt.Sprintf("open:%d", index)},
		})
	}
	return rows
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
