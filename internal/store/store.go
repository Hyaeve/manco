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
	"github.com/hyaeve/manco/internal/subscriptionstate"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")
var ErrUsernameTaken = errors.New("username already exists")

type Store struct {
	db   *sql.DB
	read *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-8192)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &Store{db: db}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	readDB, err := sql.Open("sqlite", path+"?mode=ro&_pragma=busy_timeout(10000)&_pragma=query_only(1)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-8192)")
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	readDB.SetMaxOpenConns(3)
	readDB.SetMaxIdleConns(3)
	store.read = readDB
	if _, err := db.ExecContext(context.Background(), `PRAGMA optimize`); err != nil {
		_ = readDB.Close()
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) reader() *sql.DB {
	if s.read != nil {
		return s.read
	}
	return s.db
}

func (s *Store) Close() error {
	_, _ = s.db.Exec(`PRAGMA optimize`)
	var readErr error
	if s.read != nil {
		readErr = s.read.Close()
	}
	writeErr := s.db.Close()
	if readErr != nil {
		return readErr
	}
	return writeErr
}

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
			user_agent_cipher TEXT NOT NULL DEFAULT '',
			home_url TEXT NOT NULL DEFAULT '',
			extra_json TEXT NOT NULL DEFAULT '{}',
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id TEXT NOT NULL,
			comic_id TEXT NOT NULL,
			origin_source_id TEXT NOT NULL DEFAULT '',
			origin_comic_id TEXT NOT NULL DEFAULT '',
			origin_source_url TEXT NOT NULL DEFAULT '',
			match_kind TEXT NOT NULL DEFAULT '',
			match_status TEXT NOT NULL DEFAULT '',
			match_error TEXT NOT NULL DEFAULT '',
			match_attempt_at DATETIME,
			title TEXT NOT NULL,
			cover TEXT NOT NULL DEFAULT '',
			series_dir TEXT NOT NULL DEFAULT '',
			author TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			auto_download INTEGER NOT NULL DEFAULT 1,
			cron_expr TEXT NOT NULL DEFAULT '',
			last_chapter_id TEXT NOT NULL DEFAULT '',
			last_chapter_title TEXT NOT NULL DEFAULT '',
			last_chapter_order REAL NOT NULL DEFAULT 0,
			chapter_count INTEGER NOT NULL DEFAULT 0,
			comic_status TEXT NOT NULL DEFAULT '',
			last_new_chapter_at DATETIME,
			last_checked_at DATETIME,
			disabled_at DATETIME,
			completed_at DATETIME,
			archived_at DATETIME,
			archive_reason TEXT NOT NULL DEFAULT '',
			archive_warning_at DATETIME,
			archive_warning_days INTEGER NOT NULL DEFAULT 0,
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
			comic_author TEXT NOT NULL DEFAULT '',
			comic_description TEXT NOT NULL DEFAULT '',
			comic_tags_json TEXT NOT NULL DEFAULT '[]',
			chapter_id TEXT NOT NULL,
			chapter_title TEXT NOT NULL,
			chapter_order REAL NOT NULL DEFAULT 0,
			merge_chapters_json TEXT NOT NULL DEFAULT '[]',
			queue_priority INTEGER NOT NULL DEFAULT 0,
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
		`CREATE INDEX IF NOT EXISTS idx_download_jobs_completed_finished ON download_jobs(finished_at DESC, id DESC) WHERE status = 'completed'`,
		`CREATE INDEX IF NOT EXISTS idx_download_jobs_retry_due ON download_jobs(next_retry_at ASC) WHERE status = 'failed' AND next_retry_at IS NOT NULL`,
		`CREATE TABLE IF NOT EXISTS custom_sources (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			kind TEXT NOT NULL DEFAULT 'comic',
			description TEXT NOT NULL DEFAULT '',
			homepage TEXT NOT NULL DEFAULT '',
			icon TEXT NOT NULL DEFAULT '',
			repo_url TEXT NOT NULL DEFAULT '',
			config_json TEXT NOT NULL DEFAULT '{}',
			enabled INTEGER NOT NULL DEFAULT 1,
			hidden INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS extension_repositories (
            id TEXT PRIMARY KEY,
            name TEXT NOT NULL,
            custom_name TEXT NOT NULL DEFAULT '',
            kind TEXT NOT NULL DEFAULT 'json',
            url TEXT NOT NULL,
            description TEXT NOT NULL DEFAULT '',
            icon TEXT NOT NULL DEFAULT '',
            enabled INTEGER NOT NULL DEFAULT 1,
            catalog_json TEXT NOT NULL DEFAULT '[]',
            last_sync_at DATETIME,
            last_error TEXT NOT NULL DEFAULT '',
            created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
            updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
        )`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		// 通知聚合按作品分组 + 时间范围过滤，这两个索引让查询不再整表扫描。
		`CREATE INDEX IF NOT EXISTS idx_download_jobs_activity ON download_jobs(source_id, comic_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_download_jobs_updated_at ON download_jobs(updated_at)`,
		`CREATE TABLE IF NOT EXISTS extension_source_cache (
			repository_id TEXT NOT NULL,
			extension_id TEXT NOT NULL,
			version TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL DEFAULT '[]',
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (repository_id, extension_id)
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
	if err := s.ensureColumn(ctx, "subscriptions", "temporary", `INTEGER NOT NULL DEFAULT 0`); err != nil {
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
		{"archive_warning_at", `DATETIME`},
		{"archive_warning_days", `INTEGER NOT NULL DEFAULT 0`},
	} {
		if err := s.ensureColumn(ctx, "subscriptions", column.name, column.declaration); err != nil {
			return err
		}
	}
	if err := s.ensureColumn(ctx, "subscriptions", "download_dir", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "subscriptions", "series_dir", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "subscriptions", "convert_to_simplified", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "subscriptions", "chapter_marker", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "subscriptions", "cover_blur", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "subscriptions", "chapter_count", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "subscriptions", "origin_source_id", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "subscriptions", "origin_comic_id", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "subscriptions", "origin_source_url", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "subscriptions", "match_kind", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "subscriptions", "match_status", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "subscriptions", "match_error", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "subscriptions", "match_attempt_at", `DATETIME`); err != nil {
		return err
	}
	for _, statement := range []string{
		`CREATE INDEX IF NOT EXISTS idx_subscriptions_active_created ON subscriptions(created_at ASC, id ASC) WHERE archived_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_subscriptions_active_updated ON subscriptions(updated_at DESC, id DESC) WHERE archived_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_subscriptions_active_title ON subscriptions(title COLLATE NOCASE ASC, id ASC) WHERE archived_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_subscriptions_active_dir ON subscriptions(download_dir ASC, created_at ASC, id ASC) WHERE archived_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_subscriptions_archived ON subscriptions(archived_at DESC, id DESC) WHERE archived_at IS NOT NULL`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if err := s.ensureColumn(ctx, "download_jobs", "retry_count", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "download_jobs", "comic_author", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "download_jobs", "comic_description", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "download_jobs", "comic_tags_json", `TEXT NOT NULL DEFAULT '[]'`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "download_jobs", "merge_chapters_json", `TEXT NOT NULL DEFAULT '[]'`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "download_jobs", "queue_priority", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE download_jobs
		SET comic_author = COALESCE((
			SELECT subscriptions.author
			FROM subscriptions
			WHERE subscriptions.source_id = download_jobs.source_id
				AND subscriptions.comic_id = download_jobs.comic_id
		), comic_author)
		WHERE comic_author = ''
			AND EXISTS (
				SELECT 1 FROM subscriptions
				WHERE subscriptions.source_id = download_jobs.source_id
					AND subscriptions.comic_id = download_jobs.comic_id
			)`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "download_jobs", "next_retry_at", `DATETIME`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "download_jobs", "download_dir", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "download_jobs", "convert_to_simplified", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "download_jobs", "chapter_marker", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_download_jobs_queue_priority
		ON download_jobs(queue_priority DESC, id ASC)
		WHERE status = 'queued'`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_download_jobs_directory
		ON download_jobs(download_dir)`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_download_jobs_active_order
		ON download_jobs(
			CASE status WHEN 'running' THEN 0 WHEN 'queued' THEN 1 WHEN 'waiting' THEN 2 WHEN 'paused' THEN 3 ELSE 4 END,
			CASE WHEN status = 'queued' THEN queue_priority ELSE 0 END DESC,
			CASE WHEN status IN ('failed', 'canceled') THEN -id ELSE id END ASC
		) WHERE status <> 'completed'`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "extension_repositories", "custom_name", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "source_accounts", "user_agent_cipher", `TEXT NOT NULL DEFAULT ''`); err != nil {
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

// DownloadJobStats aggregates per-work completed and failed chapter counts so
// subscription cards stay accurate even when the task list itself is paged.
func (s *Store) DownloadJobStats(ctx context.Context) (map[string]model.DownloadJobStats, error) {
	rows, err := s.reader().QueryContext(ctx, `
		SELECT source_id, comic_id,
			SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END) AS succeeded,
			SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) AS failed,
			SUM(CASE WHEN status IN ('queued', 'running', 'waiting', 'paused') THEN 1 ELSE 0 END) AS pending
		FROM download_jobs
		GROUP BY source_id, comic_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stats := map[string]model.DownloadJobStats{}
	for rows.Next() {
		var sourceID, comicID string
		var succeeded, failed, pending int
		if err := rows.Scan(&sourceID, &comicID, &succeeded, &failed, &pending); err != nil {
			return nil, err
		}
		stats[sourceID+"\x00"+comicID] = model.DownloadJobStats{Succeeded: succeeded, Failed: failed, Pending: pending}
	}
	return stats, rows.Err()
}

