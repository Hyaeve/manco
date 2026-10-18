package biquge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
)

func TestInfoIsBuiltInBookSource(t *testing.T) {
	info := New(nil).Info()
	if info.ID != "biquge" || info.Kind != source.KindBook || info.Name != "笔趣阁" {
		t.Fatalf("biquge info = %+v", info)
	}
	if info.Homepage != defaultSite+"/" {
		t.Fatalf("biquge homepage = %q", info.Homepage)
	}
}

func TestChaptersAndPaginatedContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/123/456/":
			_, _ = w.Write([]byte(`<html><head>
				<meta property="og:novel:book_name" content="测试小说"/>
				<meta property="og:novel:author" content="测试作者"/>
				<meta property="og:image" content="/cover/456.jpg"/>
				<meta property="og:novel:status" content="连载"/>
				<meta property="og:novel:update_time" content="2026-10-17 12:34:56"/>
			</head><body>
				<a href="/123/456/2.html">第二章</a>
				<a href="/123/456/1.html">第一章</a>
				<a href="/123/456/1_2.html">第一章第二页</a>
			</body></html>`))
		case "/123/456/1.html":
			_, _ = w.Write([]byte(`<div id="chaptercontent">
				<p>终极·金笔客提示您：看后求收藏（笔趣阁piquge.com）</p>
				<p>第一段</p>
				<p>第二段</p>
				<div id="morecontent"><p>更多内容加载中</p></div>
			</div><script>const next_page = "/123/456/1_2.html";</script>`))
		case "/123/456/1_2.html":
			_, _ = w.Write([]byte(`<div id="chaptercontent"><p>第三段</p></div>
				<script>const next_page = "/123/456/2.html";</script>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	item := New(server.Client())
	account := source.Account{HomeURL: server.URL}
	detail, err := item.Detail(context.Background(), account, "123/456")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if detail.ID != "123/456" || detail.Title != "测试小说" || detail.Author != "测试作者" {
		t.Fatalf("detail = %+v", detail)
	}
	if detail.UpdatedAt != "2026-10-17T12:34:56+08:00" {
		t.Fatalf("updated at = %q", detail.UpdatedAt)
	}
	if detail.Cover != server.URL+"/cover/456.jpg" {
		t.Fatalf("cover = %q", detail.Cover)
	}
	chapters, err := item.Chapters(context.Background(), account, "123/456")
	if err != nil {
		t.Fatalf("Chapters: %v", err)
	}
	if len(chapters) != 2 || chapters[0].ID != "1" || chapters[1].ID != "2" {
		t.Fatalf("chapters = %+v", chapters)
	}
	content, err := item.ChapterContent(context.Background(), account, "123/456", model.Chapter{ID: "1", ComicID: "123/456"})
	if err != nil {
		t.Fatalf("ChapterContent: %v", err)
	}
	if !strings.Contains(content, "第一段") || !strings.Contains(content, "第二段") || !strings.Contains(content, "第三段") {
		t.Fatalf("content = %q", content)
	}
	if strings.Contains(content, "piquge.com") || strings.Contains(content, "更多内容加载中") {
		t.Fatalf("content contains browser noise: %q", content)
	}
}

func TestSearchAutomaticallyPassesBrowserCheck(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/" {
			http.NotFound(w, r)
			return
		}
		requests++
		if !strings.Contains(r.Header.Get("Cookie"), "__sc_clearance=1") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<html><script>var COOKIE = "__sc_clearance"; location.reload();</script></html>`))
			return
		}
		_, _ = w.Write([]byte(`<div class="ranklist"><div class="content"><dl>
			<a href="/670/670703/" class="cover" title="凡人修仙传之一定要苟">
				<img data-src="/bookcover/670/670703/670703s.jpg">
			</a>
			<dt><a href="/670/670703/" title="凡人修仙传之一定要苟">凡人修仙传之一定要苟</a></dt>
			<dd>简介</dd>
			<dd><a href="#">苦鱼塘</a><span>连载</span><span>修真</span></dd>
		</dl></div></div><a href="?searchkey=凡人修仙传&page=2">下一页</a>`))
	}))
	defer server.Close()

	item := New(server.Client())
	result, err := item.Search(context.Background(), source.Account{HomeURL: server.URL}, "凡人修仙传", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if requests < 2 {
		t.Fatalf("search requests = %d, want browser-check retry", requests)
	}
	if len(result.Items) != 1 || result.Items[0].ID != "670/670703" || result.Items[0].Author != "苦鱼塘" {
		t.Fatalf("search result = %+v", result)
	}
	if !result.HasMore {
		t.Fatalf("expected search result to have another page")
	}
}

