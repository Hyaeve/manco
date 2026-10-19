package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

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

func TestDownloadJobActivityAggregatesPerWork(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)

	done := createJob(t, repository, "1")
	if err := repository.UpdateDownloadJob(ctx, done.ID, "completed", 1, 1, "/tmp/1.cbz", ""); err != nil {
		t.Fatalf("complete job: %v", err)
	}
	failed := createJob(t, repository, "2")
	if err := repository.UpdateDownloadJob(ctx, failed.ID, "failed", 1, 0, "", "boom"); err != nil {
		t.Fatalf("fail job: %v", err)
	}
	// 同一作品下还有一章在排队，会计入 pending。
	_ = createJob(t, repository, "3")
	// 只有排队任务的其它作品不应出现在活动聚合里。
	other := createJobForComic(t, repository, "other", "1")
	_ = other

	groups, err := repository.DownloadJobActivity(ctx, 50)
	if err != nil {
		t.Fatalf("DownloadJobActivity: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %+v, want a single work", groups)
	}
	group := groups[0]
	if group.SourceID != "jmcomic" || group.ComicID != "123" || group.Title != "测试作品" {
		t.Fatalf("group identity = %+v", group)
	}
	if group.Succeeded != 1 || group.Failed != 1 || group.Pending != 1 {
		t.Fatalf("group counts = %+v", group)
	}
	if group.LastAt.IsZero() {
		t.Fatalf("last_at did not parse: %+v", group)
	}
}