// DownloadJobStatsForSubscriptions aggregates only the works visible on the
// current page. The subscription page used to scan every historical job on
// each poll just to render at most a few dozen cards.
func (s *Store) DownloadJobStatsForSubscriptions(ctx context.Context, subscriptions []model.Subscription) (map[string]model.DownloadJobStats, error) {
	stats := map[string]model.DownloadJobStats{}
	if len(subscriptions) == 0 {
		return stats, nil
	}
	seen := make(map[string]bool, len(subscriptions))
	values := make([]string, 0, len(subscriptions))
	args := make([]any, 0, len(subscriptions)*2)
	for _, subscription := range subscriptions {
		key := subscription.SourceID + "\x00" + subscription.ComicID
		if subscription.SourceID == "" || subscription.ComicID == "" || seen[key] {
			continue
		}
		seen[key] = true
		values = append(values, "(?, ?)")
		args = append(args, subscription.SourceID, subscription.ComicID)
	}
	if len(values) == 0 {
		return stats, nil
	}
	query := `
		WITH wanted(source_id, comic_id) AS (VALUES ` + strings.Join(values, ",") + `)
		SELECT jobs.source_id, jobs.comic_id,
			SUM(CASE WHEN jobs.status = 'completed' THEN 1 ELSE 0 END) AS succeeded,
			SUM(CASE WHEN jobs.status = 'failed' THEN 1 ELSE 0 END) AS failed,
			SUM(CASE WHEN jobs.status IN ('queued', 'running', 'waiting', 'paused') THEN 1 ELSE 0 END) AS pending
		FROM download_jobs AS jobs
		INNER JOIN wanted ON wanted.source_id = jobs.source_id AND wanted.comic_id = jobs.comic_id
		GROUP BY jobs.source_id, jobs.comic_id`
	rows, err := s.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sourceID, comicID string
		var succeeded, failed, pending int
		if err := rows.Scan(&sourceID, &comicID, &succeeded, &failed, &pending); err != nil {
			return nil, err
		}
		stats[sourceID+"\x00"+comicID] = model.DownloadJobStats{Succeeded: succeeded, Failed: failed, Pending: pending}
	}
	return stats, rows.Err()
}

func (s *Store) ActiveDownloadJobCount(ctx context.Context) (int, error) {
	var count int
	err := s.reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM download_jobs WHERE status IN ('queued', 'running', 'waiting', 'paused')`).Scan(&count)
	return count, err
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
	err := s.reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	return count, err
}

func (s *Store) CreateUser(ctx context.Context, username, passwordHash string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO users(username, password_hash) VALUES(?, ?)`, username, passwordHash)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return ErrUsernameTaken
	}
	return err
}

func (s *Store) ListUsers(ctx context.Context) ([]model.User, error) {
	rows, err := s.reader().QueryContext(ctx, `SELECT id, username, password_hash, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.User, 0, 1)
	for rows.Next() {
		var item model.User
		if err := rows.Scan(&item.ID, &item.Username, &item.PasswordHash, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpsertUser(ctx context.Context, user model.User) error {
	if strings.TrimSpace(user.Username) == "" || strings.TrimSpace(user.PasswordHash) == "" {
		return errors.New("username and password hash are required")
	}
	createdAt := user.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users(username, password_hash, created_at) VALUES(?, ?, ?)
		ON CONFLICT(username) DO UPDATE SET password_hash = excluded.password_hash`, user.Username, user.PasswordHash, createdAt)
	return err
}

func (s *Store) UserByUsername(ctx context.Context, username string) (model.User, error) {
	var user model.User
	err := s.reader().QueryRowContext(ctx, `SELECT id, username, password_hash, created_at FROM users WHERE username = ?`, username).
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
	err := s.reader().QueryRowContext(ctx, `
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
		INSERT INTO source_accounts(source_id, username, secret_cipher, token_cipher, cookie_cipher, user_agent_cipher, home_url, extra_json, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(source_id) DO UPDATE SET
			username = excluded.username,
			secret_cipher = excluded.secret_cipher,
			token_cipher = excluded.token_cipher,
			cookie_cipher = excluded.cookie_cipher,
			user_agent_cipher = excluded.user_agent_cipher,
			home_url = excluded.home_url,
			extra_json = excluded.extra_json,
			updated_at = CURRENT_TIMESTAMP`,
		account.SourceID, account.Username, account.SecretCipher, account.TokenCipher, account.CookieCipher, account.UserAgentCipher, account.HomeURL, string(extra))
	return err
}

func (s *Store) SourceAccount(ctx context.Context, sourceID string) (model.SourceAccount, error) {
	return s.scanAccount(s.reader().QueryRowContext(ctx, `SELECT id, source_id, username, secret_cipher, token_cipher, cookie_cipher, user_agent_cipher, home_url, extra_json, updated_at FROM source_accounts WHERE source_id = ?`, sourceID))
}

