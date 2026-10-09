package baozimh

import (
	"testing"

	"github.com/hyaeve/manco/internal/model"
)

func chaptersFromTitles(titles ...string) []model.Chapter {
	chapters := make([]model.Chapter, 0, len(titles))
	for index, title := range titles {
		chapters = append(chapters, model.Chapter{
			ID:    title,
			Title: title,
			Order: float64(index + 1),
		})
	}
	return chapters
}

func titlesOf(chapters []model.Chapter) []string {
	titles := make([]string, 0, len(chapters))
	for _, chapter := range chapters {
		titles = append(titles, chapter.Title)
	}
	return titles
}

func TestOrderChaptersReversesNewestFirstPage(t *testing.T) {
	chapters := chaptersFromTitles("第3话", "第2话", "第1话")
	orderChapters(chapters)

	got := titlesOf(chapters)
	want := []string{"第1话", "第2话", "第3话"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("chapter %d = %q, want %q (all: %v)", index, got[index], want[index], got)
		}
	}
	for index, chapter := range chapters {
		if chapter.Order != float64(index+1) {
			t.Fatalf("chapter %q order = %v, want %v", chapter.Title, chapter.Order, index+1)
		}
	}
}

func TestOrderChaptersKeepsOldestFirstPage(t *testing.T) {
	chapters := chaptersFromTitles("第1话", "第2话", "第3话")
	orderChapters(chapters)

	got := titlesOf(chapters)
	want := []string{"第1话", "第2话", "第3话"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("chapter %d = %q, want %q (all: %v)", index, got[index], want[index], got)
		}
	}
}

func TestOrderChaptersWithoutNumbersAssumesNewestFirst(t *testing.T) {
	chapters := chaptersFromTitles("完结篇", "番外二", "番外一")
	orderChapters(chapters)

	got := titlesOf(chapters)
	want := []string{"番外一", "番外二", "完结篇"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("chapter %d = %q, want %q (all: %v)", index, got[index], want[index], got)
		}
	}
}

func TestChapterNumberReadsDecimalChapters(t *testing.T) {
	number, ok := chapterNumber(model.Chapter{Title: "第 12.5 话 特别篇"})
	if !ok || number != 12.5 {
		t.Fatalf("chapterNumber = %v, %v; want 12.5, true", number, ok)
	}
}
