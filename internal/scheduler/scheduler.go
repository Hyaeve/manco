// Package scheduler periodically checks enabled subscriptions and queues new
// chapters for download.
package scheduler

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hyaeve/manco/internal/cronutil"
	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/sources"
)

type Downloads interface {
	CreateDownloadJob(ctx context.Context, job model.DownloadJob) (model.DownloadJob, error)
	Notify()
}

type Subscriptions interface {
	ListSubscriptions(ctx context.Context) ([]model.Subscription, error)
	Subscription(ctx context.Context, id int64) (model.Subscription, error)
	UpdateSubscriptionCheck(ctx context.Context, id int64, chapterID, chapterTitle string, chapterOrder float64, comicStatus string, completed, newChapter bool) error
}

type Scheduler struct {
	registry      *sources.Registry
	subscriptions Subscriptions
	downloads     Downloads
	intervalNanos atomic.Int64
	logger        *log.Logger
}

func New(registry *sources.Registry, subscriptions Subscriptions, downloads Downloads, interval time.Duration, logger *log.Logger) *Scheduler {
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	if logger == nil {
		logger = log.Default()
	}
	scheduler := &Scheduler{
		registry:      registry,
		subscriptions: subscriptions,
		downloads:     downloads,
		logger:        logger,
	}
	scheduler.intervalNanos.Store(int64(interval))
	return scheduler
}

// SetInterval updates the scan interval. The next timer picks up the new
// value, so changes from the settings page do not require a restart.
func (s *Scheduler) SetInterval(interval time.Duration) {
	if interval <= 0 {
		return
	}
	s.intervalNanos.Store(int64(interval))
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
		s.RunDue(ctx)
		// A one-minute ticker lets per-subscription cron expressions run close
		// to their scheduled minute without a second scheduled job store.
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			s.RunDue(ctx)
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
		return s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, subscription.LastChapterID, subscription.LastChapterTitle, subscription.LastChapterOrder, subscription.ComicStatus, false, false)
	}
	newest := chapters[len(chapters)-1]
	if subscription.LastChapterID == "" {
		// First observation only records a baseline.
		return s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, newest.ID, newest.Title, newest.Order, subscription.ComicStatus, completedStatus(chapters), false)
	}
	if subscription.AutoDownload {
		for _, chapter := range chapters {
			if !isNewer(chapter, subscription) {
				continue
			}
			_, err := s.downloads.CreateDownloadJob(ctx, model.DownloadJob{
				SourceID:            subscription.SourceID,
				ComicID:             subscription.ComicID,
				ComicTitle:          subscription.Title,
				ComicCover:          subscription.Cover,
				ChapterID:           chapter.ID,
				ChapterTitle:        chapter.Title,
				ChapterOrder:        chapter.Order,
				DownloadDir:         subscription.DownloadDir,
				ConvertToSimplified: subscription.ConvertToSimplified,
			})
			if err != nil {
				s.logger.Printf("scheduler: queue %s/%s: %v", subscription.Title, chapter.Title, err)
			}
		}
		s.downloads.Notify()
	}
	if isNewer(newest, subscription) {
		return s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, newest.ID, newest.Title, newest.Order, subscription.ComicStatus, completedStatus(chapters), true)
	}
	return s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, subscription.LastChapterID, subscription.LastChapterTitle, subscription.LastChapterOrder, subscription.ComicStatus, completedStatus(chapters), false)
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

// completedStatus reports whether the newest chapter title marks the comic as
// finished, so the subscription can be archived after a grace period.
func completedStatus(chapters []model.Chapter) bool {
	if len(chapters) == 0 {
		return false
	}
	title := strings.ToLower(chapters[len(chapters)-1].Title)
	for _, marker := range []string{"完结", "已完结", "全本", "终章", "[完]", "（完）", "(完)"} {
		if strings.Contains(title, marker) {
			return true
		}
	}
	return false
}

// ErrNoChapters reports that a subscribed comic has no chapters to queue.
var ErrNoChapters = errors.New("订阅作品没有可下载章节")

// QueueLatest queues the newest chapter for one subscription even when auto
// download is disabled. It also advances the subscription baseline so the
// scheduled checker does not report the same chapter as newly published.
func (s *Scheduler) QueueLatest(ctx context.Context, subscription model.Subscription) (model.DownloadJob, error) {
	item, err := s.registry.Get(subscription.SourceID)
	if err != nil {
		return model.DownloadJob{}, err
	}
	account, err := s.registry.Account(ctx, subscription.SourceID)
	if err != nil {
		return model.DownloadJob{}, err
	}
	chapters, err := item.Chapters(ctx, account, subscription.ComicID)
	if err != nil {
		return model.DownloadJob{}, err
	}
	if len(chapters) == 0 {
		return model.DownloadJob{}, ErrNoChapters
	}
	newest := chapters[len(chapters)-1]
	job, err := s.downloads.CreateDownloadJob(ctx, model.DownloadJob{
		SourceID:            subscription.SourceID,
		ComicID:             subscription.ComicID,
		ComicTitle:          subscription.Title,
		ComicCover:          subscription.Cover,
		ChapterID:           newest.ID,
		ChapterTitle:        newest.Title,
		ChapterOrder:        newest.Order,
		DownloadDir:         subscription.DownloadDir,
		ConvertToSimplified: subscription.ConvertToSimplified,
	})
	if err != nil {
		return model.DownloadJob{}, err
	}
	if err := s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, newest.ID, newest.Title, newest.Order, subscription.ComicStatus, completedStatus(chapters), true); err != nil {
		s.logger.Printf("scheduler: update manual download baseline %s/%s: %v", subscription.SourceID, subscription.Title, err)
	}
	s.downloads.Notify()
	return job, nil
}

// RunDue checks subscriptions whose weekly cron schedule has come due.
func (s *Scheduler) RunDue(ctx context.Context) {
	subscriptions, err := s.subscriptions.ListSubscriptions(ctx)
	if err != nil {
		s.logger.Printf("scheduler: list subscriptions: %v", err)
		return
	}
	now := time.Now()
	for _, subscription := range subscriptions {
		if !subscription.Enabled || !s.due(subscription, now) {
			continue
		}
		if err := s.check(ctx, subscription); err != nil {
			s.logger.Printf("scheduler: %s/%s: %v", subscription.SourceID, subscription.Title, err)
		}
	}
}

func (s *Scheduler) due(subscription model.Subscription, now time.Time) bool {
	base := subscription.CreatedAt
	if subscription.LastCheckedAt != nil && !subscription.LastCheckedAt.IsZero() {
		base = *subscription.LastCheckedAt
	}
	if base.IsZero() {
		return false
	}
	due, err := cronutil.Due(subscription.CronExpr, base, now, time.Duration(s.intervalNanos.Load()))
	if err != nil {
		s.logger.Printf("scheduler: invalid cron for %s/%s: %v", subscription.SourceID, subscription.Title, err)
		return false
	}
	return due
}

// CheckOne checks a single subscription immediately.
func (s *Scheduler) CheckOne(ctx context.Context, id int64) error {
	subscription, err := s.subscriptions.Subscription(ctx, id)
	if err != nil {
		return err
	}
	return s.check(ctx, subscription)
}
