package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hyaeve/manco/internal/cronutil"
	"github.com/hyaeve/manco/internal/model"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")
var ErrUsernameTaken = errors.New("username already exists")

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token_hash TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at)`,
		`CREATE TABLE IF NOT EXISTS source_accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id TEXT NOT NULL UNIQUE,
			username TEXT NOT NULL DEFAULT '',
			secret_cipher TEXT NOT NULL DEFAULT '',
			token_cipher TEXT NOT NULL DEFAULT '',
			cookie_cipher TEXT NOT NULL DEFAULT '',
			home_url TEXT NOT NULL DEFAULT '',
			extra_json TEXT NOT NULL DEFAULT '{}',
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id TEXT NOT NULL,
			comic_id TEXT NOT NULL,
			title TEXT NOT NULL,
			cover TEXT NOT NULL DEFAULT '',
			author TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			auto_download INTEGER NOT NULL DEFAULT 1,
			cron_expr TEXT NOT NULL DEFAULT '',
			last_chapter_id TEXT NOT NULL DEFAULT '',
			last_chapter_title TEXT NOT NULL DEFAULT '',
			last_chapter_order REAL NOT NULL DEFAULT 0,
			comic_status TEXT NOT NULL DEFAULT '',
			last_new_chapter_at DATETIME,
			last_checked_at DATETIME,
			disabled_at DATETIME,
			completed_at DATETIME,
			archived_at DATETIME,
			archive_reason TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(source_id, comic_id)
		)`,
		`CREATE TABLE IF NOT EXISTS download_jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id TEXT NOT NULL,
			comic_id TEXT NOT NULL,
			comic_title TEXT NOT NULL,
			comic_cover TEXT NOT NULL DEFAULT '',
			chapter_id TEXT NOT NULL,
			chapter_title TEXT NOT NULL,
			chapter_order REAL NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'queued',
			total_pages INTEGER NOT NULL DEFAULT 0,
			completed_pages INTEGER NOT NULL DEFAULT 0,
			file_path TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			retry_count INTEGER NOT NULL DEFAULT 0,
			next_retry_at DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			started_at DATETIME,
			finished_at DATETIME,
			UNIQUE(source_id, comic_id, chapter_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_download_jobs_status ON download_jobs(status, id)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}
	if err := s.ensureColumn(ctx, "subscriptions", "cron_expr", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	for _, column := range []struct {
		name        string
		declaration string
	}{
		{"comic_status", `TEXT NOT NULL DEFAULT ''`},
		{"last_new_chapter_at", `DATETIME`},
		{"disabled_at", `DATETIME`},
		{"completed_at", `DATETIME`},
		{"archived_at", `DATETIME`},
		{"archive_reason", `TEXT NOT NULL DEFAULT ''`},
	} {
		if err := s.ensureColumn(ctx, "subscriptions", column.name, column.declaration); err != nil {
			return err
		}
	}
	if err := s.ensureColumn(ctx, "download_jobs", "retry_count", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "download_jobs", "next_retry_at", `DATETIME`); err != nil {
		return err
	}
	if err := s.backfillSubscriptionCron(ctx); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE subscriptions SET last_new_chapter_at = created_at WHERE last_new_chapter_at IS NULL`); err != nil {
		return fmt.Errorf("backfill subscription update baseline: %w", err)
	}
	return nil
}

func (s *Store) ensureColumn(ctx context.Context, table, column, declaration string) error {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return fmt.Errorf("inspect %s schema: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, declaration)); err != nil {
		return fmt.Errorf("add %s.%s: %w", table, column, err)
	}
	return nil
}

func (s *Store) backfillSubscriptionCron(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id, created_at FROM subscriptions WHERE cron_expr = ''`)
	if err != nil {
		return err
	}
	type pending struct {
		id        int64
		createdAt time.Time
	}
	var items []pending
	for rows.Next() {
		var item pending
		if err := rows.Scan(&item.id, &item.createdAt); err != nil {
			_ = rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range items {
		if _, err := s.db.ExecContext(ctx, `UPDATE subscriptions SET cron_expr = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, cronutil.Default(item.createdAt), item.id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) EnsureUser(ctx context.Context, username, passwordHash string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO users(username, password_hash) VALUES(?, ?) ON CONFLICT(username) DO NOTHING`, username, passwordHash)
	return err
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	return count, err
}

func (s *Store) CreateUser(ctx context.Context, username, passwordHash string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO users(username, password_hash) VALUES(?, ?)`, username, passwordHash)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return ErrUsernameTaken
	}
	return err
}

func (s *Store) UserByUsername(ctx context.Context, username string) (model.User, error) {
	var user model.User
	err := s.db.QueryRowContext(ctx, `SELECT id, username, password_hash, created_at FROM users WHERE username = ?`, username).
		Scan(&user.ID, &user.Username, &user.PasswordHash, &user.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.User{}, ErrNotFound
	}
	return user, err
}

func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID int64, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(token_hash, user_id, expires_at) VALUES(?, ?, ?)`, tokenHash, userID, expiresAt.UTC())
	return err
}

func (s *Store) UserBySession(ctx context.Context, tokenHash string, now time.Time) (model.User, error) {
	var user model.User
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.password_hash, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ?`, tokenHash, now.UTC()).
		Scan(&user.ID, &user.Username, &user.PasswordHash, &user.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.User{}, ErrNotFound
	}
	return user, err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteAllSessions invalidates every persisted login. It is called once on
// startup so a restart always requires users to sign in again.
func (s *Store) DeleteAllSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions`)
	return err
}

