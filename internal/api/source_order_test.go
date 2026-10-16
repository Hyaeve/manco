package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hyaeve/manco/internal/config"
	"github.com/hyaeve/manco/internal/downloader"
	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
)

func TestSortSourceItemsKeepsBuiltinsFirstAndAppliesPluginOrder(t *testing.T) {
	setting := map[string][]string{
		source.KindComic: {"plugin-b", "plugin-a"},
		source.KindBook:  {"plugin-book-b", "plugin-book-a"},
	}
	items := []model.SourceInfo{
		{ID: "plugin-a", Kind: source.KindComic},
		{ID: "builtin-jmcomic", Kind: source.KindComic, Builtin: true},
		{ID: "plugin-book-a", Kind: source.KindBook},
		{ID: "plugin-b", Kind: source.KindComic},
		{ID: "builtin-picacg", Kind: source.KindComic, Builtin: true},
		{ID: "plugin-book-b", Kind: source.KindBook},
		{ID: "plugin-unlisted", Kind: source.KindComic},
	}

	got := sortSourceItems(items, setting)
	want := []string{
		"builtin-jmcomic",
		"builtin-picacg",
		"plugin-book-b",
		"plugin-book-a",
		"plugin-b",
		"plugin-a",
		"plugin-unlisted",
	}
	for index, wantID := range want {
		if got[index].ID != wantID {
			t.Fatalf("order[%d] = %q, want %q (full: %v)", index, got[index].ID, wantID, sourceIDs(got))
		}
	}
}

func TestSortSourceItemsCannotPutPluginsBeforeBuiltins(t *testing.T) {
	setting := map[string][]string{
		source.KindComic: {"plugin-a", "picacg", "jmcomic"},
	}
	items := []model.SourceInfo{
		{ID: "plugin-a", Kind: source.KindComic},
		{ID: "jmcomic", Kind: source.KindComic, Builtin: true},
		{ID: "picacg", Kind: source.KindComic, Builtin: true},
	}

	got := sortSourceItems(items, setting)
	want := []string{"jmcomic", "picacg", "plugin-a"}
	for index, wantID := range want {
		if got[index].ID != wantID {
			t.Fatalf("order[%d] = %q, want %q (full: %v)", index, got[index].ID, wantID, sourceIDs(got))
		}
	}
}

func sourceIDs(items []model.SourceInfo) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func TestLocalMetadataForJobReadsSeriesComicInfo(t *testing.T) {
	root := t.TempDir()
	seriesDir := filepath.Join(root, downloader.SafeName("测试作品"))
	if err := os.MkdirAll(seriesDir, 0o755); err != nil {
		t.Fatalf("mkdir series: %v", err)
	}
	comicInfo := `<?xml version="1.0" encoding="utf-8"?>
<ComicInfo>
  <Series>测试作品</Series>
  <Writer>作者甲, 作者乙</Writer>
  <Summary>一段简介</Summary>
  <Genre>奇幻, 冒险</Genre>
</ComicInfo>`
	if err := os.WriteFile(filepath.Join(seriesDir, "ComicInfo.xml"), []byte(comicInfo), 0o644); err != nil {
		t.Fatalf("write comicinfo: %v", err)
	}

	server := &Server{cfg: config.Config{DownloadDir: root}}
	got := server.localMetadataForJob(model.DownloadJob{
		ComicTitle:  "测试作品",
		DownloadDir: root,
		FilePath:    filepath.Join(seriesDir, "第1话.cbz"),
	})
	if got.Author != "作者甲, 作者乙" || got.Description != "一段简介" {
		t.Fatalf("metadata = %+v", got)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "奇幻" || got.Tags[1] != "冒险" {
		t.Fatalf("tags = %v", got.Tags)
	}
}