func (s *Store) ListSourceAccounts(ctx context.Context) ([]model.SourceAccount, error) {
	rows, err := s.reader().QueryContext(ctx, `SELECT id, source_id, username, secret_cipher, token_cipher, cookie_cipher, user_agent_cipher, home_url, extra_json, updated_at FROM source_accounts ORDER BY source_id`)
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
	err := row.Scan(&account.ID, &account.SourceID, &account.Username, &account.SecretCipher, &account.TokenCipher, &account.CookieCipher, &account.UserAgentCipher, &account.HomeURL, &extra, &account.UpdatedAt)
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

func (s *Store) UpsertCustomSource(ctx context.Context, item model.CustomSource) (model.CustomSource, error) {
	if len(item.Config) == 0 {
		item.Config = json.RawMessage(`{}`)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO custom_sources(id, name, kind, description, homepage, icon, repo_url, config_json, enabled, hidden)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			kind = excluded.kind,
			description = excluded.description,
			homepage = excluded.homepage,
			icon = excluded.icon,
			repo_url = excluded.repo_url,
			config_json = excluded.config_json,
			enabled = excluded.enabled,
			hidden = excluded.hidden,
			updated_at = CURRENT_TIMESTAMP`,
		item.ID, item.Name, item.Kind, item.Description, item.Homepage, item.Icon, item.RepoURL, string(item.Config), item.Enabled, item.Hidden)
	if err != nil {
		return model.CustomSource{}, err
	}
	return s.CustomSource(ctx, item.ID)
}

func (s *Store) CustomSource(ctx context.Context, id string) (model.CustomSource, error) {
	return scanCustomSource(s.reader().QueryRowContext(ctx, `SELECT id, name, kind, description, homepage, icon, repo_url, config_json, enabled, hidden, created_at, updated_at FROM custom_sources WHERE id = ?`, id))
}

func (s *Store) ListCustomSources(ctx context.Context) ([]model.CustomSource, error) {
	rows, err := s.reader().QueryContext(ctx, `SELECT id, name, kind, description, homepage, icon, repo_url, config_json, enabled, hidden, created_at, updated_at FROM custom_sources ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []model.CustomSource
	for rows.Next() {
		item, err := scanCustomSource(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanCustomSource(row rowScanner) (model.CustomSource, error) {
	var item model.CustomSource
	var config string
	err := row.Scan(&item.ID, &item.Name, &item.Kind, &item.Description, &item.Homepage, &item.Icon, &item.RepoURL, &config, &item.Enabled, &item.Hidden, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.CustomSource{}, ErrNotFound
	}
	if err == nil {
		if strings.TrimSpace(config) == "" {
			config = "{}"
		}
		item.Config = json.RawMessage(config)
	}
	return item, err
}

func (s *Store) DeleteCustomSource(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM custom_sources WHERE id = ?`, id)
	return err
}

func (s *Store) UpsertExtensionRepository(ctx context.Context, item model.ExtensionRepository) (model.ExtensionRepository, error) {
	if len(item.Catalog) == 0 {
		item.Catalog = json.RawMessage(`[]`)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO extension_repositories(id, name, custom_name, kind, url, description, icon, enabled, catalog_json, last_sync_at, last_error)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			custom_name = excluded.custom_name,
			kind = excluded.kind,
			url = excluded.url,
			description = excluded.description,
			icon = excluded.icon,
			enabled = excluded.enabled,
			catalog_json = excluded.catalog_json,
			last_sync_at = excluded.last_sync_at,
			last_error = excluded.last_error,
			updated_at = CURRENT_TIMESTAMP`,
		item.ID, item.Name, item.CustomName, item.Kind, item.URL, item.Description, item.Icon, item.Enabled, string(item.Catalog), item.LastSyncAt, item.LastError)
	if err != nil {
		return model.ExtensionRepository{}, err
	}
	return s.ExtensionRepository(ctx, item.ID)
}

func (s *Store) ExtensionRepository(ctx context.Context, id string) (model.ExtensionRepository, error) {
	return scanExtensionRepository(s.reader().QueryRowContext(ctx, `SELECT id, name, custom_name, kind, url, description, icon, enabled, catalog_json, last_sync_at, last_error, created_at, updated_at FROM extension_repositories WHERE id = ?`, id))
}

func (s *Store) ListExtensionRepositories(ctx context.Context) ([]model.ExtensionRepository, error) {
	rows, err := s.reader().QueryContext(ctx, `SELECT id, name, custom_name, kind, url, description, icon, enabled, catalog_json, last_sync_at, last_error, created_at, updated_at FROM extension_repositories ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []model.ExtensionRepository
	for rows.Next() {
		item, err := scanExtensionRepository(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanExtensionRepository(row rowScanner) (model.ExtensionRepository, error) {
	var item model.ExtensionRepository
	var catalog string
	err := row.Scan(&item.ID, &item.Name, &item.CustomName, &item.Kind, &item.URL, &item.Description, &item.Icon, &item.Enabled, &catalog, &item.LastSyncAt, &item.LastError, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ExtensionRepository{}, ErrNotFound
	}
	if err == nil {
		if strings.TrimSpace(catalog) == "" {
			catalog = "[]"
		}
		item.Catalog = json.RawMessage(catalog)
	}
	return item, err
}

func (s *Store) DeleteExtensionRepository(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM extension_repositories WHERE id = ?`, id)
	return err
}

// ExtensionCandidates returns the cached candidate payload for one repository
// extension plus the extension version it was enumerated from. Enumerating an
// extension means installing the plugin and restarting the JVM sidecar, so the
// result is reused until the repository re-syncs a new version.
func (s *Store) ExtensionCandidates(ctx context.Context, repositoryID, extensionID string) (string, string, error) {
	var payload, version string
	err := s.reader().QueryRowContext(ctx,
		`SELECT payload_json, version FROM extension_source_cache WHERE repository_id = ? AND extension_id = ?`,
		repositoryID, extensionID).Scan(&payload, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	return payload, version, nil
}

func (s *Store) SaveExtensionCandidates(ctx context.Context, repositoryID, extensionID, version, payload string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO extension_source_cache(repository_id, extension_id, version, payload_json, updated_at)
		VALUES(?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(repository_id, extension_id) DO UPDATE SET
			version = excluded.version,
			payload_json = excluded.payload_json,
			updated_at = CURRENT_TIMESTAMP`,
		repositoryID, extensionID, version, payload)
	return err
}

func (s *Store) DeleteExtensionCandidates(ctx context.Context, repositoryID, extensionID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM extension_source_cache WHERE repository_id = ? AND extension_id = ?`,
		repositoryID, extensionID)
	return err
}

func (s *Store) DeleteExtensionCandidatesForRepository(ctx context.Context, repositoryID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM extension_source_cache WHERE repository_id = ?`, repositoryID)
	return err
}

func (s *Store) UpsertSubscription(ctx context.Context, sub model.Subscription) (model.Subscription, error) {

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO subscriptions(source_id, comic_id, origin_source_id, origin_comic_id, origin_source_url, match_kind, match_status, match_error, match_attempt_at, title, cover, series_dir, author, enabled, auto_download, cron_expr, download_dir, convert_to_simplified, chapter_marker, cover_blur, last_chapter_id, last_chapter_title, last_chapter_order, comic_status, last_new_chapter_at, temporary)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, ?)
		ON CONFLICT(source_id, comic_id) DO UPDATE SET
			origin_source_id = excluded.origin_source_id,
			origin_comic_id = excluded.origin_comic_id,
			origin_source_url = excluded.origin_source_url,
			match_kind = excluded.match_kind,
			match_status = excluded.match_status,
			match_error = excluded.match_error,
			match_attempt_at = excluded.match_attempt_at,
			chapter_marker = excluded.chapter_marker,
			cover_blur = excluded.cover_blur,
			title = excluded.title,
			cover = excluded.cover,
			series_dir = CASE WHEN excluded.series_dir <> '' THEN excluded.series_dir ELSE subscriptions.series_dir END,
			author = excluded.author,
			enabled = excluded.enabled,
			auto_download = excluded.auto_download,
			cron_expr = CASE WHEN excluded.cron_expr <> '' THEN excluded.cron_expr ELSE subscriptions.cron_expr END,
			download_dir = excluded.download_dir,
			convert_to_simplified = excluded.convert_to_simplified,
			chapter_marker = excluded.chapter_marker,
			comic_status = CASE WHEN excluded.comic_status <> '' THEN excluded.comic_status ELSE subscriptions.comic_status END,
			last_chapter_id = CASE WHEN subscriptions.last_chapter_id = '' THEN excluded.last_chapter_id ELSE subscriptions.last_chapter_id END,
			last_chapter_title = CASE WHEN subscriptions.last_chapter_id = '' THEN excluded.last_chapter_title ELSE subscriptions.last_chapter_title END,
			last_chapter_order = CASE WHEN subscriptions.last_chapter_id = '' THEN excluded.last_chapter_order ELSE subscriptions.last_chapter_order END,
			disabled_at = NULL,
			archived_at = NULL,
			archive_reason = '',
			temporary = excluded.temporary,
			updated_at = CURRENT_TIMESTAMP`,
		sub.SourceID, sub.ComicID, sub.OriginSourceID, sub.OriginComicID, sub.OriginSourceURL, sub.MatchKind, sub.MatchStatus, sub.MatchError, sub.MatchAttemptAt, sub.Title, sub.Cover, sub.SeriesDir, sub.Author, sub.Enabled, sub.AutoDownload, sub.CronExpr, sub.DownloadDir, sub.ConvertToSimplified, sub.ChapterMarker, sub.CoverBlur, sub.LastChapterID, sub.LastChapterTitle, sub.LastChapterOrder, sub.ComicStatus, sub.Temporary)
	if err != nil {
		return model.Subscription{}, err
	}
	return s.SubscriptionByComic(ctx, sub.SourceID, sub.ComicID)
}

func (s *Store) SubscriptionByComic(ctx context.Context, sourceID, comicID string) (model.Subscription, error) {
	return scanSubscription(s.reader().QueryRowContext(ctx, subscriptionSelect+` WHERE source_id = ? AND comic_id = ?`, sourceID, comicID))
}

func (s *Store) Subscription(ctx context.Context, id int64) (model.Subscription, error) {
	return scanSubscription(s.reader().QueryRowContext(ctx, subscriptionSelect+` WHERE id = ?`, id))
}

func (s *Store) LatestSubscriptionDownloadDir(ctx context.Context) (string, error) {
	var dir string
	err := s.reader().QueryRowContext(ctx, `
		SELECT download_dir
		FROM subscriptions
		WHERE archived_at IS NULL AND TRIM(download_dir) <> ''
		ORDER BY created_at DESC, id DESC
		LIMIT 1`).Scan(&dir)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return dir, err
}

func (s *Store) ListSubscriptions(ctx context.Context) ([]model.Subscription, error) {
	// 订阅清单按订阅先后排序（最早 → 最新），和本地仓库的目录胶囊、TG 列表一致。
	rows, err := s.reader().QueryContext(ctx, subscriptionSelect+` WHERE archived_at IS NULL ORDER BY created_at ASC, id ASC`)
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

const subscriptionSelect = `SELECT id, source_id, comic_id, origin_source_id, origin_comic_id, origin_source_url, match_kind, match_status, match_error, match_attempt_at, title, cover, series_dir, author, enabled, auto_download, cron_expr, download_dir, convert_to_simplified, chapter_marker, cover_blur, last_chapter_id, last_chapter_title, last_chapter_order, chapter_count, comic_status, last_new_chapter_at, last_checked_at, disabled_at, completed_at, archived_at, archive_reason, created_at, updated_at, temporary, archive_warning_at, archive_warning_days FROM subscriptions`

type SubscriptionFilter struct {
	SourceIDs        []string
	ExcludeSourceIDs []string
	MatchKind        string
	DownloadDir      string
	Query            string
	Sort             string
	Descending       bool
	MatchNone        bool
}

func subscriptionFilterClause(filter SubscriptionFilter) (string, []any) {
	clauses := []string{"archived_at IS NULL"}
	args := make([]any, 0, len(filter.SourceIDs)+len(filter.ExcludeSourceIDs)+1)
	if filter.MatchNone {
		clauses = append(clauses, "0 = 1")
	}
	sourceIDs := filter.SourceIDs
	if len(sourceIDs) > 0 || strings.EqualFold(filter.MatchKind, "book") {
		placeholders := make([]string, len(sourceIDs))
		for index, sourceID := range sourceIDs {
			placeholders[index] = "?"
			args = append(args, sourceID)
		}
		clause := "0 = 1"
		if len(placeholders) > 0 {
			clause = "source_id IN (" + strings.Join(placeholders, ",") + ")"
		}
		if strings.EqualFold(filter.MatchKind, "book") {
			clause = "(" + clause + " OR (source_id = 'bangumi' AND match_kind = 'book'))"
		}
		clauses = append(clauses, clause)
	}
	if excluded := filter.ExcludeSourceIDs; len(excluded) > 0 {
		placeholders := make([]string, len(excluded))
		for index, sourceID := range excluded {
			placeholders[index] = "?"
			args = append(args, sourceID)
		}
		clauses = append(clauses, "source_id NOT IN ("+strings.Join(placeholders, ",")+")")
	}
	if dir := strings.TrimSpace(filter.DownloadDir); dir != "" {
		clauses = append(clauses, "download_dir = ?")
		args = append(args, dir)
	}
	if query := strings.TrimSpace(filter.Query); query != "" {
		clauses = append(clauses, "(instr(lower(title), lower(?)) > 0 OR instr(lower(series_dir), lower(?)) > 0 OR instr(lower(author), lower(?)) > 0)")
		args = append(args, query, query, query)
	}
	if kind := strings.ToLower(strings.TrimSpace(filter.MatchKind)); kind == "comic" || kind == "other" {
		clauses = append(clauses, "(source_id <> 'bangumi' OR match_kind = ?)")
		args = append(args, kind)
	}
	return strings.Join(clauses, " AND "), args
}

func subscriptionOrder(filter SubscriptionFilter) string {
	column := "updated_at"
	switch strings.ToLower(strings.TrimSpace(filter.Sort)) {
	case "created":
		column = "created_at"
	case "title":
		column = "title COLLATE NOCASE"
	}
	direction := "ASC"
	if filter.Descending {
		direction = "DESC"
	}
	return column + " " + direction + ", id " + direction
}

// ListSubscriptionsPage applies the subscription page's filters, sort, limit
// and offset inside SQLite. The previous handler loaded and sorted all
// subscriptions in Go, so the browser's virtual list did not reduce server work.
func (s *Store) ListSubscriptionsPage(ctx context.Context, filter SubscriptionFilter, limit, offset int) ([]model.Subscription, int, error) {
	if limit <= 0 {
		limit = 60
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	where, args := subscriptionFilterClause(filter)
	var total int
	if err := s.reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM subscriptions WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	queryArgs := append(append([]any(nil), args...), limit, offset)
	rows, err := s.reader().QueryContext(ctx, subscriptionSelect+` WHERE `+where+` ORDER BY `+subscriptionOrder(filter)+` LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]model.Subscription, 0, limit)
	for rows.Next() {
		item, err := scanSubscription(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (s *Store) SubscriptionSourceCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.reader().QueryContext(ctx, `
		SELECT source_id, COUNT(*)
		FROM subscriptions
		WHERE archived_at IS NULL
		GROUP BY source_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var sourceID string
		var count int
		if err := rows.Scan(&sourceID, &count); err != nil {
			return nil, err
		}
		counts[sourceID] = count
	}
	return counts, rows.Err()
}

func (s *Store) SubscriptionBangumiKindCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.reader().QueryContext(ctx, `
		SELECT CASE WHEN match_kind = 'book' THEN 'book' ELSE 'comic' END, COUNT(*)
		FROM subscriptions
		WHERE archived_at IS NULL AND source_id = 'bangumi'
		GROUP BY CASE WHEN match_kind = 'book' THEN 'book' ELSE 'comic' END`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var kind string
		var count int
		if err := rows.Scan(&kind, &count); err != nil {
			return nil, err
		}
		counts[kind] = count
	}
	return counts, rows.Err()
}

func (s *Store) SubscriptionDownloadDirs(ctx context.Context) ([]string, error) {
	rows, err := s.reader().QueryContext(ctx, `
		SELECT download_dir
		FROM subscriptions
		WHERE archived_at IS NULL AND TRIM(download_dir) <> ''
		GROUP BY download_dir
		ORDER BY MIN(created_at) ASC, download_dir ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	dirs := make([]string, 0, 8)
	for rows.Next() {
		var dir string
		if err := rows.Scan(&dir); err != nil {
			return nil, err
		}
		dirs = append(dirs, dir)
	}
	return dirs, rows.Err()
}

// LocalDownloadDirectories never loads serialized chapter or comic payloads.
func (s *Store) LocalDownloadDirectories(ctx context.Context) ([]string, error) {
	rows, err := s.reader().QueryContext(ctx, `
		SELECT download_dir FROM (
			SELECT download_dir, 0 AS origin, MIN(id) AS first_id
			FROM subscriptions WHERE TRIM(download_dir) <> '' GROUP BY download_dir
			UNION ALL
			SELECT download_dir, 1 AS origin, MIN(id) AS first_id
			FROM download_jobs WHERE TRIM(download_dir) <> '' GROUP BY download_dir
		) GROUP BY download_dir ORDER BY MIN(origin), MIN(first_id), download_dir`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var dirs []string
	for rows.Next() {
		var dir string
		if err := rows.Scan(&dir); err != nil {
			return nil, err
		}
		dirs = append(dirs, dir)
	}
	return dirs, rows.Err()
}

func (s *Store) DownloadCoverFallbacks(ctx context.Context) (map[string]string, error) {
	rows, err := s.reader().QueryContext(ctx, `
		SELECT source_id, comic_id, MAX(comic_cover) FROM download_jobs
		WHERE TRIM(comic_cover) <> '' GROUP BY source_id, comic_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	covers := map[string]string{}
	for rows.Next() {
		var sourceID, comicID, cover string
		if err := rows.Scan(&sourceID, &comicID, &cover); err != nil {
			return nil, err
		}
		covers[sourceID+"\x00"+comicID] = cover
	}
	return covers, rows.Err()
}

func (s *Store) ListTemporarySubscriptions(ctx context.Context) ([]model.Subscription, error) {
	rows, err := s.reader().QueryContext(ctx, subscriptionSelect+` WHERE archived_at IS NULL AND temporary = 1 ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var subscriptions []model.Subscription
	for rows.Next() {
		item, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		subscriptions = append(subscriptions, item)
	}
	return subscriptions, rows.Err()
}

// ListArchivedSubscriptions 返回归档集里的订阅：按归档时间倒序，最近归档的排在最前。
func (s *Store) ListArchivedSubscriptions(ctx context.Context) ([]model.Subscription, error) {
	rows, err := s.reader().QueryContext(ctx, subscriptionSelect+` WHERE archived_at IS NOT NULL ORDER BY archived_at DESC, id DESC`)
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

func (s *Store) ListAllSubscriptions(ctx context.Context) ([]model.Subscription, error) {
	rows, err := s.reader().QueryContext(ctx, subscriptionSelect+` ORDER BY created_at ASC, id ASC`)
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

// ImportSubscription restores a complete subscription snapshot from the
// mounted /app/config directory, including polling baseline and archive state.
func (s *Store) ImportSubscription(ctx context.Context, sub model.Subscription) (model.Subscription, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO subscriptions(source_id, comic_id, title, cover, series_dir, author, enabled, auto_download, cron_expr, download_dir, convert_to_simplified, chapter_marker, cover_blur, last_chapter_id, last_chapter_title, last_chapter_order, chapter_count, comic_status, last_new_chapter_at, last_checked_at, disabled_at, completed_at, archived_at, archive_reason, archive_warning_at, archive_warning_days)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_id, comic_id) DO UPDATE SET
			title = excluded.title,
			cover = excluded.cover,
			author = excluded.author,
			enabled = excluded.enabled,
			auto_download = excluded.auto_download,
			cron_expr = excluded.cron_expr,
			series_dir = excluded.series_dir,
			download_dir = excluded.download_dir,
			convert_to_simplified = excluded.convert_to_simplified,
			chapter_marker = excluded.chapter_marker,
			cover_blur = excluded.cover_blur,
			last_chapter_id = excluded.last_chapter_id,
			last_chapter_title = excluded.last_chapter_title,
			last_chapter_order = excluded.last_chapter_order,
			chapter_count = excluded.chapter_count,
			comic_status = excluded.comic_status,
			last_new_chapter_at = excluded.last_new_chapter_at,
			last_checked_at = excluded.last_checked_at,
			disabled_at = excluded.disabled_at,
			completed_at = excluded.completed_at,
			archived_at = excluded.archived_at,
			archive_reason = excluded.archive_reason,
			archive_warning_at = excluded.archive_warning_at,
			archive_warning_days = excluded.archive_warning_days,
			updated_at = CURRENT_TIMESTAMP`,
		sub.SourceID, sub.ComicID, sub.Title, sub.Cover, sub.SeriesDir, sub.Author, sub.Enabled, sub.AutoDownload, sub.CronExpr, sub.DownloadDir, sub.ConvertToSimplified, sub.ChapterMarker, sub.CoverBlur, sub.LastChapterID, sub.LastChapterTitle, sub.LastChapterOrder, sub.ChapterCount, sub.ComicStatus, sub.LastNewChapterAt, sub.LastCheckedAt, sub.DisabledAt, sub.CompletedAt, sub.ArchivedAt, sub.ArchiveReason, sub.ArchiveWarningAt, sub.ArchiveWarningDays)
	if err != nil {
		return model.Subscription{}, err
	}
	return s.SubscriptionByComic(ctx, sub.SourceID, sub.ComicID)
}

func scanSubscription(row rowScanner) (model.Subscription, error) {
	var sub model.Subscription
	err := row.Scan(&sub.ID, &sub.SourceID, &sub.ComicID, &sub.OriginSourceID, &sub.OriginComicID, &sub.OriginSourceURL, &sub.MatchKind, &sub.MatchStatus, &sub.MatchError, &sub.MatchAttemptAt, &sub.Title, &sub.Cover, &sub.SeriesDir, &sub.Author, &sub.Enabled, &sub.AutoDownload, &sub.CronExpr, &sub.DownloadDir, &sub.ConvertToSimplified, &sub.ChapterMarker, &sub.CoverBlur, &sub.LastChapterID, &sub.LastChapterTitle, &sub.LastChapterOrder, &sub.ChapterCount, &sub.ComicStatus, &sub.LastNewChapterAt, &sub.LastCheckedAt, &sub.DisabledAt, &sub.CompletedAt, &sub.ArchivedAt, &sub.ArchiveReason, &sub.CreatedAt, &sub.UpdatedAt, &sub.Temporary, &sub.ArchiveWarningAt, &sub.ArchiveWarningDays)
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
			archive_warning_at = CASE WHEN ? = 1 AND enabled = 0 THEN NULL ELSE archive_warning_at END,
			archive_warning_days = CASE WHEN ? = 1 AND enabled = 0 THEN 0 ELSE archive_warning_days END,
			updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		enabled, autoDownload, cronExpr, enabled, cronExpr, enabled, enabled, enabled, enabled, enabled, enabled, id)
	return err
}

func (s *Store) UpdateSubscriptionConfig(ctx context.Context, id int64, enabled, autoDownload bool, cronExpr, downloadDir, seriesDir string, convertToSimplified, chapterMarker, coverBlur bool) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE subscriptions SET enabled = ?, auto_download = ?, cron_expr = ?, download_dir = ?, series_dir = ?, convert_to_simplified = ?, chapter_marker = ?, cover_blur = ?,
			last_checked_at = CASE WHEN ? = 1 THEN NULL WHEN cron_expr = ? THEN last_checked_at ELSE NULL END,
			last_new_chapter_at = CASE WHEN ? = 1 AND enabled = 0 THEN CURRENT_TIMESTAMP ELSE last_new_chapter_at END,
			disabled_at = CASE WHEN ? = 1 THEN NULL ELSE disabled_at END,
			archived_at = CASE WHEN ? = 1 THEN NULL ELSE archived_at END,
			archive_reason = CASE WHEN ? = 1 THEN '' ELSE archive_reason END,
			archive_warning_at = CASE WHEN ? = 1 AND enabled = 0 THEN NULL ELSE archive_warning_at END,
			archive_warning_days = CASE WHEN ? = 1 AND enabled = 0 THEN 0 ELSE archive_warning_days END,
			updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		enabled, autoDownload, cronExpr, downloadDir, seriesDir, convertToSimplified, chapterMarker, coverBlur, enabled, cronExpr, enabled, enabled, enabled, enabled, enabled, enabled, id)
	return err
}

func (s *Store) ArchiveSubscription(ctx context.Context, id int64, reason string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE subscriptions SET enabled = 0, archived_at = CURRENT_TIMESTAMP, archive_reason = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, reason, id)
	return err
}

// RestoreSubscription 把归档订阅移回订阅清单：清除归档与停用标记，并重新启用，
// 让调度器把它当作正常订阅继续检查更新。
func (s *Store) RestoreSubscription(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE subscriptions SET archived_at = NULL, archive_reason = '', enabled = 1, disabled_at = NULL,
			last_checked_at = NULL, last_new_chapter_at = CURRENT_TIMESTAMP,
			archive_warning_at = NULL, archive_warning_days = 0, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, id)
	return err
}

// UpdateSubscriptionChapterCount 记录该作品在内容源里的总章节数（每次成功
// 拉到章节列表都刷新），订阅卡片用它显示「共 N 话」。
func (s *Store) UpdateSubscriptionChapterCount(ctx context.Context, id int64, count int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE subscriptions SET chapter_count = ? WHERE id = ?`, count, id)
	return err
}

func (s *Store) MatchSubscription(ctx context.Context, id int64, sourceID, comicID, author, comicStatus string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE subscriptions SET
			source_id = ?, comic_id = ?,
			author = CASE WHEN ? <> '' THEN ? ELSE author END,
			comic_status = CASE WHEN ? <> '' THEN ? ELSE comic_status END,
			match_status = 'matched', match_error = '', match_attempt_at = CURRENT_TIMESTAMP,
			last_checked_at = NULL, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		strings.ToLower(strings.TrimSpace(sourceID)), strings.TrimSpace(comicID),
		strings.TrimSpace(author), strings.TrimSpace(author),
		strings.TrimSpace(comicStatus), strings.TrimSpace(comicStatus), id)
	return err
}

func (s *Store) MarkSubscriptionMatchAttempt(ctx context.Context, id int64, status, message string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE subscriptions SET
			match_status = ?, match_error = ?, match_attempt_at = CURRENT_TIMESTAMP,
			last_checked_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		strings.TrimSpace(status), strings.TrimSpace(message), id)
	return err
}

func (s *Store) UpdateSubscriptionCheck(ctx context.Context, id int64, chapterID, chapterTitle string, chapterOrder float64, comicStatus string, completed, newChapter bool) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE subscriptions SET last_chapter_id = ?, last_chapter_title = ?, last_chapter_order = ?,
			comic_status = ?,
			completed_at = CASE WHEN ? = 1 THEN COALESCE(completed_at, CURRENT_TIMESTAMP) ELSE NULL END,
			last_new_chapter_at = CASE WHEN ? = 1 THEN CURRENT_TIMESTAMP ELSE last_new_chapter_at END,
			archive_warning_at = CASE WHEN ? = 1 THEN NULL ELSE archive_warning_at END,
			archive_warning_days = CASE WHEN ? = 1 THEN 0 ELSE archive_warning_days END,
			last_checked_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, chapterID, chapterTitle, chapterOrder, comicStatus, completed, newChapter, newChapter, newChapter, id)
	return err
}

type SubscriptionMaintenanceEvent struct {
	Subscription   model.Subscription
	Completed      bool
	ThresholdDays  int
	DaysWithoutNew int
	DaysRemaining  int
}

type SubscriptionMaintenanceResult struct {
	Warnings []SubscriptionMaintenanceEvent
	Archived []SubscriptionMaintenanceEvent
}

// MaintainSubscriptions archives enabled subscriptions after 15 days without a
// new chapter for completed works, or the configured stale period (30 days by
// default) for ongoing works. Warnings are emitted once at noon on day 12/27.
func (s *Store) MaintainSubscriptions(
	ctx context.Context,
	staleDays map[string]int,
	now time.Time,
) (SubscriptionMaintenanceResult, error) {
	result := SubscriptionMaintenanceResult{}
	warningWindowOpen := now.Hour() >= 12
	nowUTC := now.UTC()
	rows, err := s.reader().QueryContext(ctx, subscriptionSelect+`
		WHERE archived_at IS NULL AND enabled = 1 AND temporary = 0
		ORDER BY id ASC`)
	if err != nil {
		return result, err
	}
	var subscriptions []model.Subscription
	for rows.Next() {
		subscription, scanErr := scanSubscription(rows)
		if scanErr != nil {
			_ = rows.Close()
			return result, scanErr
		}
		subscriptions = append(subscriptions, subscription)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return result, err
	}
	_ = rows.Close()

	for _, subscription := range subscriptions {
		anchor := subscription.CreatedAt
		if subscription.LastNewChapterAt != nil {
			anchor = *subscription.LastNewChapterAt
		}
		if anchor.IsZero() {
			continue
		}
		anchor = anchor.UTC()
		elapsed := nowUTC.Sub(anchor)
		if elapsed < 0 {
			elapsed = 0
		}
		daysWithoutNew := int(elapsed / (24 * time.Hour))
		completed := subscriptionstate.IsCompleted(subscription.ComicStatus)
		thresholdDays := subscriptionArchiveThreshold(subscription.SourceID, staleDays, completed)
		if thresholdDays <= 0 {
			continue
		}
		event := SubscriptionMaintenanceEvent{
			Subscription:   subscription,
			Completed:      completed,
			ThresholdDays:  thresholdDays,
			DaysWithoutNew: daysWithoutNew,
		}
		if elapsed >= time.Duration(thresholdDays)*24*time.Hour {
			reason := "stale"
			if completed {
				reason = "completed"
			}
			cutoff := nowUTC.Add(-time.Duration(thresholdDays) * 24 * time.Hour)
			update, err := s.db.ExecContext(ctx, `
				UPDATE subscriptions SET archived_at = ?, archive_reason = ?, enabled = 0, disabled_at = ?,
					updated_at = CURRENT_TIMESTAMP
				WHERE id = ? AND archived_at IS NULL AND enabled = 1 AND temporary = 0
				  AND COALESCE(last_new_chapter_at, created_at) <= ?`,
				nowUTC, reason, nowUTC, subscription.ID, cutoff)
			if err != nil {
				return result, err
			}
			affected, _ := update.RowsAffected()
			if affected == 0 {
				continue
			}
			event.DaysRemaining = 0
			result.Archived = append(result.Archived, event)
			continue
		}
		warningDay := subscriptionArchiveWarningDay(completed, thresholdDays)
		if !warningWindowOpen || daysWithoutNew < warningDay || subscription.ArchiveWarningDays == warningDay {
			continue
		}
		remaining := thresholdDays - daysWithoutNew
		if remaining < 1 {
			remaining = 1
		}
		cutoff := nowUTC.Add(-time.Duration(warningDay) * 24 * time.Hour)
		update, err := s.db.ExecContext(ctx, `
			UPDATE subscriptions SET archive_warning_at = ?, archive_warning_days = ?,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND archived_at IS NULL AND enabled = 1 AND temporary = 0
			  AND COALESCE(last_new_chapter_at, created_at) <= ?
			  AND (archive_warning_at IS NULL OR archive_warning_days <> ?)`,
			nowUTC, warningDay, subscription.ID, cutoff, warningDay)
		if err != nil {
			return result, err
		}
		affected, _ := update.RowsAffected()
		if affected == 0 {
			continue
		}
		event.DaysRemaining = remaining
		result.Warnings = append(result.Warnings, event)
	}
	return result, nil
}

func subscriptionArchiveThreshold(sourceID string, staleDays map[string]int, completed bool) int {
	if completed {
		return 15
	}
	if days, ok := staleDays[sourceID]; ok {
		if days <= 0 {
			return 0
		}
		return days
	}
	if days, ok := staleDays["*"]; ok {
		if days <= 0 {
			return 0
		}
		return days
	}
	return 30
}

func subscriptionArchiveWarningDay(completed bool, thresholdDays int) int {
	if completed {
		return 12
	}
	if thresholdDays == 30 {
		return 27
	}
	if thresholdDays > 3 {
		return thresholdDays - 3
	}
	if thresholdDays > 1 {
		return thresholdDays - 1
	}
	return 1
}
func (s *Store) DeleteSubscription(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM subscriptions WHERE id = ?`, id)
	return err
}

