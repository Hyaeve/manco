// Package downloader turns queued chapter jobs into library files. Comic image
// chapters become CBZ, books become EPUB, and other-source files preserve the
// content type reported by the source. Temporary data is written outside the
// final library path so an interrupted download cannot leave a half-written
// chapter behind.
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
	"mime"
	"net/http"
	"net/url"
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
	nethtml "golang.org/x/net/html"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/sources"
)

type Engine struct {
	registry    *sources.Registry
	store       Jobs
	downloadDir string
	cacheDir    string
	// maxChapterConcurrency 是**全局**同时下载章节数上限；单个内容源另有
	// defaultSourceConcurrency（默认值）与 sourceConcurrency（显式覆盖）。
	// 之前全局上限被写成「各来源上限之和」，来源一多就等于没有上限。
	maxChapterConcurrency    int
	defaultSourceConcurrency int
	maxPageConcurrency       int
	logger                   *log.Logger
	sourceConcurrency        map[string]int
	sourcePageConcurrency    map[string]int
	sourceBatchSize          map[string]int
	sourceBatchInterval      map[string]time.Duration
	sourceBatchCompleted     map[string]int
	sourceCooldownUntil      map[string]time.Time
	batchSize                int
	batchInterval            time.Duration
	batchCompleted           int
	cooldownUntil            time.Time
	convertToSimplified      bool
	activeTotal              int
	activeBySource           map[string]int

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
	// 全局上限就是设置里的章节并发数；单个来源默认也按这个值限流，
	// 但每个来源可以单独覆盖，且不会再被乘上来源数量。
	e.maxChapterConcurrency = chapters
	e.defaultSourceConcurrency = chapters
	e.mu.Unlock()
	e.Notify()
}

