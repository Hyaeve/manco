package telegram

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hyaeve/manco/internal/model"
)

type SubscriptionDownloadCompletedNotice struct {
	Title          string
	Source         string
	Author         string
	Description    string
	CoverURL       string
	CoverFile      string
	CoverBlur      bool
	Succeeded      int
	Failed         int
	NextCheckLabel string
}

type BangumiMatchedNotice struct {
	Title         string
	OriginalTitle string
	Source        string
	Author        string
	Description   string
	Status        string
	CoverURL      string
	CoverFile     string
	CoverBlur     bool
}

type SubscriptionArchiveNotice struct {
	Title          string
	Source         string
	Author         string
	CoverURL       string
	CoverFile      string
	CoverBlur      bool
	ChapterCount   int
	DaysWithoutNew int
	DaysRemaining  int
	ThresholdDays  int
	Completed      bool
}

type DownloadRetryNotice struct {
	SourceID  string
	ComicID   string
	Title     string
	Source    string
	Author    string
	CoverURL  string
	CoverFile string
	CoverBlur bool
	Chapters  []string
}

type retryAttempt struct {
	At       time.Time
	Count    int
	Chapters []string
}

type dailyRetryNotice struct {
	Day       string
	MessageID int64
	Rounds    int
	Chapters  int
	Recent    []retryAttempt
}

type retryNoticeStateStore interface {
	BotRetryNoticeState(context.Context) (string, error)
	BotSetRetryNoticeState(context.Context, string) error
}

var retryNoticeLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

// notifyPhotoAll 给每个配置的会话发一条带图通知；封面取不到就退回纯文字。
func (s *Service) notifyPhotoAll(ctx context.Context, photo, text string, coverBlur bool) {
	if !s.Enabled() || strings.TrimSpace(text) == "" {
		return
	}
	s.mu.RLock()
	chats := append([]string(nil), s.cfg.ChatIDs...)
	s.mu.RUnlock()
	photo = strings.TrimSpace(photo)
	for _, chat := range chats {
		if photo != "" {
			payload := map[string]any{
				"chat_id":    chat,
				"photo":      photo,
				"caption":    text,
				"parse_mode": "HTML",
			}
			if coverBlur {
				payload["has_spoiler"] = true
			}
			if _, err := s.callMethod(ctx, "sendPhoto", payload); err == nil {
				continue
			}
		}
		s.sendPersistentMessage(ctx, chat, text, nil)
	}
}

// SubscriptionChecked pushes a routine update-check summary：封面 + 作品名 +
// 作者 + 本次更新的章节标题列表。
func (s *Service) SubscriptionChecked(ctx context.Context, subscription model.Subscription, queued int, titles []string) {
	if !s.Enabled() || queued <= 0 {
		return
	}
	sourceNames := s.notificationSourceNames(ctx)
	text := renderSubscriptionChecked(subscription, sourceNames[subscription.SourceID], queued, titles)
	s.notifyPhotoAll(ctx, subscription.Cover, text, subscription.CoverBlur)
}

func (s *Service) BangumiMatched(ctx context.Context, notice BangumiMatchedNotice) {
	if !s.Enabled() {
		return
	}
	caption := renderBangumiMatchedNotice(notice)
	s.mu.RLock()
	chats := append([]string(nil), s.cfg.ChatIDs...)
	s.mu.RUnlock()
	for _, chat := range chats {
		if path := strings.TrimSpace(notice.CoverFile); path != "" {
			if err := s.sendPhotoFile(ctx, chat, path, caption, notice.CoverBlur); err == nil {
				continue
			}
		}
		if photo := telegramPhotoURL(notice.CoverURL); photo != "" {
			payload := map[string]any{
				"chat_id":    chat,
				"photo":      photo,
				"caption":    caption,
				"parse_mode": "HTML",
			}
			if notice.CoverBlur {
				payload["has_spoiler"] = true
			}
			if _, err := s.callMethod(ctx, "sendPhoto", payload); err == nil {
				continue
			}
		}
		s.sendPersistentMessage(ctx, chat, caption, nil)
	}
}

