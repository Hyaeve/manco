package jmcomic

import (
	"bytes"
	"context"
	"crypto/aes"
	"encoding/base64"
	"image"
	"io"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	_ "golang.org/x/image/webp"

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

func TestDecryptAPIData(t *testing.T) {
	const timestamp = int64(1760000000)
	plain := []byte(`{"total":1,"content":[{"id":"1479652","name":"示例漫画"}]}`)
	encoded := encryptAPIDataForTest(t, timestamp, plain, appDataSecret)
	got, err := decryptAPIData(timestamp, encoded, appDataSecret)
	if err != nil {
		t.Fatalf("decryptAPIData: %v", err)
	}
	if string(got) != string(plain) {
		t.Fatalf("decrypted payload = %s, want %s", got, plain)
	}
}

func TestParseSearchResultUsesAlbumCoverAndCategoryTags(t *testing.T) {
	payload := []byte(`{
		"total": 81,
		"content": [{
			"id": "1479652",
			"name": "示例漫画",
			"author": "作者A",
			"adddate": "2026-10-06",
			"category": {"id": "1", "title": "同人"},
			"category_sub": {"id": "3", "title": "短篇"}
		}]
	}`)
	var envelope apiSearchPage
	if err := decodeJSON(payload, &envelope); err != nil {
		t.Fatalf("decodeJSON: %v", err)
	}
	result, err := parseSearchResult(envelope, 1)
	if err != nil {
		t.Fatalf("parseSearchResult: %v", err)
	}
	if !result.HasMore || result.Pages != 2 || result.Total != 81 {
		t.Fatalf("unexpected pagination: %#v", result)
	}
	if len(result.Items) != 1 {
		t.Fatalf("want 1 item, got %#v", result.Items)
	}
	item := result.Items[0]
	if item.ID != "1479652" || item.Title != "示例漫画" || item.Author != "作者A" {
		t.Fatalf("unexpected item: %#v", item)
	}
	if item.Cover != "https://cdn-msp.jmapiproxy1.cc/media/albums/1479652.jpg" {
		t.Fatalf("unexpected cover: %q", item.Cover)
	}
	if len(item.Tags) != 2 || item.Tags[0] != "同人" || item.Tags[1] != "短篇" {
		t.Fatalf("unexpected tags: %#v", item.Tags)
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

func TestNormalizeBrowseValues(t *testing.T) {
	if got := normalizeCategory(""); got != "0" {
		t.Fatalf("normalizeCategory(empty) = %q, want 0", got)
	}
	if got := normalizeCategory("another_cosplay"); got != "doujin_cosplay" {
		t.Fatalf("normalizeCategory(another_cosplay) = %q", got)
	}
	if got := normalizeSort(""); got != "mr" {
		t.Fatalf("normalizeSort(empty) = %q, want mr", got)
	}
	if got := normalizeSort("weekly"); got != "mv_w" {
		t.Fatalf("normalizeSort(weekly) = %q, want mv_w", got)
	}
}

func TestLiveJMAPI(t *testing.T) {
	if os.Getenv("MANCO_LIVE_JM") == "" {
		t.Skip("set MANCO_LIVE_JM=1 to run the live 18comic API test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	item := New(source.NewHTTPClient())
	result, err := item.Search(ctx, source.Account{}, "海贼王", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result.Items) == 0 {
		t.Fatal("Search returned no items")
	}
	comic := result.Items[0]
	if comic.Cover == "" {
		t.Fatal("Search item has no cover")
	}
	detail, err := item.Detail(ctx, source.Account{}, comic.ID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if detail.Title == "" {
		t.Fatal("Detail returned no title")
	}
	chapters, err := item.Chapters(ctx, source.Account{}, comic.ID)
	if err != nil {
		t.Fatalf("Chapters: %v", err)
	}
	if len(chapters) == 0 {
		t.Fatal("Chapters returned no items")
	}
	pages, err := item.Pages(ctx, source.Account{}, comic.ID, chapters[0])
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) == 0 || pages[0].URL == "" {
		t.Fatalf("Pages returned invalid data: %#v", pages)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, pages[0].URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	request.Header.Set("User-Agent", appUserAgent)
	if pages[0].Referer != "" {
		request.Header.Set("Referer", pages[0].Referer)
	}
	for key, value := range pages[0].Headers {
		request.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("page image request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("page image HTTP %d", response.StatusCode)
	}
	imageData, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		t.Fatalf("read page image: %v", err)
	}
	if _, _, err := image.Decode(bytes.NewReader(imageData)); err != nil {
		t.Fatalf("decode page image: %v", err)
	}
}

func encryptAPIDataForTest(t *testing.T, timestamp int64, plain []byte, secret string) string {
	t.Helper()
	block, err := aes.NewCipher([]byte(md5Hex(strconv.FormatInt(timestamp, 10) + secret)))
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append([]byte(nil), plain...)
	for index := 0; index < padding; index++ {
		padded = append(padded, byte(padding))
	}
	for offset := 0; offset < len(padded); offset += aes.BlockSize {
		block.Encrypt(padded[offset:offset+aes.BlockSize], padded[offset:offset+aes.BlockSize])
	}
	return base64.StdEncoding.EncodeToString(padded)
}