// SetSourceConcurrency applies per-source chapter limits.
func (e *Engine) SetSourceConcurrency(limits map[string]int) {
	e.mu.Lock()
	for sourceID, limit := range limits {
		if limit < 1 {
			limit = 1
		}
		e.sourceConcurrency[sourceID] = limit
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
	FailedDownloadJobsByComic(ctx context.Context, sourceID, comicID string) ([]model.DownloadJob, error)
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
		registry:                 registry,
		store:                    store,
		downloadDir:              downloadDir,
		cacheDir:                 filepath.Join(os.TempDir(), "manco-download-cache"),
		maxChapterConcurrency:    maxChapterConcurrency,
		defaultSourceConcurrency: maxChapterConcurrency,
		maxPageConcurrency:       maxPageConcurrency,
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

// RetryFailedByComic restarts the automatic retry budget for failed or
// canceled chapters of one work. The scheduler uses it on cron checks so a
// later scheduled run can recover failures that exhausted their first budget.
func (e *Engine) RetryFailedByComic(ctx context.Context, sourceID, comicID string) (int, error) {
	jobs, err := e.store.FailedDownloadJobsByComic(ctx, sourceID, comicID)
	if err != nil {
		return 0, err
	}
	retried := 0
	var firstErr error
	for _, job := range jobs {
		if err := e.store.ResetDownloadJob(ctx, job.ID); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			e.logger.Printf("downloader: reset failed job %d: %v", job.ID, err)
			continue
		}
		retried++
	}
	if retried > 0 {
		e.Notify()
	}
	return retried, firstErr
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
	// 单个来源的上限：显式覆盖优先，否则用全局默认值。这样即使某个来源
	// 没有单独配置，也不会把全局并发吃满。
	limit := e.sourceConcurrency[job.SourceID]
	if limit <= 0 {
		limit = e.defaultSourceConcurrency
	}
	if limit > 0 && e.activeBySource[job.SourceID] >= limit {
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
	// 全局批量暂停：所有内容源加起来每下载 N 章整体停 M 分钟。
	if e.batchSize > 0 && time.Now().Before(e.cooldownUntil) {
		return true
	}
	batchSize := e.batchSize
	if limit, ok := e.sourceBatchSize[sourceID]; ok {
		batchSize = limit
	}
	return batchSize > 0 && time.Now().Before(e.sourceCooldownUntil[sourceID])
}

// GlobalCooldown 返回全局批量暂停的截止时间（未暂停时为零值）。任务列表用它
// 把排队中的章节显示成「等待中」。
func (e *Engine) GlobalCooldown() time.Time {
	if e == nil {
		return time.Time{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.batchSize <= 0 {
		return time.Time{}
	}
	return e.cooldownUntil
}

// markCompleted 把任务标记为完成，并在成功后记一次批量暂停计数。
func (e *Engine) markCompleted(ctx context.Context, job model.DownloadJob, total, done int, outputPath string) error {
	err := e.store.UpdateDownloadJob(ctx, job.ID, "completed", total, done, outputPath, "")
	if err == nil {
		e.noteBatchCompletion(job.SourceID)
	}
	return err
}

// noteBatchCompletion 记录一次章节完成：全局计数对所有内容源累加，达到阈值就
// 整体暂停；同时维护每个内容源自己的计数（若该源单独配了批量暂停）。
// 这两个计数器以前从来没被累加过，所以「每 N 章暂停 M 分钟」一直空转。
func (e *Engine) noteBatchCompletion(sourceID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	e.batchCompleted++
	if e.batchSize > 0 && e.batchCompleted >= e.batchSize {
		e.batchCompleted = 0
		if e.batchInterval > 0 {
			e.cooldownUntil = now.Add(e.batchInterval)
			// 暂停结束后主动唤醒一次，不用等 20 秒轮询才发现可以继续。
			go func(delay time.Duration) {
				time.Sleep(delay + time.Second)
				e.Notify()
			}(e.batchInterval)
		}
	}
	if limit := e.sourceBatchSize[sourceID]; limit > 0 {
		e.sourceBatchCompleted[sourceID]++
		if e.sourceBatchCompleted[sourceID] >= limit {
			e.sourceBatchCompleted[sourceID] = 0
			interval := e.batchInterval
			if custom, ok := e.sourceBatchInterval[sourceID]; ok {
				interval = custom
			}
			if interval > 0 {
				e.sourceCooldownUntil[sourceID] = now.Add(interval)
			}
		}
	}
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
	if source.KindOf(item.Info()) == source.KindOther {
		if len(job.MergeChapters) > 1 {
			return errors.New("其他源不支持合并下载，请按章节保留来源文件")
		}
		return e.downloadOther(ctx, item, account, job)
	}
	if len(job.MergeChapters) > 1 {
		if source.KindOf(item.Info()) == source.KindBook {
			return e.downloadMergedBook(ctx, item, account, job)
		}
		return e.downloadMergedComic(ctx, item, account, job)
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
	metadata.Title = SafeName(comicTitle)
	comicDir := filepath.Join(downloadRoot, SafeName(comicTitle))
	e.metaMu.Lock()
	infoPayload, metaErr := writeComicMetadata(ctx, client, account, item, metadata, comicDir, chapterTitle, job.ChapterOrder, len(pages), countSeriesBookFiles(comicDir))
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
	// Metadata is written before the CBZ exists so an interrupted download does
	// not leave a half-written archive. Refresh the series count once the CBZ is
	// in place; this also prevents concurrent chapter writes from racing to a
	// stale total_issues value.
	e.metaMu.Lock()
	seriesErr := writeSeriesMetadataFiles(metadata, item.Info(), comicDir)
	e.metaMu.Unlock()
	if seriesErr != nil {
		e.logger.Printf("downloader: series metadata %s/%s: %v", job.SourceID, job.ComicID, seriesErr)
	}
	if err := e.markCompleted(ctx, job, len(pages), len(pages), outputPath); err != nil {
		return err
	}
	finished = true
	return nil
}

// downloadMergedComic packages the selected chapters into one CBZ. Page names
// are prefixed by chapter number so readers and archive tools show the exact
// chapter-then-page order requested by the user.
func (e *Engine) downloadMergedComic(ctx context.Context, item source.Source, account source.Account, job model.DownloadJob) error {
	chapters := append([]model.Chapter(nil), job.MergeChapters...)
	SortChapters(chapters)
	plans := make([]mergedChapterPlan, 0, len(chapters))
	totalPages := 0
	for _, chapter := range chapters {
		pages, err := item.Pages(ctx, account, job.ComicID, chapter)
		if err != nil {
			return fmt.Errorf("章节 %s 读取图片失败: %w", chapter.Title, err)
		}
		if len(pages) == 0 {
			return fmt.Errorf("章节 %s 没有可用图片", chapter.Title)
		}
		plans = append(plans, mergedChapterPlan{chapter: chapter, pages: pages})
		totalPages += len(pages)
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
	completed := countMergedCachedPages(tempDir, plans)
	if err := e.store.UpdateDownloadJob(ctx, job.ID, "running", totalPages, completed, "", ""); err != nil {
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
	entries := make([]pageEntry, 0, totalPages)
	for chapterIndex, plan := range plans {
		for pageIndex, page := range plan.pages {
			cachePath := mergedStagingPagePath(tempDir, chapterIndex, pageIndex)
			if info, statErr := os.Stat(cachePath); statErr != nil || !info.Mode().IsRegular() || info.Size() == 0 {
				data, materialized, fetchErr := fetchPageImage(ctx, client, account, item, job.ComicID, plan.chapter, page)
				if fetchErr != nil {
					return fmt.Errorf("章节 %s 第 %d 页下载失败: %w", plan.chapter.Title, pageIndex+1, fetchErr)
				}
				if page.DescrambleParts > 0 && !materialized {
					data, fetchErr = Descramble(data, page.DescrambleParts)
					if fetchErr != nil {
						return fmt.Errorf("章节 %s 第 %d 页解扰失败: %w", plan.chapter.Title, pageIndex+1, fetchErr)
					}
				}
				if len(data) == 0 {
					return fmt.Errorf("章节 %s 第 %d 页为空", plan.chapter.Title, pageIndex+1)
				}
				if err := writeFileAtomic(cachePath, data); err != nil {
					return err
				}
				completed++
				if err := e.store.UpdateDownloadProgress(ctx, job.ID, completed); err != nil {
					return err
				}
			}
			extension, extensionErr := imageExtensionFromFile(cachePath, page.URL)
			if extensionErr != nil {
				return fmt.Errorf("章节 %s 第 %d 页缓存无效: %w", plan.chapter.Title, pageIndex+1, extensionErr)
			}
			entries = append(entries, pageEntry{
				name: fmt.Sprintf("%03d-%04d%s", chapterIndex+1, pageIndex+1, extension),
				path: cachePath,
			})
		}
	}

	metadata := model.Comic{
		SourceID:    job.SourceID,
		ID:          job.ComicID,
		Title:       job.ComicTitle,
		Cover:       job.ComicCover,
		Author:      job.ComicAuthor,
		Description: job.ComicDescription,
		Tags:        append([]string(nil), job.ComicTags...),
	}
	if detail, detailErr := item.Detail(ctx, account, job.ComicID); detailErr == nil {
		metadata = detail
		metadata.SourceID = source.FirstNonEmpty(detail.SourceID, metadata.SourceID)
		metadata.ID = source.FirstNonEmpty(detail.ID, metadata.ID)
		metadata.Title = source.FirstNonEmpty(detail.Title, metadata.Title)
		metadata.Cover = source.FirstNonEmpty(detail.Cover, metadata.Cover)
		metadata.Author = source.FirstNonEmpty(detail.Author, metadata.Author)
		metadata.Description = source.FirstNonEmpty(detail.Description, metadata.Description)
		if len(metadata.Tags) == 0 {
			metadata.Tags = append([]string(nil), job.ComicTags...)
		}
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
	metadata.Title = SafeName(comicTitle)
	comicDir := filepath.Join(downloadRoot, SafeName(comicTitle))
	e.metaMu.Lock()
	infoPayload, metaErr := writeComicMetadata(ctx, client, account, item, metadata, comicDir, chapterTitle, job.ChapterOrder, totalPages, countSeriesBookFiles(comicDir)+1)
	e.metaMu.Unlock()
	if metaErr != nil {
		return fmt.Errorf("download merged metadata: %w", metaErr)
	}
	if len(infoPayload) > 0 {
		infoPath := filepath.Join(tempDir, "ComicInfo.xml")
		if err := writeFileAtomic(infoPath, infoPayload); err != nil {
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
	e.metaMu.Lock()
	seriesErr := writeSeriesMetadataFiles(metadata, item.Info(), comicDir)
	e.metaMu.Unlock()
	if seriesErr != nil {
		e.logger.Printf("downloader: merged series metadata %s/%s: %v", job.SourceID, job.ComicID, seriesErr)
	}
	if err := e.markCompleted(ctx, job, totalPages, totalPages, outputPath); err != nil {
		return err
	}
	finished = true
	return nil
}

// downloadMergedBook supports Kototoro-style archive books. HTML and images
// from every selected chapter are packed into one CBZ in chapter order.
func (e *Engine) downloadMergedBook(ctx context.Context, item source.Source, account source.Account, job model.DownloadJob) error {
	archiveSource, ok := item.(source.ChapterArchiveSource)
	if !ok {
		return errors.New("该书籍源不支持合并下载")
	}
	chapters := append([]model.Chapter(nil), job.MergeChapters...)
	SortChapters(chapters)
	downloadRoot, err := e.jobDownloadDir(job)
	if err != nil {
		return err
	}
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
	metadata := model.Comic{
		SourceID:    job.SourceID,
		ID:          job.ComicID,
		Title:       job.ComicTitle,
		Cover:       job.ComicCover,
		Author:      job.ComicAuthor,
		Description: job.ComicDescription,
		Tags:        append([]string(nil), job.ComicTags...),
	}
	if detail, detailErr := item.Detail(ctx, account, job.ComicID); detailErr == nil {
		metadata = detail
		metadata.SourceID = source.FirstNonEmpty(detail.SourceID, metadata.SourceID)
		metadata.ID = source.FirstNonEmpty(detail.ID, metadata.ID)
		metadata.Title = source.FirstNonEmpty(detail.Title, metadata.Title)
		metadata.Cover = source.FirstNonEmpty(detail.Cover, metadata.Cover)
		metadata.Author = source.FirstNonEmpty(detail.Author, metadata.Author)
		metadata.Description = source.FirstNonEmpty(detail.Description, metadata.Description)
		if len(metadata.Tags) == 0 {
			metadata.Tags = append([]string(nil), job.ComicTags...)
		}
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
	metadata.Title = SafeName(comicTitle)
	comicDir := filepath.Join(downloadRoot, SafeName(comicTitle))
	outputPath := filepath.Join(comicDir, ChapterFileName(chapterTitle, job.ChapterOrder, job.ChapterMarker, ".epub"))
	epubChapters := make([]epubChapter, 0, len(chapters))
	epubImages := make([]pageEntry, 0, len(chapters)*8)

	totalPages := 0
	completed := 0
	for chapterIndex, chapter := range chapters {
		content, fetchErr := archiveSource.ChapterArchive(ctx, account, job.ComicID, chapter)
		if fetchErr != nil {
			return fmt.Errorf("章节 %s 获取正文失败: %w", chapter.Title, fetchErr)
		}
		if strings.TrimSpace(content.HTML) == "" && len(content.Images) == 0 {
			return fmt.Errorf("章节 %s 没有可用内容", chapter.Title)
		}
		imageNames := make(map[string]string, len(content.Images))
		for pageIndex, page := range content.Images {
			if strings.TrimSpace(page.URL) == "" {
				continue
			}
			data, materialized, pageErr := fetchPageImage(ctx, client, account, item, job.ComicID, chapter, page)
			if pageErr != nil {
				return fmt.Errorf("章节 %s 第 %d 张插图下载失败: %w", chapter.Title, pageIndex+1, pageErr)
			}
			if page.DescrambleParts > 0 && !materialized {
				data, pageErr = Descramble(data, page.DescrambleParts)
				if pageErr != nil {
					return fmt.Errorf("章节 %s 第 %d 张插图解扰失败: %w", chapter.Title, pageIndex+1, pageErr)
				}
			}
			if len(data) == 0 {
				return fmt.Errorf("章节 %s 第 %d 张插图为空", chapter.Title, pageIndex+1)
			}
			name := fmt.Sprintf("%03d-%04d%s", chapterIndex+1, pageIndex+1, imageExtension(data, page.URL))
			path := filepath.Join(tempDir, name)
			if err := writeFileAtomic(path, data); err != nil {
				return err
			}
			epubName := "images/" + name
			epubImages = append(epubImages, pageEntry{name: epubName, path: path})
			imageNames[page.URL] = epubName
			totalPages++
			completed++
			_ = e.store.UpdateDownloadProgress(ctx, job.ID, completed)
		}
		html, rewriteErr := rewriteChapterArchiveHTML(content.HTML, imageNames)
		if rewriteErr != nil {
			return fmt.Errorf("章节 %s 正文处理失败: %w", chapter.Title, rewriteErr)
		}
		epubChapters = append(epubChapters, epubChapter{Title: chapter.Title, HTML: html})
		totalPages++
		completed++
	}
	if err := e.store.UpdateDownloadJob(ctx, job.ID, "running", totalPages, completed, "", ""); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	if err := writeEPUB(outputPath, metadata, comicPageURL(item.Info().Homepage, metadata.SourceID, metadata.ID), epubChapters, epubImages); err != nil {
		return err
	}
	e.metaMu.Lock()
	metaErr := writeBookMetadata(ctx, client, account, item, metadata, comicDir)
	e.metaMu.Unlock()
	if metaErr != nil {
		e.logger.Printf("downloader: merged book metadata %s/%s: %v", job.SourceID, job.ComicID, metaErr)
	}
	if err := e.markCompleted(ctx, job, totalPages, totalPages, outputPath); err != nil {
		return err
	}
	finished = true
	return nil
}

func mergedStagingPagePath(dir string, chapterIndex, pageIndex int) string {
	return filepath.Join(dir, fmt.Sprintf("%03d-%04d.bin", chapterIndex+1, pageIndex+1))
}

type mergedChapterPlan struct {
	chapter model.Chapter
	pages   []model.Page
}

func countMergedCachedPages(dir string, plans []mergedChapterPlan) int {
	count := 0
	for chapterIndex := range plans {
		for pageIndex := range plans[chapterIndex].pages {
			info, err := os.Stat(mergedStagingPagePath(dir, chapterIndex, pageIndex))
			if err == nil && info.Mode().IsRegular() && info.Size() > 0 {
				count++
			}
		}
	}
	return count
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

// downloadBook persists one EPUB per chapter for book sources. Native EPUB
// files are kept unchanged; plain text and archive-style chapters are packaged
// into EPUB, alongside the shared metadata sidecars.
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
		if extension := bookFileExtension(filename); extension == ".txt" {
			text = string(data)
			isText = true
			data = nil
			filename = ""
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
	metadata.Title = SafeName(comicTitle)
	comicDir := filepath.Join(downloadRoot, SafeName(comicTitle))
	if err := os.MkdirAll(comicDir, 0o755); err != nil {
		return err
	}
	outputName := chapterTitle
	if !isText {
		if base := bookFileBaseName(filename); base != "" {
			outputName = base
		}
	}
	outputPath := filepath.Join(comicDir, ChapterFileName(outputName, job.ChapterOrder, job.ChapterMarker, ".epub"))
	if isText {
		if err := writeEPUB(
			outputPath,
			metadata,
			comicPageURL(item.Info().Homepage, metadata.SourceID, metadata.ID),
			[]epubChapter{{Title: chapterTitle, HTML: plainTextToXHTML(text)}},
			nil,
		); err != nil {
			return err
		}
	} else {
		extension := bookFileExtension(filename)
		if !strings.EqualFold(extension, ".epub") {
			if extension == "" {
				extension = "未知"
			}
			return fmt.Errorf("该书源返回了非 EPUB 格式（%s），当前书籍下载仅支持 EPUB", extension)
		}
		if err := writeFileAtomic(outputPath, data); err != nil {
			return err
		}
	}
	e.metaMu.Lock()
	metaErr := writeBookMetadata(ctx, e.registry.ClientForAccount(account), account, item, metadata, comicDir)
	e.metaMu.Unlock()
	if metaErr != nil {
		e.logger.Printf("downloader: book metadata %s/%s: %v", job.SourceID, job.ComicID, metaErr)
	}
	return e.markCompleted(ctx, job, 1, 1, outputPath)
}

// downloadOther preserves the content type returned by an "other" source
// instead of blindly wrapping every chapter as CBZ. A single file keeps its
// source extension, image sequences still become CBZ, and multi-file chapters
// keep each original file in a chapter directory.
func (e *Engine) downloadOther(ctx context.Context, item source.Source, account source.Account, job model.DownloadJob) error {
	chapter := model.Chapter{ID: job.ChapterID, ComicID: job.ComicID, Title: job.ChapterTitle, Order: job.ChapterOrder}
	if err := e.store.UpdateDownloadJob(ctx, job.ID, "running", 0, 0, "", ""); err != nil {
		return err
	}
	pages, err := item.Pages(ctx, account, job.ComicID, chapter)
	if err != nil {
		return fmt.Errorf("list other source files: %w", err)
	}
	if len(pages) == 0 {
		return errors.New("其他源章节没有可下载文件")
	}
	if err := e.store.UpdateDownloadJob(ctx, job.ID, "running", len(pages), 0, "", ""); err != nil {
		return err
	}

	downloadRoot, err := e.jobDownloadDir(job)
	if err != nil {
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
		metadata.Description = source.FirstNonEmpty(detail.Description, metadata.Description)
		metadata.Cover = source.FirstNonEmpty(detail.Cover, metadata.Cover)
	} else {
		e.logger.Printf("downloader: other source detail %s/%s: %v", job.SourceID, job.ComicID, detailErr)
	}
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
	metadata.Title = SafeName(comicTitle)
	comicDir := filepath.Join(downloadRoot, SafeName(comicTitle))
	if err := os.MkdirAll(comicDir, 0o755); err != nil {
		return err
	}
	tempDir := e.stagingDir(job)
	if err := os.RemoveAll(tempDir); err != nil {
		return err
	}
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
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
	type otherPage struct {
		path        string
		extension   string
		contentType string
	}
	results := make([]otherPage, len(pages))
	semaphore := make(chan struct{}, e.pageConcurrency(job.SourceID))
	otherCtx, cancelPages := context.WithCancel(ctx)
	defer cancelPages()
	var wait sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	completed := 0
	for index, page := range pages {
		index, page := index, page
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
			case <-otherCtx.Done():
				return
			}
			defer func() { <-semaphore }()
			stream, contentType, filename, address, openErr := openPageContent(otherCtx, client, account, item, job.ComicID, chapter, page)
			if openErr != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("第 %d 个文件下载失败: %w", index+1, openErr)
					cancelPages()
				}
				errMu.Unlock()
				return
			}
			path, extension, savedContentType, writeErr := writeOtherPage(stream, tempDir, index, source.FirstNonEmpty(contentType, page.ContentType), source.FirstNonEmpty(filename, page.FileName), source.FirstNonEmpty(address, page.URL))
			_ = stream.Close()
			if writeErr != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("第 %d 个文件保存失败: %w", index+1, writeErr)
					cancelPages()
				}
				errMu.Unlock()
				return
			}
			results[index] = otherPage{path: path, extension: extension, contentType: savedContentType}
			errMu.Lock()
			completed++
			current := completed
			errMu.Unlock()
			_ = e.store.UpdateDownloadProgress(ctx, job.ID, current)
		}()
	}
	wait.Wait()
	if firstErr != nil {
		return firstErr
	}
	if completed != len(pages) {
		return errors.New("其他源章节文件下载不完整")
	}

	allImages := true
	for _, result := range results {
		if !isImageFileExtension(result.extension) {
			allImages = false
			break
		}
	}
	var outputPath string
	switch {
	case len(results) == 1:
		outputPath = filepath.Join(comicDir, ChapterFileName(chapterTitle, job.ChapterOrder, job.ChapterMarker, results[0].extension))
		if err := copyFileAtomic(results[0].path, outputPath); err != nil {
			return err
		}
	case allImages:
		outputPath = filepath.Join(comicDir, ChapterFileName(chapterTitle, job.ChapterOrder, job.ChapterMarker, ".cbz"))
		entries := make([]pageEntry, 0, len(results))
		for index, result := range results {
			entries = append(entries, pageEntry{name: fmt.Sprintf("%03d%s", index+1, result.extension), path: result.path})
		}
		if err := writeZip(outputPath, entries); err != nil {
			return err
		}
	default:
		chapterDir := filepath.Join(comicDir, ChapterFileName(chapterTitle, job.ChapterOrder, job.ChapterMarker, ""))
		if err := os.RemoveAll(chapterDir); err != nil {
			return err
		}
		if err := os.MkdirAll(chapterDir, 0o755); err != nil {
			return err
		}
		for index, result := range results {
			target := filepath.Join(chapterDir, fmt.Sprintf("%03d%s", index+1, result.extension))
			if err := copyFileAtomic(result.path, target); err != nil {
				return err
			}
		}
		outputPath = chapterDir
	}

	e.metaMu.Lock()
	_, metaErr := writeComicMetadata(ctx, client, account, item, metadata, comicDir, "", 0, len(results), countSeriesBookFiles(comicDir))
	e.metaMu.Unlock()
	if metaErr != nil {
		e.logger.Printf("downloader: other source metadata %s/%s: %v", job.SourceID, job.ComicID, metaErr)
	}
	if err := e.markCompleted(ctx, job, len(results), len(results), outputPath); err != nil {
		return err
	}
	finished = true
	return nil
}

// downloadBookArchive packages a plugin book chapter as one EPUB. HTML and
// illustrations are materialized through the source runtime so parser headers
// and descrambling behavior remain intact.
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
	metadata.Title = SafeName(comicTitle)
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
	epubImages := make([]pageEntry, 0, len(content.Images))
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
		epubName := "images/" + name
		epubImages = append(epubImages, pageEntry{name: epubName, path: path})
		imageNames[page.URL] = epubName
		completedPages++
		if err := e.store.UpdateDownloadProgress(ctx, job.ID, completedPages); err != nil {
			return err
		}
	}

	html, err = rewriteChapterArchiveHTML(html, imageNames)
	if err != nil {
		return fmt.Errorf("rewrite chapter HTML: %w", err)
	}
	outputPath := filepath.Join(comicDir, ChapterFileName(chapterTitle, job.ChapterOrder, job.ChapterMarker, ".epub"))
	if err := writeEPUB(
		outputPath,
		metadata,
		comicPageURL(item.Info().Homepage, metadata.SourceID, metadata.ID),
		[]epubChapter{{Title: chapterTitle, HTML: html}},
		epubImages,
	); err != nil {
		return err
	}

	e.metaMu.Lock()
	metaErr := writeBookMetadata(ctx, client, account, item, metadata, comicDir)
	e.metaMu.Unlock()
	if metaErr != nil {
		e.logger.Printf("downloader: book archive metadata %s/%s: %v", job.SourceID, job.ComicID, metaErr)
	}
	if err := e.markCompleted(ctx, job, totalPages, totalPages, outputPath); err != nil {
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

// writeBookMetadata downloads the series cover and refreshes the metadata
// sidecars. ComicInfo.xml remains the Manco/Komga-friendly series document,
// while series.json is the schema KMRS actually reads for a series summary.
func writeBookMetadata(ctx context.Context, client *http.Client, account source.Account, item source.Source, book model.Comic, comicDir string) error {
	if err := os.MkdirAll(comicDir, 0o755); err != nil {
		return err
	}
	coverURL := strings.TrimSpace(book.Cover)
	if coverURL != "" || canMaterializeCover(item, coverURL) {
		existing, _ := filepath.Glob(filepath.Join(comicDir, "cover.*"))
		if len(existing) == 0 {
			if data, err := fetchCoverImage(ctx, client, account, item, book); err == nil {
				_ = os.WriteFile(filepath.Join(comicDir, "cover"+imageExtension(data, coverURL)), data, 0o644)
			}
		}
	}
	if _, err := writeSeriesComicInfo(item, book, comicDir); err != nil {
		return err
	}
	return writeSeriesMetadataFiles(book, item.Info(), comicDir)
}

// seriesComicInfoDoc builds the standalone series-level ComicInfo document. It
// reuses the same fields Komga/KMRS aggregate from each chapter so the series
// view can be populated, then clears the fields that only belong to a chapter.
func seriesComicInfoDoc(item source.Source, comic model.Comic) ComicInfo {
	doc := chapterComicInfo(item, comic, "", 0, 0)
	doc.Title = source.FirstNonEmpty(comic.Title, comic.ID)
	doc.Number = ""
	doc.Count = 0
	doc.PageCount = 0
	doc.Pages = nil
	return doc
}

// writeSeriesComicInfo writes the standalone series-level ComicInfo.xml. Each
// chapter CBZ also embeds its own copy. Obsolete OPF/book sidecars are removed,
// but series.json is retained because it is the only format KMRS uses for the
// series description.
func writeSeriesComicInfo(item source.Source, comic model.Comic, comicDir string) ([]byte, error) {
	if err := os.MkdirAll(comicDir, 0o755); err != nil {
		return nil, err
	}
	payload, err := marshalComicInfo(seriesComicInfoDoc(item, comic))
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(comicDir, "ComicInfo.xml"), payload, 0o644); err != nil {
		return nil, err
	}
	removeObsoleteMetadataFiles(comicDir)
	return payload, nil
}

// MylarSeries mirrors the JSON shape consumed by KMRS's MylarSeriesProvider.
// Required fields deliberately do not use omitempty: KMRS uses strict serde, so
// omitting zero/empty values makes the whole file unparseable.
type MylarSeries struct {
	Version  string        `json:"version,omitempty"`
	Metadata MylarMetadata `json:"metadata"`
}

type MylarMetadata struct {
	Type                 string   `json:"type"`
	Publisher            string   `json:"publisher"`
	Name                 string   `json:"name"`
	ComicID              string   `json:"comicid"`
	Year                 int      `json:"year"`
	DescriptionText      string   `json:"description_text,omitempty"`
	DescriptionFormatted string   `json:"description_formatted,omitempty"`
	Volume               int      `json:"volume,omitempty"`
	BookType             string   `json:"booktype"`
	AgeRating            string   `json:"age_rating,omitempty"`
	ComicImage           string   `json:"comic_image"`
	TotalIssues          int      `json:"total_issues"`
	PublicationRun       string   `json:"publication_run"`
	Status               string   `json:"status"`
	Language             string   `json:"language,omitempty"`
	ReadingDirection     string   `json:"readingDirection,omitempty"`
	Authors              []string `json:"authors,omitempty"`
	Genres               []string `json:"genres,omitempty"`
	Tags                 []string `json:"tags,omitempty"`
}

// writeSeriesMetadataFiles writes the Mylar series.json expected by KMRS.
// KMRS deliberately clears the summary when aggregating ComicInfo from chapter
// archives, so this sidecar is required for a series description.
func writeSeriesMetadataFiles(comic model.Comic, info model.SourceInfo, comicDir string) error {
	return writeSeriesMetadataFilesWithCount(comic, info, comicDir, countSeriesBookFiles(comicDir))
}

func writeSeriesMetadataFilesWithCount(comic model.Comic, info model.SourceInfo, comicDir string, totalIssues int) error {
	if err := os.MkdirAll(comicDir, 0o755); err != nil {
		return err
	}
	series := source.FirstNonEmpty(strings.TrimSpace(comic.Title), strings.TrimSpace(comic.ID), "untitled")
	genres := comicGenreValues(comic, info)
	description := strings.TrimSpace(comic.Description)
	descriptionFormatted := ""
	if description != "" {
		description = plainMetadataText(description)
		// KMRS 把 description_formatted 当普通文本展示，套 <p> 会把标签一起
		// 显示出来，所以这里保持纯文本。
		descriptionFormatted = description
	}
	year := metadataYear(comic.UpdatedAt)
	status := mylarStatus(comic.Status)
	bookType := "comic"
	readingDirection := "RIGHT_TO_LEFT"
	if source.KindOf(info) == source.KindBook {
		bookType = "book"
		readingDirection = "LEFT_TO_RIGHT"
	} else if source.KindOf(info) == source.KindOther {
		bookType = "other"
		readingDirection = "LEFT_TO_RIGHT"
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
			BookType:             bookType,
			AgeRating:            mylarAgeRating(comic, genres),
			ComicImage:           localCoverName(comicDir),
			TotalIssues:          totalIssues,
			PublicationRun:       mylarPublicationRun(year),
			Status:               status,
			Language:             "zh-CN",
			ReadingDirection:     readingDirection,
			Authors:              metadataAuthors(comic.Author),
			Genres:               genres,
			Tags:                 genres,
		},
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(comicDir, "series.json"), payload, 0o644)
}

// obsoleteMetadataFiles are no longer produced. Keeping the cleanup here makes
// existing libraries converge to ComicInfo.xml + series.json automatically.
var obsoleteMetadataFiles = []string{"metadata.opf", "book.json"}

func removeObsoleteMetadataFiles(comicDir string) {
	for _, name := range obsoleteMetadataFiles {
		_ = os.Remove(filepath.Join(comicDir, name))
	}
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

func localCoverName(comicDir string) string {
	covers, _ := filepath.Glob(filepath.Join(comicDir, "cover.*"))
	if len(covers) == 0 {
		return ""
	}
	sort.Strings(covers)
	return filepath.Base(covers[0])
}

// plainMetadataText converts a source description into plain text so metadata
// consumers (KMRS, Komga) never show stray HTML such as a leading "<p>".
// Block-level tags become line breaks; inline tags are dropped and their text
// kept; character entities are decoded by the HTML parser.
func plainMetadataText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	document, err := nethtml.Parse(strings.NewReader(value))
	if err != nil {
		return strings.Join(strings.Fields(value), " ")
	}
	var builder strings.Builder
	var walk func(node *nethtml.Node)
	walk = func(node *nethtml.Node) {
		if node.Type == nethtml.ElementNode {
			switch strings.ToLower(node.Data) {
			case "script", "style":
				return
			case "br", "p", "div", "li", "tr", "section", "article",
				"h1", "h2", "h3", "h4", "h5", "h6":
				builder.WriteString("\n")
			}
		}
		if node.Type == nethtml.TextNode {
			builder.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	lines := strings.Split(builder.String(), "\n")
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		if text := strings.Join(strings.Fields(line), " "); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func htmlEscape(value string) string {
	var buffer bytes.Buffer
	if err := xml.EscapeText(&buffer, []byte(value)); err != nil {
		return value
	}
	return buffer.String()
}

func mylarPublicationRun(year int) string {
	if year <= 0 {
		return ""
	}
	return fmt.Sprintf("%d -", year)
}

func countSeriesBookFiles(comicDir string) int {
	entries, err := os.ReadDir(comicDir)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if strings.HasPrefix(name, "cover.") ||
			name == "comicinfo.xml" ||
			name == "series.json" ||
			name == "metadata.opf" ||
			name == "book.json" ||
			strings.HasSuffix(name, ".part") {
			continue
		}
		count++
	}
	return count
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
	} else if source.KindOf(info) == source.KindOther {
		label = "其他"
	}
	if !seen[label] {
		values = append(values, label)
	}
	return values
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
	case text == "",
		text == "ongoing",
		text == "连载",
		text == "连载中",
		text == "連載",
		text == "連載中",
		text == "连載",
		text == "连載中":
		return "Continuing"
	case strings.Contains(text, "完"),
		text == "completed",
		text == "complete",
		text == "finished",
		text == "ended":
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

// EnsureSeriesMetadata verifies that a subscribed series still has its
// series-level cover and metadata files on disk and re-fetches them when one
// is missing. It returns true when at least one file had to be recreated.
// Only series directories that already contain downloaded entries are
// considered, so a freshly created subscription is left untouched.
func (e *Engine) EnsureSeriesMetadata(ctx context.Context, subscription model.Subscription) (bool, error) {
	return e.writeSeriesMetadata(ctx, subscription, false)
}

// ScrapeSeriesMetadata re-fetches the upstream metadata and overwrites the
// local ComicInfo.xml / series.json / cover even when they already look
// current. EnsureSeriesMetadata only fills in what is missing.
func (e *Engine) ScrapeSeriesMetadata(ctx context.Context, subscription model.Subscription) (bool, error) {
	return e.writeSeriesMetadata(ctx, subscription, true)
}

// SyncSeriesTitle rewrites the metadata title of an existing series directory
// to match its actual folder name. It is used after a subscription directory
// rename so sidecars and archive-internal metadata stay aligned.
func (e *Engine) SyncSeriesTitle(subscription model.Subscription) error {
	root, err := e.jobDownloadDir(model.DownloadJob{DownloadDir: subscription.DownloadDir})
	if err != nil {
		return err
	}
	series := strings.TrimSpace(subscription.SeriesDir)
	if series == "" {
		series = SeriesDirectoryName(subscription.Title, subscription.ConvertToSimplified)
	}
	comicDir := filepath.Join(root, SafeName(series))
	info, err := os.Stat(comicDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	return rewriteSeriesMetadataTitle(comicDir, filepath.Base(comicDir))
}

func rewriteSeriesMetadataTitle(comicDir, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}
	if raw, err := os.ReadFile(filepath.Join(comicDir, "ComicInfo.xml")); err == nil {
		var info ComicInfo
		if xml.Unmarshal(raw, &info) == nil {
			info.Title = title
			info.Series = title
			payload, marshalErr := marshalComicInfo(info)
			if marshalErr != nil {
				return marshalErr
			}
			if err := writeFileAtomic(filepath.Join(comicDir, "ComicInfo.xml"), payload); err != nil {
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if raw, err := os.ReadFile(filepath.Join(comicDir, "series.json")); err == nil {
		var payload map[string]any
		if json.Unmarshal(raw, &payload) == nil {
			metadata, _ := payload["metadata"].(map[string]any)
			if metadata == nil {
				metadata = map[string]any{}
				payload["metadata"] = metadata
			}
			metadata["name"] = title
			encoded, marshalErr := json.MarshalIndent(payload, "", "  ")
			if marshalErr != nil {
				return marshalErr
			}
			if err := writeFileAtomic(filepath.Join(comicDir, "series.json"), append(encoded, '\n')); err != nil {
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return filepath.WalkDir(comicDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".cbz", ".epub":
			return rewriteArchiveMetadataTitle(path, title)
		default:
			return nil
		}
	})
}

var (
	epubTitlePattern    = regexp.MustCompile(`(?s)<dc:title(?:\s[^>]*)?>.*?</dc:title>`)
	epubNCXTitlePattern = regexp.MustCompile(`(?s)<docTitle>\s*<text>.*?</text>\s*</docTitle>`)
)

func rewriteArchiveMetadataTitle(path, title string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	tempPath := path + ".part"
	output, err := os.Create(tempPath)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(output)
	archiveExtension := strings.ToLower(filepath.Ext(path))
	cleanup := func(cause error) error {
		_ = writer.Close()
		_ = output.Close()
		_ = reader.Close()
		_ = os.Remove(tempPath)
		return cause
	}
	for _, entry := range reader.File {
		handle, openErr := entry.Open()
		if openErr != nil {
			return cleanup(openErr)
		}
		data, readErr := io.ReadAll(handle)
		_ = handle.Close()
		if readErr != nil {
			return cleanup(readErr)
		}
		name := strings.ToLower(entry.Name)
		switch {
		case archiveExtension == ".cbz" && strings.EqualFold(filepath.Base(name), "comicinfo.xml"):
			var info ComicInfo
			if xml.Unmarshal(data, &info) == nil {
				info.Series = title
				if payload, marshalErr := marshalComicInfo(info); marshalErr == nil {
					data = payload
				}
			}
		case archiveExtension == ".epub" && (strings.HasSuffix(name, "content.opf") || strings.HasSuffix(name, "toc.ncx")):
			value := string(data)
			if strings.HasSuffix(name, "content.opf") {
				value = epubTitlePattern.ReplaceAllString(value, `<dc:title>`+escapeXML(title)+`</dc:title>`)
			} else {
				value = epubNCXTitlePattern.ReplaceAllString(value, `<docTitle><text>`+escapeXML(title)+`</text></docTitle>`)
			}
			data = []byte(value)
		}
		header := &zip.FileHeader{Name: entry.Name, Method: entry.Method, Modified: entry.Modified}
		target, createErr := writer.CreateHeader(header)
		if createErr != nil {
			return cleanup(createErr)
		}
		if _, writeErr := target.Write(data); writeErr != nil {
			return cleanup(writeErr)
		}
	}
	if err := reader.Close(); err != nil {
		return cleanup(err)
	}
	if err := writer.Close(); err != nil {
		_ = output.Close()
		_ = os.Remove(tempPath)
		return err
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, path); err == nil {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

func (e *Engine) writeSeriesMetadata(ctx context.Context, subscription model.Subscription, force bool) (bool, error) {
	if strings.TrimSpace(subscription.SourceID) == "" || strings.TrimSpace(subscription.ComicID) == "" {
		return false, nil
	}
	root, err := e.jobDownloadDir(model.DownloadJob{DownloadDir: subscription.DownloadDir})
	if err != nil {
		return false, err
	}
	series := strings.TrimSpace(subscription.SeriesDir)
	var comicDir string
	if series == "" {
		comicDir = filepath.Join(root, SeriesDirectoryName(subscription.Title, subscription.ConvertToSimplified))
	} else {
		comicDir = filepath.Join(root, SafeName(series))
	}
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
	if !force && seriesMetadataCurrent(comicDir) {
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
	metadata.Title = filepath.Base(comicDir)
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
	} else if _, err := writeComicMetadata(ctx, client, account, item, metadata, comicDir, "", 0, 0, countSeriesBookFiles(comicDir)); err != nil {
		return false, err
	}
	return true, nil
}

// seriesMetadataCurrent reports whether the folder carries the two sidecars
// KMRS/Komga need and no obsolete OPF/Manco files. The JSON is parsed before it
// is trusted, so folders written by older Manco versions with an incomplete
// series.json are repaired on the next subscription metadata check.
func seriesMetadataCurrent(comicDir string) bool {
	if _, err := os.Stat(filepath.Join(comicDir, "ComicInfo.xml")); err != nil {
		return false
	}
	if !validMylarSeriesJSON(filepath.Join(comicDir, "series.json")) {
		return false
	}
	if !seriesMetadataTitleCurrent(comicDir) {
		return false
	}
	// 旧版本会把简介包成 <p>…</p>，KMRS/Komga 会把标签原样显示出来。
	if metadataDescriptionHasHTML(comicDir) {
		return false
	}
	for _, name := range obsoleteMetadataFiles {
		if _, err := os.Stat(filepath.Join(comicDir, name)); err == nil {
			return false
		}
	}
	return true
}

func seriesMetadataTitleCurrent(comicDir string) bool {
	title := filepath.Base(filepath.Clean(comicDir))
	raw, err := os.ReadFile(filepath.Join(comicDir, "ComicInfo.xml"))
	if err != nil {
		return false
	}
	var info ComicInfo
	if xml.Unmarshal(raw, &info) != nil || strings.TrimSpace(info.Series) != title {
		return false
	}
	raw, err = os.ReadFile(filepath.Join(comicDir, "series.json"))
	if err != nil {
		return false
	}
	var payload struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
	}
	return json.Unmarshal(raw, &payload) == nil && strings.TrimSpace(payload.Metadata.Name) == title
}

var metadataHTMLTagPattern = regexp.MustCompile(`(?i)</?(p|div|br|span|b|i|strong|em|ul|ol|li|h[1-6])(\s|/|>)`)

func hasHTMLTag(value string) bool {
	return metadataHTMLTagPattern.MatchString(value)
}

// metadataDescriptionHasHTML reports whether stored sidecars still carry an
// HTML-wrapped description, so the next metadata check rewrites them.
func metadataDescriptionHasHTML(comicDir string) bool {
	if raw, err := os.ReadFile(filepath.Join(comicDir, "series.json")); err == nil {
		var payload struct {
			Metadata struct {
				DescriptionText      string `json:"description_text"`
				DescriptionFormatted string `json:"description_formatted"`
			} `json:"metadata"`
		}
		if json.Unmarshal(raw, &payload) == nil {
			if hasHTMLTag(payload.Metadata.DescriptionFormatted) || hasHTMLTag(payload.Metadata.DescriptionText) {
				return true
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(comicDir, "ComicInfo.xml")); err == nil {
		var info ComicInfo
		if xml.Unmarshal(raw, &info) == nil && hasHTMLTag(info.Summary) {
			return true
		}
	}
	return false
}

// validMylarSeriesJSON mirrors the required fields in KMRS's MylarMetadata
// serde struct. Empty strings and zero numbers are valid values, but the keys
// must exist and have compatible JSON types.
func validMylarSeriesJSON(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var payload struct {
		Metadata struct {
			Type           *string         `json:"type"`
			Publisher      *string         `json:"publisher"`
			Name           *string         `json:"name"`
			ComicID        json.RawMessage `json:"comicid"`
			Year           *int            `json:"year"`
			BookType       *string         `json:"booktype"`
			AgeRating      *string         `json:"age_rating"`
			ComicImage     *string         `json:"comic_image"`
			TotalIssues    json.RawMessage `json:"total_issues"`
			PublicationRun *string         `json:"publication_run"`
			Status         *string         `json:"status"`
		} `json:"metadata"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return false
	}
	metadata := payload.Metadata
	if metadata.Type == nil || metadata.Publisher == nil || metadata.Name == nil ||
		metadata.BookType == nil || metadata.ComicImage == nil ||
		metadata.PublicationRun == nil || metadata.Status == nil || metadata.Year == nil {
		return false
	}
	if !jsonStringOrInt(metadata.ComicID) || !jsonIntOrString(metadata.TotalIssues) {
		return false
	}
	if *metadata.Status != "Ended" && *metadata.Status != "Continuing" {
		return false
	}
	if metadata.AgeRating != nil {
		switch *metadata.AgeRating {
		case "All", "9+", "12+", "15+", "17+", "Adult":
		default:
			return false
		}
	}
	return true
}

func jsonStringOrInt(raw json.RawMessage) bool {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return true
	}
	var number int64
	return json.Unmarshal(raw, &number) == nil
}

func jsonIntOrString(raw json.RawMessage) bool {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false
	}
	var number int
	if json.Unmarshal(raw, &number) == nil {
		return true
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return false
	}
	_, err := strconv.Atoi(strings.TrimSpace(text))
	return err == nil
}

// comicGenre merges source-provided tags with the media type so the rendered
// ComicInfo.xml always records whether the entry is a comic or a book.
func comicGenre(comic model.Comic, info model.SourceInfo) string {
	return strings.Join(comicGenreValues(comic, info), ",")
}

func writeComicMetadata(ctx context.Context, client *http.Client, account source.Account, item source.Source, comic model.Comic, comicDir, chapterTitle string, chapterOrder float64, pageCount, totalIssues int) ([]byte, error) {
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
	// per-chapter ComicInfo.xml embedded in each CBZ. series.json is written
	// separately because KMRS only reads the series summary from that sidecar.
	if _, err := writeSeriesComicInfo(item, comic, comicDir); err != nil {
		return nil, err
	}
	if err := writeSeriesMetadataFilesWithCount(comic, item.Info(), comicDir, totalIssues); err != nil {
		return nil, err
	}
	return payload, nil
}

func chapterComicInfo(item source.Source, comic model.Comic, chapterTitle string, chapterOrder float64, pageCount int) ComicInfo {
	info := item.Info()
	manga := "YesAndRightToLeft"
	if source.KindOf(info) == source.KindBook || source.KindOf(info) == source.KindOther {
		manga = "No"
	}
	genres := comicGenreValues(comic, info)
	tags := genres
	notes := fmt.Sprintf("Manco source=%s comicId=%s", comic.SourceID, comic.ID)
	if status := strings.TrimSpace(comic.Status); status != "" {
		// ComicInfo v2.1 has no Status element, so keep it in Notes as a
		// url-escaped key/value token for Manco's own local library.
		notes += " status=" + url.QueryEscape(status)
	}
	return ComicInfo{
		Title:       source.FirstNonEmpty(chapterTitle, comic.Title, comic.ID),
		Series:      source.FirstNonEmpty(comic.Title, comic.ID),
		Number:      comicNumber(chapterOrder),
		Summary:     plainMetadataText(comic.Description),
		Notes:       notes,
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

func openPageContent(ctx context.Context, client *http.Client, account source.Account, item source.Source, comicID string, chapter model.Chapter, page model.Page) (io.ReadCloser, string, string, string, error) {
	if fileSource, ok := item.(source.PageFileSource); ok {
		reader, contentType, filename, err := fileSource.PageFile(ctx, account, comicID, chapter, page)
		return reader, contentType, filename, page.URL, err
	}
	if materializer, ok := item.(source.ImageSource); ok {
		data, contentType, err := materializer.PageImage(ctx, account, comicID, chapter, page)
		if err != nil {
			return nil, "", "", "", err
		}
		return io.NopCloser(bytes.NewReader(data)), contentType, page.FileName, page.URL, nil
	}
	return openHTTPPageContent(ctx, client, account, page)
}

func openHTTPPageContent(ctx context.Context, client *http.Client, account source.Account, page model.Page) (io.ReadCloser, string, string, string, error) {
	candidates := append([]string{page.URL}, page.Alternatives...)
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
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
			if err != nil {
				lastErr = err
				continue
			}
			request.Header.Set("User-Agent", userAgent)
			request.Header.Set("Accept", "*/*")
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
				lastErr = err
				continue
			}
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				lastErr = fmt.Errorf("HTTP %d", response.StatusCode)
				_ = response.Body.Close()
				continue
			}
			address := page.URL
			if response.Request != nil && response.Request.URL != nil {
				address = response.Request.URL.String()
			}
			return response.Body, response.Header.Get("Content-Type"), contentDispositionFilename(response.Header.Get("Content-Disposition")), address, nil
		}
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的文件地址")
	}
	return nil, "", "", "", lastErr
}

func contentDispositionFilename(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(params["filename"])
}

func writeOtherPage(reader io.Reader, dir string, index int, contentType, filename, address string) (string, string, string, error) {
	sample := make([]byte, 512)
	count, err := io.ReadFull(reader, sample)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", "", "", err
	}
	if count == 0 {
		return "", "", "", errors.New("来源文件为空")
	}
	extension := sourceFileExtension(contentType, filename, address, sample[:count])
	path := filepath.Join(dir, fmt.Sprintf("%03d%s", index+1, extension))
	file, err := os.Create(path)
	if err != nil {
		return "", "", "", err
	}
	if _, err := file.Write(sample[:count]); err != nil {
		_ = file.Close()
		return "", "", "", err
	}
	if _, err := io.Copy(file, reader); err != nil {
		_ = file.Close()
		return "", "", "", err
	}
	if err := file.Close(); err != nil {
		return "", "", "", err
	}
	return path, extension, contentType, nil
}

var safeFileExtensionPattern = regexp.MustCompile(`^\.[a-z0-9]{1,10}$`)

func sourceFileExtension(contentType, filename, address string, sample []byte) string {
	if extension := explicitFileExtension(filename); extension != "" {
		return extension
	}
	if extension := contentTypeFileExtension(contentType); extension != "" {
		return extension
	}
	if extension := detectBinaryExtension(sample); extension != "" {
		return extension
	}
	if parsed, err := url.Parse(strings.TrimSpace(address)); err == nil {
		if extension := explicitFileExtension(parsed.Path); extension != "" {
			return extension
		}
	}
	return ".bin"
}

func explicitFileExtension(value string) string {
	extension := strings.ToLower(strings.TrimSpace(filepath.Ext(strings.TrimSpace(value))))
	if safeFileExtensionPattern.MatchString(extension) {
		return extension
	}
	return ""
}

func contentTypeFileExtension(contentType string) string {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil {
		mediaType = strings.TrimSpace(strings.Split(contentType, ";")[0])
	}
	switch strings.ToLower(mediaType) {
	case "application/epub+zip":
		return ".epub"
	case "application/pdf":
		return ".pdf"
	case "application/zip", "application/x-zip-compressed":
		return ".zip"
	case "application/vnd.comicbook+zip", "application/vnd.comicbook-rar":
		if strings.Contains(strings.ToLower(mediaType), "rar") {
			return ".cbr"
		}
		return ".cbz"
	case "application/x-rar-compressed", "application/vnd.rar":
		return ".rar"
	case "application/x-7z-compressed":
		return ".7z"
	case "application/ogg":
		return ".ogg"
	case "application/vnd.apple.mpegurl", "application/x-mpegurl":
		return ".m3u8"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/x-matroska":
		return ".mkv"
	case "video/quicktime":
		return ".mov"
	case "video/x-msvideo":
		return ".avi"
	case "audio/mpeg":
		return ".mp3"
	case "audio/mp4", "audio/x-m4a":
		return ".m4a"
	case "audio/flac", "audio/x-flac":
		return ".flac"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/ogg":
		return ".ogg"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	case "text/plain":
		return ".txt"
	case "text/html":
		return ".html"
	case "application/json":
		return ".json"
	default:
		return ""
	}
}

func detectBinaryExtension(data []byte) string {
	if extension := detectImageExtension(data); extension != "" {
		return extension
	}
	if len(data) < 4 {
		return ""
	}
	switch {
	case bytes.HasPrefix(data, []byte("%PDF")):
		return ".pdf"
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		if bytes.Contains(data, []byte("application/epub+zip")) {
			return ".epub"
		}
		return ".zip"
	case len(data) >= 8 && bytes.Equal(data[4:8], []byte("ftyp")):
		return ".mp4"
	case bytes.HasPrefix(data, []byte{0x1A, 0x45, 0xDF, 0xA3}):
		return ".mkv"
	case bytes.HasPrefix(data, []byte("ID3")):
		return ".mp3"
	case bytes.HasPrefix(data, []byte("OggS")):
		return ".ogg"
	case bytes.HasPrefix(data, []byte("fLaC")):
		return ".flac"
	case len(data) >= 12 && bytes.Equal(data[0:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WAVE")):
		return ".wav"
	default:
		return ""
	}
}

func isImageFileExtension(extension string) bool {
	switch strings.ToLower(strings.TrimSpace(extension)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".svg":
		return true
	default:
		return false
	}
}

func copyFileAtomic(sourcePath, targetPath string) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer sourceFile.Close()
	tempPath := targetPath + ".part"
	targetFile, err := os.Create(tempPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(targetFile, sourceFile); err != nil {
		_ = targetFile.Close()
		_ = os.Remove(tempPath)
		return err
	}
	if err := targetFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, targetPath); err == nil {
		return nil
	}
	if err := os.Remove(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
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
	blockSize := height / parts
	remainder := height % parts
	// The reference decoder walks source strips from bottom to top and appends
	// them to the result in that order. Only the top source strip carries the
	// remainder. Track both offsets explicitly: deriving them from the loop
	// index gets the non-divisible cases wrong.
	destinationY := 0
	for sourceIndex := parts - 1; sourceIndex >= 0; sourceIndex-- {
		start := sourceIndex * blockSize
		end := start + blockSize
		if sourceIndex == parts-1 {
			end += remainder
		}
		sourceY := start
		move := end - start
		if move <= 0 {
			continue
		}
		sourcePoint := image.Pt(bounds.Min.X, bounds.Min.Y+sourceY)
		destination := image.Rect(0, destinationY, width, destinationY+move)
		draw.Draw(output, destination, decoded, sourcePoint, draw.Src)
		destinationY += move
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