func (s *Store) CreateDownloadJob(ctx context.Context, job model.DownloadJob) (model.DownloadJob, error) {
	mergeChapters, err := json.Marshal(job.MergeChapters)
	if err != nil {
		return model.DownloadJob{}, err
	}
	comicTags, err := json.Marshal(job.ComicTags)
	if err != nil {
		return model.DownloadJob{}, err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO download_jobs(source_id, comic_id, comic_title, comic_cover, comic_author, comic_description, comic_tags_json, chapter_id, chapter_title, chapter_order, merge_chapters_json, download_dir, convert_to_simplified, chapter_marker, queue_priority, status)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'queued')
		ON CONFLICT(source_id, comic_id, chapter_id) DO UPDATE SET
			comic_title = excluded.comic_title,
			comic_cover = excluded.comic_cover,
			comic_author = excluded.comic_author,
			comic_description = excluded.comic_description,
			comic_tags_json = excluded.comic_tags_json,
			chapter_title = excluded.chapter_title,
			chapter_order = excluded.chapter_order,
			merge_chapters_json = excluded.merge_chapters_json,
			download_dir = excluded.download_dir,
			convert_to_simplified = excluded.convert_to_simplified,
			queue_priority = CASE
				WHEN download_jobs.status IN ('completed', 'running') THEN download_jobs.queue_priority
				ELSE MAX(download_jobs.queue_priority, excluded.queue_priority)
			END,
			status = CASE WHEN download_jobs.status IN ('completed', 'running') THEN download_jobs.status ELSE 'queued' END,
			error = CASE WHEN download_jobs.status = 'completed' THEN download_jobs.error ELSE '' END,
			retry_count = CASE WHEN download_jobs.status IN ('completed', 'running') THEN download_jobs.retry_count ELSE 0 END,
			next_retry_at = CASE WHEN download_jobs.status IN ('completed', 'running') THEN download_jobs.next_retry_at ELSE NULL END,
			updated_at = CURRENT_TIMESTAMP`,
		job.SourceID, job.ComicID, job.ComicTitle, job.ComicCover, job.ComicAuthor, job.ComicDescription, string(comicTags), job.ChapterID, job.ChapterTitle, job.ChapterOrder, string(mergeChapters), job.DownloadDir, job.ConvertToSimplified, job.ChapterMarker, job.QueuePriority)
	if err != nil {
		return model.DownloadJob{}, err
	}
	return s.DownloadJobByChapter(ctx, job.SourceID, job.ComicID, job.ChapterID)
}

func (s *Store) DownloadJobByChapter(ctx context.Context, sourceID, comicID, chapterID string) (model.DownloadJob, error) {
	return scanJob(s.reader().QueryRowContext(ctx, jobSelect+` WHERE source_id = ? AND comic_id = ? AND chapter_id = ?`, sourceID, comicID, chapterID))
}

func (s *Store) DownloadJob(ctx context.Context, id int64) (model.DownloadJob, error) {
	return scanJob(s.reader().QueryRowContext(ctx, jobSelect+` WHERE id = ?`, id))
}

// DownloadJobPage returns one page of the task list together with the total row
// count of the same selection. status is "completed" for the finished list and
// anything else for the unfinished list (queued/running/waiting/paused plus the
// failures that still need attention).
//
// The task list used to load the whole download_jobs table in one response; on
// a NAS with tens of thousands of finished chapters that dominated both the
// query and the browser's parse time, so the UI now pages through it.
func (s *Store) DownloadJobPage(ctx context.Context, status string, limit, offset int) ([]model.DownloadJob, int, error) {
	return s.QueryDownloadJobPage(ctx, DownloadJobQuery{Status: status}, limit, offset)
}

func (s *Store) ListDownloadJobs(ctx context.Context, limit int) ([]model.DownloadJob, error) {
	if limit <= 0 {
		// SQLite treats a negative LIMIT as unlimited.
		limit = -1
	}
	// 进行中、失败和已完成记录分开查询，再合并结果。失败记录不能被大量
	// 排队任务挤出结果集，因此三类记录各自使用独立的 LIMIT。
	rows, err := s.reader().QueryContext(ctx, `
		SELECT * FROM (
			SELECT id, source_id, comic_id, comic_title, comic_cover, comic_author, comic_description, comic_tags_json, chapter_id, chapter_title, chapter_order, merge_chapters_json, status, total_pages, completed_pages, file_path, error, retry_count, next_retry_at, download_dir, convert_to_simplified, chapter_marker, queue_priority, created_at, updated_at, started_at, finished_at
			FROM download_jobs WHERE status NOT IN ('completed', 'failed', 'canceled')
			ORDER BY CASE status WHEN 'running' THEN 0 WHEN 'queued' THEN 1 WHEN 'waiting' THEN 2 WHEN 'paused' THEN 3 ELSE 4 END, CASE WHEN status = 'queued' THEN queue_priority ELSE 0 END DESC, id ASC
			LIMIT ?
		)
		UNION ALL
		SELECT * FROM (
			SELECT id, source_id, comic_id, comic_title, comic_cover, comic_author, comic_description, comic_tags_json, chapter_id, chapter_title, chapter_order, merge_chapters_json, status, total_pages, completed_pages, file_path, error, retry_count, next_retry_at, download_dir, convert_to_simplified, chapter_marker, queue_priority, created_at, updated_at, started_at, finished_at
			FROM download_jobs WHERE status IN ('failed', 'canceled')
			ORDER BY id DESC
			LIMIT ?
		)
		UNION ALL
		SELECT * FROM (
			SELECT id, source_id, comic_id, comic_title, comic_cover, comic_author, comic_description, comic_tags_json, chapter_id, chapter_title, chapter_order, merge_chapters_json, status, total_pages, completed_pages, file_path, error, retry_count, next_retry_at, download_dir, convert_to_simplified, chapter_marker, queue_priority, created_at, updated_at, started_at, finished_at
			FROM download_jobs WHERE status = 'completed'
			ORDER BY COALESCE(finished_at, updated_at, created_at) DESC, id DESC
			LIMIT ?
		)`, limit, limit, limit)
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

func (s *Store) NextQueuedJob(ctx context.Context) (model.DownloadJob, bool, error) {
	job, err := scanJob(s.reader().QueryRowContext(ctx, jobSelect+` WHERE status = 'queued' ORDER BY queue_priority DESC, id ASC LIMIT 1`))
	if errors.Is(err, ErrNotFound) {
		return model.DownloadJob{}, false, nil
	}
	if err != nil {
		return model.DownloadJob{}, false, err
	}
	return job, true, nil
}

// RequeueRunningJobs restores jobs interrupted by a process restart. Graceful
// shutdown cancels active contexts, so canceled jobs are included alongside
// running jobs. Page progress is preserved and the downloader resumes cached
// pages instead of starting the chapter from zero. It must only be called once
// at startup: the download engine owns the running state afterwards.
func (s *Store) RequeueRunningJobs(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE download_jobs
		SET status = 'queued', error = '', started_at = NULL, finished_at = NULL, updated_at = CURRENT_TIMESTAMP
		WHERE status IN ('running', 'canceled')`)
	return err
}

