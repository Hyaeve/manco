// Package discovercache stores visited discover items on disk so covers and
// details can be rendered without repeatedly hitting remote comic sources.
package discovercache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
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

const (
	defaultMaxAge = 30 * 24 * time.Hour
	maxCoverBytes = 20 << 20
)

type Cache struct {
	mu     sync.RWMutex
	root   string
	maxAge time.Duration
}

type storedDetail struct {
	SourceID  string          `json:"sourceId"`
	Kind      string          `json:"kind"`
	Comic     model.Comic     `json:"comic"`
	Chapters  []model.Chapter `json:"chapters,omitempty"`
	CoverFile string          `json:"coverFile,omitempty"`
	UpdatedAt time.Time       `json:"updatedAt"`
	Accessed  time.Time       `json:"accessedAt"`
	Extra     json.RawMessage `json:"extra,omitempty"`
}

type comicInfo struct {
	XMLName   xml.Name `xml:"ComicInfo"`
	Title     string   `xml:"Title,omitempty"`
	Series    string   `xml:"Series,omitempty"`
	Writer    string   `xml:"Writer,omitempty"`
	Summary   string   `xml:"Summary,omitempty"`
	Genre     string   `xml:"Genre,omitempty"`
	Web       string   `xml:"Web,omitempty"`
	Source    string   `xml:"Source,omitempty"`
	PageCount int      `xml:"PageCount,omitempty"`
}

