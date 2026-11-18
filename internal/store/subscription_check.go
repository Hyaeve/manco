package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/subscriptionstate"
)

// CommitSubscriptionCheck records the observed baseline and enqueues new chapters
// together. If the subscription has changed since the check started, it must be
// checked again using the current state.
func (s *Store) CommitSubscriptionCheck(ctx context.Context, previous model.Subscription, chapter model.Chapter, chapterCount int, comicStatus string, newChapter bool, jobs []model.DownloadJob) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, updateSubscriptionCheckSQL+`
		AND source_id = ? AND comic_id = ? AND enabled = ? AND auto_download = ?
		AND last_chapter_id = ? AND last_chapter_order = ?
		AND download_dir = ? AND series_dir = ? AND convert_to_simplified = ? AND chapter_marker = ?`,
		chapter.ID, chapter.Title, chapter.Order, chapterCount, chapterCount, comicStatus,
		subscriptionstate.IsCompleted(comicStatus), newChapter, newChapter, newChapter, previous.ID,
		previous.SourceID, previous.ComicID, previous.Enabled, previous.AutoDownload,
		previous.LastChapterID, previous.LastChapterOrder,
		previous.DownloadDir, previous.SeriesDir, previous.ConvertToSimplified, previous.ChapterMarker)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return errors.New("订阅状态已变更，请重新检查")
	}
	for _, job := range jobs {
		if err := upsertDownloadJob(ctx, tx, job); err != nil {
			return fmt.Errorf("加入章节 %q 到下载队列: %w", job.ChapterID, err)
		}
	}
	return tx.Commit()
}
