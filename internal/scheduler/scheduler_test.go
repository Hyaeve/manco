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
	items   []model.Subscription
	updates []model.Subscription
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
	jobs     []model.DownloadJob
	notified int
}

func (d *stubDownloads) CreateDownloadJob(_ context.Context, job model.DownloadJob) (model.DownloadJob, error) {
	d.jobs = append(d.jobs, job)
	job.ID = int64(len(d.jobs))
	return job, nil
}

func (d *stubDownloads) Notify() { d.notified++ }

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
	if downloads.notified == 0 {
		t.Fatal("downloader was not notified after queueing")
	}
	if got := subscriptions.updates[len(subscriptions.updates)-1].LastChapterID; got != "3" {
		t.Fatalf("baseline after check = %q, want 3", got)
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
