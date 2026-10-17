package baozimh

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
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

func TestBasesPutConfiguredSiteFirst(t *testing.T) {
	got := (&Source{}).bases(source.Account{HomeURL: "https://my-mirror.example/"})
	if len(got) == 0 || got[0] != "https://my-mirror.example" {
		t.Fatalf("first base = %q, want configured mirror", got[0])
	}
	if got[1] != defaultSite {
		t.Fatalf("second base = %q, want default %q", got[1], defaultSite)
	}
}

func TestSearchBasesPreferCnMirror(t *testing.T) {
	got := (&Source{}).searchBases(source.Account{HomeURL: "https://my-mirror.example/"})
	if len(got) == 0 || got[0] != "https://cn.my-mirror.example" {
		t.Fatalf("first search base = %q, want cn mirror", got[0])
	}
	if got[1] != "https://my-mirror.example" {
		t.Fatalf("second search base = %q, want plain mirror", got[1])
	}
	if cnVariant("https://cn.baozimh.com") != "" {
		t.Fatalf("cn host should not be rewritten: %q", cnVariant("https://cn.baozimh.com"))
	}
}

func TestParseComicsReadsSearchCards(t *testing.T) {
	html := `<div class="comics-card">` +
		`<a href="/comic/xinghai-biaoshi"><amp-img src="//static-tw.baozimh.com/cover/xinghai.jpg"></amp-img>` +
		`<div class="comics-card__title"><h3>星海镖师</h3></div></a></div>`
	result := parseComics(html, "https://cn.baozimh.com", 1)
	if len(result.Items) != 1 {
		t.Fatalf("parsed %d items, want 1: %#v", len(result.Items), result.Items)
	}
	item := result.Items[0]
	if item.ID != "xinghai-biaoshi" || item.Title != "星海镖师" {
		t.Fatalf("item = %#v", item)
	}
	if !strings.Contains(item.Cover, "xinghai.jpg") {
		t.Fatalf("cover = %q, want the amp-img source", item.Cover)
	}
}

func TestParseComicsReadsPlainImageAnchors(t *testing.T) {
	html := `<div class="comics-card"><a href="/comic/abc"><img data-src="//static-tw.baozimh.com/cover/abc.jpg"></a><span>作品</span></div>`
	result := parseComics(html, "https://www.baozimh.com", 1)
	if len(result.Items) != 1 || result.Items[0].ID != "abc" {
		t.Fatalf("items = %#v", result.Items)
	}
}

func TestDetailUpdatedAtReadsNovelMetadata(t *testing.T) {
	document, err := goquery.NewDocumentFromReader(strings.NewReader(
		`<html><head><meta property="og:novel:update_time" content="2026-10-17 08:30:00"></head></html>`,
	))
	if err != nil {
		t.Fatalf("parse document: %v", err)
	}
	if got := detailUpdatedAt(document); got != "2026-10-17T08:30:00+08:00" {
		t.Fatalf("detailUpdatedAt = %q", got)
	}
}
