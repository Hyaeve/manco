package discovercache

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hyaeve/manco/internal/model"
)

func TestNamedSourceCacheRoundTrip(t *testing.T) {
	coverServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\ncover"))
	}))
	defer coverServer.Close()

	root := t.TempDir()
	cache, err := Open(root, time.Hour)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	detail := model.ComicDetail{
		Comic: model.Comic{
			ID:       "work-1",
			Title:    "测试作品",
			SourceID: "baozimh",
			Cover:    coverServer.URL + "/cover.png",
		},
		Chapters: []model.Chapter{{ID: "ch-1", Title: "第1话"}},
	}
	if err := cache.PutNamed(context.Background(), coverServer.Client(), "baozimh", "包子漫画", "comic", detail); err != nil {
		t.Fatalf("PutNamed() error = %v", err)
	}

	got, ok := cache.Get("baozimh", "comic", "work-1")
	if !ok {
		t.Fatal("Get() did not find a named-source entry")
	}
	if !got.Comic.Cached || strings.TrimSpace(got.Comic.CachedCover) == "" {
		t.Fatalf("Get() cached metadata = %#v", got.Comic)
	}
	if len(got.Chapters) != 1 || got.Chapters[0].ID != "ch-1" {
		t.Fatalf("Get() chapters = %#v", got.Chapters)
	}

	var foundNamedDir bool
	var foundTitleDir bool
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr == nil && entry.IsDir() && entry.Name() == "包子漫画" {
			foundNamedDir = true
		}
		if walkErr == nil && entry.IsDir() && entry.Name() == "测试作品" {
			foundTitleDir = true
		}
		return nil
	})
	if !foundNamedDir {
		t.Fatalf("cache did not preserve the localized source directory under %s", root)
	}
	if !foundTitleDir {
		t.Fatalf("cache did not preserve the localized work-title directory under %s", root)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/discover/cover", nil)
	if !cache.ServeCover(recorder, request, "baozimh", "comic", "work-1") {
		t.Fatal("ServeCover() = false, want true")
	}
	if recorder.Code != http.StatusOK || recorder.Body.Len() == 0 {
		t.Fatalf("ServeCover() status=%d bytes=%d", recorder.Code, recorder.Body.Len())
	}
}
