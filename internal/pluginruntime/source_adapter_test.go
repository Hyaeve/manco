package pluginruntime

import (
	"testing"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
)

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
		"title": "示例作品",
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
}
