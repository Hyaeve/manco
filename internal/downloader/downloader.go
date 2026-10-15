// Package downloader turns queued chapter jobs into one CBZ archive per chapter.
// Images are always written to a temporary directory first and are only moved
// into the library after the archive is complete, so a crash never leaves a
// half-written .cbz in the download directory.
package downloader

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/liuzl/gocc"
	"golang.org/x/image/webp"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/sources"
)

type Engine struct {
	registry              *sources.Registry
	store                 Jobs
	downloadDir           string
	cacheDir              string
	maxChapterConcurrency int
	maxPageConcurrency    int
	logger                *log.Logger
	sourceConcurrency     map[string]int
	sourcePageConcurrency map[string]int
	sourceBatchSize       map[string]int
	sourceBatchInterval   map[string]time.Duration
	sourceBatchCompleted  map[string]int
	sourceCooldownUntil   map[string]time.Time
	batchSize             int
	batchInterval         time.Duration
	batchCompleted        int
	cooldownUntil         time.Time
	convertToSimplified   bool
	activeTotal           int
	activeBySource        map[string]int

	mu      sync.Mutex
	metaMu  sync.Mutex
	active  map[int64]context.CancelFunc
	kick    chan struct{}
	started bool
}

// SetConcurrency applies a settings update. New chapters and page requests
// use the new limits immediately; already running requests finish first.
func (e *Engine) SetConcurrency(chapters, pages int) {
	if chapters < 1 {
		chapters = 1
	}
	if pages < 1 {
		pages = 1
	}
	e.mu.Lock()
	e.maxPageConcurrency = pages
	for sourceID := range e.sourceConcurrency {
		e.sourceConcurrency[sourceID] = chapters
	}
	e.maxChapterConcurrency = chapters * len(e.sourceConcurrency)
	e.mu.Unlock()
	e.Notify()
}

// SetSourceConcurrency applies per-source chapter limits.
func (e *Engine) SetSourceConcurrency(limits map[string]int) {
	e.mu.Lock()
	total := 0
	for sourceID, limit := range limits {
		if limit < 1 {
			limit = 1
		}
		e.sourceConcurrency[sourceID] = limit
		total += limit
	}
	if total > 0 {
		e.maxChapterConcurrency = total
	}
	e.mu.Unlock()
	e.Notify()
}

// SetSourceSettings applies the per-source download policy shown on each
// resource card. Zero values fall back to the process defaults.
func (e *Engine) SetSourceSettings(settings map[string]model.SourceDownloadSettings) {
	e.mu.Lock()
	for sourceID, value := range settings {
		if value.ChapterConcurrency > 0 {
			e.sourceConcurrency[sourceID] = value.ChapterConcurrency
		}
		if value.PageConcurrency > 0 {
			e.sourcePageConcurrency[sourceID] = value.PageConcurrency
		}
		if value.BatchSize >= 0 {
			e.sourceBatchSize[sourceID] = value.BatchSize
		}
		if value.BatchIntervalMinutes >= 0 {
			e.sourceBatchInterval[sourceID] = time.Duration(value.BatchIntervalMinutes) * time.Minute
		}
	}
	total := 0
	for _, limit := range e.sourceConcurrency {
		if limit > 0 {
			total += limit
		}
	}
	if total > 0 {
		e.maxChapterConcurrency = total
	}
	e.mu.Unlock()
	e.Notify()
}

// SetDownloadPolicy configures the batch pause and text conversion behavior.
func (e *Engine) SetDownloadPolicy(batchSize, intervalMinutes int, convertToSimplified bool) {
	e.mu.Lock()
	if batchSize < 0 {
		batchSize = 0
	}
	if intervalMinutes < 0 {
		intervalMinutes = 0
	}
	e.batchSize = batchSize
	e.batchInterval = time.Duration(intervalMinutes) * time.Minute
	e.convertToSimplified = convertToSimplified
	e.batchCompleted = 0
	e.cooldownUntil = time.Time{}
	e.mu.Unlock()
	e.Notify()
}

// Jobs is the subset of the store used by the downloader.
type Jobs interface {
	CreateDownloadJob(ctx context.Context, job model.DownloadJob) (model.DownloadJob, error)
	ListQueuedJobs(ctx context.Context) ([]model.DownloadJob, error)
	DownloadJob(ctx context.Context, id int64) (model.DownloadJob, error)
	UpdateDownloadJob(ctx context.Context, id int64, status string, totalPages, completedPages int, filePath, jobError string) error
	FailDownloadJob(ctx context.Context, id int64, totalPages, completedPages int, jobError string, retryCount int, nextRetryAt *time.Time) error
	ResetDownloadJob(ctx context.Context, id int64) error
	ActivateDueRetries(ctx context.Context, now time.Time) (int64, error)
	UpdateDownloadProgress(ctx context.Context, id int64, completedPages int) error
}

func NewEngine(registry *sources.Registry, store Jobs, downloadDir string, maxChapterConcurrency, maxPageConcurrency int, logger *log.Logger) *Engine {
	if maxChapterConcurrency < 1 {
		maxChapterConcurrency = 1
	}
	if maxPageConcurrency < 1 {
		maxPageConcurrency = 1
	}
	if logger == nil {
		logger = log.Default()
	}
	if strings.TrimSpace(downloadDir) == "" {
		downloadDir = "downloads"
	}
	return &Engine{
		registry:              registry,
		store:                 store,
		downloadDir:           downloadDir,
		cacheDir:              filepath.Join(os.TempDir(), "manco-download-cache"),
		maxChapterConcurrency: maxChapterConcurrency,
		maxPageConcurrency:    maxPageConcurrency,
		sourceConcurrency: map[string]int{
			"picacg":  maxChapterConcurrency,
			"jmcomic": maxChapterConcurrency,
			"baozimh": maxChapterConcurrency,
		},
		sourcePageConcurrency: map[string]int{},
		sourceBatchSize:       map[string]int{},
		sourceBatchInterval:   map[string]time.Duration{},
		sourceBatchCompleted:  map[string]int{},
		sourceCooldownUntil:   map[string]time.Time{},
		logger:                logger,
		active:                map[int64]context.CancelFunc{},
		activeBySource:        map[string]int{},
		kick:                  make(chan struct{}, 1),
	}
}

// SetCacheDir points the per-chapter staging directory at a persistent volume
// (normally <data>/download-cache) instead of the download destination, so the
// library tree never contains a .tmp working directory.
func (e *Engine) SetCacheDir(dir string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	e.mu.Lock()
	e.cacheDir = dir
	e.mu.Unlock()
}

func (e *Engine) stagingDir(job model.DownloadJob) string {
	e.mu.Lock()
	cacheDir := e.cacheDir
	e.mu.Unlock()
	if strings.TrimSpace(cacheDir) == "" {
		cacheDir = filepath.Join(os.TempDir(), "manco-download-cache")
	}
	return filepath.Join(cacheDir, fmt.Sprintf("job-%d", job.ID))
}

func (e *Engine) Start(ctx context.Context) {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return
	}
	e.started = true
	e.mu.Unlock()
	go e.loop(ctx)
}

func (e *Engine) Notify() {
	select {
	case e.kick <- struct{}{}:
	default:
	}
}

func (e *Engine) Retry(ctx context.Context, id int64) error {
	if _, err := e.store.DownloadJob(ctx, id); err != nil {
		return err
	}
	if err := e.store.ResetDownloadJob(ctx, id); err != nil {
		return err
	}
	e.Notify()
	return nil
}

func (e *Engine) Cancel(id int64) bool {
	e.mu.Lock()
	cancel, ok := e.active[id]
	e.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

func (e *Engine) concurrency() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.maxChapterConcurrency
}

func (e *Engine) pageConcurrency(sourceID string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if limit := e.sourcePageConcurrency[sourceID]; limit > 0 {
		return limit
	}
	return e.maxPageConcurrency
}

func (e *Engine) jobDownloadDir(job model.DownloadJob) (string, error) {
	root := strings.TrimSpace(job.DownloadDir)
	if root == "" {
		root = e.downloadDir
	}
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) {
		root = filepath.Join(e.downloadDir, root)
	}
	root = filepath.Clean(root)
	if root == "." || root == string(filepath.Separator) || root == "" {
		return "", errors.New("下载位置不安全")
	}
	return root, nil
}

