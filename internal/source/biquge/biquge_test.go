package biquge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
)

func TestInfoIsBuiltInBookSource(t *testing.T) {
	info := New(nil).Info()
	if info.ID != "biquge" || info.Kind != source.KindBook || info.Name != "笔趣阁" {
		t.Fatalf("biquge info = %+v", info)
	}
}

func TestChaptersAndContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/book/123/":
			_, _ = w.Write([]byte(`<html><head>
				<meta property="og:novel:book_name" content="测试小说"/>
				<meta property="og:novel:author" content="测试作者"/>
			</head><body>
				<a href="/chapter/123/2.html">第二章</a>
				<a href="/chapter/123/1.html">第一章</a>
			</body></html>`))
		case "/chapter/123/1.html":
			_, _ = w.Write([]byte(`<div id="txt"><p>第一段</p><p>第二段</p></div>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	item := New(server.Client())
	account := source.Account{HomeURL: server.URL}
	detail, err := item.Detail(context.Background(), account, "123")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if detail.Title != "测试小说" || detail.Author != "测试作者" {
		t.Fatalf("detail = %+v", detail)
	}
	chapters, err := item.Chapters(context.Background(), account, "123")
	if err != nil {
		t.Fatalf("Chapters: %v", err)
	}
	if len(chapters) != 2 || chapters[0].ID != "1" || chapters[1].ID != "2" {
		t.Fatalf("chapters = %+v", chapters)
	}
	content, err := item.ChapterContent(context.Background(), account, "123", model.Chapter{ID: "1", ComicID: "123"})
	if err != nil {
		t.Fatalf("ChapterContent: %v", err)
	}
	if !strings.Contains(content, "第一段") || !strings.Contains(content, "第二段") {
		t.Fatalf("content = %q", content)
	}
}