func (s *Store) CleanupSessions(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.UTC())
	return err
}

func (s *Store) UpsertSourceAccount(ctx context.Context, account model.SourceAccount) error {
	extra := account.Extra
	if len(extra) == 0 {
		extra = json.RawMessage(`{}`)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO source_accounts(source_id, username, secret_cipher, token_cipher, cookie_cipher, home_url, extra_json, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(source_id) DO UPDATE SET
			username = excluded.username,
			secret_cipher = excluded.secret_cipher,
			token_cipher = excluded.token_cipher,
			cookie_cipher = excluded.cookie_cipher,
			home_url = excluded.home_url,
			extra_json = excluded.extra_json,
			updated_at = CURRENT_TIMESTAMP`,
		account.SourceID, account.Username, account.SecretCipher, account.TokenCipher, account.CookieCipher, account.HomeURL, string(extra))
	return err
}

func (s *Store) SourceAccount(ctx context.Context, sourceID string) (model.SourceAccount, error) {
	return s.scanAccount(s.db.QueryRowContext(ctx, `SELECT id, source_id, username, secret_cipher, token_cipher, cookie_cipher, home_url, extra_json, updated_at FROM source_accounts WHERE source_id = ?`, sourceID))
}

func (s *Store) ListSourceAccounts(ctx context.Context) ([]model.SourceAccount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, source_id, username, secret_cipher, token_cipher, cookie_cipher, home_url, extra_json, updated_at FROM source_accounts ORDER BY source_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var accounts []model.SourceAccount
	for rows.Next() {
		account, err := s.scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func (s *Store) scanAccount(row rowScanner) (model.SourceAccount, error) {
	var account model.SourceAccount
	var extra string
	err := row.Scan(&account.ID, &account.SourceID, &account.Username, &account.SecretCipher, &account.TokenCipher, &account.CookieCipher, &account.HomeURL, &extra, &account.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.SourceAccount{}, ErrNotFound
	}
	if err != nil {
		return model.SourceAccount{}, err
	}
	if extra == "" {
		extra = "{}"
	}
	account.Extra = json.RawMessage(extra)
	return account, nil
}

func (s *Store) DeleteSourceAccount(ctx context.Context, sourceID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM source_accounts WHERE source_id = ?`, sourceID)
	return err
}

func (s *Store) UpsertSubscription(ctx context.Context, sub model.Subscription) (model.Subscription, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO subscriptions(source_id, comic_id, title, cover, author, enabled, auto_download, cron_expr, last_chapter_id, last_chapter_title, last_chapter_order, comic_status, last_new_chapter_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(source_id, comic_id) DO UPDATE SET
			title = excluded.title,
			cover = excluded.cover,
			author = excluded.author,
			enabled = excluded.enabled,
			auto_download = excluded.auto_download,
			cron_expr = CASE WHEN excluded.cron_expr <> '' THEN excluded.cron_expr ELSE subscriptions.cron_expr END,
			comic_status = CASE WHEN excluded.comic_status <> '' THEN excluded.comic_status ELSE subscriptions.comic_status END,
			last_chapter_id = CASE WHEN subscriptions.last_chapter_id = '' THEN excluded.last_chapter_id ELSE subscriptions.last_chapter_id END,
			last_chapter_title = CASE WHEN subscriptions.last_chapter_id = '' THEN excluded.last_chapter_title ELSE subscriptions.last_chapter_title END,
			last_chapter_order = CASE WHEN subscriptions.last_chapter_id = '' THEN excluded.last_chapter_order ELSE subscriptions.last_chapter_order END,
			disabled_at = NULL,
			archived_at = NULL,
			archive_reason = '',
			updated_at = CURRENT_TIMESTAMP`,
		sub.SourceID, sub.ComicID, sub.Title, sub.Cover, sub.Author, sub.Enabled, sub.AutoDownload, sub.CronExpr, sub.LastChapterID, sub.LastChapterTitle, sub.LastChapterOrder, sub.ComicStatus)
	if err != nil {
		return model.Subscription{}, err
	}
	return s.SubscriptionByComic(ctx, sub.SourceID, sub.ComicID)
}

func (s *Store) SubscriptionByComic(ctx context.Context, sourceID, comicID string) (model.Subscription, error) {
	return scanSubscription(s.db.QueryRowContext(ctx, subscriptionSelect+` WHERE source_id = ? AND comic_id = ?`, sourceID, comicID))
}

func (s *Store) Subscription(ctx context.Context, id int64) (model.Subscription, error) {
	return scanSubscription(s.db.QueryRowContext(ctx, subscriptionSelect+` WHERE id = ?`, id))
}

func (s *Store) ListSubscriptions(ctx context.Context) ([]model.Subscription, error) {
	rows, err := s.db.QueryContext(ctx, subscriptionSelect+` WHERE archived_at IS NULL ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var subscriptions []model.Subscription
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		subscriptions = append(subscriptions, sub)
	}
	return subscriptions, rows.Err()
}

const subscriptionSelect = `SELECT id, source_id, comic_id, title, cover, author, enabled, auto_download, cron_expr, last_chapter_id, last_chapter_title, last_chapter_order, comic_status, last_new_chapter_at, last_checked_at, disabled_at, completed_at, archived_at, archive_reason, created_at, updated_at FROM subscriptions`

func scanSubscription(row rowScanner) (model.Subscription, error) {
	var sub model.Subscription
	err := row.Scan(&sub.ID, &sub.SourceID, &sub.ComicID, &sub.Title, &sub.Cover, &sub.Author, &sub.Enabled, &sub.AutoDownload, &sub.CronExpr, &sub.LastChapterID, &sub.LastChapterTitle, &sub.LastChapterOrder, &sub.ComicStatus, &sub.LastNewChapterAt, &sub.LastCheckedAt, &sub.DisabledAt, &sub.CompletedAt, &sub.ArchivedAt, &sub.ArchiveReason, &sub.CreatedAt, &sub.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Subscription{}, ErrNotFound
	}
	return sub, err
}

func (s *Store) UpdateSubscription(ctx context.Context, id int64, enabled, autoDownload bool, cronExpr string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE subscriptions SET enabled = ?, auto_download = ?, cron_expr = ?,
			last_checked_at = CASE WHEN ? = 1 THEN NULL WHEN cron_expr = ? THEN last_checked_at ELSE NULL END,
			last_new_chapter_at = CASE WHEN ? = 1 AND enabled = 0 THEN CURRENT_TIMESTAMP ELSE last_new_chapter_at END,
			disabled_at = CASE WHEN ? = 1 THEN NULL ELSE disabled_at END,
			archived_at = CASE WHEN ? = 1 THEN NULL ELSE archived_at END,
			archive_reason = CASE WHEN ? = 1 THEN '' ELSE archive_reason END,
			updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		enabled, autoDownload, cronExpr, enabled, cronExpr, enabled, enabled, enabled, enabled, id)
	return err
}

