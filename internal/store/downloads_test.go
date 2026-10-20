package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hyaeve/manco/internal/model"
)

func BenchmarkDownloadTaskPage(b *testing.B) {
	repository, err := Open(filepath.Join(b.TempDir(), "tasks.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	_, err = repository.db.ExecContext(ctx, `
		WITH RECURSIVE seq(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM seq WHERE x < 10000)
		INSERT INTO download_jobs(source_id, comic_id, comic_title, chapter_id, chapter_title, comic_description, comic_tags_json)
		SELECT 'source', 'work', 'Work', CAST(x AS TEXT), 'Chapter ' || x, ?, '["tag"]' FROM seq`,
		strings.Repeat("metadata ", 1024))
	if err != nil {
		b.Fatal(err)
	}
	for _, compact := range []bool{false, true} {
		name := "full"
		if compact {
			name = "compact"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, _, err := repository.QueryDownloadJobPage(ctx, DownloadJobQuery{Compact: compact}, 100, 0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestDownloadQueriesFilterBeyondFirstPageAndReturnCompactRows(t *testing.T) {
	repository := openTestStore(t)
	ctx := context.Background()
	for index, status := range []string{"queued", "running", "failed", "paused", "canceled", "completed"} {
		job, err := repository.CreateDownloadJob(ctx, model.DownloadJob{
			SourceID: "source", ComicID: "work", ComicTitle: "作品", ChapterID: status,
			ChapterTitle: status + " 100%_", ChapterOrder: float64(index), ComicAuthor: "Author",
			ComicDescription: strings.Repeat("简介", 10000), ComicTags: []string{"tag"},
			MergeChapters: []model.Chapter{{ID: "huge", Title: "metadata"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := repository.UpdateDownloadJob(ctx, job.ID, status, 10, 1, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	page, total, err := repository.QueryDownloadJobPage(ctx, DownloadJobQuery{Filter: "failed", Query: "AUTHOR", Compact: true}, 1, 1)
	if err != nil || total != 2 || len(page) != 1 {
		t.Fatalf("failed page = %d/%d: %v", len(page), total, err)
	}
	if page[0].ChapterID != "" || page[0].ComicDescription != "" || len(page[0].ComicTags) != 0 || len(page[0].MergeChapters) != 0 {
		t.Fatal("compact task row contains heavy metadata")
	}
	counts, err := repository.DownloadCounts(ctx, "100%_")
	if err != nil || counts != (DownloadJobCounts{All: 5, Active: 1, Queued: 2, Failed: 2}) {
		t.Fatalf("counts = %+v: %v", counts, err)
	}
	rows, total, err := repository.QueryDownloadJobPage(ctx, DownloadJobQuery{Filter: "queued", Query: "100%_"}, 20, 0)
	if err != nil || total != 2 || len(rows) != 2 {
		t.Fatalf("literal search = %d/%d: %v", len(rows), total, err)
	}
	snapshot, total, err := repository.QueryDownloadJobPage(ctx, DownloadJobQuery{IDs: []int64{rows[0].ID}}, 200, 0)
	if err != nil || total != 1 || len(snapshot) != 1 || snapshot[0].ID != rows[0].ID {
		t.Fatalf("visible snapshot = %v/%d: %v", snapshot, total, err)
	}
}
