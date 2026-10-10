// Package iconcache stores source favicons under /data/icon and keeps a small
// index so the UI can use stable local icon URLs.
package iconcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxIconBytes = 5 << 20

type Entry struct {
	ID        string    `json:"id"`
	SourceURL string    `json:"sourceUrl"`
	File      string    `json:"file"`
	UpdatedAt time.Time `json:"updatedAt"`
	Error     string    `json:"error,omitempty"`
}

type Cache struct {
	dir    string
	client *http.Client
	mu     sync.Mutex
	items  map[string]Entry
}

func New(dir string, client *http.Client) (*Cache, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("icon cache directory is empty")
	}
	if client == nil {
		client = http.DefaultClient
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create icon cache: %w", err)
	}
	cache := &Cache{dir: dir, client: client, items: map[string]Entry{}}
	if raw, err := os.ReadFile(filepath.Join(dir, "index.json")); err == nil {
		var items []Entry
		if json.Unmarshal(raw, &items) == nil {
			for _, item := range items {
				cache.items[item.ID] = item
			}
		}
	}
	return cache, nil
}

func (c *Cache) URL(id string) string {
	return "/api/icons/" + url.PathEscape(strings.TrimSpace(id))
}

func (c *Cache) Open(id string) (*os.File, Entry, error) {
	id = strings.TrimSpace(id)
	c.mu.Lock()
	entry, ok := c.items[id]
	c.mu.Unlock()
	if !ok || entry.File == "" {
		return nil, Entry{}, os.ErrNotExist
	}
	file, err := os.Open(filepath.Join(c.dir, entry.File))
	if err != nil {
		return nil, Entry{}, err
	}
	return file, entry, nil
}

func (c *Cache) Fetch(ctx context.Context, id, sourceURL string) (Entry, error) {
	id = strings.TrimSpace(id)
	sourceURL = strings.TrimSpace(sourceURL)
	if id == "" || sourceURL == "" {
		return Entry{}, errors.New("icon id and source URL are required")
	}
	if reader, entry, err := c.Open(id); err == nil {
		_ = reader.Close()
		return entry, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.items[id]; ok && entry.File != "" {
		if file, err := os.Open(filepath.Join(c.dir, entry.File)); err == nil {
			_ = file.Close()
			return entry, nil
		}
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return c.failed(id, sourceURL, err)
	}
	request.Header.Set("User-Agent", "Manco/0.1 (+https://github.com/Hyaeve/manco)")
	response, err := c.client.Do(request)
	if err != nil {
		return c.failed(id, sourceURL, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return c.failed(id, sourceURL, fmt.Errorf("HTTP %d", response.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxIconBytes+1))
	if err != nil {
		return c.failed(id, sourceURL, err)
	}
	if len(data) == 0 || len(data) > maxIconBytes {
		return c.failed(id, sourceURL, errors.New("icon response is empty or too large"))
	}
	extension := iconExtension(sourceURL, response.Header.Get("Content-Type"), data)
	if extension == "" {
		return c.failed(id, sourceURL, errors.New("unsupported icon format"))
	}
	name := safeID(id) + extension
	finalPath := filepath.Join(c.dir, name)
	tempPath := finalPath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o644); err != nil {
		return c.failed(id, sourceURL, err)
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		_ = os.Remove(tempPath)
		return c.failed(id, sourceURL, err)
	}
	entry := Entry{ID: id, SourceURL: sourceURL, File: name, UpdatedAt: time.Now().UTC()}
	c.items[id] = entry
	if err := c.writeIndex(); err != nil {
		return entry, err
	}
	return entry, nil
}

func (c *Cache) failed(id, sourceURL string, err error) (Entry, error) {
	entry := Entry{ID: id, SourceURL: sourceURL, UpdatedAt: time.Now().UTC(), Error: err.Error()}
	c.items[id] = entry
	_ = c.writeIndex()
	return entry, err
}

func (c *Cache) writeIndex() error {
	items := make([]Entry, 0, len(c.items))
	for _, item := range c.items {
		items = append(items, item)
	}
	raw, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(c.dir, "index.json"), append(raw, '\n'), 0o644)
}

func safeID(value string) string {
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			builder.WriteRune(r)
		}
	}
	if builder.Len() == 0 {
		return "icon"
	}
	return builder.String()
}

func iconExtension(sourceURL, contentType string, data []byte) string {
	if mediaType, _, err := mime.ParseMediaType(contentType); err == nil {
		switch strings.ToLower(mediaType) {
		case "image/jpeg":
			return ".jpg"
		case "image/png":
			return ".png"
		case "image/webp":
			return ".webp"
		case "image/x-icon", "image/vnd.microsoft.icon", "image/ico":
			return ".ico"
		}
	}
	switch {
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return ".jpg"
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return ".png"
	case len(data) >= 4 && string(data[:4]) == "RIFF":
		return ".webp"
	case len(data) >= 4 && data[0] == 0 && data[1] == 0 && data[2] == 1 && data[3] == 0:
		return ".ico"
	}
	switch strings.ToLower(filepath.Ext(sourceURL)) {
	case ".jpg", ".jpeg":
		return ".jpg"
	case ".png":
		return ".png"
	case ".webp":
		return ".webp"
	case ".ico":
		return ".ico"
	}
	return ""
}