func (e *Engine) loop(ctx context.Context) {
	defer func() {
		e.mu.Lock()
		e.started = false
		e.mu.Unlock()
	}()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-e.kick:
		}
		if activated, err := e.store.ActivateDueRetries(ctx, time.Now()); err != nil {
			e.logger.Printf("downloader: activate retries: %v", err)
		} else if activated > 0 {
			e.logger.Printf("downloader: requeued %d failed job(s) for automatic retry", activated)
		}

		jobs, err := e.store.ListQueuedJobs(ctx)
		if err != nil {
			e.logger.Printf("downloader: list queue: %v", err)
			continue
		}
		for _, job := range jobs {
			job := job
			if e.inCooldown(job.SourceID) || !e.claim(job) {
				continue
			}
			if ctx.Err() != nil {
				e.release(job)
				return
			}
			go func() {
				defer func() {
					e.release(job)
					e.Notify()
				}()
				e.runJob(ctx, job)
			}()
		}
	}
}

func (e *Engine) claim(job model.DownloadJob) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.active[job.ID]; exists {
		return false
	}
	if e.activeTotal >= e.maxChapterConcurrency {
		return false
	}
	if limit := e.sourceConcurrency[job.SourceID]; limit > 0 && e.activeBySource[job.SourceID] >= limit {
		return false
	}
	e.active[job.ID] = func() {}
	e.activeTotal++
	e.activeBySource[job.SourceID]++
	return true
}

func (e *Engine) release(job model.DownloadJob) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.active[job.ID]; !exists {
		return
	}
	delete(e.active, job.ID)
	if e.activeTotal > 0 {
		e.activeTotal--
	}
	if e.activeBySource[job.SourceID] > 0 {
		e.activeBySource[job.SourceID]--
	}
}

// inCooldown reports whether the configured batch pause is still active.
func (e *Engine) inCooldown(sourceID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	batchSize := e.batchSize
	if limit, ok := e.sourceBatchSize[sourceID]; ok {
		batchSize = limit
	}
	return batchSize > 0 && time.Now().Before(e.sourceCooldownUntil[sourceID])
}

// SourceCooldowns returns the per-source batch pause deadlines. The download
// list uses it to mark queued chapters as waiting while their source is paused.
func (e *Engine) SourceCooldowns() map[string]time.Time {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	result := make(map[string]time.Time, len(e.sourceCooldownUntil))
	for sourceID, until := range e.sourceCooldownUntil {
		if until.After(now) {
			result[sourceID] = until
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// markBatchComplete pauses the engine after the configured number of jobs.
func (e *Engine) markBatchComplete(sourceID string) {
	e.mu.Lock()
	batchSize := e.batchSize
	if limit, ok := e.sourceBatchSize[sourceID]; ok {
		batchSize = limit
	}
	if batchSize <= 0 {
		e.mu.Unlock()
		return
	}
	e.sourceBatchCompleted[sourceID]++
	if e.sourceBatchCompleted[sourceID] < batchSize {
		e.mu.Unlock()
		return
	}
	e.sourceBatchCompleted[sourceID] = 0
	interval := e.batchInterval
	if value, ok := e.sourceBatchInterval[sourceID]; ok {
		interval = value
	}
	if interval > 0 {
		e.sourceCooldownUntil[sourceID] = time.Now().Add(interval)
		e.logger.Printf("downloader: source %s batch of %d completed, pausing until %s", sourceID, batchSize, e.sourceCooldownUntil[sourceID].Format(time.RFC3339))
	}
	e.mu.Unlock()
}

func (e *Engine) runJob(parent context.Context, job model.DownloadJob) {
	ctx, cancel := context.WithCancel(parent)
	e.mu.Lock()
	e.active[job.ID] = cancel
	e.mu.Unlock()
	defer cancel()

	err := e.download(ctx, job)
	if err == nil {
		e.markBatchComplete(job.SourceID)
		return
	}
	// Reload persisted page progress before recording the terminal state. A
	// resumed attempt may have advanced further than the queued snapshot.
	if latest, reloadErr := e.store.DownloadJob(context.Background(), job.ID); reloadErr == nil {
		job = latest
	}
	message := err.Error()
	if len(message) > 500 {
		message = message[:500]
	}
	if errors.Is(err, context.Canceled) {
		_ = e.store.UpdateDownloadJob(context.Background(), job.ID, "canceled", job.TotalPages, job.CompletedPages, "", message)
		e.logger.Printf("downloader: %s/%s canceled", job.ComicTitle, job.ChapterTitle)
		return
	}
	retryCount := job.RetryCount
	var nextRetry *time.Time
	if retryCount < 2 {
		delay := 30 * time.Minute
		if retryCount == 0 {
			delay = 10 * time.Minute
		}
		at := time.Now().Add(delay)
		nextRetry = &at
		retryCount++
	}
	_ = e.store.FailDownloadJob(context.Background(), job.ID, job.TotalPages, job.CompletedPages, message, retryCount, nextRetry)
	if nextRetry != nil {
		e.logger.Printf("downloader: %s/%s failed, retry in %s: %v", job.ComicTitle, job.ChapterTitle, time.Until(*nextRetry).Round(time.Second), err)
	} else {
		e.logger.Printf("downloader: %s/%s failed, automatic retries exhausted: %v", job.ComicTitle, job.ChapterTitle, err)
	}
}

func (e *Engine) download(ctx context.Context, job model.DownloadJob) error {
	item, err := e.registry.Get(job.SourceID)
	if err != nil {
		return err
	}
	account, err := e.registry.Account(ctx, job.SourceID)
	if err != nil {
		return err
	}
	if source.KindOf(item.Info()) == source.KindBook {
		return e.downloadBook(ctx, item, account, job)
	}
	// Keep the persisted progress from a previous attempt until the current
	// page list is available, so a list-pages failure does not erase it.
	if err := e.store.UpdateDownloadJob(ctx, job.ID, "running", job.TotalPages, job.CompletedPages, "", ""); err != nil {
		return err
	}
	chapter := model.Chapter{
		ID:      job.ChapterID,
		ComicID: job.ComicID,
		Title:   job.ChapterTitle,
		Order:   job.ChapterOrder,
	}
	pages, err := item.Pages(ctx, account, job.ComicID, chapter)
	if err != nil {
		return fmt.Errorf("list pages: %w", err)
	}
	if len(pages) == 0 {
		return errors.New("章节没有可用图片")
	}

	downloadRoot, err := e.jobDownloadDir(job)
	if err != nil {
		return err
	}
	tempDir := e.stagingDir(job)
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return err
	}
	removeStagingParts(tempDir)
	cachedPages := countCachedPages(tempDir, len(pages))
	if err := e.store.UpdateDownloadJob(ctx, job.ID, "running", len(pages), cachedPages, "", ""); err != nil {
		return err
	}
	finished := false
	defer func() {
		if finished {
			_ = os.RemoveAll(tempDir)
		}
	}()

	client := e.registry.ClientForAccount(account)
	if client == nil {
		client = source.NewHTTPClient()
	}
	type fetched struct {
		index  int
		cached bool
		err    error
	}
	results := make(chan fetched, len(pages))
	semaphore := make(chan struct{}, e.pageConcurrency(job.SourceID))
	pageCtx, cancelPages := context.WithCancel(ctx)
	defer cancelPages()
	var wait sync.WaitGroup
	for index, page := range pages {
		index, page := index, page
		wait.Add(1)
		go func() {
			defer wait.Done()
			cachePath := stagingPagePath(tempDir, index)
			if info, statErr := os.Stat(cachePath); statErr == nil && info.Mode().IsRegular() && info.Size() > 0 {
				results <- fetched{index: index, cached: true}
				return
			}
			select {
			case semaphore <- struct{}{}:
			case <-pageCtx.Done():
				results <- fetched{index: index, err: pageCtx.Err()}
				return
			}
			defer func() { <-semaphore }()
			data, materialized, fetchErr := fetchPageImage(pageCtx, client, account, item, job.ComicID, chapter, page)
			if fetchErr != nil {
				results <- fetched{index: index, err: fetchErr}
				return
			}
			processed := data
			if page.DescrambleParts > 0 && !materialized {
				processed, fetchErr = Descramble(data, page.DescrambleParts)
				if fetchErr != nil {
					results <- fetched{index: index, err: fmt.Errorf("解扰失败: %w", fetchErr)}
					return
				}
			}
			if len(processed) == 0 {
				results <- fetched{index: index, err: errors.New("页面为空")}
				return
			}
			if writeErr := writeFileAtomic(cachePath, processed); writeErr != nil {
				results <- fetched{index: index, err: writeErr}
				return
			}
			results <- fetched{index: index}
		}()
	}
	go func() {
		wait.Wait()
		close(results)
	}()

	completed := cachedPages
	var firstErr error
	for result := range results {
		if result.err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("第 %d 页下载失败: %w", result.index+1, result.err)
				cancelPages()
			}
			continue
		}
		if result.cached {
			continue
		}
		completed++
		_ = e.store.UpdateDownloadProgress(ctx, job.ID, completed)
	}
	if firstErr != nil {
		return firstErr
	}

	entries := make([]pageEntry, 0, len(pages))
	for index, page := range pages {
		path := stagingPagePath(tempDir, index)
		extension, extensionErr := imageExtensionFromFile(path, page.URL)
		if extensionErr != nil {
			return fmt.Errorf("读取第 %d 页缓存失败: %w", index+1, extensionErr)
		}
		name := fmt.Sprintf("%03d%s", index+1, extension)
		entries = append(entries, pageEntry{name: name, path: path})
	}

	metadata := model.Comic{
		SourceID: job.SourceID,
		ID:       job.ComicID,
		Title:    job.ComicTitle,
		Cover:    job.ComicCover,
	}
	if detail, detailErr := item.Detail(ctx, account, job.ComicID); detailErr == nil {
		metadata = detail
		metadata.SourceID = source.FirstNonEmpty(detail.SourceID, metadata.SourceID)
		metadata.ID = source.FirstNonEmpty(detail.ID, metadata.ID)
		metadata.Title = source.FirstNonEmpty(detail.Title, metadata.Title)
		metadata.Cover = source.FirstNonEmpty(detail.Cover, metadata.Cover)
	} else {
		e.logger.Printf("downloader: detail %s/%s: %v", job.SourceID, job.ComicID, detailErr)
	}
	comicTitle := job.ComicTitle
	chapterTitle := job.ChapterTitle
	if job.ConvertToSimplified {
		comicTitle = convertToSimplifiedText(comicTitle)
		chapterTitle = convertToSimplifiedText(chapterTitle)
		metadata.Title = convertToSimplifiedText(metadata.Title)
		metadata.Author = convertToSimplifiedText(metadata.Author)
		metadata.Description = convertToSimplifiedText(metadata.Description)
		metadata.Status = convertToSimplifiedText(metadata.Status)
		for index := range metadata.Tags {
			metadata.Tags[index] = convertToSimplifiedText(metadata.Tags[index])
		}
	}
	comicDir := filepath.Join(downloadRoot, SafeName(comicTitle))
	e.metaMu.Lock()
	infoPayload, metaErr := writeComicMetadata(ctx, client, account, item, metadata, comicDir, chapterTitle, job.ChapterOrder, len(pages))
	e.metaMu.Unlock()
	if metaErr != nil {
		return fmt.Errorf("download comic metadata: %w", metaErr)
	}
	if len(infoPayload) > 0 {
		infoPath := filepath.Join(tempDir, "ComicInfo.xml")
		if err := os.WriteFile(infoPath, infoPayload, 0o644); err != nil {
			return err
		}
		entries = append(entries, pageEntry{name: "ComicInfo.xml", path: infoPath})
	}

	outputPath := filepath.Join(comicDir, ChapterFileName(chapterTitle, job.ChapterOrder, job.ChapterMarker, ".cbz"))
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	if err := writeZip(outputPath, entries); err != nil {
		return err
	}
	if err := e.store.UpdateDownloadJob(ctx, job.ID, "completed", len(pages), len(pages), outputPath, ""); err != nil {
		return err
	}
	finished = true
	return nil
}

