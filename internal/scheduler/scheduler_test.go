package scheduler

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"
	"time"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/sources"
)

type stubSubscriptions struct {
	items         []model.Subscription
	updates       []model.Subscription
	chapterCounts map[int64]int
}

func (s *stubSubscriptions) ListSubscriptions(context.Context) ([]model.Subscription, error) {
	return s.items, nil
}

func (s *stubSubscriptions) Subscription(_ context.Context, id int64) (model.Subscription, error) {
	for _, item := range s.items {
		if item.ID == id {
			return item, nil
		}
	}
	return model.Subscription{}, errors.New("not found")
}

func (s *stubSubscriptions) UpdateSubscriptionChapterCount(_ context.Context, id int64, count int) error {
	if s.chapterCounts == nil {
		s.chapterCounts = make(map[int64]int)
	}
	s.chapterCounts[id] = count
	return nil
}

func (s *stubSubscriptions) UpdateSubscriptionCheck(_ context.Context, id int64, chapterID, chapterTitle string, chapterOrder float64, comicStatus string, completed, newChapter bool) error {
	for _, item := range s.items {
		if item.ID != id {
			continue
		}
		item.LastChapterID = chapterID
		item.LastChapterTitle = chapterTitle
		item.LastChapterOrder = chapterOrder
		s.updates = append(s.updates, item)
	}
	return nil
}

type stubDownloads struct {
	jobs         []model.DownloadJob
	notified     int
	retried      []model.Subscription
	retryN       int
	createStatus string
}

func (d *stubDownloads) CreateDownloadJob(_ context.Context, job model.DownloadJob) (model.DownloadJob, error) {
	d.jobs = append(d.jobs, job)
	job.ID = int64(len(d.jobs))
	if d.createStatus != "" {
		job.Status = d.createStatus
	}
	return job, nil
}

func (d *stubDownloads) Notify() { d.notified++ }

func (d *stubDownloads) EnsureSeriesMetadata(_ context.Context, _ model.Subscription) (bool, error) {
	return false, nil
}

func (d *stubDownloads) RetryFailedByComic(_ context.Context, sourceID, comicID string, _ int) (int, error) {
	d.retried = append(d.retried, model.Subscription{SourceID: sourceID, ComicID: comicID})
	if d.retryN > 0 {
		d.notified++
	}
	return d.retryN, nil
}

type stubChapters struct {
	chapters []model.Chapter
}

func (s *stubChapters) Info() model.SourceInfo {
	return model.SourceInfo{ID: "stub", Name: "测试源"}
}

func (s *stubChapters) Search(context.Context, source.Account, string, int) (model.SearchResult, error) {
	return model.SearchResult{}, nil
}

func (s *stubChapters) Browse(context.Context, source.Account, model.BrowseOptions, int) (model.SearchResult, error) {
	return model.SearchResult{}, nil
}

func (s *stubChapters) Detail(context.Context, source.Account, string) (model.Comic, error) {
	return model.Comic{}, nil
}

func (s *stubChapters) Chapters(context.Context, source.Account, string) ([]model.Chapter, error) {
	return s.chapters, nil
}

func (s *stubChapters) Pages(context.Context, source.Account, string, model.Chapter) ([]model.Page, error) {
	return nil, nil
}

func newTestScheduler(chapters []model.Chapter, subscriptions *stubSubscriptions, downloads *stubDownloads) *Scheduler {
	registry := sources.NewRegistry(nil, nil, nil)
	registry.Register(&stubChapters{chapters: chapters})
	return New(registry, subscriptions, downloads, 0, log.New(io.Discard, "", 0))
}

func testChapters() []model.Chapter {
	return []model.Chapter{
		{ID: "1", Title: "第1话", Order: 1},
		{ID: "2", Title: "第2话", Order: 2},
		{ID: "3", Title: "第3话", Order: 3},
	}
}

