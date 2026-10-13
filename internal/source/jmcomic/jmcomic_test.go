package jmcomic

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"

	"github.com/hyaeve/manco/internal/source"
)

func TestScrambleParts(t *testing.T) {
	cases := []struct {
		name       string
		scrambleID int
		albumID    int
		fileName   string
		want       int
	}{
		{name: "no scramble", scrambleID: 0, albumID: 100, fileName: "001.jpg", want: 0},
		{name: "album below scramble", scrambleID: 220980, albumID: 100, fileName: "001.jpg", want: 0},
		{name: "legacy ten", scrambleID: 220980, albumID: 230000, fileName: "001.jpg", want: 10},
		{name: "modern range", scrambleID: 220980, albumID: 300000, fileName: "001.jpg", want: 0},
	}
	for _, item := range cases {
		got := scrambleParts(item.scrambleID, item.albumID, item.fileName)
		if item.name == "modern range" {
			if got < 2 || got > 20 || got%2 != 0 {
				t.Fatalf("%s: got %d, want even value between 2 and 20", item.name, got)
			}
			continue
		}
		if got != item.want {
			t.Fatalf("%s: got %d, want %d", item.name, got, item.want)
		}
	}
}

func TestPageArray(t *testing.T) {
	html := `<script>var page_arr = ['001.jpg', '002.jpg', '003.jpg']; var scramble_id = 220980;</script>`
	names, ok := pageArray(html)
	if !ok {
		t.Fatal("pageArray did not parse")
	}
	if len(names) != 3 || names[0] != "001.jpg" || names[2] != "003.jpg" {
		t.Fatalf("unexpected names: %#v", names)
	}
}

func TestImageDomains(t *testing.T) {
	html := `<script>var data_original_domain = "https://cdn-msp.18comic.vip";</script>`
	domains := imageDomains(html)
	if len(domains) == 0 || domains[0] != "https://cdn-msp.18comic.vip" {
		t.Fatalf("unexpected domains: %#v", domains)
	}
}

func TestParseSearchReadsVideoCardAndLazyCover(t *testing.T) {
	html := `<div class="video-item">
  <a href="https://18comic.vip/album/123456/" class="img_span">
    <img class="lazy" data-original="//cdn-msp.18comic.vip/media/albums/123456.jpg" src="data:image/gif;base64,R0lGOD">
  </a>
  <div class="video-title">星海镖师</div>
</div>`
	result, err := parseSearch(html, "https://18comic.vip", 1)
	if err != nil {
		t.Fatalf("parseSearch: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("want 1 item, got %#v", result.Items)
	}
	item := result.Items[0]
	if item.ID != "123456" || item.Title != "星海镖师" {
		t.Fatalf("unexpected item: %#v", item)
	}
	if item.Cover != "https://cdn-msp.18comic.vip/media/albums/123456.jpg" {
		t.Fatalf("unexpected cover: %q", item.Cover)
	}
}

func TestParseSearchReadsSrcsetCover(t *testing.T) {
	html := `<div class="thumb">
  <a href="/album/654321"><img srcset="//cdn-msp2.jmapinodeudzn.net/media/albums/654321.jpg 1x, //cdn-msp2.jmapinodeudzn.net/media/albums/654321@2x.jpg 2x"></a>
  <div class="title">示例漫画</div>
</div>`
	result, err := parseSearch(html, "https://18comic.vip", 1)
	if err != nil {
		t.Fatalf("parseSearch: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("want 1 item, got %#v", result.Items)
	}
	item := result.Items[0]
	if item.Title != "示例漫画" {
		t.Fatalf("unexpected title: %q", item.Title)
	}
	if item.Cover != "https://cdn-msp2.jmapinodeudzn.net/media/albums/654321.jpg" {
		t.Fatalf("unexpected cover: %q", item.Cover)
	}
}

func TestImageFromStyleBackground(t *testing.T) {
	html := `<div class="card"><div style="background-image:url('//cdn-msp.18comic.vip/media/albums/9.jpg')"></div></div>`
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("goquery: %v", err)
	}
	cover := imageFrom(document.Find(".card"))
	if cover != "//cdn-msp.18comic.vip/media/albums/9.jpg" {
		t.Fatalf("unexpected background cover: %q", cover)
	}
}

func TestNormalizeComicID(t *testing.T) {
	cases := map[string]string{
		"https://18comic.vip/album/123456": "123456",
		"https://18comic.vip/photo/654321": "654321",
		"654321":                           "654321",
		"654321.html":                      "654321",
		"not-an-id":                        "",
	}
	for input, want := range cases {
		if got := normalizeComicID(input); got != want {
			t.Fatalf("normalizeComicID(%q) = %q, want %q", input, got, want)
		}
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