func renderBangumiMatchedNotice(notice BangumiMatchedNotice) string {
	title := fallback(strings.TrimSpace(notice.Title), "未命名作品")
	source := fallback(strings.TrimSpace(notice.Source), "未知内容源")
	lines := []string{
		"<b>Bangumi 匹配成功</b>",
		notificationWorkTitle(title, source),
		"内容源：" + html.EscapeString(source),
		"作者：" + html.EscapeString(fallback(strings.TrimSpace(notice.Author), "未知作者")),
	}
	originalTitle := strings.TrimSpace(notice.OriginalTitle)
	if originalTitle != "" && originalTitle != title {
		lines = append(lines, "Bangumi 名称："+html.EscapeString(originalTitle))
	}
	if status := strings.TrimSpace(notice.Status); status != "" {
		lines = append(lines, "作品状态："+html.EscapeString(status))
	}
	if description := cleanComicSummary(title, notice.Description); description != "" {
		lines = append(lines, "简介："+html.EscapeString(truncate(description, 180)))
	}
	return strings.Join(lines, "\n")
}

func renderSubscriptionChecked(subscription model.Subscription, sourceName string, queued int, titles []string) string {
	lines := []string{
		notificationWorkTitle(subscription.Title, sourceName),
		html.EscapeString(fallback(subscription.Author, "未知作者")),
		fmt.Sprintf("发现 %d 个新章节，已加入下载队列 · 例行检查：", queued),
	}
	const titleLimit = 10
	for index, title := range titles {
		if index >= titleLimit {
			lines = append(lines, fmt.Sprintf("… 其余 %d 章", len(titles)-titleLimit))
			break
		}
		lines = append(lines, "· "+title)
	}
	return strings.Join(lines, "\n")
}

// SubscriptionDownloadCompleted sends one summary after a new subscription's
// complete historical backlog has drained.
func (s *Service) SubscriptionDownloadCompleted(ctx context.Context, notice SubscriptionDownloadCompletedNotice) {
	if !s.Enabled() {
		return
	}
	caption := renderSubscriptionDownloadCompleted(notice)
	s.mu.RLock()
	chats := append([]string(nil), s.cfg.ChatIDs...)
	s.mu.RUnlock()
	for _, chat := range chats {
		if path := strings.TrimSpace(notice.CoverFile); path != "" {
			if err := s.sendPhotoFile(ctx, chat, path, caption, notice.CoverBlur); err == nil {
				continue
			}
		}
		if photo := strings.TrimSpace(notice.CoverURL); photo != "" {
			payload := map[string]any{
				"chat_id":    chat,
				"photo":      photo,
				"caption":    caption,
				"parse_mode": "HTML",
			}
			if notice.CoverBlur {
				payload["has_spoiler"] = true
			}
			if _, err := s.callMethod(ctx, "sendPhoto", payload); err == nil {
				continue
			}
		}
		s.sendPersistentMessage(ctx, chat, caption, nil)
	}
}

func (s *Service) SubscriptionArchiveWarning(ctx context.Context, notice SubscriptionArchiveNotice) {
	if !s.Enabled() {
		return
	}
	caption := renderSubscriptionArchiveWarning(notice)
	s.sendSubscriptionArchivePhoto(ctx, caption, notice)
}

func (s *Service) SubscriptionArchived(ctx context.Context, notice SubscriptionArchiveNotice) {
	if !s.Enabled() {
		return
	}
	caption := renderSubscriptionArchived(notice)
	s.sendSubscriptionArchivePhoto(ctx, caption, notice)
}

func (s *Service) sendSubscriptionArchivePhoto(ctx context.Context, caption string, notice SubscriptionArchiveNotice) {
	s.mu.RLock()
	chats := append([]string(nil), s.cfg.ChatIDs...)
	s.mu.RUnlock()
	for _, chat := range chats {
		if path := strings.TrimSpace(notice.CoverFile); path != "" {
			if err := s.sendPhotoFile(ctx, chat, path, caption, notice.CoverBlur); err == nil {
				continue
			}
		}
		if photo := telegramPhotoURL(notice.CoverURL); photo != "" {
			payload := map[string]any{
				"chat_id":    chat,
				"photo":      photo,
				"caption":    caption,
				"parse_mode": "HTML",
			}
			if notice.CoverBlur {
				payload["has_spoiler"] = true
			}
			if _, err := s.callMethod(ctx, "sendPhoto", payload); err == nil {
				continue
			}
		}
		s.sendPersistentMessage(ctx, chat, caption, nil)
	}
}