func TestFirstCheckRecordsBaselineWithoutDownloading(t *testing.T) {
	subscriptions := &stubSubscriptions{items: []model.Subscription{{
		ID:           1,
		SourceID:     "stub",
		ComicID:      "1",
		Title:        "测试作品",
		Enabled:      true,
		AutoDownload: true,
	}}}
	downloads := &stubDownloads{}
	newTestScheduler(testChapters(), subscriptions, downloads).RunOnce(context.Background())

	if len(downloads.jobs) != 0 {
		t.Fatalf("first check queued %d jobs, want 0", len(downloads.jobs))
	}
	if len(subscriptions.updates) != 1 {
		t.Fatalf("baseline updates = %d, want 1", len(subscriptions.updates))
	}
	if got := subscriptions.updates[0].LastChapterID; got != "3" {
		t.Fatalf("baseline chapter = %q, want the newest chapter 3", got)
	}
	if got := subscriptions.chapterCounts[1]; got != 3 {
		t.Fatalf("chapter count = %d, want 3", got)
	}
}

func TestCheckOneRefreshesSnapshotAfterSuccessfulUpdate(t *testing.T) {
	subscriptions := &stubSubscriptions{items: []model.Subscription{{
		ID:           1,
		SourceID:     "stub",
		ComicID:      "1",
		Title:        "测试作品",
		Enabled:      true,
		AutoDownload: true,
	}}}
	scheduler := newTestScheduler(testChapters(), subscriptions, &stubDownloads{})
	refreshes := 0
	scheduler.SetAfterUpdate(func(context.Context) {
		refreshes++
	})
	if _, err := scheduler.CheckOneDetailed(context.Background(), 1); err != nil {
		t.Fatalf("CheckOneDetailed: %v", err)
	}
	if refreshes != 1 {
		t.Fatalf("snapshot refreshes = %d, want 1", refreshes)
	}
}

func TestNewChaptersAreQueuedOldestFirst(t *testing.T) {
	subscriptions := &stubSubscriptions{items: []model.Subscription{{
		ID:               1,
		SourceID:         "stub",
		ComicID:          "1",
		Title:            "测试作品",
		Enabled:          true,
		AutoDownload:     true,
		LastChapterID:    "1",
		LastChapterTitle: "第1话",
		LastChapterOrder: 1,
	}}}
	downloads := &stubDownloads{}
	newTestScheduler(testChapters(), subscriptions, downloads).RunOnce(context.Background())

	if len(downloads.jobs) != 2 {
		t.Fatalf("queued %d jobs, want 2", len(downloads.jobs))
	}
	if downloads.jobs[0].ChapterID != "2" || downloads.jobs[1].ChapterID != "3" {
		t.Fatalf("queued chapters = %q,%q; want 2,3", downloads.jobs[0].ChapterID, downloads.jobs[1].ChapterID)
	}
	if downloads.jobs[0].QueuePriority != model.QueuePriorityNormal || downloads.jobs[1].QueuePriority != model.QueuePriorityNormal {
		t.Fatalf("routine check priorities = %d,%d; want normal", downloads.jobs[0].QueuePriority, downloads.jobs[1].QueuePriority)
	}
	if downloads.notified == 0 {
		t.Fatal("downloader was not notified after queueing")
	}
	if got := subscriptions.updates[len(subscriptions.updates)-1].LastChapterID; got != "3" {
		t.Fatalf("baseline after check = %q, want 3", got)
	}
	if got := subscriptions.chapterCounts[1]; got != 3 {
		t.Fatalf("chapter count = %d, want 3", got)
	}
}

func TestCheckOneDetailedReportsNewAndQueued(t *testing.T) {
	subscriptions := &stubSubscriptions{items: []model.Subscription{{
		ID:               1,
		SourceID:         "stub",
		ComicID:          "1",
		Title:            "测试作品",
		Enabled:          true,
		AutoDownload:     true,
		LastChapterID:    "1",
		LastChapterTitle: "第1话",
		LastChapterOrder: 1,
	}}}
	downloads := &stubDownloads{}
	result, err := newTestScheduler(testChapters(), subscriptions, downloads).CheckOneDetailed(context.Background(), 1)
	if err != nil {
		t.Fatalf("CheckOneDetailed returned error: %v", err)
	}
	if result.NewChapters != 2 || result.Queued != 2 {
		t.Fatalf("check result = %+v, want 2 new and 2 queued", result)
	}
}

