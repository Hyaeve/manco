package subscriptioncover

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hyaeve/manco/internal/model"
)

func TestUpdateCopiesLocalCoverAndPersistsIndex(t *testing.T) {
	root := t.TempDir()
	localCover := filepath.Join(root, "downloaded-cover.png")
	if err := os.WriteFile(localCover, []byte("local-cover-data"), 0o644); err != nil {
		t.Fatalf("write local cover: %v", err)
	}
	cache, err := Open(filepath.Join(root, "subscription-covers"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	subscription := model.Subscription{
		SourceID:  "source",
		ComicID:   "comic",
		Title:     "作品",
		Cover:     "https://example.test/remote.png",
		CoverBlur: true,
	}
	coverURL, err := cache.Update(subscription, localCover)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !strings.HasPrefix(coverURL, "/api/subscription-cover?") {
		t.Fatalf("cover URL = %q", coverURL)
	}
	entry, ok := cache.Lookup("source", "comic")
	if !ok || !entry.CoverBlur || entry.CoverFile == "" {
		t.Fatalf("entry = %+v ok=%v", entry, ok)
	}
	if err := cache.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "subscription-covers", "index.json")); err != nil {
		t.Fatalf("index file: %v", err)
	}
	reopened, err := Open(filepath.Join(root, "subscription-covers"))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, ok := reopened.Lookup("source", "comic"); !ok {
		t.Fatal("cover index did not survive reopen")
	}
}

func TestMaterializeDownloadsCoverIntoDataIndex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("remote-cover-data"))
	}))
	defer server.Close()
	cache, err := Open(filepath.Join(t.TempDir(), "subscription-covers"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	subscription := model.Subscription{
		SourceID: "source",
		ComicID:  "comic",
		Title:    "作品",
		Cover:    server.URL + "/cover.jpg",
	}
	coverURL, err := cache.Materialize(context.Background(), server.Client(), subscription, nil)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if coverURL == "" {
		t.Fatal("materialized cover URL is empty")
	}
	file, err := cache.OpenCover("source", "comic")
	if err != nil {
		t.Fatalf("OpenCover: %v", err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read cover: %v", err)
	}
	if string(data) != "remote-cover-data" {
		t.Fatalf("cover data = %q", data)
	}
}
