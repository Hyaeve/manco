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
	maxCoverBytes = 8 << 20
)

type Cache struct {
	mu     sync.RWMutex
	root   string
	maxAge time.Duration
}

// CoverMaterializer lets a source-specific runtime fetch images that cannot
// be downloaded with a plain HTTP GET, such as signed plugin images.
type CoverMaterializer func(context.Context, model.Comic) ([]byte, string, error)

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
	XMLName     xml.Name `xml:"ComicInfo"`
	Title       string   `xml:"Title,omitempty"`
	Series      string   `xml:"Series,omitempty"`
	Summary     string   `xml:"Summary,omitempty"`
	Notes       string   `xml:"Notes,omitempty"`
	Writer      string   `xml:"Writer,omitempty"`
	Genre       string   `xml:"Genre,omitempty"`
	Tags        string   `xml:"Tags,omitempty"`
	Web         string   `xml:"Web,omitempty"`
	PageCount   int      `xml:"PageCount,omitempty"`
	LanguageISO string   `xml:"LanguageISO,omitempty"`
	Manga       string   `xml:"Manga,omitempty"`
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

// CachedCoverURL returns the API URL for a locally materialized discover
// cover without loading the full cached detail into the caller.
func (c *Cache) CachedCoverURL(sourceID, comicID string) string {
	if c == nil {
		return ""
	}
	entry, path, ok := c.findBySourceAndID(sourceID, comicID)
	if !ok || strings.TrimSpace(entry.CoverFile) == "" {
		return ""
	}
	info, err := os.Stat(filepath.Join(path, entry.CoverFile))
	if err != nil || info.IsDir() || info.Size() == 0 {
		return ""
	}
	id := strings.TrimSpace(entry.Comic.ID)
	if id == "" {
		id = strings.TrimSpace(comicID)
	}
	return coverURL(sourceID, entry.Kind, id)
}

func (c *Cache) Get(sourceID, kind, comicID string) (model.ComicDetail, bool) {
	entry, path, ok := c.read(sourceID, kind, comicID)
	if !ok {
		entry, path, ok = c.findBySourceAndID(sourceID, comicID)
	}
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
	return c.put(ctx, client, sourceID, kind, "", detail)
}

// PutNamed stores an entry under the localized source name. The source ID is
// still used as the stable identity, while the directory remains readable in
// the mounted /data tree.
func (c *Cache) PutNamed(ctx context.Context, client *http.Client, sourceID, sourceName, kind string, detail model.ComicDetail) error {
	return c.put(ctx, client, sourceID, kind, sourceName, detail)
}

// PutNamedWithCover stores an entry and lets the caller materialize the cover
// through the owning source before falling back to a plain HTTP request.
func (c *Cache) PutNamedWithCover(ctx context.Context, client *http.Client, sourceID, sourceName, kind string, detail model.ComicDetail, materialize CoverMaterializer) error {
	return c.put(ctx, client, sourceID, kind, sourceName, detail, materialize)
}