func TestManualCheckPrioritizesNewChapters(t *testing.T) {
	subscriptions := &stubSubscriptions{items: []model.Subscription{{
		ID:               1,
		SourceID:         "stub",
		ComicID:          "1",
		Title:            "测试作品",
		Enabled:          true,
		AutoDownload:     true,
		LastChapterID:    "1",
		LastChapterTitle: "第1话",
		LastChapterOrder: 1,
	}}}
	downloads := &stubDownloads{}
	result, err := newTestScheduler(testChapters(), subscriptions, downloads).CheckOneManualDetailed(context.Background(), 1)
	if err != nil {
		t.Fatalf("CheckOneManualDetailed returned error: %v", err)
	}
	if result.Queued != 2 || len(downloads.jobs) != 2 {
		t.Fatalf("manual check result = %+v jobs=%d, want 2 queued", result, len(downloads.jobs))
	}
	for _, job := range downloads.jobs {
		if job.QueuePriority != model.QueuePriorityManual {
			t.Fatalf("manual check job priority = %d, want %d", job.QueuePriority, model.QueuePriorityManual)
		}
	}
}

func TestQueueAllSkipsCompletedRecordsWithoutLocalFileChecks(t *testing.T) {
	subscription := model.Subscription{
		ID:           1,
		SourceID:     "stub",
		ComicID:      "1",
		Title:        "测试作品",
		Enabled:      true,
		AutoDownload: true,
	}
	subscriptions := &stubSubscriptions{items: []model.Subscription{subscription}}
	downloads := &stubDownloads{createStatus: "completed"}
	queued, skipped, err := newTestScheduler(testChapters(), subscriptions, downloads).QueueAll(context.Background(), subscription)
	if err != nil {
		t.Fatalf("QueueAll returned error: %v", err)
	}
	if queued != 0 || skipped != len(testChapters()) {
		t.Fatalf("QueueAll queued=%d skipped=%d, want 0/%d", queued, skipped, len(testChapters()))
	}
}

func TestQueueAllManualPrioritizesEveryChapter(t *testing.T) {
	subscription := model.Subscription{
		ID:           1,
		SourceID:     "stub",
		ComicID:      "1",
		Title:        "测试作品",
		Enabled:      true,
		AutoDownload: true,
	}
	subscriptions := &stubSubscriptions{items: []model.Subscription{subscription}}
	downloads := &stubDownloads{}
	queued, skipped, err := newTestScheduler(testChapters(), subscriptions, downloads).QueueAllManual(context.Background(), subscription)
	if err != nil {
		t.Fatalf("QueueAllManual returned error: %v", err)
	}
	if queued != len(testChapters()) || skipped != 0 {
		t.Fatalf("QueueAllManual queued=%d skipped=%d, want %d/0", queued, skipped, len(testChapters()))
	}
	for _, job := range downloads.jobs {
		if job.QueuePriority != model.QueuePriorityManual {
			t.Fatalf("manual full job priority = %d, want %d", job.QueuePriority, model.QueuePriorityManual)
		}
	}
}

func TestQueueLatestDownloadsNewestEvenWhenAutoDownloadDisabled(t *testing.T) {
	subscription := model.Subscription{
		ID:           1,
		SourceID:     "stub",
		ComicID:      "1",
		Title:        "测试作品",
		Cover:        "cover.jpg",
		Enabled:      true,
		AutoDownload: false,
	}
	subscriptions := &stubSubscriptions{items: []model.Subscription{subscription}}
	downloads := &stubDownloads{}
	job, err := newTestScheduler(testChapters(), subscriptions, downloads).QueueLatest(context.Background(), subscription)
	if err != nil {
		t.Fatalf("QueueLatest returned error: %v", err)
	}
	if job.ChapterID != "3" || len(downloads.jobs) != 1 {
		t.Fatalf("queued jobs = %#v, want only chapter 3", downloads.jobs)
	}
	if job.QueuePriority != model.QueuePriorityManual {
		t.Fatalf("manual latest job priority = %d, want %d", job.QueuePriority, model.QueuePriorityManual)
	}
	if downloads.notified == 0 {
		t.Fatal("downloader was not notified after manual download")
	}
	if got := subscriptions.updates[len(subscriptions.updates)-1].LastChapterID; got != "3" {
		t.Fatalf("baseline after manual download = %q, want 3", got)
	}
}