func TestMaintainSubscriptionsUsesStatusAndNewChapterAge(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local)

	create := func(comicID, status string) model.Subscription {
		t.Helper()
		item, err := repository.UpsertSubscription(ctx, model.Subscription{
			SourceID:    "bao",
			ComicID:     comicID,
			Title:       "测试" + comicID,
			ComicStatus: status,
			Enabled:     true,
		})
		if err != nil {
			t.Fatalf("create subscription %s: %v", comicID, err)
		}
		return item
	}
	setAgeAt := func(id int64, status string, base time.Time, days int) {
		t.Helper()
		if _, err := repository.db.ExecContext(ctx, `
			UPDATE subscriptions SET comic_status = ?, chapter_count = 42,
				last_new_chapter_at = ?, created_at = ?
			WHERE id = ?`,
			status, base.Add(-time.Duration(days)*24*time.Hour).UTC(), base.Add(-time.Duration(days)*24*time.Hour).UTC(), id); err != nil {
			t.Fatalf("set subscription age %d: %v", id, err)
		}
	}
	setAge := func(id int64, status string, days int) {
		t.Helper()
		setAgeAt(id, status, now, days)
	}

	completedWarning := create("1", "已完结")
	beforeNoon := now.Add(-time.Hour)
	setAgeAt(completedWarning.ID, "已完结", beforeNoon, 12)
	result, err := repository.MaintainSubscriptions(ctx, map[string]int{"*": 30}, beforeNoon)
	if err != nil {
		t.Fatalf("maintenance before noon: %v", err)
	}
	if len(result.Warnings) != 0 || len(result.Archived) != 0 {
		t.Fatalf("before-noon maintenance produced events: %+v", result)
	}

	setAge(completedWarning.ID, "已完结", 12)
	result, err = repository.MaintainSubscriptions(ctx, map[string]int{"*": 30}, now)
	if err != nil {
		t.Fatalf("maintenance at noon: %v", err)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Subscription.ID != completedWarning.ID {
		t.Fatalf("completed warning events = %+v", result.Warnings)
	}

	completedArchive := create("2", "Completed")
	setAge(completedArchive.ID, "Completed", 15)
	ongoingWarning := create("3", "连载中")
	setAge(ongoingWarning.ID, "连载中", 27)
	ongoingArchive := create("4", "Ongoing")
	setAge(ongoingArchive.ID, "Ongoing", 30)
	titleOnly := create("5", "连载中")
	setAge(titleOnly.ID, "连载中", 15)
	if _, err := repository.db.ExecContext(ctx, `UPDATE subscriptions SET last_chapter_title = '终章' WHERE id = ?`, titleOnly.ID); err != nil {
		t.Fatalf("set chapter title: %v", err)
	}

	result, err = repository.MaintainSubscriptions(ctx, map[string]int{"*": 30}, now)
	if err != nil {
		t.Fatalf("maintenance: %v", err)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Subscription.ID != ongoingWarning.ID {
		t.Fatalf("ongoing warning events = %+v", result.Warnings)
	}
	if result.Warnings[0].DaysRemaining != 3 {
		t.Fatalf("ongoing days remaining = %d, want 3", result.Warnings[0].DaysRemaining)
	}
	if len(result.Archived) != 2 {
		t.Fatalf("archived events = %+v, want two", result.Archived)
	}
	archivedIDs := map[int64]bool{}
	for _, event := range result.Archived {
		archivedIDs[event.Subscription.ID] = true
	}
	if !archivedIDs[completedArchive.ID] || !archivedIDs[ongoingArchive.ID] || archivedIDs[titleOnly.ID] {
		t.Fatalf("archived ids = %v", archivedIDs)
	}

	again, err := repository.MaintainSubscriptions(ctx, map[string]int{"*": 30}, now)
	if err != nil {
		t.Fatalf("second maintenance: %v", err)
	}
	if len(again.Warnings) != 0 || len(again.Archived) != 0 {
		t.Fatalf("maintenance repeated events: %+v", again)
	}

	archived, err := repository.Subscription(ctx, completedArchive.ID)
	if err != nil {
		t.Fatalf("reload archived subscription: %v", err)
	}
	if archived.Enabled || archived.ArchivedAt == nil || archived.ArchiveReason != "completed" {
		t.Fatalf("completed archive state = %+v", archived)
	}
	tracked, err := repository.Subscription(ctx, titleOnly.ID)
	if err != nil {
		t.Fatalf("reload title-only subscription: %v", err)
	}
	if tracked.ArchivedAt != nil || !tracked.Enabled {
		t.Fatalf("terminal chapter title affected archive state: %+v", tracked)
	}
}

func TestSubscriptionArchiveWarningLifecycle(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)
	subscription, err := repository.UpsertSubscription(ctx, model.Subscription{
		SourceID:     "bao",
		ComicID:      "warning",
		Title:        "预警测试",
		Enabled:      true,
		CronExpr:     "0 12 * * *",
		AutoDownload: true,
	})
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if _, err := repository.db.ExecContext(ctx, `
		UPDATE subscriptions SET archive_warning_at = CURRENT_TIMESTAMP, archive_warning_days = 12
		WHERE id = ?`, subscription.ID); err != nil {
		t.Fatalf("seed warning: %v", err)
	}
	if err := repository.UpdateSubscriptionConfig(
		ctx,
		subscription.ID,
		true,
		true,
		subscription.CronExpr,
		subscription.DownloadDir,
		subscription.SeriesDir,
		true,
		false,
		false,
	); err != nil {
		t.Fatalf("update enabled subscription config: %v", err)
	}
	current, err := repository.Subscription(ctx, subscription.ID)
	if err != nil {
		t.Fatalf("reload after config update: %v", err)
	}
	if current.ArchiveWarningAt == nil || current.ArchiveWarningDays != 12 {
		t.Fatalf("editing enabled subscription cleared warning: %+v", current)
	}
	if err := repository.UpdateSubscriptionCheck(ctx, subscription.ID, "chapter-2", "第2话", 2, "连载中", false, false); err != nil {
		t.Fatalf("update without new chapter: %v", err)
	}
	current, err = repository.Subscription(ctx, subscription.ID)
	if err != nil {
		t.Fatalf("reload after normal check: %v", err)
	}
	if current.ArchiveWarningAt == nil || current.ArchiveWarningDays != 12 {
		t.Fatalf("normal check cleared warning: %+v", current)
	}
	if err := repository.UpdateSubscriptionCheck(ctx, subscription.ID, "chapter-3", "第3话", 3, "连载中", false, true); err != nil {
		t.Fatalf("update with new chapter: %v", err)
	}
	current, err = repository.Subscription(ctx, subscription.ID)
	if err != nil {
		t.Fatalf("reload after new chapter: %v", err)
	}
	if current.ArchiveWarningAt != nil || current.ArchiveWarningDays != 0 {
		t.Fatalf("new chapter did not clear warning: %+v", current)
	}
	if err := repository.UpdateSubscription(ctx, subscription.ID, false, true, subscription.CronExpr); err != nil {
		t.Fatalf("disable subscription: %v", err)
	}
	if err := repository.UpdateSubscription(ctx, subscription.ID, true, true, subscription.CronExpr); err != nil {
		t.Fatalf("reenable subscription: %v", err)
	}
	reenabled, err := repository.Subscription(ctx, subscription.ID)
	if err != nil {
		t.Fatalf("reload reenabled subscription: %v", err)
	}
	if reenabled.LastNewChapterAt == nil || reenabled.ArchiveWarningAt != nil || reenabled.ArchiveWarningDays != 0 {
		t.Fatalf("reenabled subscription did not reset inactivity cycle: %+v", reenabled)
	}
}

func TestMaintainSubscriptionsKeepsCompletedRuleWhenOngoingRuleDisabled(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local)
	createAged := func(comicID, status string, days int) model.Subscription {
		t.Helper()
		item, err := repository.UpsertSubscription(ctx, model.Subscription{
			SourceID:    "bao",
			ComicID:     comicID,
			Title:       "测试" + comicID,
			ComicStatus: status,
			Enabled:     true,
		})
		if err != nil {
			t.Fatalf("create subscription: %v", err)
		}
		anchor := now.Add(-time.Duration(days) * 24 * time.Hour).UTC()
		if _, err := repository.db.ExecContext(ctx, `
			UPDATE subscriptions SET comic_status = ?, last_new_chapter_at = ?, created_at = ?
			WHERE id = ?`, status, anchor, anchor, item.ID); err != nil {
			t.Fatalf("age subscription: %v", err)
		}
		return item
	}
	completed := createAged("completed", "已完结", 15)
	ongoing := createAged("ongoing", "连载中", 30)
	result, err := repository.MaintainSubscriptions(ctx, map[string]int{"*": 0}, now)
	if err != nil {
		t.Fatalf("maintenance: %v", err)
	}
	if len(result.Archived) != 1 || result.Archived[0].Subscription.ID != completed.ID {
		t.Fatalf("archived events = %+v", result.Archived)
	}
	stillActive, err := repository.Subscription(ctx, ongoing.ID)
	if err != nil {
		t.Fatalf("reload ongoing: %v", err)
	}
	if stillActive.ArchivedAt != nil || !stillActive.Enabled {
		t.Fatalf("disabled ongoing archive rule still archived subscription: %+v", stillActive)
	}
}

