package zlibrary

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
)

func TestLoginUsesEAPIAndEncodesSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != loginPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, loginPath)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("email") != "reader@example.com" || r.Form.Get("password") != "secret" {
			t.Fatalf("unexpected login form: %#v", r.Form)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"user": map[string]any{
				"id":            "42",
				"email":         "reader@example.com",
				"remix_userkey": "key-42",
			},
		})
	}))
	defer server.Close()

	result, err := New(server.Client()).Login(t.Context(), source.Account{
		HomeURL:  server.URL,
		Username: "reader@example.com",
		Password: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionValue, err := decodeSession(result.Token)
	if err != nil {
		t.Fatal(err)
	}
	if sessionValue.UserID != "42" || sessionValue.UserKey != "key-42" {
		t.Fatalf("session = %#v", sessionValue)
	}
	if result.Username != "reader@example.com" {
		t.Fatalf("username = %q", result.Username)
	}
}

func TestSearchParsesBooks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != searchPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, searchPath)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"books": []any{
				map[string]any{
					"id":        "123",
					"hash":      "abc",
					"name":      "测试电子书",
					"author":    "测试作者",
					"cover":     "/cover/123.jpg",
					"extension": "epub",
				},
			},
			"pagination": map[string]any{"total": 1, "pages": 1},
		})
	}))
	defer server.Close()

	token, err := encodeSession("42", "key-42")
	if err != nil {
		t.Fatal(err)
	}
	result, err := New(server.Client()).Search(t.Context(), source.Account{HomeURL: server.URL, Token: token}, "测试", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(result.Items))
	}
	item := result.Items[0]
	if item.ID != "123~abc" || item.Title != "测试电子书" || item.Author != "测试作者" {
		t.Fatalf("item = %#v", item)
	}
	if item.Cover != server.URL+"/cover/123.jpg" {
		t.Fatalf("cover = %q", item.Cover)
	}
	if len(item.Tags) != 1 || item.Tags[0] != "epub" {
		t.Fatalf("tags = %#v", item.Tags)
	}
}

func TestChapterFileDownloadsOriginalExtension(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eapi/book/123/abc/file":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"file": map[string]any{
					"description":  "Test Book",
					"author":       "Test Author",
					"extension":    "epub",
					"downloadLink": "/download/book",
				},
			})
		case "/download/book":
			_, _ = w.Write([]byte("epub-bytes"))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	token, err := encodeSession("42", "key-42")
	if err != nil {
		t.Fatal(err)
	}
	data, filename, err := New(server.Client()).ChapterFile(t.Context(), source.Account{HomeURL: server.URL, Token: token}, "123~abc", modelChapter())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "epub-bytes" {
		t.Fatalf("data = %q", data)
	}
	if !strings.HasSuffix(filename, ".epub") || !strings.Contains(filename, "Test Author") {
		t.Fatalf("filename = %q", filename)
	}
}

func TestDetailWithChaptersUsesSingleDetailRequest(t *testing.T) {
	var detailRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/eapi/book/123/abc" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		detailRequests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"book": map[string]any{
				"id":          "123",
				"hash":        "abc",
				"name":        "测试电子书",
				"author":      "测试作者",
				"description": "简介",
				"extension":   "epub",
			},
		})
	}))
	defer server.Close()

	token, err := encodeSession("42", "key-42")
	if err != nil {
		t.Fatal(err)
	}
	detail, err := New(server.Client()).DetailWithChapters(
		t.Context(),
		source.Account{HomeURL: server.URL, Token: token},
		"123~abc",
	)
	if err != nil {
		t.Fatal(err)
	}
	if detailRequests.Load() != 1 {
		t.Fatalf("detail requests = %d, want 1", detailRequests.Load())
	}
	if len(detail.Chapters) != 1 || detail.Chapters[0].ID != "123~abc" || detail.Chapters[0].Title != "测试电子书" {
		t.Fatalf("chapters = %#v", detail.Chapters)
	}
}

func modelChapter() model.Chapter {
	return model.Chapter{
		ID:      "123~abc",
		ComicID: "123~abc",
		Title:   "Test Book",
		Order:   1,
	}
}