func (c *Cache) put(ctx context.Context, client *http.Client, sourceID, kind, sourceName string, detail model.ComicDetail, materialize ...CoverMaterializer) error {
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
	existing, existingPath, exists := c.findBySourceAndID(sourceID, detail.Comic.ID)
	path := c.entryPathNamed(sourceID, sourceName, kind, detail.Comic.Title, detail.Comic.ID)
	if exists && existingPath != "" {
		if existingPath != path {
			if err := migrateEntryPath(existingPath, path); err != nil {
				path = existingPath
			}
		} else {
			path = existingPath
		}
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	comic := detail.Comic
	coverURL := strings.TrimSpace(comic.Cover)
	coverFile := ""
	if len(materialize) > 0 && materialize[0] != nil {
		if data, contentType, err := materialize[0](ctx, comic); err == nil && len(data) > 0 {
			if name, err := c.writeCover(path, data, contentType, coverURL); err == nil {
				coverFile = name
			}
		}
	}
	if coverFile == "" && coverURL != "" && client != nil {
		if name, err := c.downloadCover(ctx, client, path, coverURL); err == nil {
			coverFile = name
		}
	}
	if coverFile == "" && exists {
		coverFile = existing.CoverFile
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
	if !ok {
		entry, path, ok = c.findBySourceAndID(sourceID, comicID)
	}
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
	var found storedDetail
	var foundPath string
	_ = filepath.WalkDir(c.root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || foundPath != "" || entry.IsDir() || entry.Name() != "item.json" {
			return nil
		}
		candidate, readErr := readEntry(filepath.Dir(path))
		if readErr == nil && candidate.SourceID == sourceID && candidate.Comic.ID == comicID {
			found = candidate
			foundPath = filepath.Dir(path)
		}
		return nil
	})
	if foundPath != "" {
		return found, foundPath, true
	}
	return storedDetail{}, "", false
}

func (c *Cache) findBySourceAndIDLegacy(sourceID, comicID string) (storedDetail, string, bool) {
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

func (c *Cache) entryPathNamed(sourceID, sourceName, kind, comicTitle, comicID string) string {
	slug := slugify(comicTitle)
	if slug == "" {
		slug = slugify(comicID)
	}
	if slug == "" {
		slug = "item"
	}
	sourceDir := slugify(strings.TrimSpace(sourceName))
	if sourceDir == "" {
		sourceDir = slugify(sourceID)
	}
	return c.uniqueEntryPath(filepath.Join(c.root, NormalizeKind(kind), sourceDir, slug), comicID)
}

func (c *Cache) uniqueEntryPath(path, comicID string) string {
	if _, err := os.Stat(filepath.Join(path, "item.json")); err != nil {
		return path
	}
	entry, err := readEntry(path)
	if err == nil && entry.Comic.ID == comicID {
		return path
	}
	hash := sha256.Sum256([]byte(comicID))
	return path + "-" + hex.EncodeToString(hash[:4])
}

func migrateEntryPath(source, target string) error {
	if source == "" || target == "" || source == target {
		return nil
	}
	if _, err := os.Stat(filepath.Join(source, "item.json")); err != nil {
		return err
	}
	if _, err := os.Stat(target); err == nil {
		return errors.New("target discover cache path already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.Rename(source, target)
}

func (c *Cache) entryPath(sourceID, kind, comicID string) string {
	slug := slugify(comicID)
	if slug == "" {
		slug = "item"
	}
	hash := sha256.Sum256([]byte(sourceID + "\x00" + comicID))
	return filepath.Join(c.root, NormalizeKind(kind), slugify(sourceID), slug+"-"+hex.EncodeToString(hash[:6]))
}

func (c *Cache) writeCover(dir string, data []byte, contentType, address string) (string, error) {
	if len(data) == 0 {
		return "", errors.New("cover is empty")
	}
	if len(data) > maxCoverBytes {
		return "", fmt.Errorf("cover exceeds %d bytes", maxCoverBytes)
	}
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	name := "cover" + extensionFor(contentType, address)
	target := filepath.Join(dir, name)
	temp := target + ".part"
	if err := os.WriteFile(temp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(temp, target); err != nil {
		_ = os.Remove(temp)
		return "", err
	}
	return name, nil
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
	manga := "Unknown"
	switch NormalizeKind(entry.Kind) {
	case "book":
		manga = "No"
	case "comic":
		manga = "YesAndRightToLeft"
	}
	info := comicInfo{
		Title:       entry.Comic.Title,
		Series:      entry.Comic.Title,
		Summary:     entry.Comic.Description,
		Notes:       fmt.Sprintf("Manco source=%s", entry.SourceID),
		Writer:      entry.Comic.Author,
		Genre:       strings.Join(entry.Comic.Tags, ", "),
		Tags:        strings.Join(entry.Comic.Tags, ", "),
		Web:         entry.Comic.Cover,
		PageCount:   len(entry.Chapters),
		LanguageISO: "zh-Hans",
		Manga:       manga,
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
