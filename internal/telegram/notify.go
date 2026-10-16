package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/hyaeve/manco/internal/model"
)

// SubscriptionChecked pushes a routine update-check summary.
func (s *Service) SubscriptionChecked(ctx context.Context, subscription model.Subscription, queued int) {
	if !s.Enabled() || queued <= 0 {
		return
	}
	text := fmt.Sprintf("例行检查：《%s》发现 %d 个新章节，已加入下载队列。", strings.TrimSpace(subscription.Title), queued)
	s.NotifyAll(ctx, text)
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
