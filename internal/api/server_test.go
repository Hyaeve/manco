package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/sources"
)

type lazyFilterFixture struct {
	info    model.SourceInfo
	filters []model.FilterGroup
	calls   int
}

func (f *lazyFilterFixture) Info() model.SourceInfo { return f.info }
func (f *lazyFilterFixture) Search(context.Context, source.Account, string, int) (model.SearchResult, error) {
	return model.SearchResult{}, nil
}
func (f *lazyFilterFixture) Browse(context.Context, source.Account, model.BrowseOptions, int) (model.SearchResult, error) {
	return model.SearchResult{}, nil
}
func (f *lazyFilterFixture) Detail(context.Context, source.Account, string) (model.Comic, error) {
	return model.Comic{}, nil
}
func (f *lazyFilterFixture) Chapters(context.Context, source.Account, string) ([]model.Chapter, error) {
	return nil, nil
}
func (f *lazyFilterFixture) Pages(context.Context, source.Account, string, model.Chapter) ([]model.Page, error) {
	return nil, nil
}
func (f *lazyFilterFixture) Filters(context.Context) ([]model.FilterGroup, error) {
	f.calls++
	return f.filters, nil
}

func TestLazyPluginFiltersCanBeNormalizedAndMarkedDisabled(t *testing.T) {
	item := &lazyFilterFixture{
		info: model.SourceInfo{ID: "plugin-source"},
		filters: []model.FilterGroup{{
			Key:   "category",
			Label: "类型",
			Options: []model.FilterOption{
				{Value: "MANGA", Label: "漫画"},
				{Value: "MANHWA", Label: "韩漫"},
			},
		}},
	}
	groups, err := sourceFilterGroups(context.Background(), item)
	if err != nil {
		t.Fatalf("sourceFilterGroups: %v", err)
	}
	if item.calls != 1 || len(groups) != 1 {
		t.Fatalf("lazy filters = calls %d groups %d, want 1 and 1", item.calls, len(groups))
	}

	normalized := normalizeDisabledCategories(groups, []string{"MANHWA", "missing", "MANHWA"})
	if len(normalized) != 1 || normalized[0] != "MANHWA" {
		t.Fatalf("normalized disabled categories = %#v, want [MANHWA]", normalized)
	}
	marked := applyDisabledCategories(groups, normalized)
	if !marked[0].Options[1].Disabled || marked[0].Options[0].Disabled {
		t.Fatalf("disabled flags = %#v, want only MANHWA disabled", marked[0].Options)
	}
}

func TestParseLogEntryAddsModuleAndLevel(t *testing.T) {
	entry := parseLogEntry("2026/10/11 16:00:00 downloader: chapter failed: timeout")
	if entry.Module != "downloads" {
		t.Fatalf("module = %q, want downloads", entry.Module)
	}
	if entry.Level != "error" {
		t.Fatalf("level = %q, want error", entry.Level)
	}
	debug := parseLogEntry("scheduler: debug subscription baseline")
	if debug.Module != "subscriptions" || debug.Level != "debug" {
		t.Fatalf("debug entry = %#v", debug)
	}
	system := parseLogEntry("manco listening on :15600 (downloads: /downloads)")
	if system.Module != "system" || system.Level != "info" {
		t.Fatalf("system entry = %#v", system)
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name  string
		left  string
		right string
		want  int
	}{
		{name: "newer patch", left: "v0.1.4", right: "v0.1.3", want: 1},
		{name: "older patch", left: "v0.1.3", right: "v0.1.4", want: -1},
		{name: "equal", left: "v0.1.4", right: "0.1.4", want: 0},
		{name: "hundred base middle", left: "v0.101.0", right: "v0.100.99", want: 1},
		{name: "longer patch", left: "v1.2.0", right: "v1.2", want: 0},
		{name: "prerelease suffix ignored", left: "v0.1.4-rc1", right: "v0.1.4", want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := compareVersions(test.left, test.right); got != test.want {
				t.Fatalf("compareVersions(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
			}
		})
	}
}

func TestBackfillChapterMarkerFilesRenamesAndPersistsPath(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "第1话.cbz")
	if err := os.WriteFile(oldPath, []byte("cbz"), 0o644); err != nil {
		t.Fatalf("write chapter: %v", err)
	}

	var persistedID int64
	var persistedPath string
	renamed, failed, err := backfillChapterMarkerFiles(
		context.Background(),
		[]model.DownloadJob{{ID: 42, FilePath: oldPath, ChapterTitle: "第1话", ChapterOrder: 1}},
		func(_ context.Context, id int64, path string) error {
			persistedID = id
			persistedPath = path
			return nil
		},
		func(string, ...any) {},
	)
	if err != nil {
		t.Fatalf("backfill chapter markers: %v", err)
	}
	if renamed != 1 || failed != 0 {
		t.Fatalf("backfill result = renamed %d, failed %d, want 1, 0", renamed, failed)
	}
	newPath := filepath.Join(dir, "#1 第1话.cbz")
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("renamed chapter missing: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old chapter still exists, stat error = %v", err)
	}
	if persistedID != 42 || persistedPath != newPath {
		t.Fatalf("persisted path = id %d path %q, want 42 %q", persistedID, persistedPath, newPath)
	}
}

func TestFetchGitHubAtomReadsLatestVersion(t *testing.T) {
	feed := `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <id>tag:github.com,2008:Repository/1411402288/v9.8.7</id>
    <link rel="alternate" type="text/html" href="https://github.com/Hyaeve/manco/releases/tag/v9.8.7"/>
  </entry>
</feed>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		_, _ = w.Write([]byte(feed))
	}))
	defer server.Close()

	var latest, releaseURL string
	status, err := (&Server{registry: sources.NewRegistry(server.Client(), nil, nil)}).fetchGitHubAtom(
		context.Background(), server.URL, &latest, &releaseURL,
	)
	if err != nil {
		t.Fatalf("fetchGitHubAtom: %v", err)
	}
	if status != http.StatusOK || latest != "v9.8.7" {
		t.Fatalf("status = %d, latest = %q, want 200 and v9.8.7", status, latest)
	}
	if releaseURL != "https://github.com/Hyaeve/manco/releases/tag/v9.8.7" {
		t.Fatalf("releaseURL = %q", releaseURL)
	}
}