func renderSubscriptionArchiveWarning(notice SubscriptionArchiveNotice) string {
	title := fallback(strings.TrimSpace(notice.Title), "未命名作品")
	status := "未检测到完结状态"
	if notice.Completed {
		status = "已完结"
	}
	remaining := notice.DaysRemaining
	if remaining < 1 {
		remaining = 1
	}
	lines := []string{
		"<b>订阅归档预警</b>",
		notificationWorkTitle(title, notice.Source),
		"内容源：" + html.EscapeString(fallback(strings.TrimSpace(notice.Source), "未知")),
		"作者：" + html.EscapeString(fallback(strings.TrimSpace(notice.Author), "未知作者")),
		"章节总数：" + chapterCountNoticeLabel(notice.ChapterCount),
		"作品状态：" + html.EscapeString(status),
		fmt.Sprintf("已连续 %d 天未检测到新章节。", notice.DaysWithoutNew),
		fmt.Sprintf("预计 %d 天后自动归档。", remaining),
	}
	return strings.Join(lines, "\n")
}

func renderSubscriptionArchived(notice SubscriptionArchiveNotice) string {
	title := fallback(strings.TrimSpace(notice.Title), "未命名作品")
	threshold := notice.ThresholdDays
	if threshold <= 0 {
		threshold = 30
		if notice.Completed {
			threshold = 15
		}
	}
	reason := fmt.Sprintf("未检测到完结状态，连续 %d 天未检测到新章节。", threshold)
	if notice.Completed {
		reason = fmt.Sprintf("作品状态为完结，连续 %d 天未检测到新章节。", threshold)
	}
	lines := []string{
		"<b>订阅已归档</b>",
		notificationWorkTitle(title, notice.Source),
		"内容源：" + html.EscapeString(fallback(strings.TrimSpace(notice.Source), "未知")),
		"作者：" + html.EscapeString(fallback(strings.TrimSpace(notice.Author), "未知作者")),
		"章节总数：" + chapterCountNoticeLabel(notice.ChapterCount),
		"归档原因：" + html.EscapeString(reason),
		"已自动移入归档集。",
	}
	return strings.Join(lines, "\n")
}

func chapterCountNoticeLabel(count int) string {
	if count <= 0 {
		return "未知"
	}
	return fmt.Sprintf("%d 章", count)
}

// DownloadRetryScheduled sends one work-level notification after failed
// chapters have been put back into the queue.
func (s *Service) DownloadRetryScheduled(ctx context.Context, notice DownloadRetryNotice) {
	s.downloadRetryScheduledAt(ctx, notice, time.Now())
}

func (s *Service) downloadRetryScheduledAt(ctx context.Context, notice DownloadRetryNotice, now time.Time) {
	if !s.Enabled() || len(notice.Chapters) == 0 {
		return
	}
	// Serialize replacements so overlapping chapter completions cannot leave
	// multiple "latest" notifications for one work.
	s.retryMu.Lock()
	defer s.retryMu.Unlock()
	day := now.In(retryNoticeLocation).Format("2006-01-02")
	if s.retryNotices == nil {
		s.retryNotices = make(map[string]*dailyRetryNotice)
	}
	if !s.retryLoaded {
		s.retryLoaded = true
		s.mu.RLock()
		app := s.app
		s.mu.RUnlock()
		if stateStore, ok := app.(retryNoticeStateStore); ok {
			if raw, err := stateStore.BotRetryNoticeState(ctx); err == nil && strings.TrimSpace(raw) != "" {
				_ = json.Unmarshal([]byte(raw), &s.retryNotices)
			}
		}
		if s.retryNotices == nil {
			s.retryNotices = make(map[string]*dailyRetryNotice)
		}
	}
	for key, entry := range s.retryNotices {
		if entry == nil || entry.Day != day {
			delete(s.retryNotices, key)
		}
	}
	s.mu.RLock()
	chats := append([]string(nil), s.cfg.ChatIDs...)
	botID := fmt.Sprintf("%x", sha256.Sum256([]byte(s.cfg.Token)))
	s.mu.RUnlock()
	workKey := notice.SourceID + "\x00" + notice.ComicID
	if notice.SourceID == "" || notice.ComicID == "" {
		workKey = notice.Source + "\x00" + notice.Title
	}
	for _, chat := range chats {
		key := botID + "\x00" + chat + "\x00" + workKey
		state := s.retryNotices[key]
		if state == nil {
			state = &dailyRetryNotice{Day: day}
			s.retryNotices[key] = state
		}
		state.Rounds++
		state.Chapters += len(notice.Chapters)
		state.Recent = append(state.Recent, retryAttempt{
			At: now, Count: len(notice.Chapters),
			Chapters: append([]string(nil), notice.Chapters[:min(3, len(notice.Chapters))]...),
		})
		if len(state.Recent) > 3 {
			state.Recent = append([]retryAttempt(nil), state.Recent[len(state.Recent)-3:]...)
		}
		caption := renderDailyRetryNotice(notice, *state)
		messageID := int64(0)
		if path := strings.TrimSpace(notice.CoverFile); path != "" {
			messageID, _ = s.sendPhotoFileWithKeyboard(ctx, chat, path, caption, notice.CoverBlur, nil)
		}
		if photo := strings.TrimSpace(notice.CoverURL); messageID == 0 && photo != "" {
			payload := map[string]any{
				"chat_id":    chat,
				"photo":      photo,
				"caption":    caption,
				"parse_mode": "HTML",
			}
			if notice.CoverBlur {
				payload["has_spoiler"] = true
			}
			messageID, _ = s.callMethod(ctx, "sendPhoto", payload)
		}
		if messageID == 0 {
			messageID = s.sendPersistentMessage(ctx, chat, caption, nil)
		}
		if messageID != 0 {
			s.deleteMessage(ctx, chat, state.MessageID)
			state.MessageID = messageID
		}
	}
	s.mu.RLock()
	app := s.app
	s.mu.RUnlock()
	if stateStore, ok := app.(retryNoticeStateStore); ok {
		if raw, err := json.Marshal(s.retryNotices); err == nil {
			if err := stateStore.BotSetRetryNoticeState(ctx, string(raw)); err != nil {
				s.logger.Printf("telegram: save daily retry notices: %v", err)
			}
		}
	}
}

