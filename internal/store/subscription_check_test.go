package store

import (
	"context"
	"strings"
	"testing"

	"github.com/hyaeve/manco/internal/model"
)

func TestCommitSubscriptionCheckRollsBackPartialQueueAndRetries(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)
	subscription, err := repository.UpsertSubscription(ctx, model.Subscription{
		SourceID:         "source",
		ComicID:          "work",
		Title:            "Work",
		Enabled:          true,
		AutoDownload:     true,
		LastChapterID:    "1",
		LastChapterTitle: "Chapter 1",
		LastChapterOrder: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	jobs := []model.DownloadJob{
		{SourceID: "source", ComicID: "work", ChapterID: "2", ChapterTitle: "Chapter 2", ChapterOrder: 2},
		{SourceID: "source", ComicID: "work", ChapterID: "3", ChapterTitle: "Chapter 3", ChapterOrder: 3},
		{SourceID: "source", ComicID: "work", ChapterID: "4", ChapterTitle: "Chapter 4", ChapterOrder: 4},
	}
	latest := model.Chapter{ID: "4", Title: "Chapter 4", Order: 4}
	_, err = repository.db.ExecContext(ctx, `CREATE TRIGGER reject_chapter_job BEFORE INSERT ON download_jobs
		WHEN NEW.chapter_id = '3' BEGIN SELECT RAISE(ABORT, 'test queue failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CommitSubscriptionCheck(ctx, subscription, latest, 4, "连载", true, jobs); err == nil || !strings.Contains(err.Error(), "test queue failure") {
		t.Fatalf("failed check error = %v", err)
	}
	afterFailure, err := repository.Subscription(ctx, subscription.ID)
	if err != nil || afterFailure.LastChapterID != "1" || afterFailure.ChapterCount != 0 || afterFailure.LastCheckedAt != nil {
		t.Fatalf("subscription after failure = %+v, err = %v", afterFailure, err)
	}
	persisted, err := repository.DownloadJobsByComic(ctx, "source", "work")
	if err != nil || len(persisted) != 0 {
		t.Fatalf("jobs after failure = %+v, err = %v", persisted, err)
	}
	if _, err := repository.db.ExecContext(ctx, `DROP TRIGGER reject_chapter_job`); err != nil {
		t.Fatal(err)
	}
	manualJob := jobs[0]
	manualJob.QueuePriority = model.QueuePriorityManual
	if _, err := repository.CreateDownloadJob(ctx, manualJob); err != nil {
		t.Fatal(err)
	}
	if err := repository.CommitSubscriptionCheck(ctx, subscription, latest, 4, "连载", true, jobs); err != nil {
		t.Fatal(err)
	}
	persisted, err = repository.DownloadJobsByComic(ctx, "source", "work")
	if err != nil || len(persisted) != len(jobs) {
		t.Fatalf("jobs after retry = %+v, err = %v", persisted, err)
	}
	for index, job := range persisted {
		if job.ChapterID != jobs[index].ChapterID || job.Status != "queued" {
			t.Fatalf("job #%d after retry = %+v", index, job)
		}
	}
	if persisted[0].QueuePriority != model.QueuePriorityManual {
		t.Fatalf("existing manual task priority = %d", persisted[0].QueuePriority)
	}
	// A check that started with the old baseline may not publish a stale result.
	if err := repository.CommitSubscriptionCheck(ctx, subscription, model.Chapter{ID: "2", Order: 2}, 2, "", true, nil); err == nil {
		t.Fatal("stale check committed after newer baseline")
	}
	afterRetry, err := repository.Subscription(ctx, subscription.ID)
	if err != nil || afterRetry.LastChapterID != "4" || afterRetry.LastChapterOrder != 4 || afterRetry.ChapterCount != 4 || afterRetry.LastCheckedAt == nil {
		t.Fatalf("subscription after retry = %+v, err = %v", afterRetry, err)
	}
}
