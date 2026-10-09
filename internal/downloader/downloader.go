// Package downloader turns queued chapter jobs into one CBZ archive per chapter.
// Images are always written to a temporary directory first and are only moved
// into the library after the archive is complete, so a crash never leaves a
// half-written .cbz in the download directory.
package downloader

import (
	"archive/zip"
	"bytes"
	"context"
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
	"strings"
	"sync"
	"time"

	"golang.org/x/image/webp"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/sources"
)

type Engine struct {
	registry              *sources.Registry
	store                 Jobs
	downloadDir           string
	maxChapterConcurrency int
	maxPageConcurrency    int
	logger                *log.Logger

	mu      sync.Mutex
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
	e.maxChapterConcurrency = chapters
	e.maxPageConcurrency = pages
	e.mu.Unlock()
	e.Notify()
}

// Jobs is the subset of the store used by the downloader.
type Jobs interface {
	CreateDownloadJob(ctx context.Context, job model.DownloadJob) (model.DownloadJob, error)
	ListQueuedJobs(ctx context.Context) ([]model.DownloadJob, error)
	DownloadJob(ctx context.Context, id int64) (model.DownloadJob, error)
	UpdateDownloadJob(ctx context.Context, id int64, status string, totalPages, completedPages int, filePath, jobError string) error
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
	return &Engine{
		registry:              registry,
		store:                 store,
		downloadDir:           downloadDir,
		maxChapterConcurrency: maxChapterConcurrency,
		maxPageConcurrency:    maxPageConcurrency,
		logger:                logger,
		active:                map[int64]context.CancelFunc{},
		kick:                  make(chan struct{}, 1),
	}
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
	job, err := e.store.DownloadJob(ctx, id)
	if err != nil {
		return err
	}
	if err := e.store.UpdateDownloadJob(ctx, id, "queued", job.TotalPages, 0, job.FilePath, ""); err != nil {
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

func (e *Engine) pageConcurrency() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.maxPageConcurrency
}

func (e *Engine) loop(ctx context.Context) {
	defer func() {
		e.mu.Lock()
		e.started = false
		e.mu.Unlock()
	}()
	semaphore := make(chan struct{}, e.concurrency())
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-e.kick:
		}
		jobs, err := e.store.ListQueuedJobs(ctx)
		if err != nil {
			e.logger.Printf("downloader: list queue: %v", err)
			continue
		}
		for _, job := range jobs {
			job := job
			if !e.claim(job.ID) {
				continue
			}
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				e.release(job.ID)
				return
			}
			go func() {
				defer func() {
					<-semaphore
					e.release(job.ID)
					e.Notify()
				}()
				e.runJob(ctx, job)
			}()
		}
	}
}

func (e *Engine) claim(id int64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.active[id]; exists {
		return false
	}
	e.active[id] = func() {}
	return true
}

func (e *Engine) release(id int64) {
	e.mu.Lock()
	delete(e.active, id)
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
		return
	}
	status := "failed"
	if errors.Is(err, context.Canceled) {
		status = "canceled"
	}
	message := err.Error()
	if len(message) > 500 {
		message = message[:500]
	}
	_ = e.store.UpdateDownloadJob(context.Background(), job.ID, status, job.TotalPages, job.CompletedPages, "", message)
	e.logger.Printf("downloader: %s/%s failed: %v", job.ComicTitle, job.ChapterTitle, err)
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
	if err := e.store.UpdateDownloadJob(ctx, job.ID, "running", 0, 0, "", ""); err != nil {
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
	_ = e.store.UpdateDownloadJob(ctx, job.ID, "running", len(pages), 0, "", "")

	tempDir := filepath.Join(e.downloadDir, ".tmp", fmt.Sprintf("job-%d", job.ID))
	if err := os.RemoveAll(tempDir); err != nil {
		return err
	}
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	client := e.registry.Client()
	if client == nil {
		client = source.NewHTTPClient()
	}
	type fetched struct {
		index int
		data  []byte
		err   error
	}
	results := make(chan fetched, len(pages))
	semaphore := make(chan struct{}, e.pageConcurrency())
	var wait sync.WaitGroup
	for index, page := range pages {
		index, page := index, page
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				results <- fetched{index: index, err: ctx.Err()}
				return
			}
			defer func() { <-semaphore }()
			data, err := fetchImage(ctx, client, account, page)
			results <- fetched{index: index, data: data, err: err}
		}()
	}
	go func() {
		wait.Wait()
		close(results)
	}()

	images := make([][]byte, len(pages))
	completed := 0
	for result := range results {
		if result.err != nil {
			return fmt.Errorf("第 %d 页下载失败: %w", result.index+1, result.err)
		}
		images[result.index] = result.data
		completed++
		_ = e.store.UpdateDownloadProgress(ctx, job.ID, completed)
	}

	entries := make([]pageEntry, 0, len(images))
	for index, data := range images {
		if len(data) == 0 {
			return fmt.Errorf("第 %d 页为空", index+1)
		}
		processed := data
		if pages[index].DescrambleParts > 0 {
			processed, err = Descramble(data, pages[index].DescrambleParts)
			if err != nil {
				return fmt.Errorf("第 %d 页解扰失败: %w", index+1, err)
			}
		}
		extension := imageExtension(data, pages[index].URL)
		name := fmt.Sprintf("%03d%s", index+1, extension)
		path := filepath.Join(tempDir, name)
		if err := os.WriteFile(path, processed, 0o644); err != nil {
			return err
		}
		entries = append(entries, pageEntry{name: name, path: path})
	}

	outputPath := filepath.Join(e.downloadDir, SafeName(job.ComicTitle), SafeName(job.ChapterTitle)+".cbz")
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	if err := writeZip(outputPath, entries); err != nil {
		return err
	}
	return e.store.UpdateDownloadJob(ctx, job.ID, "completed", len(pages), len(pages), outputPath, "")
}

type pageEntry struct {
	name string
	path string
}

func fetchImage(ctx context.Context, client *http.Client, account source.Account, page model.Page) ([]byte, error) {
	candidates := append([]string{page.URL}, page.Alternatives...)
	var lastErr error
	for _, address := range candidates {
		if strings.TrimSpace(address) == "" {
			continue
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			lastErr = err
			continue
		}
		request.Header.Set("User-Agent", source.DefaultUserAgent)
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
			lastErr = err
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			lastErr = fmt.Errorf("HTTP %d", response.StatusCode)
			_ = response.Body.Close()
			continue
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
		_ = response.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if len(data) == 0 {
			lastErr = errors.New("空响应")
			continue
		}
		return data, nil
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的图片地址")
	}
	return nil, lastErr
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