func renderDailyRetryNotice(notice DownloadRetryNotice, state dailyRetryNotice) string {
	lines := []string{
		notificationWorkTitle(notice.Title, notice.Source),
		"作者：" + html.EscapeString(fallback(strings.TrimSpace(notice.Author), "未知作者")),
		fmt.Sprintf("今日失败重试 · %d 次，累计 %d 章次", state.Rounds, state.Chapters),
	}
	for index := len(state.Recent) - 1; index >= 0; index-- {
		attempt := state.Recent[index]
		titles := make([]string, 0, len(attempt.Chapters))
		for _, title := range attempt.Chapters {
			titles = append(titles, truncate(strings.TrimSpace(title), 45))
		}
		line := fmt.Sprintf("· %s · %d 章：%s", attempt.At.In(retryNoticeLocation).Format("15:04"), attempt.Count, strings.Join(titles, "、"))
		if attempt.Count > len(titles) {
			line += fmt.Sprintf(" … 其余 %d 章", attempt.Count-len(titles))
		}
		lines = append(lines, html.EscapeString(line))
	}
	if state.Rounds > len(state.Recent) {
		lines = append(lines, fmt.Sprintf("… 省略更早 %d 次重试", state.Rounds-len(state.Recent)))
	}
	return strings.Join(lines, "\n")
}

func renderDownloadRetryNotice(notice DownloadRetryNotice) string {
	title := fallback(strings.TrimSpace(notice.Title), "未命名作品")
	lines := []string{
		notificationWorkTitle(title, notice.Source),
		"作者：" + html.EscapeString(fallback(strings.TrimSpace(notice.Author), "未知作者")),
		fmt.Sprintf("失败重试 · %d 个章节已重新加入下载队列：", len(notice.Chapters)),
	}
	const limit = 3
	for index, chapter := range notice.Chapters {
		if index >= limit {
			remaining := notice.Chapters[limit:]
			summary := fmt.Sprintf("… 其余 %d 章", len(remaining))
			first := strings.TrimSpace(remaining[0])
			last := strings.TrimSpace(remaining[len(remaining)-1])
			switch {
			case first != "" && last != "" && len(remaining) > 1 && first != last:
				summary += fmt.Sprintf("（%s 至 %s）", first, last)
			case first != "":
				summary += fmt.Sprintf("（%s）", first)
			}
			lines = append(lines, html.EscapeString(summary))
			break
		}
		lines = append(lines, "· "+html.EscapeString(fallback(strings.TrimSpace(chapter), "未命名章节")))
	}
	return strings.Join(lines, "\n")
}

