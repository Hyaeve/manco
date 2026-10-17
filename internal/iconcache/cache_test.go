package iconcache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func pngBytes() []byte {
	return []byte("\x89PNG\r\n\x1a\n" + "test-icon")
}

func TestSaveListDeleteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cache, err := New(dir, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if cache.Has("jmcomic") {
		t.Fatalf("expected empty cache")
	}
	if _, err := cache.Save("jmcomic", pngBytes(), ""); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !cache.Has("jmcomic") {
		t.Fatalf("expected cached icon")
	}
	if _, err := os.Stat(filepath.Join(dir, "jmcomic.png")); err != nil {
		t.Fatalf("expected icon file on disk: %v", err)
	}
	items := cache.List()
	if len(items) != 1 || items[0].ID != "jmcomic" {
		t.Fatalf("unexpected list: %+v", items)
	}
	if err := cache.Delete("jmcomic"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if cache.Has("jmcomic") {
		t.Fatalf("expected icon removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "jmcomic.png")); !os.IsNotExist(err) {
		t.Fatalf("expected icon file removed, got %v", err)
	}
}

func TestSaveReplacesPreviousExtension(t *testing.T) {
	dir := t.TempDir()
	cache, err := New(dir, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := cache.Save("picacg", []byte("\xff\xd8\xffjpeg"), ".jpg"); err != nil {
		t.Fatalf("Save jpg: %v", err)
	}
	if _, err := cache.Save("picacg", pngBytes(), ".png"); err != nil {
		t.Fatalf("Save png: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "picacg.jpg")); !os.IsNotExist(err) {
		t.Fatalf("expected old jpg removed, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "picacg.png")); err != nil {
		t.Fatalf("expected new png present: %v", err)
	}
}

func TestSaveIndexesSVGIcon(t *testing.T) {
	dir := t.TempDir()
	cache, err := New(dir, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64"/></svg>`)
	entry, err := cache.Save("ext-svg", svg, "")
	if err != nil {
		t.Fatalf("Save SVG: %v", err)
	}
	if entry.File != "ext-svg.svg" {
		t.Fatalf("entry file = %q, want ext-svg.svg", entry.File)
	}
	items := cache.List()
	if len(items) != 1 || items[0].ID != "ext-svg" {
		t.Fatalf("SVG icon missing from library list: %+v", items)
	}
}

func TestFetchFallsBackWhenMissing(t *testing.T) {
	dir := t.TempDir()
	cache, err := New(dir, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := cache.Fetch(context.Background(), "", ""); err == nil {
		t.Fatalf("expected error for empty id/url")
	}
}
