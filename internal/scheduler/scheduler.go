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
	EnsureSeriesMetadata(ctx context.Context, subscription model.Subscription) (bool, error)
	RetryFailedByComic(ctx context.Context, sourceID, comicID string) (int, error)
	Notify()
}

type Subscriptions interface {
	ListSubscriptions(ctx context.Context) ([]model.Subscription, error)
	Subscription(ctx context.Context, id int64) (model.Subscription, error)
	UpdateSubscriptionCheck(ctx context.Context, id int64, chapterID, chapterTitle string, chapterOrder float64, comicStatus string, completed, newChapter bool) error
	UpdateSubscriptionChapterCount(ctx context.Context, id int64, count int) error
}

// Notifier receives routine check results so an external service (for example
// the Telegram bot) can push summaries. It is optional.
type Notifier interface {
	SubscriptionChecked(ctx context.Context, subscription model.Subscription, queued int, titles []string)
}

// SubscriptionCheckResult reports what one subscription check discovered.
type SubscriptionCheckResult struct {
	NewChapters int
	Queued      int
}

type Scheduler struct {
	registry      *sources.Registry
	subscriptions Subscriptions
	downloads     Downloads
	intervalNanos atomic.Int64
	logger        *log.Logger
	notifier      Notifier
}

// SetNotifier installs a routine check notifier.
func (s *Scheduler) SetNotifier(notifier Notifier) {
	s.notifier = notifier
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
		if _, err := s.check(ctx, subscription); err != nil {
			s.logger.Printf("scheduler: %s/%s: %v", subscription.SourceID, subscription.Title, err)
		}
	}
}

func (s *Scheduler) check(ctx context.Context, subscription model.Subscription) (SubscriptionCheckResult, error) {
	result := SubscriptionCheckResult{}
	item, err := s.registry.Get(subscription.SourceID)
	if err != nil {
		return result, err
	}
	account, err := s.registry.Account(ctx, subscription.SourceID)
	if err != nil {
		return result, err
	}
	// Repair series-level cover/metadata if a previous download lost them,
	// so subscriptions self-heal on every scheduled or manual check.
	if repaired, metaErr := s.downloads.EnsureSeriesMetadata(ctx, subscription); metaErr != nil {
		s.logger.Printf("scheduler: metadata %s/%s: %v", subscription.SourceID, subscription.Title, metaErr)
	} else if repaired {
		s.logger.Printf("scheduler: re-fetched series metadata for %s/%s", subscription.SourceID, subscription.Title)
	}
	chapters, err := item.Chapters(ctx, account, subscription.ComicID)
	if err != nil {
		return result, err
	}
	comicStatus := subscription.ComicStatus
	if detail, detailErr := item.Detail(ctx, account, subscription.ComicID); detailErr == nil && strings.TrimSpace(detail.Status) != "" {
		comicStatus = strings.TrimSpace(detail.Status)
	}
	if len(chapters) == 0 {
		return result, s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, subscription.LastChapterID, subscription.LastChapterTitle, subscription.LastChapterOrder, comicStatus, completedStatus(chapters, comicStatus), false)
	}
	newest := chapters[len(chapters)-1]
	// 每次成功拉到章节列表都刷新总章节数，供订阅卡片的「共 N 话」使用。
	if err := s.subscriptions.UpdateSubscriptionChapterCount(ctx, subscription.ID, len(chapters)); err != nil {
		return result, err
	}
	if subscription.LastChapterID == "" {
		// First observation only records a baseline.
		return result, s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, newest.ID, newest.Title, newest.Order, comicStatus, completedStatus(chapters, comicStatus), false)
	}
	queuedTitles := make([]string, 0, 4)
	for _, chapter := range chapters {
		if !isNewer(chapter, subscription) {
			continue
		}
		result.NewChapters++
		if subscription.AutoDownload {
			_, err := s.downloads.CreateDownloadJob(ctx, model.DownloadJob{
				SourceID:            subscription.SourceID,
				ComicID:             subscription.ComicID,
				ComicTitle:          subscriptionSeriesDir(subscription),
				ComicCover:          subscription.Cover,
				ComicAuthor:         subscription.Author,
				ChapterID:           chapter.ID,
				ChapterTitle:        chapter.Title,
				ChapterOrder:        chapter.Order,
				DownloadDir:         subscription.DownloadDir,
				ConvertToSimplified: subscription.ConvertToSimplified,
				ChapterMarker:       subscription.ChapterMarker,
			})
			if err != nil {
				s.logger.Printf("scheduler: queue %s/%s: %v", subscription.Title, chapter.Title, err)
				continue
			}
			result.Queued++
			title := strings.TrimSpace(chapter.Title)
			if title == "" {
				title = chapter.ID
			}
			queuedTitles = append(queuedTitles, title)
		}
	}
	if result.Queued > 0 {
		s.downloads.Notify()
		if s.notifier != nil {
			s.notifier.SubscriptionChecked(ctx, subscription, result.Queued, queuedTitles)
		}
	}
	if isNewer(newest, subscription) {
		return result, s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, newest.ID, newest.Title, newest.Order, comicStatus, completedStatus(chapters, comicStatus), true)
	}
	return result, s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, subscription.LastChapterID, subscription.LastChapterTitle, subscription.LastChapterOrder, comicStatus, completedStatus(chapters, comicStatus), false)
}