func Open(root string, maxAge time.Duration) (*Cache, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("discover cache directory is empty")
	}
	if maxAge <= 0 {
		maxAge = defaultMaxAge
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	cache := &Cache{root: filepath.Clean(root), maxAge: maxAge}
	if err := cache.Prune(time.Now()); err != nil {
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

func (c *Cache) Get(sourceID, kind, comicID string) (model.ComicDetail, bool) {
	entry, path, ok := c.read(sourceID, kind, comicID)
	if !ok {
		return model.ComicDetail{}, false
	}
	entry.Accessed = time.Now()
	entry.Comic.Cached = true
	if entry.CoverFile != "" {
		if info, err := os.Stat(filepath.Join(path, entry.CoverFile)); err == nil && info.Size() > 0 {
			entry.Comic.CachedCover = coverURL(sourceID, kind, entry.Comic.ID)
		}
	}
	_ = c.writeEntry(path, entry)
	return model.ComicDetail{Comic: entry.Comic, Chapters: entry.Chapters}, true
}

func (c *Cache) Annotate(comic model.Comic) model.Comic {
	if c == nil {
		return comic
	}
	sourceID := strings.TrimSpace(comic.SourceID)
	if sourceID == "" {
		return comic
	}
	kind := NormalizeKind("")
	entry, path, ok := c.read(sourceID, kind, comic.ID)
	if !ok {
		entry, path, ok = c.findBySourceAndID(sourceID, comic.ID)
	}
	if !ok {
		return comic
	}
	entry.Accessed = time.Now()
	if entry.CoverFile != "" {
		if info, err := os.Stat(filepath.Join(path, entry.CoverFile)); err == nil && info.Size() > 0 {
			comic.CachedCover = coverURL(sourceID, entry.Kind, entry.Comic.ID)
			comic.Cached = true
		}
	}
	if comic.Author == "" {
		comic.Author = entry.Comic.Author
	}
	if comic.Description == "" {
		comic.Description = entry.Comic.Description
	}
	if comic.Status == "" {
		comic.Status = entry.Comic.Status
	}
	if len(comic.Tags) == 0 && len(entry.Comic.Tags) > 0 {
		comic.Tags = append([]string(nil), entry.Comic.Tags...)
	}
	if comic.ChapterCount == 0 && len(entry.Chapters) > 0 {
		comic.ChapterCount = len(entry.Chapters)
	}
	_ = c.writeEntry(path, entry)
	return comic
}

func (c *Cache) Put(ctx context.Context, client *http.Client, sourceID, kind string, detail model.ComicDetail) error {
	if c == nil {
		return nil
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		sourceID = strings.TrimSpace(detail.Comic.SourceID)
	}
	if sourceID == "" || strings.TrimSpace(detail.Comic.ID) == "" {
		return errors.New("discover cache requires source and comic id")
	}
	if strings.TrimSpace(kind) == "" {
		kind = NormalizeKind("")
	}
	path := c.entryPath(sourceID, kind, detail.Comic.ID)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	comic := detail.Comic
	coverURL := strings.TrimSpace(comic.Cover)
	coverFile := ""
	if coverURL != "" && client != nil {
		if name, err := c.downloadCover(ctx, client, path, coverURL); err == nil {
			coverFile = name
		}
	}
	if existing, existingPath, ok := c.read(sourceID, kind, comic.ID); ok {
		if coverFile == "" {
			coverFile = existing.CoverFile
		}
		if existingPath != "" {
			path = existingPath
		}
	}
	now := time.Now()
	entry := storedDetail{
		SourceID:  sourceID,
		Kind:      kind,
		Comic:     comic,
		Chapters:  detail.Chapters,
		CoverFile: coverFile,
		UpdatedAt: now,
		Accessed:  now,
	}
	if err := c.writeEntry(path, entry); err != nil {
		return err
	}
	return c.writeMetadata(path, entry)
}

func (c *Cache) ServeCover(w http.ResponseWriter, r *http.Request, sourceID, kind, comicID string) bool {
	entry, path, ok := c.read(sourceID, kind, comicID)
	if !ok || strings.TrimSpace(entry.CoverFile) == "" {
		return false
	}
	coverPath := filepath.Join(path, entry.CoverFile)
	file, err := os.Open(coverPath)
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		return false
	}
	entry.Accessed = time.Now()
	_ = c.writeEntry(path, entry)
	http.ServeContent(w, r, entry.CoverFile, info.ModTime(), file)
	return true
}

func (c *Cache) Prune(now time.Time) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return filepath.WalkDir(c.root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() || entry.Name() != "item.json" {
			return nil
		}
		dir := filepath.Dir(path)
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		var stored storedDetail
		if json.Unmarshal(raw, &stored) != nil {
			return nil
		}
		stamp := stored.Accessed
		if stamp.IsZero() {
			stamp = stored.UpdatedAt
		}
		if stamp.IsZero() {
			if info, statErr := entry.Info(); statErr == nil {
				stamp = info.ModTime()
			}
		}
		if !stamp.IsZero() && now.Sub(stamp) > c.maxAge {
			_ = os.RemoveAll(dir)
		}
		return nil
	})
}

func (c *Cache) read(sourceID, kind, comicID string) (storedDetail, string, bool) {
	if c == nil {
		return storedDetail{}, "", false
	}
	sourceID = strings.TrimSpace(sourceID)
	comicID = strings.TrimSpace(comicID)
	if sourceID == "" || comicID == "" {
		return storedDetail{}, "", false
	}
	kind = NormalizeKind(kind)
	path := c.entryPath(sourceID, kind, comicID)
	entry, err := readEntry(path)
	if err != nil {
		return storedDetail{}, "", false
	}
	return entry, path, true
}

func (c *Cache) findBySourceAndID(sourceID, comicID string) (storedDetail, string, bool) {
	if c == nil {
		return storedDetail{}, "", false
	}
	for _, kind := range []string{"comic", "book", "other"} {
		if entry, path, ok := c.read(sourceID, kind, comicID); ok {
			return entry, path, true
		}
	}
	return storedDetail{}, "", false
}

func (c *Cache) entryPath(sourceID, kind, comicID string) string {
	slug := slugify(comicID)
	if slug == "" {
		slug = "item"
	}
	hash := sha256.Sum256([]byte(sourceID + "\x00" + comicID))
	return filepath.Join(c.root, NormalizeKind(kind), slugify(sourceID), slug+"-"+hex.EncodeToString(hash[:6]))
}