func createJobForComic(t *testing.T, repository *Store, comicID, chapterID string) model.DownloadJob {
	t.Helper()
	job, err := repository.CreateDownloadJob(context.Background(), model.DownloadJob{
		SourceID:     "jmcomic",
		ComicID:      comicID,
		ComicTitle:   "作品" + comicID,
		ChapterID:    chapterID,
		ChapterTitle: "第" + chapterID + "话",
	})
	if err != nil {
		t.Fatalf("create job %s/%s: %v", comicID, chapterID, err)
	}
	return job
}

func TestDownloadJobPersistsComicAuthor(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)
	job, err := repository.CreateDownloadJob(ctx, model.DownloadJob{
		SourceID:     "picacg",
		ComicID:      "comic-1",
		ComicTitle:   "作品",
		ComicAuthor:  "作者",
		ChapterID:    "chapter-1",
		ChapterTitle: "第 1 话",
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	stored, err := repository.DownloadJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("reload job: %v", err)
	}
	if stored.ComicAuthor != "作者" {
		t.Fatalf("ComicAuthor = %q, want 作者", stored.ComicAuthor)
	}
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
	next, ok, err := repository.NextQueuedJob(ctx)
	if err != nil {
		t.Fatalf("next queued: %v", err)
	}
	if !ok || next.ID != queued.ID {
		t.Fatalf("next queued = %+v, %v; want job %d", next, ok, queued.ID)
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

func TestListQueuedJobsPageReturnsBoundedRows(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)
	for _, chapterID := range []string{"1", "2", "3", "4", "5"} {
		createJob(t, repository, chapterID)
	}
	first, err := repository.ListQueuedJobsPage(ctx, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.ListQueuedJobsPage(ctx, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("page sizes = %d, %d; want 2, 2", len(first), len(second))
	}
	if first[0].ID == second[0].ID || first[1].ID == second[1].ID {
		t.Fatalf("pages overlap: first=%v second=%v", first, second)
	}
}

func TestNextQueuedJobPrefersManualPriority(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)

	normal := createJob(t, repository, "normal")
	manual, err := repository.CreateDownloadJob(ctx, model.DownloadJob{
		SourceID:      "jmcomic",
		ComicID:       "123",
		ComicTitle:    "测试作品",
		ChapterID:     "manual",
		ChapterTitle:  "手动章节",
		QueuePriority: model.QueuePriorityManual,
	})
	if err != nil {
		t.Fatalf("create manual job: %v", err)
	}

	next, ok, err := repository.NextQueuedJob(ctx)
	if err != nil {
		t.Fatalf("next queued: %v", err)
	}
	if !ok || next.ID != manual.ID {
		t.Fatalf("next queued = %+v, %v; want manual job %d before %d", next, ok, manual.ID, normal.ID)
	}
	jobs, err := repository.ListQueuedJobs(ctx)
	if err != nil {
		t.Fatalf("list queued: %v", err)
	}
	if len(jobs) != 2 || jobs[0].ID != manual.ID || jobs[1].ID != normal.ID {
		t.Fatalf("queued order = %+v, want manual then normal", jobs)
	}
}

func TestInitialDownloadBatchPendingIncludesRetries(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)
	job := createJob(t, repository, "initial")
	pending, err := repository.InitialDownloadBatchPending(ctx, job.SourceID, job.ComicID)
	if err != nil || !pending {
		t.Fatalf("queued batch pending = %v, %v", pending, err)
	}
	retryAt := time.Now().Add(10 * time.Minute)
	if err := repository.FailDownloadJob(ctx, job.ID, 10, 1, "retry", 1, &retryAt); err != nil {
		t.Fatal(err)
	}
	pending, err = repository.InitialDownloadBatchPending(ctx, job.SourceID, job.ComicID)
	if err != nil || !pending {
		t.Fatalf("retrying batch pending = %v, %v", pending, err)
	}
	if err := repository.FailDownloadJob(ctx, job.ID, 10, 1, "terminal", 2, nil); err != nil {
		t.Fatal(err)
	}
	pending, err = repository.InitialDownloadBatchPending(ctx, job.SourceID, job.ComicID)
	if err != nil || pending {
		t.Fatalf("terminal batch pending = %v, %v", pending, err)
	}
}

