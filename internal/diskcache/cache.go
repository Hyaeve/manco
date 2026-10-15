package diskcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultMaxEntries = 256
	defaultMaxBytes   = 64 << 20
	maxEntryBytes     = 4 << 20
	indexVersion      = 1
)

// Cache stores API response payloads on disk so discovery pages survive view
// remounts and process restarts without repeatedly hitting every source.
//
// Only the small index is kept in memory. Response bodies are read on demand,
// which keeps a large discover cache from inflating the Go heap.
type Cache struct {
	mu         sync.Mutex
	dir        string
	indexPath  string
	ttl        time.Duration
	maxEntries int
	maxBytes   int64
	entries    map[string]indexEntry
	totalBytes int64
}

type indexFile struct {
	Version int                   `json:"version"`
	Entries map[string]indexEntry `json:"entries"`
}

type indexEntry struct {
	StoredAt  time.Time `json:"storedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Size      int64     `json:"size"`
}

func Open(dir string, ttl time.Duration) (*Cache, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("response cache directory is empty")
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	dir = filepath.Clean(dir)
	cache := &Cache{
		dir:        dir,
		indexPath:  filepath.Join(dir, "index.json"),
		ttl:        ttl,
		maxEntries: defaultMaxEntries,
		maxBytes:   defaultMaxBytes,
		entries:    map[string]indexEntry{},
	}
	if err := cache.load(); err != nil {
		return nil, err
	}
	return cache, nil
}

func (c *Cache) Get(key string) (json.RawMessage, time.Time, bool) {
	if c == nil {
		return nil, time.Time{}, false
	}
	key = hashKey(key)
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.entries[key]
	if !ok || now.After(item.ExpiresAt) || item.Size <= 0 {
		if ok {
			c.removeLocked(key, item)
			_ = c.persistLocked()
		}
		return nil, time.Time{}, false
	}
	raw, err := os.ReadFile(c.bodyPath(key))
	if err != nil || int64(len(raw)) != item.Size {
		c.removeLocked(key, item)
		_ = c.persistLocked()
		return nil, time.Time{}, false
	}
	return raw, item.StoredAt, true
}

func (c *Cache) Set(key string, body any) error {
	if c == nil || strings.TrimSpace(key) == "" || body == nil {
		return nil
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	if len(raw) == 0 || int64(len(raw)) > maxEntryBytes {
		return nil
	}
	now := time.Now()
	hashed := hashKey(key)
	path := c.bodyPath(hashed)
	temp := path + ".tmp"
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(temp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(now)
	if previous, ok := c.entries[hashed]; ok {
		c.totalBytes -= previous.Size
	}
	c.entries[hashed] = indexEntry{
		StoredAt:  now,
		ExpiresAt: now.Add(c.ttl),
		Size:      int64(len(raw) + 1),
	}
	c.totalBytes += int64(len(raw) + 1)
	c.evictLocked()
	return c.persistLocked()
}

func (c *Cache) Clear() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.entries {
		_ = os.Remove(c.bodyPath(key))
	}
	c.entries = map[string]indexEntry{}
	c.totalBytes = 0
	return c.persistLocked()
}

func (c *Cache) load() error {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	raw, err := os.ReadFile(c.indexPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	var index indexFile
	if err := json.Unmarshal(raw, &index); err != nil {
		return err
	}
	if index.Entries != nil {
		c.entries = index.Entries
	}
	now := time.Now()
	c.pruneLocked(now)
	c.recountLocked()
	c.evictLocked()
	return c.persistLocked()
}

func (c *Cache) pruneLocked(now time.Time) {
	for key, item := range c.entries {
		if now.After(item.ExpiresAt) {
			c.removeLocked(key, item)
		}
	}
}

func (c *Cache) recountLocked() {
	c.totalBytes = 0
	for _, item := range c.entries {
		c.totalBytes += item.Size
	}
}

func (c *Cache) evictLocked() {
	if len(c.entries) <= c.maxEntries && c.totalBytes <= c.maxBytes {
		return
	}
	type candidate struct {
		key   string
		entry indexEntry
	}
	items := make([]candidate, 0, len(c.entries))
	for key, item := range c.entries {
		items = append(items, candidate{key: key, entry: item})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].entry.StoredAt.Before(items[j].entry.StoredAt)
	})
	for _, item := range items {
		if len(c.entries) <= c.maxEntries && c.totalBytes <= c.maxBytes {
			break
		}
		c.removeLocked(item.key, item.entry)
	}
}

func (c *Cache) removeLocked(key string, item indexEntry) {
	delete(c.entries, key)
	c.totalBytes -= item.Size
	if c.totalBytes < 0 {
		c.totalBytes = 0
	}
	_ = os.Remove(c.bodyPath(key))
}

func (c *Cache) persistLocked() error {
	raw, err := json.Marshal(indexFile{Version: indexVersion, Entries: c.entries})
	if err != nil {
		return err
	}
	temp := c.indexPath + ".tmp"
	if err := os.WriteFile(temp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temp, c.indexPath); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func (c *Cache) bodyPath(key string) string {
	return filepath.Join(c.dir, key+".json")
}

func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16])
}
