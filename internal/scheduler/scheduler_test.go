package scheduler

import (
	"context"
	"io"
	"log"
	"testing"

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

func (s *stubSubscriptions) UpdateSubscriptionCheck(_ context.Context, id int64, chapterID, chapterTitle string, chapterOrder float64) error {
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
