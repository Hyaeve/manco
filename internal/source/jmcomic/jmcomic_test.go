package jmcomic

import (
	"testing"

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