func (s *Service) DownloadResumed(ctx context.Context, jobs []model.DownloadJob) {
	groups := groupDownloadJobs(jobs)
	if len(groups) == 0 {
		return
	}
	s.mu.RLock()
	app := s.app
	s.mu.RUnlock()
	sourceNames := s.notificationSourceNames(ctx)
	for _, group := range groups {
		if len(group) == 0 {
			continue
		}
		first := group[0]
		title := fallback(strings.TrimSpace(first.ComicTitle), "未命名作品")
		author := strings.TrimSpace(first.ComicAuthor)
		sourceName := sourceNames[first.SourceID]
		coverURL := first.ComicCover
		coverFile := ""
		coverBlur := false
		if app != nil {
			if visual, err := app.BotSubscriptionVisual(ctx, first.SourceID, first.ComicID); err == nil {
				title = fallback(strings.TrimSpace(visual.Title), title)
				author = strings.TrimSpace(visual.Author)
				sourceName = visual.Source
				coverURL = visual.CoverURL
				coverFile = visual.CoverFile
				coverBlur = visual.CoverBlur
			}
		}
		chapters := make([]string, 0, len(group))
		for _, job := range group {
			chapters = append(chapters, job.ChapterTitle)
		}
		s.sendDownloadResumedNotice(ctx, DownloadResumedNotice{
			Title:     title,
			Author:    author,
			Source:    sourceName,
			CoverURL:  coverURL,
			CoverFile: coverFile,
			CoverBlur: coverBlur,
			Chapters:  chapters,
		})
	}
}

type DownloadResumedNotice struct {
	Title     string
	Author    string
	Source    string
	CoverURL  string
	CoverFile string
	CoverBlur bool
	Chapters  []string
}

func groupDownloadJobs(jobs []model.DownloadJob) [][]model.DownloadJob {
	if len(jobs) == 0 {
		return nil
	}
	var groups [][]model.DownloadJob
	index := map[string]int{}
	for _, job := range jobs {
		key := job.SourceID + "\x00" + job.ComicID
		if position, ok := index[key]; ok {
			groups[position] = append(groups[position], job)
			continue
		}
		index[key] = len(groups)
		groups = append(groups, []model.DownloadJob{job})
	}
	return groups
}

func (s *Service) sendDownloadResumedNotice(ctx context.Context, notice DownloadResumedNotice) {
	caption := renderDownloadResumedNotice(notice)
	s.mu.RLock()
	chats := append([]string(nil), s.cfg.ChatIDs...)
	s.mu.RUnlock()
	for _, chat := range chats {
		if path := strings.TrimSpace(notice.CoverFile); path != "" {
			if err := s.sendPhotoFile(ctx, chat, path, caption, notice.CoverBlur); err == nil {
				continue
			}
		}
		if photo := strings.TrimSpace(notice.CoverURL); photo != "" {
			payload := map[string]any{
				"chat_id":    chat,
				"photo":      photo,
				"caption":    caption,
				"parse_mode": "HTML",
			}
			if notice.CoverBlur {
				payload["has_spoiler"] = true
			}
			if _, err := s.callMethod(ctx, "sendPhoto", payload); err == nil {
				continue
			}
		}
		s.sendPersistentMessage(ctx, chat, caption, nil)
	}
}

func renderDownloadResumedNotice(notice DownloadResumedNotice) string {
	title := fallback(strings.TrimSpace(notice.Title), "未命名作品")
	lines := []string{
		notificationWorkTitle(title, notice.Source),
		"作者：" + html.EscapeString(fallback(strings.TrimSpace(notice.Author), "未知作者")),
		fmt.Sprintf("Manco 已恢复任务下载 · %d 个章节已重新加入下载队列：", len(notice.Chapters)),
	}
	const limit = 5
	for index, chapter := range notice.Chapters {
		if index >= limit {
			lines = append(lines, fmt.Sprintf("… 其余 %d 章", len(notice.Chapters)-limit))
			break
		}
		lines = append(lines, "· "+html.EscapeString(fallback(strings.TrimSpace(chapter), "未命名章节")))
	}
	return strings.Join(lines, "\n")
}