type pageEntry struct {
	name string
	path string
}

func stagingPagePath(dir string, index int) string {
	return filepath.Join(dir, fmt.Sprintf("%03d.bin", index+1))
}

func countCachedPages(dir string, total int) int {
	count := 0
	for index := 0; index < total; index++ {
		info, err := os.Stat(stagingPagePath(dir, index))
		if err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			count++
		}
	}
	return count
}

func removeStagingParts(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".part") {
			continue
		}
		_ = os.Remove(filepath.Join(dir, entry.Name()))
	}
}

func imageExtensionFromFile(path, address string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	header := make([]byte, 16)
	count, err := file.Read(header)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if extension := detectImageExtension(header[:count]); extension != "" {
		return extension, nil
	}
	return imageExtension(header[:count], address), nil
}

// ComicInfo mirrors the ComicInfo.xml v2.1 schema used by comic readers.
// Field order is significant because the schema defines an xs:sequence.
type ComicInfo struct {
	XMLName             xml.Name        `xml:"ComicInfo"`
	Title               string          `xml:"Title,omitempty"`
	Series              string          `xml:"Series,omitempty"`
	Number              string          `xml:"Number,omitempty"`
	Count               int             `xml:"Count,omitempty"`
	Volume              int             `xml:"Volume,omitempty"`
	AlternateSeries     string          `xml:"AlternateSeries,omitempty"`
	AlternateNumber     string          `xml:"AlternateNumber,omitempty"`
	AlternateCount      int             `xml:"AlternateCount,omitempty"`
	Summary             string          `xml:"Summary,omitempty"`
	Notes               string          `xml:"Notes,omitempty"`
	Year                int             `xml:"Year,omitempty"`
	Month               int             `xml:"Month,omitempty"`
	Day                 int             `xml:"Day,omitempty"`
	Writer              string          `xml:"Writer,omitempty"`
	Penciller           string          `xml:"Penciller,omitempty"`
	Inker               string          `xml:"Inker,omitempty"`
	Colorist            string          `xml:"Colorist,omitempty"`
	Letterer            string          `xml:"Letterer,omitempty"`
	CoverArtist         string          `xml:"CoverArtist,omitempty"`
	Editor              string          `xml:"Editor,omitempty"`
	Translator          string          `xml:"Translator,omitempty"`
	Publisher           string          `xml:"Publisher,omitempty"`
	Imprint             string          `xml:"Imprint,omitempty"`
	Genre               string          `xml:"Genre,omitempty"`
	Tags                string          `xml:"Tags,omitempty"`
	Web                 string          `xml:"Web,omitempty"`
	PageCount           int             `xml:"PageCount,omitempty"`
	LanguageISO         string          `xml:"LanguageISO,omitempty"`
	Format              string          `xml:"Format,omitempty"`
	BlackAndWhite       string          `xml:"BlackAndWhite,omitempty"`
	Manga               string          `xml:"Manga,omitempty"`
	Characters          string          `xml:"Characters,omitempty"`
	Teams               string          `xml:"Teams,omitempty"`
	Locations           string          `xml:"Locations,omitempty"`
	ScanInformation     string          `xml:"ScanInformation,omitempty"`
	StoryArc            string          `xml:"StoryArc,omitempty"`
	StoryArcNumber      string          `xml:"StoryArcNumber,omitempty"`
	SeriesGroup         string          `xml:"SeriesGroup,omitempty"`
	AgeRating           string          `xml:"AgeRating,omitempty"`
	Pages               *ComicInfoPages `xml:"Pages,omitempty"`
	CommunityRating     string          `xml:"CommunityRating,omitempty"`
	MainCharacterOrTeam string          `xml:"MainCharacterOrTeam,omitempty"`
	Review              string          `xml:"Review,omitempty"`
	GTIN                string          `xml:"GTIN,omitempty"`
}

type ComicInfoPages struct {
	Page []ComicInfoPage `xml:"Page"`
}

