package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/hyaeve/manco/internal/model"
)

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