func subscriptionSeriesDir(subscription model.Subscription) string {
	if value := strings.TrimSpace(subscription.SeriesDir); value != "" {
		return value
	}
	return subscription.Title
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

// completedStatus reports whether the source status or newest chapter title
// marks the comic as finished, so the subscription can be archived later.
func completedStatus(chapters []model.Chapter, comicStatus string) bool {
	if completedText(comicStatus) {
		return true
	}
	if len(chapters) == 0 {
		return false
	}
	title := strings.ToLower(chapters[len(chapters)-1].Title)
	return completedText(title)
}

func completedText(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, marker := range []string{"完结", "已完结", "全本", "终章", "[完]", "（完）", "(完)"} {
		if strings.Contains(value, marker) {
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
		ComicTitle:          subscriptionSeriesDir(subscription),
		ComicCover:          subscription.Cover,
		ComicAuthor:         subscription.Author,
		ChapterID:           newest.ID,
		ChapterTitle:        newest.Title,
		ChapterOrder:        newest.Order,
		DownloadDir:         subscription.DownloadDir,
		ConvertToSimplified: subscription.ConvertToSimplified,
		ChapterMarker:       subscription.ChapterMarker,
	})
	if err != nil {
		return model.DownloadJob{}, err
	}
	if err := s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, newest.ID, newest.Title, newest.Order, subscription.ComicStatus, completedStatus(chapters, subscription.ComicStatus), true); err != nil {
		s.logger.Printf("scheduler: update manual download baseline %s/%s: %v", subscription.SourceID, subscription.Title, err)
	}
	s.downloads.Notify()
	return job, nil
}

// QueueAll queues every chapter in a subscription. Completed chapters are
// returned as skipped by the store and retain their existing files, so this is
// safe to use both for initial full subscriptions and later full refreshes.
func (s *Scheduler) QueueAll(ctx context.Context, subscription model.Subscription) (queued, skipped int, err error) {
	item, err := s.registry.Get(subscription.SourceID)
	if err != nil {
		return 0, 0, err
	}
	account, err := s.registry.Account(ctx, subscription.SourceID)
	if err != nil {
		return 0, 0, err
	}
	chapters, err := item.Chapters(ctx, account, subscription.ComicID)
	if err != nil {
		return 0, 0, err
	}
	if len(chapters) == 0 {
		return 0, 0, ErrNoChapters
	}
	for _, chapter := range chapters {
		if strings.TrimSpace(chapter.ID) == "" {
			continue
		}
		job, createErr := s.downloads.CreateDownloadJob(ctx, model.DownloadJob{
			SourceID:            subscription.SourceID,
			ComicID:             subscription.ComicID,
			ComicTitle:          subscriptionSeriesDir(subscription),
			ComicCover:          subscription.Cover,
			ComicAuthor:         subscription.Author,
			ChapterID:           chapter.ID,
			ChapterTitle:        chapter.Title,
			ChapterOrder:        chapter.Order,
			DownloadDir:         subscription.DownloadDir,
			ConvertToSimplified: subscription.ConvertToSimplified,
			ChapterMarker:       subscription.ChapterMarker,
		})
		if createErr != nil {
			return queued, skipped, createErr
		}
		switch job.Status {
		case "completed", "running":
			skipped++
		default:
			queued++
		}
	}
	newest := chapters[len(chapters)-1]
	if err := s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, newest.ID, newest.Title, newest.Order, subscription.ComicStatus, completedStatus(chapters, subscription.ComicStatus), true); err != nil {
		s.logger.Printf("scheduler: update full download baseline %s/%s: %v", subscription.SourceID, subscription.Title, err)
	}
	s.downloads.Notify()
	return queued, skipped, nil
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
		if _, err := s.check(ctx, subscription); err != nil {
			s.logger.Printf("scheduler: %s/%s: %v", subscription.SourceID, subscription.Title, err)
		}
		if retried, err := s.downloads.RetryFailedByComic(ctx, subscription.SourceID, subscription.ComicID); err != nil {
			s.logger.Printf("scheduler: retry failed downloads %s/%s: %v", subscription.SourceID, subscription.Title, err)
		} else if retried > 0 {
			s.logger.Printf("scheduler: retried %d failed download(s) for %s/%s", retried, subscription.SourceID, subscription.Title)
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
	due, err := cronutil.Due(subscription.CronExpr, base, now)
	if err != nil {
		s.logger.Printf("scheduler: invalid cron for %s/%s: %v", subscription.SourceID, subscription.Title, err)
		return false
	}
	return due
}

// CheckOne checks a single subscription immediately.
func (s *Scheduler) CheckOne(ctx context.Context, id int64) error {
	_, err := s.CheckOneDetailed(ctx, id)
	return err
}

// CheckOneDetailed checks one subscription and returns how many new chapters
// were discovered and queued.
func (s *Scheduler) CheckOneDetailed(ctx context.Context, id int64) (SubscriptionCheckResult, error) {
	subscription, err := s.subscriptions.Subscription(ctx, id)
	if err != nil {
		return SubscriptionCheckResult{}, err
	}
	return s.check(ctx, subscription)
}