type ComicInfoPage struct {
	Image       int    `xml:"Image,attr"`
	Type        string `xml:"Type,attr,omitempty"`
	DoublePage  bool   `xml:"DoublePage,attr,omitempty"`
	ImageSize   int64  `xml:"ImageSize,attr,omitempty"`
	Key         string `xml:"Key,attr,omitempty"`
	Bookmark    string `xml:"Bookmark,attr,omitempty"`
	ImageWidth  int    `xml:"ImageWidth,attr,omitempty"`
	ImageHeight int    `xml:"ImageHeight,attr,omitempty"`
}

var (
	textConvertOnce sync.Once
	textConverter   *gocc.OpenCC
	textConvertErr  error
)

// convertToSimplifiedText converts Traditional Chinese metadata to Simplified
// Chinese. If the converter cannot be initialized, the original text is kept.
func convertToSimplifiedText(value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	textConvertOnce.Do(func() {
		textConverter, textConvertErr = gocc.New("t2s")
	})
	if textConvertErr != nil || textConverter == nil {
		return value
	}
	converted, err := textConverter.Convert(value)
	if err != nil || converted == "" {
		return value
	}
	return converted
}

// SimplifiedText exposes the project's stable Traditional-to-Simplified
// conversion for local library maintenance.
func SimplifiedText(value string) string {
	return convertToSimplifiedText(value)
}

// downloadBook persists one file per chapter for book sources (the original
// ebook binary when the source implements FileContentSource, otherwise the
// chapter text), plus a short book.json metadata file shared by the work.
func (e *Engine) downloadBook(ctx context.Context, item source.Source, account source.Account, job model.DownloadJob) error {
	chapter := model.Chapter{ID: job.ChapterID, ComicID: job.ComicID, Title: job.ChapterTitle, Order: job.ChapterOrder}
	// Mark the job running before the (potentially large) ebook fetch so the
	// download list reports progress instead of staying queued.
	if err := e.store.UpdateDownloadJob(ctx, job.ID, "running", 1, 0, "", ""); err != nil {
		return err
	}
	var (
		data     []byte
		filename string
		text     string
		isText   bool
	)
	if fileSource, ok := item.(source.FileContentSource); ok {
		var err error
		data, filename, err = fileSource.ChapterFile(ctx, account, job.ComicID, chapter)
		if err != nil {
			return fmt.Errorf("fetch book file: %w", err)
		}
		if len(data) == 0 {
			return errors.New("书籍文件为空")
		}
	} else if archiveSource, ok := item.(source.ChapterArchiveSource); ok {
		return e.downloadBookArchive(ctx, item, account, job, chapter, archiveSource)
	} else if contentSource, ok := item.(source.ContentSource); ok {
		var err error
		text, err = contentSource.ChapterContent(ctx, account, job.ComicID, chapter)
		if err != nil {
			return fmt.Errorf("fetch chapter content: %w", err)
		}
		data = []byte(text)
		isText = true
	} else {
		return errors.New("该书源不支持正文下载")
	}
	comicTitle := job.ComicTitle
	chapterTitle := job.ChapterTitle
	metadata := model.Comic{SourceID: job.SourceID, ID: job.ComicID, Title: comicTitle, Cover: job.ComicCover}
	if detail, detailErr := item.Detail(ctx, account, job.ComicID); detailErr == nil {
		metadata = detail
		metadata.SourceID = source.FirstNonEmpty(detail.SourceID, metadata.SourceID)
		metadata.ID = source.FirstNonEmpty(detail.ID, metadata.ID)
		metadata.Title = source.FirstNonEmpty(detail.Title, metadata.Title)
		metadata.Cover = source.FirstNonEmpty(detail.Cover, metadata.Cover)
	} else {
		e.logger.Printf("downloader: book detail %s/%s: %v", job.SourceID, job.ComicID, detailErr)
	}
	if job.ConvertToSimplified {
		if isText {
			text = convertToSimplifiedText(text)
			data = []byte(text)
		}
		comicTitle = convertToSimplifiedText(comicTitle)
		chapterTitle = convertToSimplifiedText(chapterTitle)
		metadata.Title = convertToSimplifiedText(metadata.Title)
		metadata.Author = convertToSimplifiedText(metadata.Author)
		metadata.Description = convertToSimplifiedText(metadata.Description)
	}
	downloadRoot, err := e.jobDownloadDir(job)
	if err != nil {
		return err
	}
	comicDir := filepath.Join(downloadRoot, SafeName(comicTitle))
	if err := os.MkdirAll(comicDir, 0o755); err != nil {
		return err
	}
	outputName := chapterTitle
	extension := ".txt"
	if !isText {
		if base := bookFileBaseName(filename); base != "" {
			outputName = base
		}
		extension = bookFileExtension(filename)
	}
	outputPath := filepath.Join(comicDir, ChapterFileName(outputName, job.ChapterOrder, job.ChapterMarker, extension))
	if err := writeFileAtomic(outputPath, data); err != nil {
		return err
	}
	e.metaMu.Lock()
	metaErr := writeBookMetadata(ctx, e.registry.ClientForAccount(account), account, item, metadata, comicDir)
	e.metaMu.Unlock()
	if metaErr != nil {
		e.logger.Printf("downloader: book metadata %s/%s: %v", job.SourceID, job.ComicID, metaErr)
	}
	return e.store.UpdateDownloadJob(ctx, job.ID, "completed", 1, 1, outputPath, "")
}

type chapterArchiveIndex struct {
	ID          string                           `json:"id"`
	Title       string                           `json:"title"`
	Authors     []string                         `json:"authors,omitempty"`
	URL         string                           `json:"url,omitempty"`
	PublicURL   string                           `json:"public_url,omitempty"`
	Cover       string                           `json:"cover,omitempty"`
	Description string                           `json:"description,omitempty"`
	State       string                           `json:"state,omitempty"`
	Source      string                           `json:"source"`
	Tags        []chapterArchiveIndexTag         `json:"tags,omitempty"`
	Chapters    map[string]chapterArchiveChapter `json:"chapters"`
}

type chapterArchiveIndexTag struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

type chapterArchiveChapter struct {
	Number  float64               `json:"number,omitempty"`
	Order   int                   `json:"order"`
	Name    string                `json:"name,omitempty"`
	URL     string                `json:"url,omitempty"`
	Source  string                `json:"source"`
	Entries string                `json:"entries"`
	File    string                `json:"file,omitempty"`
	Images  []chapterArchiveImage `json:"images,omitempty"`
}

type chapterArchiveImage struct {
	Remote string `json:"remote"`
	Local  string `json:"local"`
}

