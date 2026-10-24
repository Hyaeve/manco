package pluginruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
)

func TestCoverUsesFreshMetadataWithStableComicID(t *testing.T) {
	manager, calls := newProtocolTestManagerResult(t, map[string]any{
		"artifact": map[string]any{"relativePath": "cover.img", "contentType": "image/png"},
	})
	manager.dataDir = t.TempDir()
	imagePath := manager.ImagePath(RuntimeKototoro, "cover.img")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imagePath, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter, err := NewAdapter(manager, model.CustomSource{
		ID: "demo", Config: json.RawMessage(`{"runtime":"kototoro-parser","pluginId":"demo","sourceName":"demo"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := encodeObject(map[string]any{"id": "1", "coverUrl": "https://cdn.example/old.png"})
	_, _, err = adapter.CoverImage(context.Background(), source.Account{}, model.Comic{
		ID: id, Cover: "https://cdn.example/new.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	call := <-calls
	content := call["content"].(map[string]any)
	if content["coverUrl"] != "https://cdn.example/new.png" || content["largeCoverUrl"] != "https://cdn.example/new.png" {
		t.Fatal("stable work ID caused the old cover to be reused")
	}
}

func TestResolveMediaUsesRuntimeHeadersWithoutImageArtifact(t *testing.T) {
	manager, calls := newProtocolTestManagerResult(t, map[string]any{
		"url":     "https://cdn.example/video.mp4",
		"headers": map[string]any{"Cookie": "runtime=1", "Referer": "https://source.example/"},
	})
	adapter, err := NewAdapter(manager, model.CustomSource{
		ID: "video", Config: json.RawMessage(`{"runtime":"kototoro-parser","pluginId":"demo","sourceName":"demo","contentType":"VIDEO"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	asset, _ := encodeObject(map[string]any{"id": "page", "source": map[string]any{"name": "demo"}})
	page, err := adapter.ResolveMedia(context.Background(), source.Account{Extra: map[string]any{"useProxy": false}}, model.Page{AssetID: asset})
	if err != nil || page.URL != "https://cdn.example/video.mp4" || page.Headers["Cookie"] != "runtime=1" {
		t.Fatalf("resolved page = %+v: %v", page, err)
	}
	call := <-calls
	if call["operation"] != "media" || call["useProxy"] != false {
		t.Fatalf("wrong runtime operation: %v", call["operation"])
	}
}

func TestKindForContentType(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{value: "MANGA", want: source.KindComic},
		{value: "HENTAI_MANGA", want: source.KindComic},
		{value: "MANHWA", want: source.KindComic},
		{value: "MANHUA", want: source.KindComic},
		{value: "NOVEL", want: source.KindBook},
		{value: "HENTAI_NOVEL", want: source.KindBook},
		{value: "VIDEO", want: source.KindOther},
		{value: "HENTAI_VIDEO", want: source.KindOther},
		{value: "", want: source.KindComic},
	}
	for _, test := range tests {
		if got := KindForContentType(test.value); got != test.want {
			t.Fatalf("KindForContentType(%q) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestSortFilterGroups(t *testing.T) {
	groups := sortFilterGroups(map[string]any{
		"descriptor": map[string]any{
			"sortOrders":       []any{"UPDATED", "POPULARITY", "RATING"},
			"defaultSortOrder": "UPDATED",
		},
	})
	if len(groups) != 1 {
		t.Fatalf("sortFilterGroups returned %d groups, want 1", len(groups))
	}
	group := groups[0]
	if group.Key != "sort" || group.Default != "UPDATED" {
		t.Fatalf("unexpected sort group: %+v", group)
	}
	want := []model.FilterOption{
		{Value: "UPDATED", Label: "最新"},
		{Value: "POPULARITY", Label: "热度"},
		{Value: "RATING", Label: "评分"},
	}
	if len(group.Options) != len(want) {
		t.Fatalf("options = %+v, want %+v", group.Options, want)
	}
	for index := range want {
		if group.Options[index] != want[index] {
			t.Fatalf("option %d = %+v, want %+v", index, group.Options[index], want[index])
		}
	}
}

func TestToComicExtractsLatestChapterTitleWithoutKeepingChapters(t *testing.T) {
	adapter := &Adapter{info: model.SourceInfo{ID: "plugin-source"}}
	comic := adapter.toComic(map[string]any{
		"title":     "示例作品",
		"publicUrl": "https://plugin.example/works/42",
		"chapters": []any{
			map[string]any{"title": "第2话", "number": float64(2)},
			map[string]any{
				"title":      "第10话 最终话",
				"number":     float64(10),
				"uploadDate": float64(1792209600000),
			},
			map[string]any{"title": "第1话", "number": float64(1)},
		},
	})
	if comic.LatestChapterTitle != "第10话 最终话" {
		t.Fatalf("latest chapter = %q", comic.LatestChapterTitle)
	}
	if comic.ChapterCount != 3 {
		t.Fatalf("chapter count = %d, want 3", comic.ChapterCount)
	}
	if comic.UpdatedAt != "2026-10-17T04:00:00Z" {
		t.Fatalf("updated at = %q, want newest chapter timestamp", comic.UpdatedAt)
	}
	if comic.SourceURL != "https://plugin.example/works/42" {
		t.Fatalf("source URL = %q", comic.SourceURL)
	}
	if resolved := adapter.ComicPageURL(comic.ID); resolved != comic.SourceURL {
		t.Fatalf("ComicPageURL() = %q, want %q", resolved, comic.SourceURL)
	}
	identity, err := decodeObject(comic.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := identity["chapters"]; exists {
		t.Fatal("comic ID retained chapter payloads")
	}
}

func TestPluginPublicContentURL(t *testing.T) {
	for _, test := range []struct {
		raw      string
		homepage string
		want     string
	}{
		{"https://plugin.example/work/1", "https://plugin.example/", "https://plugin.example/work/1"},
		{"/work/2", "https://plugin.example/", "https://plugin.example/work/2"},
		{"//cdn.plugin.example/work/3", "https://plugin.example/", "https://cdn.plugin.example/work/3"},
		{"javascript:alert(1)", "https://plugin.example/", ""},
		{"", "https://plugin.example/", ""},
	} {
		if got := pluginPublicContentURL(test.raw, test.homepage); got != test.want {
			t.Fatalf("pluginPublicContentURL(%q) = %q, want %q", test.raw, got, test.want)
		}
	}
}
