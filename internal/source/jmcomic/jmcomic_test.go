package jmcomic

import (
	"bytes"
	"context"
	"crypto/aes"
	"encoding/base64"
	"encoding/json"
	"image"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "golang.org/x/image/webp"

	"github.com/hyaeve/manco/internal/model"
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
		{name: "base ten", scrambleID: 220980, albumID: 300000, fileName: "00001.jpg", want: 2},
		{name: "segment count varies per file", scrambleID: 220980, albumID: 300000, fileName: "00002.jpg", want: 16},
		{name: "extension is ignored", scrambleID: 220980, albumID: 300000, fileName: "00002", want: 16},
		{name: "base eight above 421926", scrambleID: 220980, albumID: 500000, fileName: "00001.jpg", want: 12},
	}
	for _, item := range cases {
		got := scrambleParts(item.scrambleID, item.albumID, item.fileName)
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

func TestInfoIncludesKototoroFilterGroups(t *testing.T) {
	groups := map[string]model.FilterGroup{}
	for _, group := range New(nil).Info().Filters {
		groups[group.Key] = group
	}
	expected := []struct {
		key    string
		label  string
		labels []string
	}{
		{key: "weekly", label: "每周必看", labels: []string{"最新一期"}},
		{key: "category", label: "分类", labels: []string{"同人", "短篇", "Cosplay", "禁漫汉化组"}},
		{key: "theme", label: "主题A漫", labels: []string{"無修正", "NTR", "禁漫漢化組"}},
		{key: "role", label: "角色扮演", labels: []string{"御姐", "兔女郎"}},
		{key: "play", label: "特殊PLAY", labels: []string{"束縛", "騎大車"}},
		{key: "other", label: "其他", labels: []string{"CG", "站長推薦"}},
		{key: "sort", label: "排序", labels: []string{"最新", "最多观看"}},
	}
	for _, want := range expected {
		group, ok := groups[want.key]
		if !ok {
			t.Fatalf("missing filter group %q", want.key)
		}
		if group.Label != want.label {
			t.Fatalf("group %s label = %q, want %q", want.key, group.Label, want.label)
		}
		labels := map[string]bool{}
		for _, option := range group.Options {
			labels[option.Label] = true
		}
		for _, label := range want.labels {
			if !labels[label] {
				t.Fatalf("group %s missing option %q: %#v", want.key, label, group.Options)
			}
		}
	}
}

func TestBrowseUsesKototoroFilterRoutes(t *testing.T) {
	tests := []struct {
		name      string
		options   model.BrowseOptions
		page      int
		wantPath  string
		wantQuery map[string]string
		weekly    bool
	}{
		{
			name:     "category",
			options:  model.BrowseOptions{Category: "doujin", Sort: "mv"},
			page:     2,
			wantPath: "/categories/filter",
			wantQuery: map[string]string{
				"c": "doujin", "o": "mv", "page": "2",
			},
		},
		{
			name:     "weekly",
			options:  model.BrowseOptions{Sort: "mv", Extra: map[string]string{"weekly": "321"}},
			page:     3,
			wantPath: "/week/filter",
			wantQuery: map[string]string{
				"id": "321", "type": "1", "page": "2",
			},
			weekly: true,
		},
		{
			name: "search tags",
			options: model.BrowseOptions{
				Sort:  "tf",
				Extra: map[string]string{"theme": "s:純愛", "role": "s:御姐"},
			},
			page:     2,
			wantPath: "/search",
			wantQuery: map[string]string{
				"search_query": "純愛 御姐", "o": "tf", "page": "2",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var gotPath string
			var gotQuery url.Values
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotQuery = r.URL.Query()
				payload := map[string]any{"total": 0, "content": []any{}}
				if test.weekly {
					payload = map[string]any{"total": 0, "list": []any{}}
				}
				body, err := jmEncryptedResponse(r, payload)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
			}))
			defer server.Close()

			item := New(server.Client())
			if _, err := item.Browse(
				context.Background(),
				source.Account{HomeURL: server.URL},
				test.options,
				test.page,
			); err != nil {
				t.Fatalf("Browse: %v", err)
			}
			if gotPath != test.wantPath {
				t.Fatalf("path = %q, want %q", gotPath, test.wantPath)
			}
			for key, want := range test.wantQuery {
				if got := gotQuery.Get(key); got != want {
					t.Fatalf("query %s = %q, want %q (all: %s)", key, got, want, gotQuery.Encode())
				}
			}
		})
	}
}

func TestLatestWeekIDUsesNewestNumericPeriod(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, err := jmEncryptedResponse(r, map[string]any{
			"categories": []any{
				map[string]any{"id": "9", "time": "旧期"},
				map[string]any{"id": "11", "time": "新期"},
			},
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()

	item := New(server.Client())
	got, err := item.latestWeekID(context.Background(), source.Account{HomeURL: server.URL})
	if err != nil {
		t.Fatalf("latestWeekID: %v", err)
	}
	if gotPath != "/week" || got != "11" {
		t.Fatalf("latestWeekID path/id = %q/%q, want /week/11", gotPath, got)
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
	weekly, err := item.Browse(ctx, source.Account{}, model.BrowseOptions{
		Sort:  "mv_w",
		Extra: map[string]string{"weekly": "latest"},
	}, 1)
	if err != nil {
		t.Fatalf("Browse weekly: %v", err)
	}
	if len(weekly.Items) == 0 {
		t.Fatal("Weekly browse returned no items")
	}
	tagged, err := item.Browse(ctx, source.Account{}, model.BrowseOptions{
		Sort:  "mr",
		Extra: map[string]string{"theme": "純愛", "role": "御姐"},
	}, 1)
	if err != nil {
		t.Fatalf("Browse tagged: %v", err)
	}
	if len(tagged.Items) == 0 {
		t.Fatal("Tagged browse returned no items")
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

func jmEncryptedResponse(r *http.Request, payload any) ([]byte, error) {
	timestampText := strings.SplitN(r.Header.Get("Tokenparam"), ",", 2)[0]
	timestamp, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil {
		return nil, err
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	encoded, err := encryptAPIDataRaw(timestamp, plain, appDataSecret)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"code": http.StatusOK, "data": encoded})
}

func encryptAPIDataForTest(t *testing.T, timestamp int64, plain []byte, secret string) string {
	t.Helper()
	encoded, err := encryptAPIDataRaw(timestamp, plain, secret)
	if err != nil {
		t.Fatalf("encryptAPIDataRaw: %v", err)
	}
	return encoded
}

func encryptAPIDataRaw(timestamp int64, plain []byte, secret string) (string, error) {
	block, err := aes.NewCipher([]byte(md5Hex(strconv.FormatInt(timestamp, 10) + secret)))
	if err != nil {
		return "", err
	}
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append([]byte(nil), plain...)
	for index := 0; index < padding; index++ {
		padded = append(padded, byte(padding))
	}
	for offset := 0; offset < len(padded); offset += aes.BlockSize {
		block.Encrypt(padded[offset:offset+aes.BlockSize], padded[offset:offset+aes.BlockSize])
	}
	return base64.StdEncoding.EncodeToString(padded), nil
}
