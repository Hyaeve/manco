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
	"github.com/hyaeve/manco/internal/subscriptionstate"
)

type Downloads interface {
	CreateDownloadJob(ctx context.Context, job model.DownloadJob) (model.DownloadJob, error)
	EnsureSeriesMetadata(ctx context.Context, subscription model.Subscription) (bool, error)
	RetryFailedByComic(ctx context.Context, sourceID, comicID string, queuePriority int) (int, error)
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

// Matcher resolves provisional subscriptions such as Bangumi entries to a
// concrete executable content source.
type Matcher interface {
	MatchSubscription(ctx context.Context, subscription model.Subscription) (model.Subscription, bool, error)
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
	matcher       Matcher
	afterUpdate   func(context.Context)
}

// SetNotifier installs a routine check notifier.
func (s *Scheduler) SetNotifier(notifier Notifier) {
	s.notifier = notifier
}

func (s *Scheduler) SetMatcher(matcher Matcher) {
	s.matcher = matcher
}

// SetAfterUpdate installs a callback used to refresh the mounted config
// snapshot after scheduled checks changed subscription state. It runs once per
// scan cycle, not after every individual subscription.
func (s *Scheduler) SetAfterUpdate(callback func(context.Context)) {
	s.afterUpdate = callback
}

func (s *Scheduler) runAfterUpdate(ctx context.Context) {
	if s.afterUpdate != nil {
		s.afterUpdate(ctx)
	}
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
	changed := false
	for _, subscription := range subscriptions {
		if !subscription.Enabled {
			continue
		}
		if _, err := s.check(ctx, subscription, model.QueuePriorityNormal); err != nil {
			s.logger.Printf("scheduler: %s/%s: %v", subscription.SourceID, subscription.Title, err)
		} else {
			changed = true
		}
	}
	if changed {
		s.runAfterUpdate(ctx)
	}
}

func (s *Scheduler) check(ctx context.Context, subscription model.Subscription, queuePriority int) (SubscriptionCheckResult, error) {
	result := SubscriptionCheckResult{}
	if strings.EqualFold(strings.TrimSpace(subscription.SourceID), "bangumi") {
		if s.matcher == nil {
			return result, errors.New("Bangumi 内容源匹配器不可用")
		}
		matched, ok, err := s.matcher.MatchSubscription(ctx, subscription)
		if err != nil {
			return result, err
		}
		if !ok {
			return result, nil
		}
		subscription = matched
	}
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
		return result, s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, subscription.LastChapterID, subscription.LastChapterTitle, subscription.LastChapterOrder, comicStatus, completedStatus(comicStatus), false)
	}
	newest := chapters[len(chapters)-1]
	// 每次成功拉到章节列表都刷新总章节数，供订阅卡片的「共 N 话」使用。
	if err := s.subscriptions.UpdateSubscriptionChapterCount(ctx, subscription.ID, len(chapters)); err != nil {
		return result, err
	}
	if subscription.LastChapterID == "" {
		// First observation only records a baseline.
		return result, s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, newest.ID, newest.Title, newest.Order, comicStatus, completedStatus(comicStatus), false)
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
				QueuePriority:       queuePriority,
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
		return result, s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, newest.ID, newest.Title, newest.Order, comicStatus, completedStatus(comicStatus), true)
	}
	return result, s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, subscription.LastChapterID, subscription.LastChapterTitle, subscription.LastChapterOrder, comicStatus, completedStatus(comicStatus), false)
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

// completedStatus only trusts the source-provided serialization status.
func completedStatus(comicStatus string) bool {
	return subscriptionstate.IsCompleted(comicStatus)
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
		QueuePriority:       model.QueuePriorityManual,
	})
	if err != nil {
		return model.DownloadJob{}, err
	}
	if err := s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, newest.ID, newest.Title, newest.Order, subscription.ComicStatus, completedStatus(subscription.ComicStatus), true); err != nil {
		s.logger.Printf("scheduler: update manual download baseline %s/%s: %v", subscription.SourceID, subscription.Title, err)
	}
	s.downloads.Notify()
	return job, nil
}

// QueueAll queues every chapter in a subscription. Completed download records
// are authoritative and are returned as skipped; fill does not verify whether
// the local file still exists.
func (s *Scheduler) QueueAll(ctx context.Context, subscription model.Subscription) (queued, skipped int, err error) {
	return s.queueAll(ctx, subscription, model.QueuePriorityNormal)
}

// QueueAllManual queues every missing chapter and gives the batch manual
// priority so it is consumed before routine subscription checks.
func (s *Scheduler) QueueAllManual(ctx context.Context, subscription model.Subscription) (queued, skipped int, err error) {
	return s.queueAll(ctx, subscription, model.QueuePriorityManual)
}

func (s *Scheduler) queueAll(ctx context.Context, subscription model.Subscription, queuePriority int) (queued, skipped int, err error) {
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
			QueuePriority:       queuePriority,
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
	if err := s.subscriptions.UpdateSubscriptionCheck(ctx, subscription.ID, newest.ID, newest.Title, newest.Order, subscription.ComicStatus, completedStatus(subscription.ComicStatus), true); err != nil {
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
	changed := false
	for _, subscription := range subscriptions {
		if !subscription.Enabled || !s.due(subscription, now) {
			continue
		}
		if _, err := s.check(ctx, subscription, model.QueuePriorityNormal); err != nil {
			s.logger.Printf("scheduler: %s/%s: %v", subscription.SourceID, subscription.Title, err)
		} else {
			changed = true
		}
		if retried, err := s.downloads.RetryFailedByComic(ctx, subscription.SourceID, subscription.ComicID, model.QueuePriorityNormal); err != nil {
			s.logger.Printf("scheduler: retry failed downloads %s/%s: %v", subscription.SourceID, subscription.Title, err)
		} else if retried > 0 {
			s.logger.Printf("scheduler: retried %d failed download(s) for %s/%s", retried, subscription.SourceID, subscription.Title)
		}
	}
	if changed {
		s.runAfterUpdate(ctx)
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

// CheckOneManual checks a subscription from a user action and prioritizes any
// newly discovered chapters.
func (s *Scheduler) CheckOneManual(ctx context.Context, id int64) error {
	_, err := s.CheckOneManualDetailed(ctx, id)
	return err
}

// CheckOneDetailed checks one subscription and returns how many new chapters
// were discovered and queued.
func (s *Scheduler) CheckOneDetailed(ctx context.Context, id int64) (SubscriptionCheckResult, error) {
	return s.checkOneDetailed(ctx, id, model.QueuePriorityNormal)
}

// CheckOneManualDetailed is the user-triggered variant of CheckOneDetailed.
func (s *Scheduler) CheckOneManualDetailed(ctx context.Context, id int64) (SubscriptionCheckResult, error) {
	return s.checkOneDetailed(ctx, id, model.QueuePriorityManual)
}

func (s *Scheduler) checkOneDetailed(ctx context.Context, id int64, queuePriority int) (SubscriptionCheckResult, error) {
	subscription, err := s.subscriptions.Subscription(ctx, id)
	if err != nil {
		return SubscriptionCheckResult{}, err
	}
	result, err := s.check(ctx, subscription, queuePriority)
	if err == nil {
		s.runAfterUpdate(ctx)
	}
	return result, err
}