// downloadBookArchive packages a plugin book chapter as one Kototoro-style
// CBZ. The chapter HTML is page 0 and every illustration follows as a source
// page materialized by the plugin runtime, preserving parser headers and
// descrambling behavior.
func (e *Engine) downloadBookArchive(
	ctx context.Context,
	item source.Source,
	account source.Account,
	job model.DownloadJob,
	chapter model.Chapter,
	archiveSource source.ChapterArchiveSource,
) error {
	content, err := archiveSource.ChapterArchive(ctx, account, job.ComicID, chapter)
	if err != nil {
		return fmt.Errorf("fetch chapter archive: %w", err)
	}
	if strings.TrimSpace(content.HTML) == "" && len(content.Images) == 0 {
		return errors.New("章节没有可用内容")
	}
	totalPages := len(content.Images) + 1
	if err := e.store.UpdateDownloadJob(ctx, job.ID, "running", totalPages, 0, "", ""); err != nil {
		return err
	}

	comicTitle := job.ComicTitle
	chapterTitle := job.ChapterTitle
	metadata := model.Comic{SourceID: job.SourceID, ID: job.ComicID, Title: comicTitle, Cover: job.ComicCover}
	if detail, detailErr := item.Detail(ctx, account, job.ComicID); detailErr == nil {
		metadata = detail
		metadata.SourceID = source.FirstNonEmpty(detail.SourceID, metadata.SourceID)
		metadata.ID = source.FirstNonEmpty(detail.ID, metadata.ID)
		metadata.Title = source.FirstNonEmpty(detail.Title, metadata.Title)
		metadata.Author = source.FirstNonEmpty(detail.Author, metadata.Author)
		metadata.Cover = source.FirstNonEmpty(detail.Cover, metadata.Cover)
	} else {
		e.logger.Printf("downloader: book archive detail %s/%s: %v", job.SourceID, job.ComicID, detailErr)
	}
	html := content.HTML
	if job.ConvertToSimplified {
		html = convertToSimplifiedText(html)
		comicTitle = convertToSimplifiedText(comicTitle)
		chapterTitle = convertToSimplifiedText(chapterTitle)
		metadata.Title = convertToSimplifiedText(metadata.Title)
		metadata.Author = convertToSimplifiedText(metadata.Author)
		metadata.Description = convertToSimplifiedText(metadata.Description)
		metadata.Status = convertToSimplifiedText(metadata.Status)
		for index := range metadata.Tags {
			metadata.Tags[index] = convertToSimplifiedText(metadata.Tags[index])
		}
	}

	downloadRoot, err := e.jobDownloadDir(job)
	if err != nil {
		return err
	}
	comicDir := filepath.Join(downloadRoot, SafeName(comicTitle))
	tempDir := e.stagingDir(job)
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return err
	}
	removeStagingParts(tempDir)
	finished := false
	defer func() {
		if finished {
			_ = os.RemoveAll(tempDir)
		}
	}()

	client := e.registry.ClientForAccount(account)
	if client == nil {
		client = source.NewHTTPClient()
	}
	imageEntries := make([]pageEntry, 0, len(content.Images))
	imageIndex := make([]chapterArchiveImage, 0, len(content.Images))
	imageNames := make(map[string]string, len(content.Images))
	completedPages := 1
	for index, page := range content.Images {
		if strings.TrimSpace(page.URL) == "" {
			continue
		}
		data, materialized, fetchErr := fetchPageImage(ctx, client, account, item, job.ComicID, chapter, page)
		if fetchErr != nil {
			return fmt.Errorf("第 %d 张插图下载失败: %w", index+1, fetchErr)
		}
		if page.DescrambleParts > 0 && !materialized {
			data, fetchErr = Descramble(data, page.DescrambleParts)
			if fetchErr != nil {
				return fmt.Errorf("第 %d 张插图解扰失败: %w", index+1, fetchErr)
			}
		}
		if len(data) == 0 {
			return fmt.Errorf("第 %d 张插图为空", index+1)
		}
		name := chapterArchivePageName(index+1, imageExtension(data, page.URL))
		path := filepath.Join(tempDir, name)
		if err := writeFileAtomic(path, data); err != nil {
			return err
		}
		imageEntries = append(imageEntries, pageEntry{name: name, path: path})
		imageNames[page.URL] = name
		imageIndex = append(imageIndex, chapterArchiveImage{Remote: page.URL, Local: name})
		completedPages++
		if err := e.store.UpdateDownloadProgress(ctx, job.ID, completedPages); err != nil {
			return err
		}
	}

	html, err = rewriteChapterArchiveHTML(html, imageNames)
	if err != nil {
		return fmt.Errorf("rewrite chapter HTML: %w", err)
	}
	htmlName := chapterArchivePageName(0, ".html")
	htmlPath := filepath.Join(tempDir, htmlName)
	if err := writeFileAtomic(htmlPath, []byte(html)); err != nil {
		return err
	}
	entries := make([]pageEntry, 0, len(imageEntries)+3)
	entries = append(entries, pageEntry{name: htmlName, path: htmlPath})
	entries = append(entries, imageEntries...)

	outputPath := filepath.Join(comicDir, ChapterFileName(chapterTitle, job.ChapterOrder, job.ChapterMarker, ".cbz"))
	archiveIndex := chapterArchiveIndex{
		ID:          metadata.ID,
		Title:       metadata.Title,
		URL:         comicPageURL(item.Info().Homepage, metadata.SourceID, metadata.ID),
		PublicURL:   comicPageURL(item.Info().Homepage, metadata.SourceID, metadata.ID),
		Cover:       metadata.Cover,
		Description: metadata.Description,
		State:       metadata.Status,
		Source:      job.SourceID,
		Chapters: map[string]chapterArchiveChapter{
			job.ChapterID: {
				Number:  job.ChapterOrder,
				Order:   1,
				Name:    chapterTitle,
				Source:  job.SourceID,
				Entries: fmt.Sprintf("00000000_%04d\\d{4}", 1),
				File:    filepath.Base(outputPath),
				Images:  imageIndex,
			},
		},
	}
	if strings.TrimSpace(metadata.Author) != "" {
		archiveIndex.Authors = []string{metadata.Author}
	}
	for _, tag := range metadata.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		archiveIndex.Tags = append(archiveIndex.Tags, chapterArchiveIndexTag{Key: tag, Title: tag})
	}
	indexPayload, err := json.MarshalIndent(archiveIndex, "", "  ")
	if err != nil {
		return err
	}
	indexPath := filepath.Join(tempDir, "index.json")
	if err := writeFileAtomic(indexPath, indexPayload); err != nil {
		return err
	}
	entries = append(entries, pageEntry{name: "index.json", path: indexPath})

	infoPayload, err := marshalComicInfo(chapterComicInfo(item, metadata, chapterTitle, job.ChapterOrder, totalPages))
	if err != nil {
		return err
	}
	infoPath := filepath.Join(tempDir, "ComicInfo.xml")
	if err := writeFileAtomic(infoPath, infoPayload); err != nil {
		return err
	}
	entries = append(entries, pageEntry{name: "ComicInfo.xml", path: infoPath})

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	if err := writeZip(outputPath, entries); err != nil {
		return err
	}

	e.metaMu.Lock()
	metaErr := writeBookMetadata(ctx, client, account, item, metadata, comicDir)
	e.metaMu.Unlock()
	if metaErr != nil {
		e.logger.Printf("downloader: book archive metadata %s/%s: %v", job.SourceID, job.ComicID, metaErr)
	}
	if err := e.store.UpdateDownloadJob(ctx, job.ID, "completed", totalPages, totalPages, outputPath, ""); err != nil {
		return err
	}
	finished = true
	return nil
}

func chapterArchivePageName(page int, extension string) string {
	if strings.TrimSpace(extension) == "" {
		extension = ".jpg"
	}
	return fmt.Sprintf("%08d_%04d%04d%s", 0, 1, page, extension)
}

func rewriteChapterArchiveHTML(html string, imageNames map[string]string) (string, error) {
	if strings.TrimSpace(html) == "" || len(imageNames) == 0 {
		return html, nil
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return "", err
	}
	document.Find("img").Each(func(_ int, selection *goquery.Selection) {
		for _, attribute := range []string{"src", "data-src"} {
			address, exists := selection.Attr(attribute)
			if !exists {
				continue
			}
			localName, ok := imageNames[strings.TrimSpace(address)]
			if !ok {
				continue
			}
			selection.SetAttr("src", localName)
			selection.RemoveAttr("data-src")
			selection.RemoveAttr("srcset")
			break
		}
	})
	return document.Html()
}

