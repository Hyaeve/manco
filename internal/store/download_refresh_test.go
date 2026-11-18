package store

import (
	"context"
	"strings"
	"testing"

	"github.com/hyaeve/manco/internal/model"
)

func TestFullRefreshRollsBackReplacementTasksAndBaseline(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)
	subscription, err := repository.UpsertSubscription(ctx, model.Subscription{
		SourceID: "source", ComicID: "work", Title: "Work", LastChapterID: "1", LastChapterOrder: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	jobs := []model.DownloadJob{
		{SourceID: "source", ComicID: "work", ChapterID: "1", ChapterOrder: 1, QueuePriority: model.QueuePriorityManual},
		{SourceID: "source", ComicID: "work", ChapterID: "2", ChapterOrder: 2, QueuePriority: model.QueuePriorityManual},
	}
	completed, err := repository.CreateDownloadJob(ctx, jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateDownloadJob(ctx, completed.ID, "completed", 8, 8, "chapter.cbz", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.ExecContext(ctx, `CREATE TRIGGER reject_refresh_chapter BEFORE INSERT ON download_jobs
		WHEN NEW.chapter_id = '2' BEGIN SELECT RAISE(ABORT, 'test refresh failure'); END`); err != nil {
		t.Fatal(err)
	}
	queued, _, replaced, err := repository.QueueFullRefresh(ctx, subscription, jobs)
	if err == nil || !strings.Contains(err.Error(), "test refresh failure") || queued != 0 || replaced != 0 {
		t.Fatalf("failed full refresh queued %d, replaced %d, error = %v", queued, replaced, err)
	}
	current, err := repository.DownloadJobByChapter(ctx, "source", "work", "1")
	if err != nil || current.ID != completed.ID || current.Status != "completed" || current.FilePath != "chapter.cbz" {
		t.Fatalf("completed job after rollback = %+v, error = %v", current, err)
	}
	actual, err := repository.Subscription(ctx, subscription.ID)
	if err != nil || actual.LastChapterID != "1" || actual.LastCheckedAt != nil {
		t.Fatalf("subscription after rollback = %+v, error = %v", actual, err)
	}
	if _, err := repository.db.ExecContext(ctx, `DROP TRIGGER reject_refresh_chapter`); err != nil {
		t.Fatal(err)
	}
	queued, _, replaced, err = repository.QueueFullRefresh(ctx, subscription, jobs)
	if err != nil || queued != 2 || replaced != 1 {
		t.Fatalf("retried full refresh queued %d, replaced %d, error = %v", queued, replaced, err)
	}
	current, err = repository.DownloadJobByChapter(ctx, "source", "work", "1")
	if err != nil || current.ID == completed.ID || current.FilePath != "chapter.cbz" || current.CompletedPages != 0 {
		t.Fatalf("replacement job after retry = %+v, error = %v", current, err)
	}
	if err := repository.UpdateDownloadJob(ctx, current.ID, "running", 8, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := repository.FailDownloadJob(ctx, current.ID, 8, 0, "failed", 1, nil); err != nil {
		t.Fatal(err)
	}
	current, err = repository.DownloadJob(ctx, current.ID)
	if err != nil || current.Status != "failed" || current.FilePath != "chapter.cbz" {
		t.Fatalf("preserved path after retry failure = %+v, error = %v", current, err)
	}
}
