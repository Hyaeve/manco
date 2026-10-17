// Package subscriptioncover stores a dedicated subscription-cover index and
// its cover files under the persistent data directory.
package subscriptioncover

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hyaeve/manco/internal/model"
)

const maxCoverBytes = 8 << 20

type Entry struct {
	SourceID    string    `json:"sourceId"`
	ComicID     string    `json:"comicId"`
	Title       string    `json:"title,omitempty"`
	CoverFile   string    `json:"coverFile,omitempty"`
	CoverBlur   bool      `json:"coverBlur"`
	SourceFile  string    `json:"sourceFile,omitempty"`
	SourceMod   int64     `json:"sourceMod,omitempty"`
	SourceSize  int64     `json:"sourceSize,omitempty"`
	SourceCover string    `json:"sourceCover,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type document struct {
	Version int     `json:"version"`
	Items   []Entry `json:"items"`
}

type Cache struct {
	mu        sync.Mutex
	root      string
	coversDir string
	entries   map[string]Entry
	dirty     bool
}

type Materializer func(context.Context, model.Comic) ([]byte, string, error)

func Open(root string) (*Cache, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("subscription cover directory is empty")
	}
	cache := &Cache{
		root:      filepath.Clean(root),
		coversDir: filepath.Join(filepath.Clean(root), "covers"),
		entries:   map[string]Entry{},
	}
	if err := os.MkdirAll(cache.coversDir, 0o755); err != nil {
		return nil, err
	}
	if err := cache.load(); err != nil {
		return nil, err
	}
	return cache, nil
}

func (c *Cache) Root() string {
	if c == nil {
		return ""
	}
	return c.root
}

func key(sourceID, comicID string) string {
	return strings.TrimSpace(sourceID) + "\x00" + strings.TrimSpace(comicID)
}

func (c *Cache) load() error {
	raw, err := os.ReadFile(filepath.Join(c.root, "index.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var payload document
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("parse subscription cover index: %w", err)
	}
	for _, entry := range payload.Items {
		if entry.SourceID == "" || entry.ComicID == "" {
			continue
		}
		c.entries[key(entry.SourceID, entry.ComicID)] = entry
	}
	return nil
}

// Update records subscription metadata and copies a downloaded work cover into
// the persistent subscription cover index when one exists.
func (c *Cache) Update(subscription model.Subscription, localFile string) (string, error) {
	if c == nil || subscription.SourceID == "" || subscription.ComicID == "" {
		return "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cacheKey := key(subscription.SourceID, subscription.ComicID)
	entry := c.entries[cacheKey]
	entry.SourceID = subscription.SourceID
	entry.ComicID = subscription.ComicID
	entry.Title = strings.TrimSpace(subscription.Title)
	entry.CoverBlur = subscription.CoverBlur
	entry.SourceCover = strings.TrimSpace(subscription.Cover)
	entry.UpdatedAt = time.Now()

	if localFile != "" {
		if info, err := os.Stat(localFile); err == nil && !info.IsDir() && info.Size() > 0 {
			if entry.SourceFile != localFile || entry.SourceMod != info.ModTime().UnixNano() || entry.SourceSize != info.Size() || !c.coverExistsLocked(entry) {
				name := c.coverName(subscription.SourceID, subscription.ComicID, filepath.Ext(localFile))
				if err := copyFileAtomic(localFile, filepath.Join(c.coversDir, name)); err != nil {
					return "", err
				}
				entry.CoverFile = filepath.ToSlash(filepath.Join("covers", name))
			}
			entry.SourceFile = localFile
			entry.SourceMod = info.ModTime().UnixNano()
			entry.SourceSize = info.Size()
		}
	}
	c.entries[cacheKey] = entry
	c.dirty = true
	return c.coverURLForEntry(entry), nil
}

// Materialize downloads a cover through the source runtime or HTTP and stores
// it in the dedicated data directory. Existing files are reused.
func (c *Cache) Materialize(ctx context.Context, client *http.Client, subscription model.Subscription, materialize Materializer) (string, error) {
	if c == nil || subscription.SourceID == "" || subscription.ComicID == "" {
		return "", nil
	}
	cacheKey := key(subscription.SourceID, subscription.ComicID)
	c.mu.Lock()
	entry := c.entries[cacheKey]
	if c.coverExistsLocked(entry) {
		url := c.coverURLForEntry(entry)
		c.mu.Unlock()
		return url, nil
	}
	c.mu.Unlock()

	var (
		data        []byte
		contentType string
		err         error
	)
	if materialize != nil {
		data, contentType, err = materialize(ctx, model.Comic{
			ID:       subscription.ComicID,
			SourceID: subscription.SourceID,
			Title:    subscription.Title,
			Cover:    subscription.Cover,
		})
	}
	if len(data) == 0 {
		downloaded, downloadedType, downloadErr := downloadCover(ctx, client, subscription.Cover)
		if downloadErr == nil {
			data, contentType, err = downloaded, downloadedType, nil
		} else if err == nil {
			err = downloadErr
		}
	}
	if err != nil {
		return "", err
	}
	if len(data) == 0 || len(data) > maxCoverBytes {
		return "", errors.New("invalid subscription cover data")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	entry = c.entries[cacheKey]
	if c.coverExistsLocked(entry) {
		return c.coverURLForEntry(entry), nil
	}
	extension := extensionFor(contentType, subscription.Cover)
	name := c.coverName(subscription.SourceID, subscription.ComicID, extension)
	if err := writeFileAtomic(filepath.Join(c.coversDir, name), data); err != nil {
		return "", err
	}
	entry.SourceID = subscription.SourceID
	entry.ComicID = subscription.ComicID
	entry.Title = strings.TrimSpace(subscription.Title)
	entry.CoverFile = filepath.ToSlash(filepath.Join("covers", name))
	entry.CoverBlur = subscription.CoverBlur
	entry.SourceCover = strings.TrimSpace(subscription.Cover)
	entry.UpdatedAt = time.Now()
	c.entries[cacheKey] = entry
	c.dirty = true
	return c.coverURLForEntry(entry), nil
}

func (c *Cache) Lookup(sourceID, comicID string) (Entry, bool) {
	if c == nil {
		return Entry{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key(sourceID, comicID)]
	if !ok || !c.coverExistsLocked(entry) {
		return entry, false
	}
	return entry, true
}

func (c *Cache) OpenCover(sourceID, comicID string) (*os.File, error) {
	if c == nil {
		return nil, errors.New("subscription cover cache is unavailable")
	}
	c.mu.Lock()
	entry, ok := c.entries[key(sourceID, comicID)]
	c.mu.Unlock()
	if !ok || strings.TrimSpace(entry.CoverFile) == "" {
		return nil, os.ErrNotExist
	}
	filePath, err := c.safeCoverPath(entry.CoverFile)
	if err != nil {
		return nil, err
	}
	return os.Open(filePath)
}

func (c *Cache) Flush() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dirty {
		return nil
	}
	items := make([]Entry, 0, len(c.entries))
	for _, entry := range c.entries {
		if c.coverExistsLocked(entry) {
			items = append(items, entry)
		} else if entry.SourceID != "" && entry.ComicID != "" {
			items = append(items, entry)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].SourceID == items[j].SourceID {
			return items[i].ComicID < items[j].ComicID
		}
		return items[i].SourceID < items[j].SourceID
	})
	payload, err := json.MarshalIndent(document{Version: 1, Items: items}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(c.root, "index.json"), append(payload, '\n')); err != nil {
		return err
	}
	c.dirty = false
	return nil
}

func (c *Cache) coverName(sourceID, comicID, extension string) string {
	sum := sha256.Sum256([]byte(key(sourceID, comicID)))
	extension = strings.ToLower(strings.TrimSpace(extension))
	if extension == "" {
		extension = ".jpg"
	}
	if !strings.HasPrefix(extension, ".") {
		extension = "." + extension
	}
	if len(extension) > 8 {
		extension = ".jpg"
	}
	return hex.EncodeToString(sum[:16]) + extension
}

func (c *Cache) coverURLForEntry(entry Entry) string {
	if !c.coverExistsLocked(entry) {
		return ""
	}
	values := url.Values{}
	values.Set("sourceId", entry.SourceID)
	values.Set("comicId", entry.ComicID)
	return "/api/subscription-cover?" + values.Encode()
}

func (c *Cache) coverExistsLocked(entry Entry) bool {
	if strings.TrimSpace(entry.CoverFile) == "" {
		return false
	}
	filePath, err := c.safeCoverPath(entry.CoverFile)
	if err != nil {
		return false
	}
	info, err := os.Stat(filePath)
	return err == nil && !info.IsDir() && info.Size() > 0
}

func (c *Cache) safeCoverPath(relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", errors.New("subscription cover path must be relative")
	}
	filePath := filepath.Join(c.root, filepath.FromSlash(relative))
	relativeToRoot, err := filepath.Rel(c.root, filePath)
	if err != nil || relativeToRoot == ".." || strings.HasPrefix(relativeToRoot, ".."+string(os.PathSeparator)) {
		return "", errors.New("subscription cover path escapes cache root")
	}
	return filePath, nil
}

func downloadCover(ctx context.Context, client *http.Client, address string) ([]byte, string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, "", errors.New("subscription cover URL is empty")
	}
	if client == nil {
		client = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("subscription cover HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxCoverBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxCoverBytes {
		return nil, "", errors.New("subscription cover exceeds size limit")
	}
	return data, response.Header.Get("Content-Type"), nil
}

func extensionFor(contentType, address string) string {
	if mediaType, _, err := mime.ParseMediaType(contentType); err == nil {
		switch mediaType {
		case "image/jpeg":
			return ".jpg"
		case "image/png":
			return ".png"
		case "image/webp":
			return ".webp"
		case "image/gif":
			return ".gif"
		}
	}
	if parsed, err := url.Parse(address); err == nil {
		switch strings.ToLower(filepath.Ext(parsed.Path)) {
		case ".jpg", ".jpeg":
			return ".jpg"
		case ".png":
			return ".png"
		case ".webp":
			return ".webp"
		case ".gif":
			return ".gif"
		}
	}
	return ".jpg"
}

func copyFileAtomic(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	temp := target + ".tmp"
	output, err := os.OpenFile(temp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(output, io.LimitReader(input, maxCoverBytes+1))
	closeErr := output.Close()
	if copyErr != nil {
		_ = os.Remove(temp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(temp)
		return closeErr
	}
	if written > maxCoverBytes {
		_ = os.Remove(temp)
		return errors.New("subscription cover exceeds size limit")
	}
	if err := os.Rename(temp, target); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func writeFileAtomic(target string, data []byte) error {
	temp := target + ".tmp"
	if err := os.WriteFile(temp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(temp, target); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}