// writeFileAtomic keeps book downloads invisible until the complete file has
// been written, mirroring the CBZ .part-to-final behavior.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp := path + ".part"
	if err := os.WriteFile(temp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err == nil {
		return nil
	}
	// Windows does not replace an existing destination during Rename.
	// Retrying after removing the old file keeps manual retries useful.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(temp)
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func bookFileBaseName(filename string) string {
	filename = strings.TrimSpace(filename)
	if index := strings.LastIndexAny(filename, `/\\`); index >= 0 {
		filename = filename[index+1:]
	}
	return strings.TrimSuffix(filename, filepath.Ext(filename))
}

func bookFileExtension(filename string) string {
	extension := strings.ToLower(strings.TrimSpace(filepath.Ext(strings.TrimSpace(filename))))
	if extension == "" || strings.ContainsAny(extension, `/\\:*?\"<>|`) {
		return ".bin"
	}
	return extension
}

// writeBookMetadata stores the series-level metadata used by Komga/Mylar and
// keeps the lightweight book.json consumed by Manco's own local library.
func writeBookMetadata(ctx context.Context, client *http.Client, account source.Account, item source.Source, book model.Comic, comicDir string) error {
	info := item.Info()
	coverURL := strings.TrimSpace(book.Cover)
	if coverURL != "" || canMaterializeCover(item, coverURL) {
		existing, _ := filepath.Glob(filepath.Join(comicDir, "cover.*"))
		if len(existing) == 0 {
			if data, err := fetchCoverImage(ctx, client, account, item, book); err == nil {
				_ = os.WriteFile(filepath.Join(comicDir, "cover"+imageExtension(data, coverURL)), data, 0o644)
			}
		}
	}
	if err := writeSeriesMetadataFiles(book, info, comicDir); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(map[string]any{
		"type":        "book",
		"source":      book.SourceID,
		"id":          book.ID,
		"title":       book.Title,
		"author":      book.Author,
		"description": book.Description,
		"tags":        book.Tags,
		"status":      book.Status,
		"cover":       book.Cover,
		"web":         comicPageURL(info.Homepage, book.SourceID, book.ID),
		"updatedAt":   time.Now().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(comicDir, "book.json"), payload, 0o644)
}

// MylarSeries mirrors the narrow subset of Mylar's series.json that Komga
// imports. The outer version field is accepted by Mylar tooling and ignored by
// older readers.
type MylarSeries struct {
	Version  string        `json:"version,omitempty"`
	Metadata MylarMetadata `json:"metadata"`
}

type MylarMetadata struct {
	Type                 string   `json:"type"`
	Publisher            string   `json:"publisher"`
	Name                 string   `json:"name"`
	ComicID              string   `json:"comicid"`
	Year                 int      `json:"year,omitempty"`
	DescriptionText      string   `json:"description_text,omitempty"`
	DescriptionFormatted string   `json:"description_formatted,omitempty"`
	Volume               int      `json:"volume,omitempty"`
	BookType             string   `json:"booktype"`
	AgeRating            string   `json:"age_rating,omitempty"`
	ComicImage           string   `json:"comic_image"`
	TotalIssues          int      `json:"total_issues,omitempty"`
	PublicationRun       string   `json:"publication_run,omitempty"`
	Status               string   `json:"status"`
	Language             string   `json:"language,omitempty"`
	ReadingDirection     string   `json:"readingDirection,omitempty"`
	Authors              []string `json:"authors,omitempty"`
	Genres               []string `json:"genres,omitempty"`
	Tags                 []string `json:"tags,omitempty"`
}

// writeSeriesMetadataFiles writes the metadata formats understood by Komga:
// series.json is the Mylar series provider, metadata.opf mirrors the EPUB/OPF
// metadata model, and ComicInfo.xml remains the per-book format inside each CBZ.
func writeSeriesMetadataFiles(comic model.Comic, info model.SourceInfo, comicDir string) error {
	if err := os.MkdirAll(comicDir, 0o755); err != nil {
		return err
	}
	series := source.FirstNonEmpty(strings.TrimSpace(comic.Title), strings.TrimSpace(comic.ID), "untitled")
	genres := comicGenreValues(comic, info)
	description := strings.TrimSpace(comic.Description)
	ageRating := mylarAgeRating(comic, genres)
	status := mylarStatus(comic.Status)
	year := metadataYear(comic.UpdatedAt)
	authors := metadataAuthors(comic.Author)
	cover := localCoverName(comicDir)
	language := "zh-CN"
	readingDirection := "LEFT_TO_RIGHT"
	if source.KindOf(info) != source.KindBook {
		readingDirection = "RIGHT_TO_LEFT"
	}
	descriptionFormatted := description
	if descriptionFormatted != "" {
		descriptionFormatted = "<p>" + htmlEscape(descriptionFormatted) + "</p>"
	}
	payload, err := json.MarshalIndent(MylarSeries{
		Version: "1.0.1",
		Metadata: MylarMetadata{
			Type:                 "comicSeries",
			Publisher:            "",
			Name:                 series,
			ComicID:              strings.TrimSpace(comic.ID),
			Year:                 year,
			DescriptionText:      description,
			DescriptionFormatted: descriptionFormatted,
			Volume:               1,
			BookType:             "comic",
			AgeRating:            ageRating,
			ComicImage:           cover,
			Status:               status,
			Language:             language,
			ReadingDirection:     readingDirection,
			Authors:              authors,
			Genres:               genres,
			Tags:                 genres,
		},
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(comicDir, "series.json"), payload, 0o644); err != nil {
		return err
	}
	opf, err := marshalOPFMetadata(comic, info, series, authors, description, language, cover)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(comicDir, "metadata.opf"), opf, 0o644)
}

func comicGenreValues(comic model.Comic, info model.SourceInfo) []string {
	values := make([]string, 0, len(comic.Tags)+1)
	seen := map[string]bool{}
	for _, tag := range comic.Tags {
		text := strings.TrimSpace(tag)
		if text == "" || seen[text] {
			continue
		}
		seen[text] = true
		values = append(values, text)
	}
	label := "漫画"
	if source.KindOf(info) == source.KindBook {
		label = "书籍"
	}
	if !seen[label] {
		values = append(values, label)
	}
	return values
}

func metadataAuthors(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '；' || r == '/' || r == '、'
	})
	result := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		text := strings.TrimSpace(part)
		if text == "" || seen[text] {
			continue
		}
		seen[text] = true
		result = append(result, text)
	}
	return result
}

func adultComic(comic model.Comic, tags []string) bool {
	combined := strings.ToLower(strings.Join(append(append([]string{}, tags...), comic.Status, comic.Description), " "))
	for _, value := range []string{"成人", "adult", "18+", "r18", "x18", "mature", "色情"} {
		if strings.Contains(combined, value) {
			return true
		}
	}
	return false
}

func comicInfoAgeRating(comic model.Comic, tags []string) string {
	if adultComic(comic, tags) {
		return "Adults Only 18+"
	}
	return ""
}

func mylarAgeRating(comic model.Comic, tags []string) string {
	if adultComic(comic, tags) {
		return "Adult"
	}
	return ""
}

func mylarStatus(value string) string {
	text := strings.ToLower(strings.TrimSpace(value))
	switch {
	case text == "", text == "ongoing", text == "连载", text == "连载中", text == "連載", text == "連載中", text == "连載", text == "连載中":
		return "Continuing"
	case strings.Contains(text, "完"), text == "completed", text == "complete", text == "finished", text == "ended":
		return "Ended"
	default:
		return "Continuing"
	}
}

func metadataYear(value string) int {
	value = strings.TrimSpace(value)
	if len(value) >= 4 {
		if year, err := strconv.Atoi(value[:4]); err == nil && year >= 1000 && year <= 9999 {
			return year
		}
	}
	return 0
}

func localCoverName(comicDir string) string {
	covers, _ := filepath.Glob(filepath.Join(comicDir, "cover.*"))
	if len(covers) == 0 {
		return ""
	}
	sort.Strings(covers)
	return filepath.Base(covers[0])
}

func htmlEscape(value string) string {
	var buffer bytes.Buffer
	if err := xml.EscapeText(&buffer, []byte(value)); err != nil {
		return value
	}
	return buffer.String()
}

func marshalOPFMetadata(comic model.Comic, info model.SourceInfo, series string, authors []string, description, language, cover string) ([]byte, error) {
	identifier := strings.TrimSpace(comic.ID)
	if identifier == "" {
		identifier = series
	}
	var builder strings.Builder
	builder.WriteString(xml.Header)
	builder.WriteString(`<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0" unique-identifier="manco-id">`)
	builder.WriteString("\n  <metadata>\n")
	builder.WriteString("    <dc:identifier id=\"manco-id\">" + htmlEscape(identifier) + "</dc:identifier>\n")
	builder.WriteString("    <dc:title>" + htmlEscape(series) + "</dc:title>\n")
	for _, author := range authors {
		author = strings.TrimSpace(author)
		if author != "" {
			builder.WriteString("    <dc:creator>" + htmlEscape(author) + "</dc:creator>\n")
		}
	}
	if description = strings.TrimSpace(description); description != "" {
		builder.WriteString("    <dc:description>" + htmlEscape(description) + "</dc:description>\n")
	}
	builder.WriteString("    <dc:language>" + htmlEscape(language) + "</dc:language>\n")
	if date := metadataDate(comic.UpdatedAt); date != "" {
		builder.WriteString("    <dc:date>" + htmlEscape(date) + "</dc:date>\n")
	}
	builder.WriteString(`    <meta property="belongs-to-collection">` + htmlEscape(series) + "</meta>\n")
	builder.WriteString("  </metadata>\n")
	direction := "rtl"
	if source.KindOf(info) == source.KindBook {
		direction = "ltr"
	}
	builder.WriteString(`  <spine page-progression-direction="` + direction + `"></spine>`)
	builder.WriteString("\n</package>\n")
	return []byte(builder.String()), nil
}

