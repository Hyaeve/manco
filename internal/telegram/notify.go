package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hyaeve/manco/internal/model"
)

type SubscriptionDownloadCompletedNotice struct {
	Title          string
	Author         string
	Description    string
	CoverURL       string
	CoverFile      string
	Succeeded      int
	Failed         int
	NextCheckLabel string
}

// notifyPhotoAll 给每个配置的会话发一条带图通知；封面取不到就退回纯文字。
func (s *Service) notifyPhotoAll(ctx context.Context, photo, text string) {
	if !s.Enabled() || strings.TrimSpace(text) == "" {
		return
	}
	s.mu.RLock()
	chats := append([]string(nil), s.cfg.ChatIDs...)
	s.mu.RUnlock()
	photo = strings.TrimSpace(photo)
	for _, chat := range chats {
		if photo != "" {
			if _, err := s.callMethod(ctx, "sendPhoto", map[string]any{
				"chat_id":    chat,
				"photo":      photo,
				"caption":    text,
				"parse_mode": "HTML",
			}); err == nil {
				continue
			}
		}
		s.sendMessage(ctx, chat, text, nil)
	}
}

// SubscriptionChecked pushes a routine update-check summary：封面 + 作品名 +
// 作者 + 本次更新的章节标题列表。
func (s *Service) SubscriptionChecked(ctx context.Context, subscription model.Subscription, queued int, titles []string) {
	if !s.Enabled() || queued <= 0 {
		return
	}
	lines := []string{
		fmt.Sprintf("<b>%s</b>", fallback(strings.TrimSpace(subscription.Title), "未命名作品")),
		fmt.Sprintf("%s · 例行检查", fallback(subscription.Author, "未知作者")),
		fmt.Sprintf("发现 %d 个新章节，已加入下载队列：", queued),
	}
	const titleLimit = 10
	for index, title := range titles {
		if index >= titleLimit {
			lines = append(lines, fmt.Sprintf("… 其余 %d 章", len(titles)-titleLimit))
			break
		}
		lines = append(lines, "· "+title)
	}
	s.notifyPhotoAll(ctx, subscription.Cover, strings.Join(lines, "\n"))
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
			if err := s.sendPhotoFile(ctx, chat, path, caption); err == nil {
				continue
			}
		}
		if photo := strings.TrimSpace(notice.CoverURL); photo != "" {
			if _, err := s.callMethod(ctx, "sendPhoto", map[string]any{
				"chat_id":    chat,
				"photo":      photo,
				"caption":    caption,
				"parse_mode": "HTML",
			}); err == nil {
				continue
			}
		}
		s.sendMessage(ctx, chat, caption, nil)
	}
}

func (s *Service) DownloadResumed(ctx context.Context, jobs []model.DownloadJob) {
	text := renderDownloadResumed(jobs)
	if text == "" {
		return
	}
	s.NotifyAll(ctx, text)
}

func renderDownloadResumed(jobs []model.DownloadJob) string {
	if len(jobs) == 0 {
		return ""
	}
	lines := []string{
		"<b>Manco 已恢复任务下载</b>",
		"正在下载：",
	}
	const limit = 20
	for index, job := range jobs {
		if index >= limit {
			lines = append(lines, fmt.Sprintf("… 另有 %d 个中断任务已恢复", len(jobs)-limit))
			break
		}
		title := fallback(strings.TrimSpace(job.ComicTitle), "未命名作品")
		chapter := fallback(strings.TrimSpace(job.ChapterTitle), "未命名章节")
		lines = append(lines, fmt.Sprintf(
			"%d. 《%s》· %s",
			index+1,
			html.EscapeString(title),
			html.EscapeString(chapter),
		))
	}
	return strings.Join(lines, "\n")
}

func renderSubscriptionDownloadCompleted(notice SubscriptionDownloadCompletedNotice) string {
	title := fallback(strings.TrimSpace(notice.Title), "未命名作品")
	lines := []string{
		fmt.Sprintf("<b>%s</b>", html.EscapeString(title)),
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

func (s *Service) sendPhotoFile(ctx context.Context, chat, path, caption string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("chat_id", chat); err != nil {
		return err
	}
	if err := writer.WriteField("caption", caption); err != nil {
		return err
	}
	if err := writer.WriteField("parse_mode", "HTML"); err != nil {
		return err
	}
	part, err := writer.CreateFormFile("photo", filepath.Base(path))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	s.mu.RLock()
	token := s.cfg.Token
	s.mu.RUnlock()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", token), &body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	var envelope struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	if !envelope.OK {
		return fmt.Errorf("telegram sendPhoto: %s", strings.TrimSpace(envelope.Description))
	}
	return nil
}

// DownloadSummary pushes an aggregated download result for one work.
func (s *Service) DownloadSummary(ctx context.Context, title string, succeeded, failed int) {
	if !s.Enabled() {
		return
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "未命名作品"
	}
	switch {
	case failed > 0 && succeeded > 0:
		s.NotifyAll(ctx, fmt.Sprintf("《%s》下载完成 %d 章，失败 %d 章。", title, succeeded, failed))
	case failed > 0:
		s.NotifyAll(ctx, fmt.Sprintf("《%s》有 %d 章下载失败，可在任务列表重试。", title, failed))
	case succeeded > 0:
		s.NotifyAll(ctx, fmt.Sprintf("《%s》已成功下载 %d 章。", title, succeeded))
	}
}
