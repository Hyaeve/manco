package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hyaeve/manco/internal/model"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "manco.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func createJob(t *testing.T, repository *Store, chapterID string) model.DownloadJob {
	t.Helper()
	job, err := repository.CreateDownloadJob(context.Background(), model.DownloadJob{
		SourceID:     "jmcomic",
		ComicID:      "123",
		ComicTitle:   "测试作品",
		ChapterID:    chapterID,
		ChapterTitle: "第" + chapterID + "话",
	})
	if err != nil {
		t.Fatalf("create job %s: %v", chapterID, err)
	}
	return job
}

func TestListQueuedJobsDoesNotResetRunningJobs(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)

	running := createJob(t, repository, "1")
	queued := createJob(t, repository, "2")
	if err := repository.UpdateDownloadJob(ctx, running.ID, "running", 12, 3, "", ""); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	jobs, err := repository.ListQueuedJobs(ctx)
	if err != nil {
		t.Fatalf("list queued: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != queued.ID {
		t.Fatalf("queued jobs = %v, want only job %d", jobs, queued.ID)
	}

	stored, err := repository.DownloadJob(ctx, running.ID)
	if err != nil {
		t.Fatalf("reload running job: %v", err)
	}
	if stored.Status != "running" {
		t.Fatalf("running job status = %q, want running", stored.Status)
	}
	if stored.CompletedPages != 3 || stored.TotalPages != 12 {
		t.Fatalf("running job progress = %d/%d, want 3/12", stored.CompletedPages, stored.TotalPages)
	}
}

func TestUpdateUsername(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)
	if err := repository.CreateUser(ctx, "alice", "hash-a"); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if err := repository.CreateUser(ctx, "bob", "hash-b"); err != nil {
		t.Fatalf("create bob: %v", err)
	}

	alice, err := repository.UserByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("load alice: %v", err)
	}
	if err := repository.UpdateUsername(ctx, alice.ID, "alice2"); err != nil {
		t.Fatalf("rename alice: %v", err)
	}
	if _, err := repository.UserByUsername(ctx, "alice2"); err != nil {
		t.Fatalf("renamed user missing: %v", err)
	}
	if err := repository.UpdateUsername(ctx, alice.ID, "bob"); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("duplicate rename error = %v, want ErrUsernameTaken", err)
	}
}

func TestRequeueRunningJobsRestoresInterruptedWork(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)

	running := createJob(t, repository, "1")
	if err := repository.UpdateDownloadJob(ctx, running.ID, "running", 12, 3, "", ""); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	if err := repository.RequeueRunningJobs(ctx); err != nil {
		t.Fatalf("requeue running jobs: %v", err)
	}

	jobs, err := repository.ListQueuedJobs(ctx)
	if err != nil {
		t.Fatalf("list queued: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != running.ID {
		t.Fatalf("queued jobs = %v, want only job %d", jobs, running.ID)
	}
	if jobs[0].Status != "queued" {
		t.Fatalf("requeued status = %q, want queued", jobs[0].Status)
	}
}

func TestCreateDownloadJobKeepsCompletedStatus(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)

	job := createJob(t, repository, "1")
	if err := repository.UpdateDownloadJob(ctx, job.ID, "completed", 10, 10, "downloads/作品/第1话.cbz", ""); err != nil {
		t.Fatalf("complete job: %v", err)
	}
	again := createJob(t, repository, "1")
	if again.Status != "completed" {
		t.Fatalf("status after re-create = %q, want completed", again.Status)
	}
	if again.FilePath == "" {
		t.Fatal("completed job lost its file path")
	}
}