func metadataDate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 10 {
		return value[:10]
	}
	return value
}

// EnsureSeriesMetadata verifies that a subscribed series still has its
// series-level cover and metadata files on disk and re-fetches them when one
// is missing. It returns true when at least one file had to be recreated.
// Only series directories that already contain downloaded entries are
// considered, so a freshly created subscription is left untouched.
func (e *Engine) EnsureSeriesMetadata(ctx context.Context, subscription model.Subscription) (bool, error) {
	if strings.TrimSpace(subscription.SourceID) == "" || strings.TrimSpace(subscription.ComicID) == "" {
		return false, nil
	}
	root, err := e.jobDownloadDir(model.DownloadJob{DownloadDir: subscription.DownloadDir})
	if err != nil {
		return false, err
	}
	series := strings.TrimSpace(subscription.SeriesDir)
	if series == "" {
		series = subscription.Title
	}
	comicDir := filepath.Join(root, SeriesDirectoryName(series, subscription.ConvertToSimplified))
	entries, err := os.ReadDir(comicDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if len(entries) == 0 {
		return false, nil
	}
	item, err := e.registry.Get(subscription.SourceID)
	if err != nil {
		return false, err
	}
	book := source.KindOf(item.Info()) == source.KindBook
	if seriesMetadataCurrent(comicDir, book) {
		if covers, _ := filepath.Glob(filepath.Join(comicDir, "cover.*")); len(covers) > 0 {
			return false, nil
		}
	}
	account, err := e.registry.Account(ctx, subscription.SourceID)
	if err != nil {
		return false, err
	}
	metadata := model.Comic{
		SourceID: subscription.SourceID,
		ID:       subscription.ComicID,
		Title:    subscription.Title,
		Author:   subscription.Author,
		Cover:    subscription.Cover,
	}
	if detail, detailErr := item.Detail(ctx, account, subscription.ComicID); detailErr == nil {
		metadata = detail
		metadata.SourceID = source.FirstNonEmpty(detail.SourceID, metadata.SourceID)
		metadata.ID = source.FirstNonEmpty(detail.ID, metadata.ID)
		metadata.Title = source.FirstNonEmpty(detail.Title, metadata.Title)
		metadata.Author = source.FirstNonEmpty(detail.Author, metadata.Author)
		metadata.Cover = source.FirstNonEmpty(detail.Cover, metadata.Cover)
	} else {
		e.logger.Printf("downloader: metadata detail %s/%s: %v", subscription.SourceID, subscription.ComicID, detailErr)
	}
	if subscription.ConvertToSimplified {
		metadata.Title = convertToSimplifiedText(metadata.Title)
		metadata.Author = convertToSimplifiedText(metadata.Author)
		metadata.Description = convertToSimplifiedText(metadata.Description)
		metadata.Status = convertToSimplifiedText(metadata.Status)
		for index := range metadata.Tags {
			metadata.Tags[index] = convertToSimplifiedText(metadata.Tags[index])
		}
	}
	client := e.registry.ClientForAccount(account)
	if client == nil {
		client = source.NewHTTPClient()
	}
	e.metaMu.Lock()
	defer e.metaMu.Unlock()
	if book {
		if err := writeBookMetadata(ctx, client, account, item, metadata, comicDir); err != nil {
			return false, err
		}
	} else if _, err := writeComicMetadata(ctx, client, account, item, metadata, comicDir, "", 0, 0); err != nil {
		return false, err
	}
	return true, nil
}

// seriesMetadataCurrent reports whether the Komga-facing metadata files exist.
// Mylar series.json is the authoritative series descriptor, while metadata.opf
// covers EPUB/OPF consumers. Legacy folders can still be repaired in place.
func seriesMetadataCurrent(comicDir string, book bool) bool {
	if _, err := os.Stat(filepath.Join(comicDir, "series.json")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(comicDir, "metadata.opf")); err != nil {
		return false
	}
	legacy := "ComicInfo.xml"
	if book {
		legacy = "book.json"
	}
	_, err := os.Stat(filepath.Join(comicDir, legacy))
	return err == nil
}

// comicGenre merges source-provided tags with the media type so the rendered
// ComicInfo.xml always records whether the entry is a comic or a book.
func comicGenre(comic model.Comic, info model.SourceInfo) string {
	return strings.Join(comicGenreValues(comic, info), ",")
}

func writeComicMetadata(ctx context.Context, client *http.Client, account source.Account, item source.Source, comic model.Comic, comicDir, chapterTitle string, chapterOrder float64, pageCount int) ([]byte, error) {
	if err := os.MkdirAll(comicDir, 0o755); err != nil {
		return nil, err
	}
	coverURL := strings.TrimSpace(comic.Cover)
	if coverURL != "" || canMaterializeCover(item, coverURL) {
		existing, _ := filepath.Glob(filepath.Join(comicDir, "cover.*"))
		if len(existing) == 0 {
			data, err := fetchCoverImage(ctx, client, account, item, comic)
			if err != nil {
				if _, pluginSource := item.(source.ImageSource); !pluginSource {
					return nil, fmt.Errorf("cover: %w", err)
				}
			} else {
				coverPath := filepath.Join(comicDir, "cover"+imageExtension(data, coverURL))
				if err := os.WriteFile(coverPath, data, 0o644); err != nil {
					return nil, err
				}
			}
		}
	}
	chapterDoc := chapterComicInfo(item, comic, chapterTitle, chapterOrder, pageCount)
	payload, err := marshalComicInfo(chapterDoc)
	if err != nil {
		return nil, err
	}
	// The standalone ComicInfo.xml describes the series, so it must not depend
	// on whichever chapter finished last. Chapter-level fields stay in the
	// per-chapter ComicInfo.xml embedded in each CBZ.
	seriesDoc := chapterDoc
	seriesDoc.Title = source.FirstNonEmpty(comic.Title, comic.ID)
	seriesDoc.Number = ""
	seriesDoc.PageCount = 0
	seriesDoc.Pages = nil
	seriesPayload, err := marshalComicInfo(seriesDoc)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(comicDir, "ComicInfo.xml"), seriesPayload, 0o644); err != nil {
		return nil, err
	}
	// Mylar/OPF sidecars are written after the cover attempt so series.json can
	// point at the local cover filename that Komga should use.
	if err := writeSeriesMetadataFiles(comic, item.Info(), comicDir); err != nil {
		return nil, err
	}
	return payload, nil
}

func chapterComicInfo(item source.Source, comic model.Comic, chapterTitle string, chapterOrder float64, pageCount int) ComicInfo {
	info := item.Info()
	manga := "YesAndRightToLeft"
	if source.KindOf(info) == source.KindBook {
		manga = "No"
	}
	genres := comicGenreValues(comic, info)
	tags := genres
	return ComicInfo{
		Title:       source.FirstNonEmpty(chapterTitle, comic.Title, comic.ID),
		Series:      source.FirstNonEmpty(comic.Title, comic.ID),
		Number:      comicNumber(chapterOrder),
		Summary:     comic.Description,
		Notes:       fmt.Sprintf("Manco source=%s comicId=%s", comic.SourceID, comic.ID),
		Year:        metadataYear(comic.UpdatedAt),
		Writer:      comic.Author,
		Publisher:   "",
		Genre:       strings.Join(genres, ","),
		Tags:        strings.Join(tags, ","),
		Web:         comicPageURL(info.Homepage, comic.SourceID, comic.ID),
		PageCount:   pageCount,
		LanguageISO: "zh-Hans",
		Manga:       manga,
		AgeRating:   comicInfoAgeRating(comic, tags),
		Pages:       comicInfoPages(pageCount),
	}
}

func comicInfoPages(pageCount int) *ComicInfoPages {
	if pageCount <= 0 {
		return nil
	}
	pages := &ComicInfoPages{Page: make([]ComicInfoPage, pageCount)}
	for index := range pages.Page {
		pages.Page[index] = ComicInfoPage{Image: index, Type: "Story"}
	}
	return pages
}

