package diskcache

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestCachePersistsWithoutKeepingBodiesInMemory(t *testing.T) {
	root := t.TempDir()
	cache, err := Open(root, time.Hour)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	body := map[string]any{"items": []string{"one", "two"}}
	if err := cache.Set("browse:comic:1", body); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if len(cache.entries) != 1 {
		t.Fatalf("in-memory entries = %d, want 1", len(cache.entries))
	}
	for key, entry := range cache.entries {
		if entry.Size <= 0 {
			t.Fatalf("entry %s has invalid size: %#v", key, entry)
		}
	}

	raw, storedAt, ok := cache.Get("browse:comic:1")
	if !ok || storedAt.IsZero() {
		t.Fatal("Get() did not return the stored value")
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("cached body is not JSON: %v", err)
	}
	if len(got["items"].([]any)) != 2 {
		t.Fatalf("cached body = %#v", got)
	}

	reopened, err := Open(root, time.Hour)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	if _, _, ok := reopened.Get("browse:comic:1"); !ok {
		t.Fatal("reopened cache did not load the disk index")
	}
}

func TestCacheEvictsOldestEntries(t *testing.T) {
	root := t.TempDir()
	cache, err := Open(root, time.Hour)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	cache.maxEntries = 2
	for _, key := range []string{"one", "two", "three"} {
		if err := cache.Set(key, map[string]string{"key": key}); err != nil {
			t.Fatalf("Set(%q) error = %v", key, err)
		}
		time.Sleep(time.Millisecond)
	}
	if len(cache.entries) != 2 {
		t.Fatalf("entries after eviction = %d, want 2", len(cache.entries))
	}
	if _, _, ok := cache.Get("one"); ok {
		t.Fatal("oldest entry was not evicted")
	}
	if _, _, ok := cache.Get("three"); !ok {
		t.Fatal("newest entry was evicted")
	}
}

func TestCacheSkipsOversizedBodies(t *testing.T) {
	cache, err := Open(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	large := make([]byte, maxEntryBytes+1)
	if err := cache.Set("large", large); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if len(cache.entries) != 0 {
		t.Fatalf("oversized body was cached: %#v", cache.entries)
	}
	if _, err := os.Stat(cache.dir); err != nil {
		t.Fatalf("cache directory was not created: %v", err)
	}
}