func TestQueueLatestRejectsComicWithoutChapters(t *testing.T) {
	subscription := model.Subscription{ID: 1, SourceID: "stub", ComicID: "1", Title: "测试作品", Enabled: true}
	subscriptions := &stubSubscriptions{}
	downloads := &stubDownloads{}
	_, err := newTestScheduler(nil, subscriptions, downloads).QueueLatest(context.Background(), subscription)
	if !errors.Is(err, ErrNoChapters) {
		t.Fatalf("QueueLatest error = %v, want ErrNoChapters", err)
	}
	if len(downloads.jobs) != 0 || len(subscriptions.updates) != 0 {
		t.Fatalf("empty comic produced jobs=%d updates=%d", len(downloads.jobs), len(subscriptions.updates))
	}
}

func TestCronDueUsesCreationAndLastCheckBase(t *testing.T) {
	scheduler := newTestScheduler(nil, &stubSubscriptions{}, &stubDownloads{})
	created := time.Date(2026, time.October, 9, 10, 0, 0, 0, time.Local)
	subscription := model.Subscription{
		CronExpr:  "0 12 * * 5",
		CreatedAt: created,
	}
	if scheduler.due(subscription, time.Date(2026, time.October, 9, 11, 59, 59, 0, time.Local)) {
		t.Fatal("subscription ran before its cron time")
	}
	if !scheduler.due(subscription, time.Date(2026, time.October, 9, 12, 0, 1, 0, time.Local)) {
		t.Fatal("subscription did not run at its cron time")
	}
	checked := time.Date(2026, time.October, 9, 12, 0, 30, 0, time.Local)
	subscription.LastCheckedAt = &checked
	if scheduler.due(subscription, time.Date(2026, time.October, 9, 12, 1, 0, 0, time.Local)) {
		t.Fatal("weekly subscription ran twice in the same cron window")
	}
}

func TestCompletedStatusOnlyUsesComicStatus(t *testing.T) {
	if !completedStatus("已完结") {
		t.Fatal("source status 已完结 should mark the subscription completed")
	}
	if completedStatus("连载中") {
		t.Fatal("ongoing comic should not be marked completed")
	}
	if completedStatus("未完结") {
		t.Fatal("未完结 must not be treated as completed")
	}
}

func TestDisabledSubscriptionIsSkipped(t *testing.T) {
	subscriptions := &stubSubscriptions{items: []model.Subscription{{
		ID: 1, SourceID: "stub", ComicID: "1", Title: "测试作品", Enabled: false,
	}}}
	downloads := &stubDownloads{}
	newTestScheduler(testChapters(), subscriptions, downloads).RunOnce(context.Background())

	if len(downloads.jobs) != 0 || len(subscriptions.updates) != 0 {
		t.Fatalf("disabled subscription produced work: jobs=%d updates=%d", len(downloads.jobs), len(subscriptions.updates))
	}
}

func TestRunDueRetriesFailedDownloads(t *testing.T) {
	subscriptions := &stubSubscriptions{items: []model.Subscription{{
		ID:        1,
		SourceID:  "stub",
		ComicID:   "1",
		Title:     "测试作品",
		Enabled:   true,
		CronExpr:  "* * * * *",
		CreatedAt: time.Now().Add(-2 * time.Minute),
	}}}
	downloads := &stubDownloads{retryN: 2}

	newTestScheduler(testChapters(), subscriptions, downloads).RunDue(context.Background())

	if len(downloads.retried) != 1 {
		t.Fatalf("RetryFailedByComic calls = %d, want 1", len(downloads.retried))
	}
	if downloads.retried[0].SourceID != "stub" || downloads.retried[0].ComicID != "1" {
		t.Fatalf("unexpected retry target: %#v", downloads.retried[0])
	}
	if downloads.notified != 1 {
		t.Fatalf("Notify calls = %d, want 1", downloads.notified)
	}
}