func (s *Store) UpdateSubscriptionCheck(ctx context.Context, id int64, chapterID, chapterTitle string, chapterOrder float64, comicStatus string, completed, newChapter bool) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE subscriptions SET last_chapter_id = ?, last_chapter_title = ?, last_chapter_order = ?,
			comic_status = ?,
			completed_at = CASE WHEN ? = 1 THEN COALESCE(completed_at, CURRENT_TIMESTAMP) ELSE NULL END,
			last_new_chapter_at = CASE WHEN ? = 1 THEN CURRENT_TIMESTAMP ELSE last_new_chapter_at END,
			last_checked_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, chapterID, chapterTitle, chapterOrder, comicStatus, completed, newChapter, id)
	return err
}

// MaintainSubscriptions closes stale subscriptions and archives them after
// the configured grace period. Only subscriptions closed automatically are
// archived by the stale rule; manual pauses remain visible for the user.
func (s *Store) MaintainSubscriptions(ctx context.Context, staleDays map[string]int, now time.Time) error {
	now = now.UTC()
	completedCutoff := now.Add(-15 * 24 * time.Hour)
	if _, err := s.db.ExecContext(ctx, `
		UPDATE subscriptions SET archived_at = ?, archive_reason = 'completed', enabled = 0, updated_at = CURRENT_TIMESTAMP
		WHERE archived_at IS NULL AND completed_at IS NOT NULL AND completed_at <= ?`, now, completedCutoff); err != nil {
		return err
	}
	for sourceID, days := range staleDays {
		if days <= 0 {
			continue
		}
		cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
		if _, err := s.db.ExecContext(ctx, `
			UPDATE subscriptions SET enabled = 0, disabled_at = ?, updated_at = CURRENT_TIMESTAMP
			WHERE archived_at IS NULL AND enabled = 1 AND source_id = ?
			  AND COALESCE(last_new_chapter_at, created_at) <= ?`, now, sourceID, cutoff); err != nil {
			return err
		}
	}
	disabledCutoff := now.Add(-15 * 24 * time.Hour)
	_, err := s.db.ExecContext(ctx, `
		UPDATE subscriptions SET archived_at = ?, archive_reason = 'stale', updated_at = CURRENT_TIMESTAMP
		WHERE archived_at IS NULL AND disabled_at IS NOT NULL AND disabled_at <= ?`, now, disabledCutoff)
	return err
}
func (s *Store) DeleteSubscription(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM subscriptions WHERE id = ?`, id)
	return err
}

func (s *Store) CreateDownloadJob(ctx context.Context, job model.DownloadJob) (model.DownloadJob, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO download_jobs(source_id, comic_id, comic_title, comic_cover, chapter_id, chapter_title, chapter_order, status)
		VALUES(?, ?, ?, ?, ?, ?, ?, 'queued')
		ON CONFLICT(source_id, comic_id, chapter_id) DO UPDATE SET
			comic_title = excluded.comic_title,
			comic_cover = excluded.comic_cover,
			chapter_title = excluded.chapter_title,
			chapter_order = excluded.chapter_order,
			status = CASE WHEN download_jobs.status IN ('completed', 'running') THEN download_jobs.status ELSE 'queued' END,
			error = CASE WHEN download_jobs.status = 'completed' THEN download_jobs.error ELSE '' END,
			retry_count = CASE WHEN download_jobs.status IN ('completed', 'running') THEN download_jobs.retry_count ELSE 0 END,
			next_retry_at = CASE WHEN download_jobs.status IN ('completed', 'running') THEN download_jobs.next_retry_at ELSE NULL END,
			updated_at = CURRENT_TIMESTAMP`,
		job.SourceID, job.ComicID, job.ComicTitle, job.ComicCover, job.ChapterID, job.ChapterTitle, job.ChapterOrder)
	if err != nil {
		return model.DownloadJob{}, err
	}
	return s.DownloadJobByChapter(ctx, job.SourceID, job.ComicID, job.ChapterID)
}

