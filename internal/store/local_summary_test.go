package store

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLocalSummariesDoNotDecodeJobPayloads(t *testing.T) {
	repository := openTestStore(t)
	ctx := context.Background()
	_, err := repository.db.ExecContext(ctx, `
		INSERT INTO subscriptions(source_id, comic_id, title, download_dir)
		VALUES ('source', 'sub', 'Subscription', '/subscription');
		INSERT INTO download_jobs(source_id, comic_id, comic_title, chapter_id, chapter_title,
			download_dir, comic_cover, comic_tags_json, merge_chapters_json)
		VALUES ('source', 'work', 'Work', 'chapter', 'Chapter', '/download',
			'https://example.com/cover.jpg', 'invalid JSON', 'invalid JSON')`)
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := repository.LocalDownloadDirectories(ctx)
	if err != nil || !reflect.DeepEqual(dirs, []string{"/subscription", "/download"}) {
		t.Fatalf("directories = %v, err = %v", dirs, err)
	}
	covers, err := repository.DownloadCoverFallbacks(ctx)
	if err != nil || covers["source\x00work"] != "https://example.com/cover.jpg" {
		t.Fatalf("cover summaries = %v, err = %v", covers, err)
	}
}

func BenchmarkLocalDownloadDirectories(b *testing.B) {
	repository, err := Open(filepath.Join(b.TempDir(), "manco.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer repository.Close()
	_, err = repository.db.Exec(`
		WITH RECURSIVE seq(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM seq WHERE x < 1800)
		INSERT INTO download_jobs(source_id, comic_id, comic_title, chapter_id, chapter_title, download_dir, comic_description)
		SELECT 'source', 'work', 'Work', CAST(x AS TEXT), 'Chapter', '/downloads', ? FROM seq`,
		strings.Repeat("metadata ", 1024))
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		if _, err := repository.LocalDownloadDirectories(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}
