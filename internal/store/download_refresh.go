package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/subscriptionstate"
)

// QueueFullRefresh atomically replaces inactive task rows for the current
// catalog. New row IDs keep a fresh attempt separate from old page caches.
// The previous artifact stays in place until the downloader publishes output.
func (s *Store) QueueFullRefresh(ctx context.Context, subscription model.Subscription, jobs []model.DownloadJob) (queued, skipped, replaced int, err error) {
	if len(jobs) == 0 {
		return 0, 0, 0, errors.New("没有可下载章节")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, 0, err
	}
	defer tx.Rollback()

	newest := jobs[len(jobs)-1]
	chapterID, chapterTitle, chapterOrder := newest.ChapterID, newest.ChapterTitle, newest.ChapterOrder
	if subscription.LastChapterID != "" && chapterOrder < subscription.LastChapterOrder {
		chapterID, chapterTitle, chapterOrder = subscription.LastChapterID, subscription.LastChapterTitle, subscription.LastChapterOrder
	}
	result, err := tx.ExecContext(ctx, updateSubscriptionCheckSQL+`
		AND source_id = ? AND comic_id = ? AND last_chapter_id = ? AND last_chapter_order = ?
		AND download_dir = ? AND series_dir = ? AND convert_to_simplified = ? AND chapter_marker = ?`,
		chapterID, chapterTitle, chapterOrder, 0, 0, subscription.ComicStatus,
		subscriptionstate.IsCompleted(subscription.ComicStatus), true, true, true, subscription.ID,
		subscription.SourceID, subscription.ComicID, subscription.LastChapterID, subscription.LastChapterOrder,
		subscription.DownloadDir, subscription.SeriesDir, subscription.ConvertToSimplified, subscription.ChapterMarker)
	if err != nil {
		return 0, 0, 0, err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return 0, 0, 0, err
	} else if changed == 0 {
		return 0, 0, 0, errors.New("订阅状态已变化，请重试全量下载")
	}

	for _, job := range jobs {
		var id int64
		var status string
		err := tx.QueryRowContext(ctx, `SELECT id, status, file_path FROM download_jobs
			WHERE source_id = ? AND comic_id = ? AND chapter_id = ?`, job.SourceID, job.ComicID, job.ChapterID).
			Scan(&id, &status, &job.FilePath)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, 0, 0, err
		}
		switch status {
		case "running":
			skipped++
			continue
		case "queued":
			if _, err := tx.ExecContext(ctx, `UPDATE download_jobs SET queue_priority = MAX(queue_priority, ?) WHERE id = ?`, job.QueuePriority, id); err != nil {
				return 0, 0, 0, err
			}
			skipped++
			continue
		}
		if id != 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM download_jobs WHERE id = ?`, id); err != nil {
				return 0, 0, 0, err
			}
			replaced++
		}
		if err := upsertDownloadJob(ctx, tx, job); err != nil {
			return 0, 0, 0, err
		}
		queued++
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, 0, err
	}
	return queued, skipped, replaced, nil
}