func (s *Store) DownloadJobByChapter(ctx context.Context, sourceID, comicID, chapterID string) (model.DownloadJob, error) {
	return scanJob(s.db.QueryRowContext(ctx, jobSelect+` WHERE source_id = ? AND comic_id = ? AND chapter_id = ?`, sourceID, comicID, chapterID))
}

func (s *Store) DownloadJob(ctx context.Context, id int64) (model.DownloadJob, error) {
	return scanJob(s.db.QueryRowContext(ctx, jobSelect+` WHERE id = ?`, id))
}

func (s *Store) ListDownloadJobs(ctx context.Context, limit int) ([]model.DownloadJob, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, jobSelect+` ORDER BY CASE status WHEN 'running' THEN 0 WHEN 'queued' THEN 1 WHEN 'paused' THEN 2 WHEN 'failed' THEN 3 ELSE 4 END, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []model.DownloadJob
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// RequeueRunningJobs resets jobs that were interrupted by a restart back to
// the queue. It must only be called once at startup: the download engine owns
// the running state afterwards and requeueing mid-run would corrupt progress.
func (s *Store) RequeueRunningJobs(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE download_jobs SET status = 'queued', started_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE status = 'running'`)
	return err
}

func (s *Store) ListQueuedJobs(ctx context.Context) ([]model.DownloadJob, error) {
	rows, err := s.db.QueryContext(ctx, jobSelect+` WHERE status = 'queued' ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []model.DownloadJob
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

const jobSelect = `SELECT id, source_id, comic_id, comic_title, comic_cover, chapter_id, chapter_title, chapter_order, status, total_pages, completed_pages, file_path, error, retry_count, next_retry_at, created_at, updated_at, started_at, finished_at FROM download_jobs`

func scanJob(row rowScanner) (model.DownloadJob, error) {
	var job model.DownloadJob
	err := row.Scan(&job.ID, &job.SourceID, &job.ComicID, &job.ComicTitle, &job.ComicCover, &job.ChapterID, &job.ChapterTitle, &job.ChapterOrder, &job.Status, &job.TotalPages, &job.CompletedPages, &job.FilePath, &job.Error, &job.RetryCount, &job.NextRetryAt, &job.CreatedAt, &job.UpdatedAt, &job.StartedAt, &job.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DownloadJob{}, ErrNotFound
	}
	return job, err
}

func (s *Store) UpdateDownloadJob(ctx context.Context, id int64, status string, totalPages, completedPages int, filePath, jobError string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE download_jobs SET status = ?, total_pages = ?, completed_pages = ?, file_path = ?, error = ?, updated_at = CURRENT_TIMESTAMP,
			started_at = CASE WHEN ? = 'running' THEN COALESCE(started_at, CURRENT_TIMESTAMP) ELSE started_at END,
			finished_at = CASE WHEN ? IN ('completed', 'failed', 'canceled') THEN CURRENT_TIMESTAMP ELSE finished_at END
		WHERE id = ?`, status, totalPages, completedPages, filePath, jobError, status, status, id)
	return err
}

