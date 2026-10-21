package store

import (
	"context"
	"strings"

	"github.com/hyaeve/manco/internal/model"
)

type DownloadJobQuery struct {
	Status  string
	Filter  string
	Query   string
	Compact bool
	IDs     []int64
}

type DownloadJobCounts struct {
	All    int `json:"all"`
	Active int `json:"active"`
	Queued int `json:"queued"`
	Failed int `json:"failed"`
}

const activeDownloadOrder = `CASE status WHEN 'running' THEN 0 WHEN 'queued' THEN 1 WHEN 'waiting' THEN 2 WHEN 'paused' THEN 3 ELSE 4 END, CASE WHEN status = 'queued' THEN queue_priority ELSE 0 END DESC, CASE WHEN status IN ('failed', 'canceled') THEN -id ELSE id END ASC`

// Repeated descriptions, encoded chapter payloads and merged chapter arrays
// are only needed when running a task, not when displaying its progress.
const compactJobSelect = `SELECT id, source_id, comic_id, comic_title, '', comic_author, '', '[]', '', chapter_title, chapter_order, '[]', status, total_pages, completed_pages, file_path, error, retry_count, next_retry_at, download_dir, convert_to_simplified, chapter_marker, queue_priority, created_at, updated_at, started_at, finished_at FROM download_jobs`

func downloadSearchClause(query string) (string, []any) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", nil
	}
	return ` AND (instr(lower(comic_title), lower(?)) > 0 OR instr(lower(chapter_title), lower(?)) > 0 OR instr(lower(comic_author), lower(?)) > 0)`, []any{query, query, query}
}

// ListQueuedJobRefsPage scans queue order without reading the large serialized
// chapter, page, description, and tag payloads for work that is not starting.
func (s *Store) ListQueuedJobRefsPage(ctx context.Context, offset, limit int) ([]model.QueuedJobRef, error) {
	if offset < 0 {
		offset = 0
	}
	if limit < 1 {
		limit = 64
	}
	rows, err := s.reader().QueryContext(ctx, `
		SELECT id, source_id FROM download_jobs
		WHERE status = 'queued'
		ORDER BY queue_priority DESC, id ASC
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := make([]model.QueuedJobRef, 0, limit)
	for rows.Next() {
		var ref model.QueuedJobRef
		if err := rows.Scan(&ref.ID, &ref.SourceID); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func (s *Store) QueryDownloadJobPage(ctx context.Context, options DownloadJobQuery, limit, offset int) ([]model.DownloadJob, int, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	where, order := `status <> 'completed'`, activeDownloadOrder
	if strings.EqualFold(strings.TrimSpace(options.Status), "completed") {
		where, order = `status = 'completed'`, `COALESCE(finished_at, updated_at, created_at) DESC, id DESC`
	} else {
		switch options.Filter {
		case "active":
			where += ` AND status = 'running'`
		case "queued":
			where += ` AND status IN ('queued', 'waiting', 'paused')`
		case "failed":
			where += ` AND status IN ('failed', 'canceled')`
		}
	}
	search, args := downloadSearchClause(options.Query)
	where += search
	if len(options.IDs) > 0 {
		placeholders := make([]string, len(options.IDs))
		for index, id := range options.IDs {
			placeholders[index] = "?"
			args = append(args, id)
		}
		where += ` AND id IN (` + strings.Join(placeholders, ",") + `)`
	}
	var total int
	if err := s.reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM download_jobs WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	projection := jobSelect
	if options.Compact {
		projection = compactJobSelect
	}
	args = append(args, limit, offset)
	rows, err := s.reader().QueryContext(ctx, projection+` WHERE `+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	jobs := make([]model.DownloadJob, 0, min(limit, total))
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, 0, err
		}
		jobs = append(jobs, job)
	}
	return jobs, total, rows.Err()
}

func (s *Store) DownloadCounts(ctx context.Context, query string) (DownloadJobCounts, error) {
	search, args := downloadSearchClause(query)
	var counts DownloadJobCounts
	err := s.reader().QueryRowContext(ctx, `
		SELECT COUNT(*),
			COALESCE(SUM(status = 'running'), 0),
			COALESCE(SUM(status IN ('queued', 'waiting', 'paused')), 0),
			COALESCE(SUM(status IN ('failed', 'canceled')), 0)
		FROM download_jobs WHERE status <> 'completed'`+search, args...).
		Scan(&counts.All, &counts.Active, &counts.Queued, &counts.Failed)
	return counts, err
}