func renderSubscriptionDownloadCompleted(notice SubscriptionDownloadCompletedNotice) string {
	title := fallback(strings.TrimSpace(notice.Title), "未命名作品")
	lines := []string{
		notificationWorkTitle(title, notice.Source),
		"作者：" + html.EscapeString(fallback(strings.TrimSpace(notice.Author), "未知作者")),
	}
	description := cleanComicSummary(title, notice.Description)
	if description != "" {
		lines = append(lines, "简介："+html.EscapeString(truncate(description, 160)))
	}
	lines = append(lines, fmt.Sprintf("历史章节下载完成：成功 %d 章", notice.Succeeded))
	if notice.Failed > 0 {
		lines = append(lines, fmt.Sprintf("下载失败：%d 章", notice.Failed))
	}
	lines = append(lines, "下次检查更新："+html.EscapeString(fallback(strings.TrimSpace(notice.NextCheckLabel), "未启用自动追更")))
	return strings.Join(lines, "\n")
}

func (s *Service) notificationSourceNames(ctx context.Context) map[string]string {
	s.mu.RLock()
	app := s.app
	s.mu.RUnlock()
	result := map[string]string{}
	if app == nil {
		return result
	}
	sources, err := app.BotSources(ctx)
	if err != nil {
		return result
	}
	for _, item := range sources {
		result[item.ID] = item.Name
	}
	return result
}

func notificationWorkTitle(title, sourceName string) string {
	title = fallback(strings.TrimSpace(title), "未命名作品")
	sourceName = fallback(strings.TrimSpace(sourceName), "未知")
	return fmt.Sprintf(
		"<b>%s（%s）</b>",
		html.EscapeString(title),
		html.EscapeString(shortSourceName(sourceName)),
	)
}

func (s *Service) sendPhotoFile(ctx context.Context, chat, path, caption string, coverBlur bool) error {
	_, err := s.sendPhotoFileWithKeyboard(ctx, chat, path, caption, coverBlur, nil)
	return err
}

func (s *Service) sendPhotoFileWithKeyboard(
	ctx context.Context,
	chat, path, caption string,
	coverBlur bool,
	keyboard any,
) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("chat_id", chat); err != nil {
		return 0, err
	}
	if err := writer.WriteField("caption", caption); err != nil {
		return 0, err
	}
	if err := writer.WriteField("parse_mode", "HTML"); err != nil {
		return 0, err
	}
	if coverBlur {
		if err := writer.WriteField("has_spoiler", "true"); err != nil {
			return 0, err
		}
	}
	if keyboard != nil {
		rawKeyboard, err := json.Marshal(keyboard)
		if err != nil {
			return 0, err
		}
		if err := writer.WriteField("reply_markup", string(rawKeyboard)); err != nil {
			return 0, err
		}
	}
	part, err := writer.CreateFormFile("photo", filepath.Base(path))
	if err != nil {
		return 0, err
	}
	if _, err := io.Copy(part, file); err != nil {
		return 0, err
	}
	if err := writer.Close(); err != nil {
		return 0, err
	}
	s.mu.RLock()
	token := s.cfg.Token
	s.mu.RUnlock()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", token), &body)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := s.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, err
	}
	var envelope struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return 0, err
	}
	if !envelope.OK {
		return 0, fmt.Errorf("telegram sendPhoto: %s", strings.TrimSpace(envelope.Description))
	}
	return envelope.Result.MessageID, nil
}

// DownloadSummary pushes an aggregated download result for one work.
func (s *Service) DownloadSummary(ctx context.Context, title string, succeeded, failed int, sourceNames ...string) {
	if !s.Enabled() {
		return
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "未命名作品"
	}
	sourceName := ""
	if len(sourceNames) > 0 {
		sourceName = strings.TrimSpace(sourceNames[0])
	}
	sourceLabel := shortSourceName(fallback(sourceName, "未知"))
	workTitle := fmt.Sprintf("《%s》（%s）", title, sourceLabel)
	switch {
	case failed > 0 && succeeded > 0:
		s.NotifyAll(ctx, fmt.Sprintf("%s下载完成 %d 章，失败 %d 章。", workTitle, succeeded, failed))
	case failed > 0:
		s.NotifyAll(ctx, fmt.Sprintf("%s有 %d 章下载失败，可在任务列表重试。", workTitle, failed))
	case succeeded > 0:
		s.NotifyAll(ctx, fmt.Sprintf("%s已成功下载 %d 章。", workTitle, succeeded))
	}
}
