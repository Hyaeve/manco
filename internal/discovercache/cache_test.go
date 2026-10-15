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
	if cachedURL := cache.CachedCoverURL("baozimh", "work-1"); cachedURL != got.Comic.CachedCover {
		t.Fatalf("CachedCoverURL() = %q, want %q", cachedURL, got.Comic.CachedCover)
	}
	if len(got.Chapters) != 1 || got.Chapters[0].ID != "ch-1" {
		t.Fatalf("Get() chapters = %#v", got.Chapters)
	}

	var foundNamedDir bool
	var foundTitleDir bool
	var infoPath string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr == nil && entry.IsDir() && entry.Name() == "包子漫画" {
			foundNamedDir = true
		}
		if walkErr == nil && entry.IsDir() && entry.Name() == "测试作品" {
			foundTitleDir = true
		}
		if walkErr == nil && !entry.IsDir() && entry.Name() == "ComicInfo.xml" {
			infoPath = path
		}
		return nil
	})
	if !foundNamedDir {
		t.Fatalf("cache did not preserve the localized source directory under %s", root)
	}
	if !foundTitleDir {
		t.Fatalf("cache did not preserve the localized work-title directory under %s", root)
	}
	if infoPath == "" {
		t.Fatal("cache did not write ComicInfo.xml")
	}
	rawInfo, err := os.ReadFile(infoPath)
	if err != nil {
		t.Fatalf("read ComicInfo.xml: %v", err)
	}
	textInfo := string(rawInfo)
	if strings.Contains(textInfo, "<Source>") {
		t.Fatalf("ComicInfo.xml contains non-v2.1 Source element: %s", textInfo)
	}
	for _, value := range []string{
		"<Notes>Manco source=baozimh</Notes>",
		"<LanguageISO>zh-Hans</LanguageISO>",
		"<Manga>YesAndRightToLeft</Manga>",
	} {
		if !strings.Contains(textInfo, value) {
			t.Fatalf("ComicInfo.xml is missing %q: %s", value, textInfo)
		}
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

func TestPutNamedWithCoverUsesMaterializer(t *testing.T) {
	root := t.TempDir()
	cache, err := Open(root, time.Hour)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	detail := model.ComicDetail{Comic: model.Comic{
		ID:       "plugin-work",
		Title:    "插件作品",
		SourceID: "plugin-source",
		Cover:    "https://invalid.example/cover.jpg",
	}}
	materializer := func(context.Context, model.Comic) ([]byte, string, error) {
		return []byte("plugin-cover"), "image/png", nil
	}
	if err := cache.PutNamedWithCover(context.Background(), http.DefaultClient, "plugin-source", "插件来源", "comic", detail, materializer); err != nil {
		t.Fatalf("PutNamedWithCover() error = %v", err)
	}
	got, ok := cache.Get("plugin-source", "comic", "plugin-work")
	if !ok || !got.Comic.Cached || got.Comic.CachedCover == "" {
		t.Fatalf("materialized cover was not cached: %#v", got.Comic)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/discover/cover", nil)
	if !cache.ServeCover(recorder, request, "plugin-source", "comic", "plugin-work") {
		t.Fatal("ServeCover() = false, want true")
	}
	if got := recorder.Body.String(); got != "plugin-cover" {
		t.Fatalf("ServeCover() body = %q", got)
	}
}