func (s *Store) ListQueuedJobs(ctx context.Context) ([]model.DownloadJob, error) {
	rows, err := s.reader().QueryContext(ctx, jobSelect+` WHERE status = 'queued' ORDER BY queue_priority DESC, id ASC`)
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

// ListQueuedJobsPage returns one bounded page of queued work. The downloader
// advances through pages instead of materializing the entire queue on every
// scheduler kick, which keeps memory flat for very large backlogs.
func (s *Store) ListQueuedJobsPage(ctx context.Context, offset, limit int) ([]model.DownloadJob, error) {
	if offset < 0 {
		offset = 0
	}
	if limit < 1 {
		limit = 64
	}
	rows, err := s.reader().QueryContext(ctx, jobSelect+`
		WHERE status = 'queued'
		ORDER BY queue_priority DESC, id ASC
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]model.DownloadJob, 0, limit)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Store) ListRunningJobs(ctx context.Context) ([]model.DownloadJob, error) {
	rows, err := s.reader().QueryContext(ctx, jobSelect+` WHERE status = 'running' ORDER BY id ASC`)
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

// ListInterruptedJobs returns jobs that were active when the previous process
// stopped. Graceful shutdown marks running jobs canceled before exit, so both
// states are included before RequeueRunningJobs converts them back to queued.
func (s *Store) ListInterruptedJobs(ctx context.Context) ([]model.DownloadJob, error) {
	rows, err := s.reader().QueryContext(ctx, jobSelect+` WHERE status IN ('running', 'canceled') ORDER BY id ASC`)
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

const jobSelect = `SELECT id, source_id, comic_id, comic_title, comic_cover, comic_author, comic_description, comic_tags_json, chapter_id, chapter_title, chapter_order, merge_chapters_json, status, total_pages, completed_pages, file_path, error, retry_count, next_retry_at, download_dir, convert_to_simplified, chapter_marker, queue_priority, created_at, updated_at, started_at, finished_at FROM download_jobs`

func scanJob(row rowScanner) (model.DownloadJob, error) {
	var job model.DownloadJob
	var mergeChapters, comicTags string
	err := row.Scan(&job.ID, &job.SourceID, &job.ComicID, &job.ComicTitle, &job.ComicCover, &job.ComicAuthor, &job.ComicDescription, &comicTags, &job.ChapterID, &job.ChapterTitle, &job.ChapterOrder, &mergeChapters, &job.Status, &job.TotalPages, &job.CompletedPages, &job.FilePath, &job.Error, &job.RetryCount, &job.NextRetryAt, &job.DownloadDir, &job.ConvertToSimplified, &job.ChapterMarker, &job.QueuePriority, &job.CreatedAt, &job.UpdatedAt, &job.StartedAt, &job.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DownloadJob{}, ErrNotFound
	}
	if err == nil {
		if strings.TrimSpace(mergeChapters) != "" {
			_ = json.Unmarshal([]byte(mergeChapters), &job.MergeChapters)
		}
		if strings.TrimSpace(comicTags) != "" {
			_ = json.Unmarshal([]byte(comicTags), &job.ComicTags)
		}
	}
	return job, err
}

func (s *Store) SetDownloadJobFilePath(ctx context.Context, id int64, filePath string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE download_jobs SET file_path = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, filePath, id)
	return err
}

// SetDownloadJobLocation 在作品目录改名或改位置后同步更新任务的下载目录与文件路径。
func (s *Store) SetDownloadJobLocation(ctx context.Context, id int64, downloadDir, filePath string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE download_jobs SET download_dir = ?, file_path = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, downloadDir, filePath, id)
	return err
}

func (s *Store) UpdateDownloadJob(ctx context.Context, id int64, status string, totalPages, completedPages int, filePath, jobError string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE download_jobs SET status = ?, total_pages = ?, completed_pages = ?, file_path = ?, error = ?, updated_at = CURRENT_TIMESTAMP,
			queue_priority = CASE WHEN ? IN ('completed', 'failed', 'canceled') THEN 0 ELSE queue_priority END,
			started_at = CASE WHEN ? = 'running' THEN COALESCE(started_at, CURRENT_TIMESTAMP) ELSE started_at END,
			finished_at = CASE WHEN ? IN ('completed', 'failed', 'canceled') THEN CURRENT_TIMESTAMP ELSE finished_at END
		WHERE id = ?`, status, totalPages, completedPages, filePath, jobError, status, status, status, id)
	return err
}

// FailDownloadJob records a failed attempt and its next automatic retry.
func (s *Store) FailDownloadJob(ctx context.Context, id int64, totalPages, completedPages int, jobError string, retryCount int, nextRetryAt *time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE download_jobs SET status = 'failed', total_pages = ?, completed_pages = ?, error = ?,
			retry_count = ?, next_retry_at = ?, queue_priority = 0, updated_at = CURRENT_TIMESTAMP,
			finished_at = CURRENT_TIMESTAMP
		WHERE id = ?`, totalPages, completedPages, jobError, retryCount, nextRetryAt, id)
	return err
}

// ResetDownloadJob queues a task and restarts its retry budget.
func (s *Store) ResetDownloadJob(ctx context.Context, id int64, queuePriority int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE download_jobs SET status = 'queued', completed_pages = 0, error = '', retry_count = 0,
			next_retry_at = NULL, queue_priority = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, queuePriority, id)
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

// DueRetryJobs returns the failed jobs that ActivateDueRetries will requeue on
// this pass, so the engine can emit one grouped notification per work.
func (s *Store) DueRetryJobs(ctx context.Context, now time.Time) ([]model.DownloadJob, error) {
	rows, err := s.reader().QueryContext(ctx, `
		SELECT id, source_id, comic_id, comic_title, comic_author, comic_cover, chapter_title
		FROM download_jobs
		WHERE status = 'failed' AND retry_count < 2
			AND next_retry_at IS NOT NULL AND next_retry_at <= ?
		ORDER BY source_id ASC, comic_id ASC, chapter_order ASC, id ASC`, now.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []model.DownloadJob
	for rows.Next() {
		var job model.DownloadJob
		if err := rows.Scan(&job.ID, &job.SourceID, &job.ComicID, &job.ComicTitle, &job.ComicAuthor, &job.ComicCover, &job.ChapterTitle); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Store) UpdateDownloadProgress(ctx context.Context, id int64, completedPages int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE download_jobs SET completed_pages = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, completedPages, id)
	return err
}

// UnfinishedDownloadJobsByComic 返回某部作品还在下载或排队中的任务。删除订阅
// 卡片时用它决定要取消哪些任务，也用来判断临时卡片是否可以移除。
func (s *Store) UnfinishedDownloadJobsByComic(ctx context.Context, sourceID, comicID string) ([]model.DownloadJob, error) {
	rows, err := s.reader().QueryContext(ctx, jobSelect+`
		WHERE source_id = ? AND comic_id = ? AND status IN ('queued', 'running', 'waiting', 'paused')
		ORDER BY id ASC`, sourceID, comicID)
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

func (s *Store) InitialDownloadBatchPending(ctx context.Context, sourceID, comicID string) (bool, error) {
	var count int
	err := s.reader().QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM download_jobs
		WHERE source_id = ? AND comic_id = ?
			AND (
				status IN ('queued', 'running', 'waiting', 'paused')
				OR (status = 'failed' AND next_retry_at IS NOT NULL)
			)`, sourceID, comicID).Scan(&count)
	return count > 0, err
}