// FailDownloadJob records a failed attempt and its next automatic retry.
func (s *Store) FailDownloadJob(ctx context.Context, id int64, totalPages, completedPages int, jobError string, retryCount int, nextRetryAt *time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE download_jobs SET status = 'failed', total_pages = ?, completed_pages = ?, error = ?,
			retry_count = ?, next_retry_at = ?, updated_at = CURRENT_TIMESTAMP,
			finished_at = CURRENT_TIMESTAMP
		WHERE id = ?`, totalPages, completedPages, jobError, retryCount, nextRetryAt, id)
	return err
}

// ResetDownloadJob queues a task manually and restarts its retry budget.
func (s *Store) ResetDownloadJob(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE download_jobs SET status = 'queued', completed_pages = 0, error = '', retry_count = 0,
			next_retry_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

// ActivateDueRetries moves failed jobs whose retry delay has elapsed back to
// the queue. Automatic attempts are capped at two retries per manual run.
func (s *Store) ActivateDueRetries(ctx context.Context, now time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE download_jobs SET status = 'queued', next_retry_at = NULL, updated_at = CURRENT_TIMESTAMP
		WHERE status = 'failed' AND retry_count < 2 AND next_retry_at IS NOT NULL AND next_retry_at <= ?`, now.UTC())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
func (s *Store) UpdateDownloadProgress(ctx context.Context, id int64, completedPages int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE download_jobs SET completed_pages = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, completedPages, id)
	return err
}

func (s *Store) DeleteDownloadJob(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM download_jobs WHERE id = ?`, id)
	return err
}

func (s *Store) Stats(ctx context.Context) (model.Stats, error) {
	var stats model.Stats
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM subscriptions`).Scan(&stats.Subscriptions)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM download_jobs WHERE status IN ('queued','running','paused')`).Scan(&stats.ActiveJobs)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM download_jobs WHERE status = 'completed'`).Scan(&stats.CompletedJobs)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM download_jobs WHERE status = 'completed'`).Scan(&stats.LibraryItems)
	return stats, nil
}

func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`, key, value)
	return err
}

func (s *Store) UpdateUserPassword(ctx context.Context, id int64, passwordHash string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdateUsername(ctx context.Context, id int64, username string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE users SET username = ? WHERE id = ?`, username, id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return ErrUsernameTaken
		}
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}