func TestListInterruptedJobsIncludesRunningAndCanceled(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)
	running := createJob(t, repository, "running")
	canceled := createJob(t, repository, "canceled")
	queued := createJob(t, repository, "queued")
	if err := repository.UpdateDownloadJob(ctx, running.ID, "running", 10, 2, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateDownloadJob(ctx, canceled.ID, "canceled", 10, 1, "", "shutdown"); err != nil {
		t.Fatal(err)
	}
	jobs, err := repository.ListInterruptedJobs(ctx)
	if err != nil {
		t.Fatalf("list interrupted: %v", err)
	}
	if len(jobs) != 2 || jobs[0].ID != running.ID || jobs[1].ID != canceled.ID {
		t.Fatalf("interrupted jobs = %+v, want %d and %d", jobs, running.ID, canceled.ID)
	}
	for _, job := range jobs {
		if job.ID == queued.ID {
			t.Fatal("ordinary queued job was included in restart notice")
		}
	}
}

func TestListDownloadJobsWithoutLimitReturnsEveryRecord(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)
	const total = 605
	for index := 0; index < total; index++ {
		createJob(t, repository, strconv.Itoa(index+1))
	}

	jobs, err := repository.ListDownloadJobs(ctx, 0)
	if err != nil {
		t.Fatalf("list download jobs: %v", err)
	}
	if len(jobs) != total {
		t.Fatalf("download jobs = %d, want %d", len(jobs), total)
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

	running, err := repository.CreateDownloadJob(ctx, model.DownloadJob{
		SourceID:      "jmcomic",
		ComicID:       "123",
		ComicTitle:    "测试作品",
		ChapterID:     "1",
		ChapterTitle:  "第1话",
		QueuePriority: model.QueuePriorityManual,
	})
	if err != nil {
		t.Fatalf("create manual job: %v", err)
	}
	if err := repository.UpdateDownloadJob(ctx, running.ID, "running", 12, 3, "", ""); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	canceled := createJob(t, repository, "2")
	if err := repository.UpdateDownloadJob(ctx, canceled.ID, "canceled", 8, 2, "", "服务停止"); err != nil {
		t.Fatalf("mark canceled: %v", err)
	}
	if err := repository.RequeueRunningJobs(ctx); err != nil {
		t.Fatalf("requeue running jobs: %v", err)
	}

	jobs, err := repository.ListQueuedJobs(ctx)
	if err != nil {
		t.Fatalf("list queued: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("queued jobs = %v, want 2 restored jobs", jobs)
	}
	wantProgress := map[int64]int{running.ID: 3, canceled.ID: 2}
	for _, job := range jobs {
		if job.Status != "queued" {
			t.Fatalf("job %d requeued status = %q, want queued", job.ID, job.Status)
		}
		if job.CompletedPages != wantProgress[job.ID] {
			t.Fatalf("job %d completed pages = %d, want %d", job.ID, job.CompletedPages, wantProgress[job.ID])
		}
		if job.StartedAt != nil || job.FinishedAt != nil || job.Error != "" {
			t.Fatalf("job %d retained interrupted metadata: %+v", job.ID, job)
		}
		if job.ID == running.ID && job.QueuePriority != model.QueuePriorityManual {
			t.Fatalf("manual job priority = %d after requeue, want %d", job.QueuePriority, model.QueuePriorityManual)
		}
		if job.ID == canceled.ID && job.QueuePriority != model.QueuePriorityNormal {
			t.Fatalf("routine job priority = %d after requeue, want %d", job.QueuePriority, model.QueuePriorityNormal)
		}
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

func TestOpenBackfillsCronForLegacySubscriptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manco.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if _, err := database.Exec(`CREATE TABLE subscriptions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		source_id TEXT NOT NULL,
		comic_id TEXT NOT NULL,
		title TEXT NOT NULL,
		cover TEXT NOT NULL DEFAULT '',
		author TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 1,
		auto_download INTEGER NOT NULL DEFAULT 1,
		last_chapter_id TEXT NOT NULL DEFAULT '',
		last_chapter_title TEXT NOT NULL DEFAULT '',
		last_chapter_order REAL NOT NULL DEFAULT 0,
		last_checked_at DATETIME,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(source_id, comic_id)
	)`); err != nil {
		_ = database.Close()
		t.Fatalf("create legacy subscriptions table: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO subscriptions(source_id, comic_id, title, created_at) VALUES('stub', '1', '测试作品', '2026-10-09 10:30:00')`); err != nil {
		_ = database.Close()
		t.Fatalf("insert legacy subscription: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	repository, err := Open(path)
	if err != nil {
		t.Fatalf("migrate legacy database: %v", err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	subscriptions, err := repository.ListSubscriptions(context.Background())
	if err != nil {
		t.Fatalf("list migrated subscriptions: %v", err)
	}
	if len(subscriptions) != 1 {
		t.Fatalf("subscriptions after migration = %d, want 1", len(subscriptions))
	}
	if subscriptions[0].CronExpr != "0 10 * * 5" {
		t.Fatalf("backfilled cron = %q, want 0 10 * * 5", subscriptions[0].CronExpr)
	}
}

func TestDeleteAllSessionsInvalidatesLogins(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)
	if err := repository.CreateUser(ctx, "alice", "hash-a"); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	user, err := repository.UserByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("load alice: %v", err)
	}
	if err := repository.CreateSession(ctx, "token-hash-a", user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := repository.UserBySession(ctx, "token-hash-a", time.Now()); err != nil {
		t.Fatalf("session before restart: %v", err)
	}

	if err := repository.DeleteAllSessions(ctx); err != nil {
		t.Fatalf("delete all sessions: %v", err)
	}
	if _, err := repository.UserBySession(ctx, "token-hash-a", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session after restart error = %v, want ErrNotFound", err)
	}
}

func TestUpdateSubscriptionReactivationResetsCheckCycle(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)
	sub, err := repository.UpsertSubscription(ctx, model.Subscription{
		SourceID:     "stub",
		ComicID:      "1",
		Title:        "测试作品",
		Enabled:      true,
		AutoDownload: true,
		CronExpr:     "0 10 * * 5",
	})
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if _, err := repository.db.ExecContext(ctx, `UPDATE subscriptions SET last_checked_at = '2026-10-01 00:00:00', enabled = 0 WHERE id = ?`, sub.ID); err != nil {
		t.Fatalf("seed disabled subscription: %v", err)
	}
	if err := repository.UpdateSubscription(ctx, sub.ID, true, true, "0 10 * * 5"); err != nil {
		t.Fatalf("reactivate subscription: %v", err)
	}
	updated, err := repository.Subscription(ctx, sub.ID)
	if err != nil {
		t.Fatalf("reload subscription: %v", err)
	}
	if updated.LastCheckedAt != nil && !updated.LastCheckedAt.IsZero() {
		t.Fatalf("last_checked_at = %v, want cleared on reactivation", updated.LastCheckedAt)
	}
	if !updated.Enabled {
		t.Fatal("subscription should be enabled after reactivation")
	}
}

func TestSubscriptionChapterCountRoundTrip(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)
	sub, err := repository.UpsertSubscription(ctx, model.Subscription{
		SourceID: "stub",
		ComicID:  "chapter-count",
		Title:    "章节统计作品",
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if err := repository.UpdateSubscriptionChapterCount(ctx, sub.ID, 37); err != nil {
		t.Fatalf("update chapter count: %v", err)
	}
	updated, err := repository.Subscription(ctx, sub.ID)
	if err != nil {
		t.Fatalf("reload subscription: %v", err)
	}
	if updated.ChapterCount != 37 {
		t.Fatalf("chapter count = %d, want 37", updated.ChapterCount)
	}

	if _, err := repository.ImportSubscription(ctx, model.Subscription{
		SourceID:     "stub",
		ComicID:      "imported-count",
		Title:        "恢复章节统计作品",
		Enabled:      true,
		ChapterCount: 58,
	}); err != nil {
		t.Fatalf("import subscription: %v", err)
	}
	imported, err := repository.SubscriptionByComic(ctx, "stub", "imported-count")
	if err != nil {
		t.Fatalf("reload imported subscription: %v", err)
	}
	if imported.ChapterCount != 58 {
		t.Fatalf("imported chapter count = %d, want 58", imported.ChapterCount)
	}
}

func TestListArchivedSubscriptionsSeparatesArchivedFromActive(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)
	active, err := repository.UpsertSubscription(ctx, model.Subscription{
		SourceID: "stub",
		ComicID:  "1",
		Title:    "在订作品",
		Enabled:  true,
		CronExpr: "0 10 * * 5",
	})
	if err != nil {
		t.Fatalf("create active subscription: %v", err)
	}
	archived, err := repository.UpsertSubscription(ctx, model.Subscription{
		SourceID: "stub",
		ComicID:  "2",
		Title:    "归档作品",
		Enabled:  true,
		CronExpr: "0 10 * * 5",
	})
	if err != nil {
		t.Fatalf("create archived subscription: %v", err)
	}
	if err := repository.ArchiveSubscription(ctx, archived.ID, "manual"); err != nil {
		t.Fatalf("archive subscription: %v", err)
	}

	visible, err := repository.ListSubscriptions(ctx)
	if err != nil {
		t.Fatalf("list subscriptions: %v", err)
	}
	if len(visible) != 1 || visible[0].ID != active.ID {
		t.Fatalf("订阅清单应只保留在订订阅，got %+v", visible)
	}

	list, err := repository.ListArchivedSubscriptions(ctx)
	if err != nil {
		t.Fatalf("list archived subscriptions: %v", err)
	}
	if len(list) != 1 || list[0].ID != archived.ID {
		t.Fatalf("归档集应只包含已归档订阅，got %+v", list)
	}
	if list[0].ArchivedAt == nil {
		t.Fatal("归档订阅应带上 archivedAt")
	}
}

func TestRestoreSubscriptionReturnsToActiveList(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)
	sub, err := repository.UpsertSubscription(ctx, model.Subscription{
		SourceID: "stub",
		ComicID:  "restore-1",
		Title:    "待恢复作品",
		Enabled:  true,
		CronExpr: "0 10 * * 5",
	})
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if err := repository.ArchiveSubscription(ctx, sub.ID, "manual"); err != nil {
		t.Fatalf("archive subscription: %v", err)
	}
	if err := repository.RestoreSubscription(ctx, sub.ID); err != nil {
		t.Fatalf("restore subscription: %v", err)
	}

	archived, err := repository.ListArchivedSubscriptions(ctx)
	if err != nil {
		t.Fatalf("list archived subscriptions: %v", err)
	}
	if len(archived) != 0 {
		t.Fatalf("恢复后归档集应为空，got %+v", archived)
	}

	active, err := repository.ListSubscriptions(ctx)
	if err != nil {
		t.Fatalf("list subscriptions: %v", err)
	}
	if len(active) != 1 || active[0].ID != sub.ID {
		t.Fatalf("恢复后订阅清单应包含该订阅，got %+v", active)
	}
	if !active[0].Enabled {
		t.Fatal("恢复后的订阅应重新启用")
	}
	if active[0].ArchivedAt != nil || active[0].ArchiveReason != "" {
		t.Fatalf("恢复后应清除归档标记，got archivedAt=%v reason=%q", active[0].ArchivedAt, active[0].ArchiveReason)
	}
}

func TestCompletedDownloadJobsByComic(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)

	done := createJob(t, repository, "1")
	if err := repository.UpdateDownloadJob(ctx, done.ID, "completed", 8, 8, "/downloads/测试作品/第1话.cbz", ""); err != nil {
		t.Fatalf("complete job: %v", err)
	}
	createJob(t, repository, "2")
	other, err := repository.CreateDownloadJob(ctx, model.DownloadJob{
		SourceID:     "baozimh",
		ComicID:      "123",
		ComicTitle:   "测试作品",
		ChapterID:    "1",
		ChapterTitle: "第1话",
	})
	if err != nil {
		t.Fatalf("create other source job: %v", err)
	}
	if err := repository.UpdateDownloadJob(ctx, other.ID, "completed", 8, 8, "/downloads/other/第1话.cbz", ""); err != nil {
		t.Fatalf("complete other source job: %v", err)
	}

	jobs, err := repository.CompletedDownloadJobsByComic(ctx, "jmcomic", "123")
	if err != nil {
		t.Fatalf("list completed jobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != done.ID {
		t.Fatalf("completed jobs = %+v, want only job %d", jobs, done.ID)
	}
}

func TestCompletedDownloadJobsByComicIgnoresHistoryLimits(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)
	const total = 10050

	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO download_jobs(
			source_id, comic_id, comic_title, chapter_id, chapter_title, chapter_order,
			status, file_path, created_at, updated_at, finished_at
		) VALUES(?, ?, ?, ?, ?, ?, 'completed', ?, '2020-01-01 00:00:00', '2020-01-01 00:00:00', '2020-01-01 00:00:00')`)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("prepare insert: %v", err)
	}
	for index := 0; index < total; index++ {
		chapter := strconv.Itoa(index + 1)
		if _, err := statement.ExecContext(ctx,
			"subscription-source",
			"subscription-work",
			"长期订阅作品",
			"chapter-"+chapter,
			"第"+chapter+"话",
			float64(index+1),
			"/downloads/长期订阅作品/第"+chapter+"话.cbz",
		); err != nil {
			_ = statement.Close()
			_ = tx.Rollback()
			t.Fatalf("insert completed row %d: %v", index, err)
		}
	}
	if _, err := statement.ExecContext(ctx,
		"subscription-source",
		"other-work",
		"其他作品",
		"other-1",
		"第1话",
		1.0,
		"/downloads/其他作品/第1话.cbz",
	); err != nil {
		_ = statement.Close()
		_ = tx.Rollback()
		t.Fatalf("insert unrelated completed row: %v", err)
	}
	if err := statement.Close(); err != nil {
		_ = tx.Rollback()
		t.Fatalf("close insert statement: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit completed rows: %v", err)
	}

	jobs, err := repository.CompletedDownloadJobsByComic(ctx, "subscription-source", "subscription-work")
	if err != nil {
		t.Fatalf("list completed subscription jobs: %v", err)
	}
	if len(jobs) != total {
		t.Fatalf("completed subscription jobs = %d, want %d", len(jobs), total)
	}
	if jobs[0].ChapterOrder != 1 || jobs[len(jobs)-1].ChapterOrder != total {
		t.Fatalf("completed subscription record order/range mismatch: first=%v last=%v", jobs[0].ChapterOrder, jobs[len(jobs)-1].ChapterOrder)
	}
}

func TestListDownloadJobsKeepsCompletedWhenManyActive(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)

	done := createJob(t, repository, "done-1")
	if err := repository.UpdateDownloadJob(ctx, done.ID, "completed", 10, 10, "/tmp/done.cbz", ""); err != nil {
		t.Fatalf("complete job: %v", err)
	}
	for index := 0; index < 40; index++ {
		createJob(t, repository, "active-"+strconv.Itoa(index))
	}

	jobs, err := repository.ListDownloadJobs(ctx, 20)
	if err != nil {
		t.Fatalf("list downloads: %v", err)
	}
	found := false
	for _, job := range jobs {
		if job.ID == done.ID && job.Status == "completed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("completed job %d missing from %d results", done.ID, len(jobs))
	}

	stats, err := repository.DownloadJobStats(ctx)
	if err != nil {
		t.Fatalf("download stats: %v", err)
	}
	key := "jmcomic\x00" + done.ComicID
	if got := stats[key].Succeeded; got != 1 {
		t.Fatalf("succeeded = %d, want 1", got)
	}
}

func TestListDownloadJobsKeepsFailedWhenManyQueued(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)

	failed := createJob(t, repository, "failed-1")
	if err := repository.FailDownloadJob(ctx, failed.ID, 0, 0, "download failed", 2, nil); err != nil {
		t.Fatalf("mark failed job: %v", err)
	}
	for index := 0; index < 40; index++ {
		createJob(t, repository, "queued-"+strconv.Itoa(index))
	}

	jobs, err := repository.ListDownloadJobs(ctx, 5)
	if err != nil {
		t.Fatalf("list downloads: %v", err)
	}
	found := false
	for _, job := range jobs {
		if job.ID == failed.ID && job.Status == "failed" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("failed job %d missing from %d results", failed.ID, len(jobs))
	}
}

func TestListSubscriptionsPageFiltersSortsAndCounts(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)
	items := []model.Subscription{
		{SourceID: "comic-source", ComicID: "1", Title: "Zulu Comic", DownloadDir: "/downloads/a"},
		{SourceID: "book-source", ComicID: "2", Title: "Alpha Book", DownloadDir: "/downloads/a"},
		{SourceID: "other-source", ComicID: "3", Title: "Beta Video", DownloadDir: "/downloads/b"},
	}
	for _, item := range items {
		item.Enabled = true
		if _, err := repository.UpsertSubscription(ctx, item); err != nil {
			t.Fatalf("create subscription %q: %v", item.Title, err)
		}
	}

	page, total, err := repository.ListSubscriptionsPage(ctx, SubscriptionFilter{
		SourceIDs:   []string{"book-source"},
		Sort:        "title",
		Descending:  false,
		DownloadDir: "/downloads/a",
	}, 10, 0)
	if err != nil {
		t.Fatalf("book page: %v", err)
	}
	if total != 1 || len(page) != 1 || page[0].Title != "Alpha Book" {
		t.Fatalf("book page total=%d items=%+v", total, page)
	}

	page, total, err = repository.ListSubscriptionsPage(ctx, SubscriptionFilter{
		ExcludeSourceIDs: []string{"book-source", "other-source"},
		Sort:             "title",
	}, 10, 0)
	if err != nil {
		t.Fatalf("comic page: %v", err)
	}
	if total != 1 || len(page) != 1 || page[0].Title != "Zulu Comic" {
		t.Fatalf("comic page total=%d items=%+v", total, page)
	}

	page, total, err = repository.ListSubscriptionsPage(ctx, SubscriptionFilter{
		DownloadDir: "/downloads/a",
		Sort:        "title",
		Descending:  true,
	}, 1, 1)
	if err != nil {
		t.Fatalf("sorted second page: %v", err)
	}
	if total != 2 || len(page) != 1 || page[0].Title != "Alpha Book" {
		t.Fatalf("sorted second page total=%d items=%+v", total, page)
	}

	counts, err := repository.SubscriptionSourceCounts(ctx)
	if err != nil {
		t.Fatalf("source counts: %v", err)
	}
	if counts["comic-source"] != 1 || counts["book-source"] != 1 || counts["other-source"] != 1 {
		t.Fatalf("source counts = %+v", counts)
	}
	dirs, err := repository.SubscriptionDownloadDirs(ctx)
	if err != nil {
		t.Fatalf("download dirs: %v", err)
	}
	if len(dirs) != 2 || dirs[0] != "/downloads/a" || dirs[1] != "/downloads/b" {
		t.Fatalf("download dirs = %+v", dirs)
	}
}

func TestDownloadJobStatsForSubscriptionsOnlyScansRequestedWorks(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)
	completed := createJobForComic(t, repository, "wanted", "1")
	if err := repository.UpdateDownloadJob(ctx, completed.ID, "completed", 1, 1, "/tmp/1.cbz", ""); err != nil {
		t.Fatalf("complete wanted job: %v", err)
	}
	failed := createJobForComic(t, repository, "wanted", "2")
	if err := repository.FailDownloadJob(ctx, failed.ID, 1, 0, "boom", 0, nil); err != nil {
		t.Fatalf("fail wanted job: %v", err)
	}
	_ = createJobForComic(t, repository, "ignored", "3")

	stats, err := repository.DownloadJobStatsForSubscriptions(ctx, []model.Subscription{
		{SourceID: "jmcomic", ComicID: "wanted"},
	})
	if err != nil {
		t.Fatalf("targeted stats: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("targeted stats = %+v", stats)
	}
	got := stats["jmcomic\x00wanted"]
	if got.Succeeded != 1 || got.Failed != 1 || got.Pending != 0 {
		t.Fatalf("wanted stats = %+v", got)
	}
}

func TestExtensionCandidatesCacheRoundTrip(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)

	if _, _, err := repository.ExtensionCandidates(ctx, "repo", "ext"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty cache error = %v, want ErrNotFound", err)
	}
	if err := repository.SaveExtensionCandidates(ctx, "repo", "ext", "1.2.3", `{"candidates":[]}`); err != nil {
		t.Fatalf("SaveExtensionCandidates: %v", err)
	}
	payload, version, err := repository.ExtensionCandidates(ctx, "repo", "ext")
	if err != nil {
		t.Fatalf("ExtensionCandidates: %v", err)
	}
	if version != "1.2.3" || payload != `{"candidates":[]}` {
		t.Fatalf("payload = %q version = %q", payload, version)
	}

	// 同一个仓库+扩展再写一次是覆盖，保留最新一次枚举的结果。
	if err := repository.SaveExtensionCandidates(ctx, "repo", "ext", "1.2.4", `{"candidates":[1]}`); err != nil {
		t.Fatalf("overwrite candidates: %v", err)
	}
	payload, version, err = repository.ExtensionCandidates(ctx, "repo", "ext")
	if err != nil {
		t.Fatalf("ExtensionCandidates after overwrite: %v", err)
	}
	if version != "1.2.4" || payload != `{"candidates":[1]}` {
		t.Fatalf("overwritten payload = %q version = %q", payload, version)
	}

	if err := repository.SaveExtensionCandidates(ctx, "repo", "other", "1", `{}`); err != nil {
		t.Fatalf("save second extension: %v", err)
	}
	if err := repository.DeleteExtensionCandidates(ctx, "repo", "ext"); err != nil {
		t.Fatalf("DeleteExtensionCandidates: %v", err)
	}
	if _, _, err := repository.ExtensionCandidates(ctx, "repo", "ext"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted entry error = %v, want ErrNotFound", err)
	}
	if _, _, err := repository.ExtensionCandidates(ctx, "repo", "other"); err != nil {
		t.Fatalf("deleting one extension removed another: %v", err)
	}

	// 删除仓库时整片清掉，避免留下无主的缓存行。
	if err := repository.DeleteExtensionCandidatesForRepository(ctx, "repo"); err != nil {
		t.Fatalf("DeleteExtensionCandidatesForRepository: %v", err)
	}
	if _, _, err := repository.ExtensionCandidates(ctx, "repo", "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repository purge left entries: %v", err)
	}
}

func TestDownloadJobPageFiltersAndCounts(t *testing.T) {
	ctx := context.Background()
	repository := openTestStore(t)

	first := createJob(t, repository, "1")
	if err := repository.UpdateDownloadJob(ctx, first.ID, "completed", 1, 1, "/tmp/1.cbz", ""); err != nil {
		t.Fatalf("complete job 1: %v", err)
	}
	second := createJob(t, repository, "2")
	if err := repository.UpdateDownloadJob(ctx, second.ID, "completed", 1, 1, "/tmp/2.cbz", ""); err != nil {
		t.Fatalf("complete job 2: %v", err)
	}
	failed := createJob(t, repository, "3")
	if err := repository.UpdateDownloadJob(ctx, failed.ID, "failed", 1, 0, "", "boom"); err != nil {
		t.Fatalf("fail job: %v", err)
	}
	_ = createJob(t, repository, "4") // 仍在排队

	// active 页 = 未完成 + 失败，分页取且总数只算这一类。
	page, total, err := repository.DownloadJobPage(ctx, "active", 1, 0)
	if err != nil {
		t.Fatalf("DownloadJobPage(active) error = %v", err)
	}
	if total != 2 {
		t.Fatalf("active total = %d, want 2", total)
	}
	if len(page) != 1 {
		t.Fatalf("active page size = %d, want 1", len(page))
	}
	second1, total1, err := repository.DownloadJobPage(ctx, "active", 1, 1)
	if err != nil {
		t.Fatalf("DownloadJobPage(active, offset 1) error = %v", err)
	}
	if len(second1) != 1 || total1 != 2 {
		t.Fatalf("active second page = %d rows, total %d", len(second1), total1)
	}
	if page[0].ID == second1[0].ID {
		t.Fatalf("分页两页返回了同一条：%d", page[0].ID)
	}

	// completed 页只看已完成。
	completedPage, completedTotal, err := repository.DownloadJobPage(ctx, "completed", 10, 0)
	if err != nil {
		t.Fatalf("DownloadJobPage(completed) error = %v", err)
	}
	if completedTotal != 2 || len(completedPage) != 2 {
		t.Fatalf("completed page = %d rows, total %d, want 2/2", len(completedPage), completedTotal)
	}
	for _, job := range completedPage {
		if job.Status != "completed" {
			t.Fatalf("completed page 混入了状态 %q", job.Status)
		}
	}

	// 越界 offset 返回空页但总数不变，虚拟列表据此保留滚动条高度。
	emptyPage, emptyTotal, err := repository.DownloadJobPage(ctx, "completed", 10, 99)
	if err != nil {
		t.Fatalf("DownloadJobPage(completed, offset 99) error = %v", err)
	}
	if len(emptyPage) != 0 || emptyTotal != 2 {
		t.Fatalf("越界分页 = %d rows, total %d", len(emptyPage), emptyTotal)
	}
}