func (s *Store) DeleteDownloadJob(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM download_jobs WHERE id = ?`, id)
	return err
}

// ActivityGroup is one work-level summary for the activity feed.
type ActivityGroup struct {
	SourceID  string
	ComicID   string
	Title     string
	Cover     string
	Succeeded int
	Failed    int
	Pending   int
	LastAt    time.Time
}

// DownloadJobActivity aggregates download tasks per work in SQLite so the cost
// stays bounded by the number of works rather than the whole task history.
// Queued/running-only works are excluded: the activity feed shows finished
// results, with pending chapters counted only for works that already finished.
func (s *Store) DownloadJobActivity(ctx context.Context, limit int) ([]ActivityGroup, error) {
	if limit <= 0 {
		limit = 50
	}
	// 只聚合最近 30 天的任务：通知窗口不需要全量历史，限定时间范围才能让
	// SQLite 走索引、避免每次轮询都整表 GROUP BY。
	since := time.Now().Add(-30 * 24 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	rows, err := s.reader().QueryContext(ctx, `
		SELECT source_id, comic_id, MAX(comic_title) AS title,
			MAX(NULLIF(TRIM(comic_cover), '')) AS comic_cover,
			SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END) AS succeeded,
			SUM(CASE WHEN status IN ('failed', 'canceled') THEN 1 ELSE 0 END) AS failed,
			SUM(CASE WHEN status IN ('queued', 'running', 'waiting', 'paused') THEN 1 ELSE 0 END) AS pending,
			MAX(COALESCE(finished_at, updated_at, created_at)) AS last_at
		FROM download_jobs
		WHERE updated_at >= ?
		GROUP BY source_id, comic_id
		HAVING succeeded > 0 OR failed > 0
		ORDER BY last_at DESC
		LIMIT ?`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ActivityGroup, 0, limit)
	for rows.Next() {
		var (
			item      ActivityGroup
			succeeded sql.NullInt64
			failed    sql.NullInt64
			pending   sql.NullInt64
			lastAt    sql.NullString
		)
		if err := rows.Scan(&item.SourceID, &item.ComicID, &item.Title, &item.Cover, &succeeded, &failed, &pending, &lastAt); err != nil {
			return nil, err
		}
		item.Succeeded = int(succeeded.Int64)
		item.Failed = int(failed.Int64)
		item.Pending = int(pending.Int64)
		item.LastAt = parseSQLiteTime(lastAt.String)
		items = append(items, item)
	}
	return items, rows.Err()
}

// parseSQLiteTime accepts the timestamp shapes SQLite and the driver produce.
func parseSQLiteTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02T15:04:05.999999999Z07:00",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if parsed, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

// DownloadJobsByComic returns every chapter task for one work, including
// completed and failed records, so a full refresh can clear them before
// re-queuing the chapters from the source.
func (s *Store) DownloadJobsByComic(ctx context.Context, sourceID, comicID string) ([]model.DownloadJob, error) {
	rows, err := s.reader().QueryContext(ctx, jobSelect+` WHERE source_id = ? AND comic_id = ? ORDER BY chapter_order ASC, id ASC`, sourceID, comicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []model.DownloadJob
	for rows.Next() {
		item, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// DeleteDownloadJobsForComic removes every chapter task for one work and
// reports how many records were deleted.
func (s *Store) DeleteDownloadJobsForComic(ctx context.Context, sourceID, comicID string) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM download_jobs WHERE source_id = ? AND comic_id = ?`, sourceID, comicID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) Stats(ctx context.Context) (model.Stats, error) {
	var stats model.Stats
	err := s.reader().QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM subscriptions),
			COALESCE(SUM(CASE WHEN status IN ('queued','running','waiting','paused') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status IN ('queued','waiting','paused') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0)
		FROM download_jobs`).Scan(
		&stats.Subscriptions,
		&stats.ActiveJobs,
		&stats.RunningJobs,
		&stats.QueuedJobs,
		&stats.FailedJobs,
		&stats.CompletedJobs,
	)
	stats.LibraryItems = stats.CompletedJobs
	return stats, err
}

func (s *Store) ListSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.reader().QueryContext(ctx, `SELECT key, value FROM settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		values[key] = value
	}
	return values, rows.Err()
}