func (c *Cache) downloadCover(ctx context.Context, client *http.Client, dir, address string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg,image/*,*/*;q=0.8")
	request.Header.Set("User-Agent", "Manco/"+time.Now().Format("20060102"))
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("cover HTTP %d", response.StatusCode)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	extension := extensionFor(contentType, address)
	name := "cover" + extension
	target := filepath.Join(dir, name)
	temp := target + ".part"
	file, err := os.Create(temp)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(file, io.LimitReader(response.Body, maxCoverBytes))
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(temp)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(temp)
		return "", closeErr
	}
	if err := os.Rename(temp, target); err != nil {
		_ = os.Remove(temp)
		return "", err
	}
	return name, nil
}

func (c *Cache) writeEntry(path string, entry storedDetail) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	raw, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(path, "item.json"), append(raw, '\n'))
}

func (c *Cache) writeMetadata(path string, entry storedDetail) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	info := comicInfo{
		Title:     entry.Comic.Title,
		Series:    entry.Comic.Title,
		Writer:    entry.Comic.Author,
		Summary:   entry.Comic.Description,
		Genre:     strings.Join(entry.Comic.Tags, ", "),
		Web:       entry.Comic.Cover,
		Source:    entry.SourceID,
		PageCount: len(entry.Chapters),
	}
	raw, err := xml.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	header := []byte(xml.Header)
	return atomicWrite(filepath.Join(path, "ComicInfo.xml"), append(header, raw...))
}

func readEntry(path string) (storedDetail, error) {
	raw, err := os.ReadFile(filepath.Join(path, "item.json"))
	if err != nil {
		return storedDetail{}, err
	}
	var entry storedDetail
	if err := json.Unmarshal(raw, &entry); err != nil {
		return storedDetail{}, err
	}
	if entry.Comic.ID == "" {
		return storedDetail{}, errors.New("invalid discover entry")
	}
	return entry, nil
}

func atomicWrite(path string, raw []byte) error {
	temp := path + ".tmp"
	if err := os.WriteFile(temp, raw, 0o644); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func NormalizeKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "book", "novel":
		return "book"
	case "other", "anime", "video":
		return "other"
	default:
		return "comic"
	}
}

func coverURL(sourceID, kind, comicID string) string {
	values := url.Values{}
	values.Set("sourceId", sourceID)
	values.Set("kind", NormalizeKind(kind))
	values.Set("comicId", comicID)
	return "/api/discover/cover?" + values.Encode()
}

func extensionFor(contentType, address string) string {
	switch contentType {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/avif":
		return ".avif"
	case "image/gif":
		return ".gif"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	}
	path := strings.ToLower(strings.TrimSpace(address))
	if index := strings.IndexAny(path, "?#"); index >= 0 {
		path = path[:index]
	}
	for _, extension := range []string{".png", ".webp", ".avif", ".gif", ".jpg", ".jpeg"} {
		if strings.HasSuffix(path, extension) {
			if extension == ".jpeg" {
				return ".jpg"
			}
			return extension
		}
	}
	return ".jpg"
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, char := range value {
		ok := (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9')
		if ok {
			builder.WriteRune(char)
			lastDash = false
			continue
		}
		if char >= 0x4e00 && char <= 0x9fff {
			builder.WriteRune(char)
			lastDash = false
			continue
		}
		if !lastDash && builder.Len() > 0 {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(builder.String(), "-")
	if len(result) > 72 {
		result = result[:72]
	}
	return result
}

// SortComics keeps cached entries first only when explicitly requested by a
// caller; it is kept here to avoid duplicating stable ordering logic.
func SortComics(items []model.Comic) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Cached != items[j].Cached {
			return items[i].Cached
		}
		return false
	})
}