func TestParseListReadsFeaturedAndUpdatedBooks(t *testing.T) {
	html := `<div class="ranklist"><div class="content"><dl>
		<a href="/456/456789/" class="cover" title="封面书籍">
			<img data-original="//img.example.com/cover/456.jpg" src="/public/img/nocover.svg">
		</a>
		<dt><a href="/456/456789/">封面书籍</a></dt>
		<dd><a href="#">封面作者</a><span>连载</span></dd>
	</dl></div></div>
	<ul class="update"><li>
		<span>[玄幻]</span>
		<span><a href="/797/797775/" title="更新书籍">更新书籍</a></span>
		<span><a href="/797/797775/3072.html">最新章节</a></span>
		<span><a href="#">更新作者</a></span>
		<span>1天前</span>
	</li></ul>
	<a href="/list1/2/">下一页</a>`
	result := parseList(html, "https://www.piquge.com", 1)
	if len(result.Items) != 2 {
		t.Fatalf("items = %+v", result.Items)
	}
	if result.Items[0].Cover != "https://img.example.com/cover/456.jpg" {
		t.Fatalf("cover = %q", result.Items[0].Cover)
	}
	if result.Items[1].ID != "797/797775" || result.Items[1].Author != "更新作者" {
		t.Fatalf("updated item = %+v", result.Items[1])
	}
	if !result.HasMore {
		t.Fatalf("expected list to have another page")
	}
}

func TestParseSearchReadsBookCard(t *testing.T) {
	html := `<div class="ranklist"><div class="content"><dl>
		<a href="/51/51667/" class="cover" title="凡人修仙传">
			<img data-src="/bookcover/51/51667/51667s.jpg">
		</a>
		<dt><a href="/51/51667/">凡人修仙传</a></dt>
		<dd>简介</dd>
		<dd><a href="#">忘语</a><span>完结</span></dd>
	</dl></div></div>`
	result := parseSearch(html, "https://www.piquge.com", 1)
	if len(result.Items) != 1 {
		t.Fatalf("items = %+v", result.Items)
	}
	if result.Items[0].ID != "51/51667" || result.Items[0].Author != "忘语" {
		t.Fatalf("item = %+v", result.Items[0])
	}
	if result.Items[0].Cover != "https://www.piquge.com/bookcover/51/51667/51667s.jpg" {
		t.Fatalf("cover = %q", result.Items[0].Cover)
	}
}

func TestNormalizeLegacyAndCurrentBookIDs(t *testing.T) {
	cases := map[string]string{
		"123/456":                         "123/456",
		"/123/456/":                       "123/456",
		"https://www.piquge.com/123/456/": "123/456",
		"/123/456/9_2.html":               "123/456",
		"123":                             "",
	}
	for input, want := range cases {
		if got := normalizeBookID(input); got != want {
			t.Fatalf("normalizeBookID(%q) = %q, want %q", input, got, want)
		}
	}
	if got := normalizeChapterID("/123/456/9_2.html"); got != "9" {
		t.Fatalf("normalizeChapterID = %q", got)
	}
}

func TestLivePiqugeSearchBrowseDetailAndContent(t *testing.T) {
	if os.Getenv("MANCO_LIVE_TEST") != "1" {
		t.Skip("set MANCO_LIVE_TEST=1 to run live 笔趣阁 checks")
	}
	item := New(&http.Client{Timeout: 20 * time.Second})
	account := source.Account{}
	search, err := item.Search(context.Background(), account, "凡人修仙传", 1)
	if err != nil {
		t.Fatalf("live Search: %v", err)
	}
	if len(search.Items) == 0 {
		t.Fatalf("live Search returned no items")
	}
	browse, err := item.Browse(context.Background(), account, model.BrowseOptions{Category: "1"}, 1)
	if err != nil {
		t.Fatalf("live Browse: %v", err)
	}
	if len(browse.Items) == 0 {
		t.Fatalf("live Browse returned no items")
	}
	detail, err := item.Detail(context.Background(), account, search.Items[0].ID)
	if err != nil {
		t.Fatalf("live Detail: %v", err)
	}
	chapters, err := item.Chapters(context.Background(), account, detail.ID)
	if err != nil {
		t.Fatalf("live Chapters: %v", err)
	}
	if len(chapters) == 0 {
		t.Fatalf("live Chapters returned no items")
	}
	content, err := item.ChapterContent(context.Background(), account, detail.ID, chapters[0])
	if err != nil {
		t.Fatalf("live ChapterContent: %v", err)
	}
	if strings.TrimSpace(content) == "" {
		t.Fatalf("live ChapterContent returned empty text")
	}
}