func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
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

// CompletedDownloadJobsByComic returns every completed chapter for one
// subscription work. Fill state is deliberately unbounded by task-list page
// limits, history-size caps or age-based activity windows.
func (s *Store) CompletedDownloadJobsByComic(ctx context.Context, sourceID, comicID string) ([]model.DownloadJob, error) {
	rows, err := s.reader().QueryContext(ctx, jobSelect+` WHERE source_id = ? AND comic_id = ? AND status = 'completed' ORDER BY chapter_order ASC, id ASC`, sourceID, comicID)
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

// FailedDownloadJobsByComic returns every failed or canceled chapter for a work
// so the subscription checker can retry them without touching finished files.
func (s *Store) FailedDownloadJobsByComic(ctx context.Context, sourceID, comicID string) ([]model.DownloadJob, error) {
	rows, err := s.reader().QueryContext(ctx, `
		SELECT id, source_id, comic_id, comic_title, comic_author, comic_cover, chapter_title
		FROM download_jobs WHERE source_id = ? AND comic_id = ? AND status IN ('failed', 'canceled')
		ORDER BY chapter_order ASC, id ASC`, sourceID, comicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []model.DownloadJob
	for rows.Next() {
		var job model.DownloadJob
		if err := rows.Scan(&job.ID, &job.SourceID, &job.ComicID, &job.ComicTitle, &job.ComicAuthor, &job.ComicCover, &job.ChapterTitle); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}
