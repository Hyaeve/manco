package diskcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Cache stores API response payloads on disk so discovery pages survive view
// remounts and process restarts without repeatedly hitting every source.
type Cache struct {
	mu      sync.Mutex
	path    string
	ttl     time.Duration
	entries map[string]entry
}

type entry struct {
	Body      json.RawMessage `json:"body"`
	StoredAt  time.Time       `json:"storedAt"`
	ExpiresAt time.Time       `json:"expiresAt"`
}

func Open(path string, ttl time.Duration) (*Cache, error) {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	cache := &Cache{path: path, ttl: ttl, entries: map[string]entry{}}
	if err := cache.load(); err != nil {
		return nil, err
	}
	return cache, nil
}

func (c *Cache) Get(key string) (json.RawMessage, time.Time, bool) {
	if c == nil {
		return nil, time.Time{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.entries[hashKey(key)]
	if !ok || time.Now().After(item.ExpiresAt) || len(item.Body) == 0 {
		return nil, time.Time{}, false
	}
	return append(json.RawMessage(nil), item.Body...), item.StoredAt, true
}

func (c *Cache) Set(key string, body any) error {
	if c == nil || len(key) == 0 || body == nil {
		return nil
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	now := time.Now()
	c.mu.Lock()
	c.entries[hashKey(key)] = entry{Body: raw, StoredAt: now, ExpiresAt: now.Add(c.ttl)}
	c.pruneLocked(now)
	err = c.persistLocked()
	c.mu.Unlock()
	return err
}

func (c *Cache) Clear() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]entry{}
	return c.persistLocked()
}

func (c *Cache) load() error {
	raw, err := os.ReadFile(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	var entries map[string]entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return err
	}
	if entries != nil {
		c.entries = entries
	}
	c.pruneLocked(time.Now())
	return nil
}

func (c *Cache) pruneLocked(now time.Time) {
	for key, item := range c.entries {
		if now.After(item.ExpiresAt) {
			delete(c.entries, key)
		}
	}
}

func (c *Cache) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(c.entries)
	if err != nil {
		return err
	}
	temp := c.path + ".tmp"
	if err := os.WriteFile(temp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temp, c.path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16])
}
