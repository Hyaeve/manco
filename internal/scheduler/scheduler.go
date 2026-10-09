// Package scheduler periodically checks enabled subscriptions and queues new
// chapters for download.
package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/sources"
)

type Downloads interface {
	CreateDownloadJob(ctx context.Context, job model.DownloadJob) (model.DownloadJob, error)
	Notify()
}

type Subscriptions interface {
	ListSubscriptions(ctx context.Context) ([]model.Subscription, error)
	UpdateSubscriptionCheck(ctx context.Context, id int64, chapterID, chapterTitle string, chapterOrder float64) error
}

type Scheduler struct {
	registry      *sources.Registry
	subscriptions Subscriptions
	downloads     Downloads
	interval      time.Duration
	logger        *log.Logger
}

func New(registry *sources.Registry, subscriptions Subscriptions, downloads Downloads, interval time.Duration, logger *log.Logger) *Scheduler {
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Scheduler{
		registry:      registry,
		subscriptions: subscriptions,
		downloads:     downloads,
		interval:      interval,
		logger:        logger,
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	go func() {
		// Delay the first scan so the HTTP server becomes responsive quickly.
		timer := time.NewTimer(15 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		s.RunOnce(ctx)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.RunOnce(ctx)
			}
		}
	}()
}

// RunOnce checks every enabled subscription and queues newly discovered
// chapters. When a subscription has never been checked, it records the current
// newest chapter without downloading the entire backlog.
func (s *Scheduler) RunOnce(ctx context.Context) {
	subscriptions, err := s.subscriptions.ListSubscriptions(ctx)
	if err != nil {
		s.logger.Printf("scheduler: list subscriptions: %v", err)
		return
	}
	for _, subscription := range subscriptions {
		if !subscription.Enabled {
			continue
		}
		if err := s.check(ctx, subscription); err != nil {
			s.logger.Printf("scheduler: %s/%s: %v", subscription.SourceID, subscription.Title, err)
		}
	}
}

func (s *Scheduler) check(ctx context.Context, subscription model.Subscription) error {
	item, err := s.registry.Get(subscription.SourceID)
	if err != nil {
		return err
	}
	account, err := s.registry.Account(ctx, subscription.SourceID)
	if err != nil {
		return err
	}
	chapters, err := item.Chapters(ctx, account, subscription.ComicID)
	if err != nil {
		return err
	}
	if len(chapters) == 0 {
		return s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, subscription.LastChapterID, subscription.LastChapterTitle, subscription.LastChapterOrder)
	}
	newest := chapters[len(chapters)-1]
	if subscription.LastChapterID == "" {
		// First observation only records a baseline.
		return s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, newest.ID, newest.Title, newest.Order)
	}
	if subscription.AutoDownload {
		for _, chapter := range chapters {
			if !isNewer(chapter, subscription) {
				continue
			}
			_, err := s.downloads.CreateDownloadJob(ctx, model.DownloadJob{
				SourceID:     subscription.SourceID,
				ComicID:      subscription.ComicID,
				ComicTitle:   subscription.Title,
				ComicCover:   subscription.Cover,
				ChapterID:    chapter.ID,
				ChapterTitle: chapter.Title,
				ChapterOrder: chapter.Order,
			})
			if err != nil {
				s.logger.Printf("scheduler: queue %s/%s: %v", subscription.Title, chapter.Title, err)
			}
		}
		s.downloads.Notify()
	}
	if isNewer(newest, subscription) {
		return s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, newest.ID, newest.Title, newest.Order)
	}
	return s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, subscription.LastChapterID, subscription.LastChapterTitle, subscription.LastChapterOrder)
}

func isNewer(chapter model.Chapter, subscription model.Subscription) bool {
	if chapter.ID == subscription.LastChapterID {
		return false
	}
	if chapter.Order > subscription.LastChapterOrder {
		return true
	}
	return chapter.Order == subscription.LastChapterOrder && chapter.ID > subscription.LastChapterID
}
