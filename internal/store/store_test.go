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

	running := createJob(t, repository, "1")
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