// marshalComicInfo renders a ComicInfo document with the XML header.
func marshalComicInfo(doc ComicInfo) ([]byte, error) {
	payload, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), payload...), nil
}

func comicNumber(order float64) string {
	if order <= 0 {
		return ""
	}
	if order == float64(int64(order)) {
		return strconv.FormatInt(int64(order), 10)
	}
	return strconv.FormatFloat(order, 'f', -1, 64)
}

// comicPageURL builds a best-effort link back to the source detail page.
func comicPageURL(homepage, sourceID, comicID string) string {
	base := strings.TrimRight(strings.TrimSpace(homepage), "/")
	if base == "" {
		return ""
	}
	switch sourceID {
	case "jmcomic":
		return base + "/album/" + comicID
	case "picacg":
		return base + "/comic/" + comicID
	case "baozimh":
		return base + "/comic/" + comicID
	default:
		return base
	}
}

func fetchPageImage(ctx context.Context, client *http.Client, account source.Account, item source.Source, comicID string, chapter model.Chapter, page model.Page) ([]byte, bool, error) {
	if materializer, ok := item.(source.ImageSource); ok {
		data, _, err := materializer.PageImage(ctx, account, comicID, chapter, page)
		return data, true, err
	}
	data, err := fetchImage(ctx, client, account, page)
	return data, false, err
}

func fetchCoverImage(ctx context.Context, client *http.Client, account source.Account, item source.Source, comic model.Comic) ([]byte, error) {
	if materializer, ok := item.(source.ImageSource); ok {
		data, _, err := materializer.CoverImage(ctx, account, comic)
		return data, err
	}
	return fetchImage(ctx, client, account, model.Page{URL: comic.Cover, Referer: item.Info().Homepage})
}

func canMaterializeCover(item source.Source, coverURL string) bool {
	if strings.TrimSpace(coverURL) != "" {
		return true
	}
	_, ok := item.(source.ImageSource)
	return ok
}

func fetchImage(ctx context.Context, client *http.Client, account source.Account, page model.Page) ([]byte, error) {
	candidates := append([]string{page.URL}, page.Alternatives...)
	// 部分站点（禁漫天堂等）会按 User-Agent 做防盗链，优先用来源账号配置的
	// UA；若该 UA 被 CDN 拒绝，再回退到库默认浏览器 UA。
	userAgents := make([]string, 0, 2)
	if ua := strings.TrimSpace(account.UserAgent); ua != "" {
		userAgents = append(userAgents, ua)
	}
	if len(userAgents) == 0 || userAgents[0] != source.DefaultUserAgent {
		userAgents = append(userAgents, source.DefaultUserAgent)
	}
	var lastErr error
	for _, address := range candidates {
		if strings.TrimSpace(address) == "" {
			continue
		}
		for _, userAgent := range userAgents {
			data, err := requestImage(ctx, client, account, page, address, userAgent)
			if err != nil {
				lastErr = err
				continue
			}
			return data, nil
		}
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的图片地址")
	}
	return nil, lastErr
}

func requestImage(ctx context.Context, client *http.Client, account source.Account, page model.Page, address, userAgent string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.6")
	if page.Referer != "" {
		request.Header.Set("Referer", page.Referer)
	}
	for key, value := range page.Headers {
		request.Header.Set(key, value)
	}
	for key, value := range source.HeaderCookie(account.Cookie) {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("空响应")
	}
	return data, nil
}

func writeZip(path string, entries []pageEntry) error {
	temp := path + ".part"
	file, err := os.Create(temp)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate, Modified: time.Now()}
		handle, err := writer.CreateHeader(header)
		if err != nil {
			_ = writer.Close()
			_ = file.Close()
			_ = os.Remove(temp)
			return err
		}
		source, err := os.Open(entry.path)
		if err != nil {
			_ = writer.Close()
			_ = file.Close()
			_ = os.Remove(temp)
			return err
		}
		_, copyErr := io.Copy(handle, source)
		_ = source.Close()
		if copyErr != nil {
			_ = writer.Close()
			_ = file.Close()
			_ = os.Remove(temp)
			return copyErr
		}
	}
	if err := writer.Close(); err != nil {
		_ = file.Close()
		_ = os.Remove(temp)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return os.Rename(temp, path)
}

func imageExtension(data []byte, address string) string {
	if extension := detectImageExtension(data); extension != "" {
		return extension
	}
	if index := strings.LastIndex(address, "."); index >= 0 {
		extension := strings.ToLower(address[index:])
		if mark := strings.IndexAny(extension, "?#"); mark >= 0 {
			extension = extension[:mark]
		}
		switch extension {
		case ".jpg", ".jpeg", ".png", ".gif", ".webp":
			return extension
		}
	}
	return ".jpg"
}

func detectImageExtension(data []byte) string {
	if len(data) < 4 {
		return ""
	}
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return ".jpg"
	case bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G'}):
		return ".png"
	case bytes.HasPrefix(data, []byte("GIF8")):
		return ".gif"
	case len(data) >= 12 && bytes.Equal(data[0:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return ".webp"
	}
	return ""
}

func decodeImage(data []byte) (image.Image, error) {
	if detectImageExtension(data) == ".webp" {
		return webp.Decode(bytes.NewReader(data))
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	return decoded, err
}

// Descramble restores an 18comic image whose horizontal strips have been
// rotated. parts is the strip count reported by the source adapter.
func Descramble(data []byte, parts int) ([]byte, error) {
	if parts <= 1 {
		return data, nil
	}
	decoded, err := decodeImage(data)
	if err != nil {
		return nil, err
	}
	bounds := decoded.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, errors.New("图片尺寸无效")
	}
	if parts > height {
		parts = height
	}
	output := image.NewRGBA(image.Rect(0, 0, width, height))
	over := height % parts
	for index := 0; index < parts; index++ {
		move := height / parts
		ySource := height - move*(index+1) - over
		yDest := move * index
		if index == 0 {
			move += over
		} else {
			yDest += over
		}
		if move <= 0 {
			continue
		}
		sourcePoint := image.Pt(bounds.Min.X, bounds.Min.Y+ySource)
		destination := image.Rect(0, yDest, width, yDest+move)
		draw.Draw(output, destination, decoded, sourcePoint, draw.Src)
	}
	format := detectImageExtension(data)
	buffer := &bytes.Buffer{}
	switch format {
	case ".png", ".webp":
		err = png.Encode(buffer, output)
	default:
		err = jpeg.Encode(buffer, output, &jpeg.Options{Quality: 92})
	}
	if err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

var unsafeNamePattern = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]+`)

// SeriesDirectoryName returns the stable parent folder for a subscription.
// The name is converted once when the subscription is created or edited, then
// reused for every later chapter so traditional and simplified names never
// split one work into two folders.
func SeriesDirectoryName(title string, convertToSimplified bool) string {
	if convertToSimplified {
		title = convertToSimplifiedText(title)
	}
	return SafeName(title)
}

// ChapterFileName builds the stable chapter file name. When marker is enabled,
// the chapter order is prefixed as #1, #2, ... so readers and file managers can
// sort downloaded chapters without relying on the source title text.
func ChapterFileName(title string, order float64, marker bool, extension string) string {
	name := SafeName(title)
	if marker && order > 0 {
		orderText := strconv.FormatFloat(order, 'f', -1, 64)
		name = "#" + orderText + " " + name
	}
	return name + extension
}

// SafeName converts a title into a portable file name.
func SafeName(value string) string {
	value = strings.TrimSpace(value)
	value = unsafeNamePattern.ReplaceAllString(value, "_")
	value = strings.Trim(value, " .")
	if value == "" {
		value = "untitled"
	}
	if len(value) > 120 {
		value = strings.TrimSpace(value[:120])
	}
	return value
}

// SortChapters orders chapters from oldest to newest by their numeric order.
func SortChapters(chapters []model.Chapter) {
	sort.SliceStable(chapters, func(i, j int) bool {
		if chapters[i].Order == chapters[j].Order {
			return chapters[i].ID < chapters[j].ID
		}
		return chapters[i].Order < chapters[j].Order
	})
}
