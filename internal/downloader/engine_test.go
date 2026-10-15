package downloader

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/sources"
)

// memoryJobs is a minimal in-memory implementation of the downloader's job
// store, used to drive the engine without a database.
type memoryJobs struct {
	mu   sync.Mutex
	jobs []model.DownloadJob
	next int64
}

func (m *memoryJobs) CreateDownloadJob(_ context.Context, job model.DownloadJob) (model.DownloadJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	job.ID = m.next
	if job.Status == "" {
		job.Status = "queued"
	}
	m.jobs = append(m.jobs, job)
	return job, nil
}

func (m *memoryJobs) ListQueuedJobs(_ context.Context) ([]model.DownloadJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	queued := make([]model.DownloadJob, 0, len(m.jobs))
	for _, job := range m.jobs {
		if job.Status == "queued" {
			queued = append(queued, job)
		}
	}
	return queued, nil
}

func (m *memoryJobs) DownloadJob(_ context.Context, id int64) (model.DownloadJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, job := range m.jobs {
		if job.ID == id {
			return job, nil
		}
	}
	return model.DownloadJob{}, fmt.Errorf("job %d not found", id)
}

func (m *memoryJobs) UpdateDownloadJob(_ context.Context, id int64, status string, totalPages, completedPages int, filePath, jobError string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.jobs {
		if m.jobs[index].ID != id {
			continue
		}
		m.jobs[index].Status = status
		m.jobs[index].TotalPages = totalPages
		m.jobs[index].CompletedPages = completedPages
		if filePath != "" {
			m.jobs[index].FilePath = filePath
		}
		m.jobs[index].Error = jobError
		return nil
	}
	return fmt.Errorf("job %d not found", id)
}

func (m *memoryJobs) UpdateDownloadProgress(_ context.Context, id int64, completedPages int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.jobs {
		if m.jobs[index].ID == id {
			m.jobs[index].CompletedPages = completedPages
			return nil
		}
	}
	return fmt.Errorf("job %d not found", id)
}

func (m *memoryJobs) find(id int64) (model.DownloadJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, job := range m.jobs {
		if job.ID == id {
			return job, true
		}
	}
	return model.DownloadJob{}, false
}

func (m *memoryJobs) FailDownloadJob(_ context.Context, id int64, totalPages, completedPages int, jobError string, retryCount int, nextRetryAt *time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.jobs {
		if m.jobs[index].ID != id {
			continue
		}
		m.jobs[index].Status = "failed"
		m.jobs[index].TotalPages = totalPages
		m.jobs[index].CompletedPages = completedPages
		m.jobs[index].Error = jobError
		m.jobs[index].RetryCount = retryCount
		m.jobs[index].NextRetryAt = nextRetryAt
		return nil
	}
	return fmt.Errorf("job %d not found", id)
}

func (m *memoryJobs) ResetDownloadJob(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.jobs {
		if m.jobs[index].ID != id {
			continue
		}
		m.jobs[index].Status = "queued"
		m.jobs[index].CompletedPages = 0
		m.jobs[index].Error = ""
		m.jobs[index].RetryCount = 0
		m.jobs[index].NextRetryAt = nil
		return nil
	}
	return fmt.Errorf("job %d not found", id)
}

func (m *memoryJobs) ActivateDueRetries(_ context.Context, now time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var activated int64
	for index := range m.jobs {
		job := &m.jobs[index]
		if job.Status != "failed" || job.RetryCount >= 2 || job.NextRetryAt == nil {
			continue
		}
		if job.NextRetryAt.After(now) {
			continue
		}
		job.Status = "queued"
		job.NextRetryAt = nil
		activated++
	}
	return activated, nil
}

// staticSource returns a fixed chapter list and page list.
type staticSource struct {
	detail model.Comic
	pages  map[string][]model.Page
}

func (s *staticSource) Info() model.SourceInfo {
	return model.SourceInfo{ID: "stub", Name: "测试源", CanSearch: true, CanBrowse: true}
}

func (s *staticSource) Search(context.Context, source.Account, string, int) (model.SearchResult, error) {
	return model.SearchResult{}, nil
}

func (s *staticSource) Browse(context.Context, source.Account, model.BrowseOptions, int) (model.SearchResult, error) {
	return model.SearchResult{}, nil
}

func (s *staticSource) Detail(context.Context, source.Account, string) (model.Comic, error) {
	if s.detail.Title == "" {
		return model.Comic{SourceID: "stub", ID: "1", Title: "测试作品"}, nil
	}
	return s.detail, nil
}

func (s *staticSource) Chapters(context.Context, source.Account, string) ([]model.Chapter, error) {
	return nil, nil
}

func (s *staticSource) Pages(_ context.Context, _ source.Account, _ string, chapter model.Chapter) ([]model.Page, error) {
	return s.pages[chapter.ID], nil
}

type resumableSource struct {
	*staticSource
	mu             sync.Mutex
	cond           *sync.Cond
	firstDone      bool
	failSecond     bool
	firstRequests  int
	secondRequests int
	firstPage      []byte
	secondPage     []byte
}

func newResumableSource(base *staticSource, firstPage, secondPage []byte) *resumableSource {
	source := &resumableSource{
		staticSource: base,
		firstPage:    firstPage,
		secondPage:   secondPage,
	}
	source.cond = sync.NewCond(&source.mu)
	return source
}

func (s *resumableSource) PageImage(_ context.Context, _ source.Account, _ string, _ model.Chapter, page model.Page) ([]byte, string, error) {
	switch page.URL {
	case "page-1":
		s.mu.Lock()
		s.firstRequests++
		s.firstDone = true
		s.cond.Broadcast()
		data := s.firstPage
		s.mu.Unlock()
		return data, "", nil
	case "page-2":
		s.mu.Lock()
		for s.failSecond && !s.firstDone {
			s.cond.Wait()
		}
		s.secondRequests++
		fail := s.failSecond
		data := s.secondPage
		s.mu.Unlock()
		if fail {
			return nil, "", fmt.Errorf("temporary failure")
		}
		return data, "", nil
	default:
		return nil, "", fmt.Errorf("unknown page %q", page.URL)
	}
}

func (s *resumableSource) CoverImage(context.Context, source.Account, model.Comic) ([]byte, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.firstPage, "", nil
}

func (s *resumableSource) setFailSecond(fail bool) {
	s.mu.Lock()
	s.failSecond = fail
	s.cond.Broadcast()
	s.mu.Unlock()
}

func (s *resumableSource) requestCounts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.firstRequests, s.secondRequests
}

func pngImage(t *testing.T, shade uint8) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			canvas.Set(x, y, color.RGBA{R: shade, G: shade, B: shade, A: 255})
		}
	}
	buffer := &bytes.Buffer{}
	if err := png.Encode(buffer, canvas); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buffer.Bytes()
}

// TestEngineWritesOneCBZPerChapter drives the real download engine against a
// stub source and asserts that every chapter ends up as exactly one archive.
func TestEngineWritesOneCBZPerChapter(t *testing.T) {
	images := map[string][]byte{
		"/cover.png": pngImage(t, 0x01),
		"/1.png":     pngImage(t, 0x11),
		"/2.png":     pngImage(t, 0x22),
		"/3.png":     pngImage(t, 0x33),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := images[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(data)
	}))
	defer server.Close()

	registry := sources.NewRegistry(server.Client(), nil, nil)
	registry.Register(&staticSource{
		detail: model.Comic{
			SourceID:    "stub",
			ID:          "1",
			Title:       "测试作品",
			Author:      "测试作者",
			Description: "测试简介",
			Cover:       server.URL + "/cover.png",
			Tags:        []string{"测试", "漫画"},
		},
		pages: map[string][]model.Page{
			"1": {
				{URL: server.URL + "/2.png"},
				{URL: server.URL + "/1.png"},
				{URL: server.URL + "/3.png"},
			},
			"2": {
				{URL: server.URL + "/3.png"},
				{URL: server.URL + "/2.png"},
			},
		}})

	store := &memoryJobs{}
	downloadDir := t.TempDir()
	cacheDir := t.TempDir()
	engine := NewEngine(registry, store, downloadDir, 2, 2, log.New(io.Discard, "", 0))
	engine.SetCacheDir(cacheDir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine.Start(ctx)

	for _, chapter := range []model.Chapter{
		{ID: "1", ComicID: "1", Title: "第1话", Order: 1},
		{ID: "2", ComicID: "1", Title: "第2话", Order: 2},
	} {
		if _, err := store.CreateDownloadJob(ctx, model.DownloadJob{
			SourceID:     "stub",
			ComicID:      "1",
			ComicTitle:   "测试作品",
			ChapterID:    chapter.ID,
			ChapterTitle: chapter.Title,
			ChapterOrder: chapter.Order,
		}); err != nil {
			t.Fatalf("create job: %v", err)
		}
	}
	engine.Notify()

	waitFor(t, 10*time.Second, func() bool {
		first, okFirst := store.find(1)
		second, okSecond := store.find(2)
		return okFirst && okSecond && first.Status == "completed" && second.Status == "completed"
	})

	archives := listCBZ(t, downloadDir)
	if len(archives) != 2 {
		t.Fatalf("found %d CBZ files, want 2 (%v)", len(archives), archives)
	}

	first := readZip(t, filepath.Join(downloadDir, "测试作品", "第1话.cbz"))
	assertEntries(t, first, []string{"001.png", "002.png", "003.png", "ComicInfo.xml"})
	if !bytes.Equal(first["001.png"], images["/2.png"]) {
		t.Fatal("chapter 1 page order was not preserved")
	}
	if !bytes.Equal(first["003.png"], images["/3.png"]) {
		t.Fatal("chapter 1 last page mismatch")
	}

	second := readZip(t, filepath.Join(downloadDir, "测试作品", "第2话.cbz"))
	assertEntries(t, second, []string{"001.png", "002.png", "ComicInfo.xml"})
	if !bytes.Equal(second["001.png"], images["/3.png"]) {
		t.Fatal("chapter 2 first page mismatch")
	}

	if _, err := os.Stat(filepath.Join(downloadDir, ".tmp")); !os.IsNotExist(err) {
		t.Fatalf("temporary directory was not cleaned up: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "job-1")); !os.IsNotExist(err) {
		t.Fatalf("download cache directory was not cleaned up: %v", err)
	}

	job, _ := store.find(1)
	if job.TotalPages != 3 || job.CompletedPages != 3 {
		t.Fatalf("job progress = %d/%d, want 3/3", job.CompletedPages, job.TotalPages)
	}
	if !strings.HasSuffix(job.FilePath, filepath.Join("测试作品", "第1话.cbz")) {
		t.Fatalf("job file path = %q", job.FilePath)
	}
	cover, err := os.ReadFile(filepath.Join(downloadDir, "测试作品", "cover.png"))
	if err != nil {
		t.Fatalf("read downloaded cover: %v", err)
	}
	if !bytes.Equal(cover, images["/cover.png"]) {
		t.Fatal("downloaded cover does not match the source")
	}
	nfo, err := os.ReadFile(filepath.Join(downloadDir, "测试作品", "ComicInfo.xml"))
	if err != nil {
		t.Fatalf("read comic nfo: %v", err)
	}
	// The standalone file describes the series, so it is chapter-independent
	// (PageCount/Number live only in each chapter's embedded ComicInfo.xml).
	for _, value := range []string{"<ComicInfo", "<Title>测试作品</Title>", "<Series>测试作品</Series>", "<Writer>测试作者</Writer>", "<Summary>测试简介</Summary>", "<Genre>测试,漫画</Genre>"} {
		if !bytes.Contains(nfo, []byte(value)) {
			t.Fatalf("ComicInfo.xml is missing %q: %s", value, nfo)
		}
	}
	embedded, ok := first["ComicInfo.xml"]
	if !ok {
		t.Fatal("CBZ is missing embedded ComicInfo.xml")
	}
	if !bytes.Contains(embedded, []byte("<Series>测试作品</Series>")) || !bytes.Contains(embedded, []byte("<PageCount>3</PageCount>")) {
		t.Fatalf("embedded ComicInfo.xml content mismatch: %s", embedded)
	}
}

func TestDownloadResumesCachedPagesAfterInterruption(t *testing.T) {
	firstPage := pngImage(t, 0x32)
	secondPage := pngImage(t, 0x33)
	resumable := newResumableSource(&staticSource{
		detail: model.Comic{
			SourceID: "stub",
			ID:       "1",
			Title:    "断点作品",
		},
		pages: map[string][]model.Page{
			"1": {
				{URL: "page-1"},
				{URL: "page-2"},
			},
		},
	}, firstPage, secondPage)
	resumable.setFailSecond(true)
	registry := sources.NewRegistry(source.NewHTTPClient(), nil, nil)
	registry.Register(resumable)

	store := &memoryJobs{}
	downloadDir := t.TempDir()
	cacheDir := t.TempDir()
	engine := NewEngine(registry, store, downloadDir, 1, 2, log.New(io.Discard, "", 0))
	engine.SetCacheDir(cacheDir)
	ctx := context.Background()
	job, err := store.CreateDownloadJob(ctx, model.DownloadJob{
		SourceID:     "stub",
		ComicID:      "1",
		ComicTitle:   "断点作品",
		ChapterID:    "1",
		ChapterTitle: "第1话",
		ChapterOrder: 1,
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	if err := engine.download(ctx, job); err == nil {
		t.Fatal("first download unexpectedly succeeded")
	}
	jobDir := filepath.Join(cacheDir, fmt.Sprintf("job-%d", job.ID))
	if _, err := os.Stat(filepath.Join(jobDir, "001.bin")); err != nil {
		t.Fatalf("first page was not retained for resume: %v", err)
	}
	if _, err := os.Stat(filepath.Join(jobDir, "002.bin")); !os.IsNotExist(err) {
		t.Fatalf("failed page should not be cached: %v", err)
	}
	if interrupted, _ := store.find(job.ID); interrupted.CompletedPages != 1 || interrupted.TotalPages != 2 {
		t.Fatalf("interrupted progress = %d/%d, want 1/2", interrupted.CompletedPages, interrupted.TotalPages)
	}

	resumable.setFailSecond(false)
	if err := engine.download(ctx, job); err != nil {
		t.Fatalf("resumed download: %v", err)
	}
	firstRequests, secondRequests := resumable.requestCounts()
	if firstRequests != 1 {
		t.Fatalf("first page requests = %d, want 1 cached request", firstRequests)
	}
	if secondRequests != 2 {
		t.Fatalf("second page requests = %d, want one failed and one resumed request", secondRequests)
	}
	archive := readZip(t, filepath.Join(downloadDir, "断点作品", "第1话.cbz"))
	assertEntries(t, archive, []string{"001.png", "002.png", "ComicInfo.xml"})
	if !bytes.Equal(archive["001.png"], firstPage) || !bytes.Equal(archive["002.png"], secondPage) {
		t.Fatal("resumed archive content mismatch")
	}
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Fatalf("completed resume cache was not cleaned up: %v", err)
	}
	if completed, _ := store.find(job.ID); completed.CompletedPages != 2 || completed.TotalPages != 2 {
		t.Fatalf("resumed progress = %d/%d, want 2/2", completed.CompletedPages, completed.TotalPages)
	}
}

func TestSeriesDirectoryNameIsStableForSimplifiedTitles(t *testing.T) {
	first := SeriesDirectoryName("龍珠", true)
	if first != "龙珠" {
		t.Fatalf("simplified directory = %q, want 龙珠", first)
	}
	if second := SeriesDirectoryName(first, true); second != first {
		t.Fatalf("directory name changed after repeat conversion: %q -> %q", first, second)
	}
}

func TestBatchPauseIsPerSourceAndReportedByCooldown(t *testing.T) {
	engine := NewEngine(nil, &memoryJobs{}, t.TempDir(), 1, 1, log.New(io.Discard, "", 0))
	engine.SetSourceSettings(map[string]model.SourceDownloadSettings{
		"baozimh": {BatchSize: 2, BatchIntervalMinutes: 20},
		"picacg":  {BatchSize: 5, BatchIntervalMinutes: 20},
	})

	engine.markBatchComplete("baozimh")
	if engine.inCooldown("baozimh") {
		t.Fatalf("baozimh should not be paused before reaching batch size")
	}

	engine.markBatchComplete("baozimh")
	cooldowns := engine.SourceCooldowns()
	if until, ok := cooldowns["baozimh"]; !ok || !until.After(time.Now()) {
		t.Fatalf("baozimh should be paused after reaching batch size: %v", cooldowns)
	}
	if !engine.inCooldown("baozimh") {
		t.Fatalf("baozimh should report in cooldown")
	}
	if engine.inCooldown("picacg") {
		t.Fatalf("picacg must not inherit baozimh cooldown")
	}
}

func TestStagingDirUsesConfiguredDataCache(t *testing.T) {
	engine := NewEngine(nil, &memoryJobs{}, t.TempDir(), 1, 1, log.New(io.Discard, "", 0))
	cacheDir := t.TempDir()
	engine.SetCacheDir(cacheDir)

	got := engine.stagingDir(model.DownloadJob{ID: 42})
	want := filepath.Join(cacheDir, "job-42")
	if got != want {
		t.Fatalf("stagingDir = %q, want %q", got, want)
	}
}

// TestEnsureSeriesMetadataRepairsMissingFiles verifies that a subscription
// check re-creates a missing series cover and ComicInfo.xml while leaving an
// intact series alone.
func TestEnsureSeriesMetadataRepairsMissingFiles(t *testing.T) {
	images := map[string][]byte{"/cover.png": pngImage(t, 0x42)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := images[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(data)
	}))
	defer server.Close()

	registry := sources.NewRegistry(server.Client(), nil, nil)
	registry.Register(&staticSource{detail: model.Comic{
		SourceID:    "stub",
		ID:          "1",
		Title:       "测试作品",
		Author:      "测试作者",
		Description: "测试简介",
		Cover:       server.URL + "/cover.png",
		Tags:        []string{"测试", "漫画"},
	}})

	store := &memoryJobs{}
	downloadDir := t.TempDir()
	engine := NewEngine(registry, store, downloadDir, 2, 2, log.New(io.Discard, "", 0))
	ctx := context.Background()
	subscription := model.Subscription{SourceID: "stub", ComicID: "1", Title: "测试作品", DownloadDir: downloadDir}

	// Nothing downloaded yet: no repair happens.
	repaired, err := engine.EnsureSeriesMetadata(ctx, subscription)
	if err != nil {
		t.Fatalf("empty series: %v", err)
	}
	if repaired {
		t.Fatal("unexpected repair for a series without downloads")
	}

	seriesDir := filepath.Join(downloadDir, "测试作品")
	if err := os.MkdirAll(seriesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seriesDir, "第1话.cbz"), []byte("zip"), 0o644); err != nil {
		t.Fatal(err)
	}

	repaired, err = engine.EnsureSeriesMetadata(ctx, subscription)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if !repaired {
		t.Fatal("missing metadata was not repaired")
	}
	cover, err := os.ReadFile(filepath.Join(seriesDir, "cover.png"))
	if err != nil {
		t.Fatalf("read cover: %v", err)
	}
	if !bytes.Equal(cover, images["/cover.png"]) {
		t.Fatal("cover does not match the source")
	}
	info, err := os.ReadFile(filepath.Join(seriesDir, "ComicInfo.xml"))
	if err != nil {
		t.Fatalf("read ComicInfo.xml: %v", err)
	}
	if !bytes.Contains(info, []byte("<Series>测试作品</Series>")) {
		t.Fatalf("unexpected ComicInfo.xml: %s", info)
	}

	// A second check finds nothing missing.
	repaired, err = engine.EnsureSeriesMetadata(ctx, subscription)
	if err != nil {
		t.Fatalf("second check: %v", err)
	}
	if repaired {
		t.Fatal("metadata was repaired twice")
	}
}
func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition was not met before the deadline")
}

func listCBZ(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(path), ".cbz") {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return found
}

func readZip(t *testing.T, path string) map[string][]byte {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer reader.Close()
	entries := map[string][]byte{}
	for _, file := range reader.File {
		handle, err := file.Open()
		if err != nil {
			t.Fatalf("open entry %s: %v", file.Name, err)
		}
		data, err := io.ReadAll(handle)
		_ = handle.Close()
		if err != nil {
			t.Fatalf("read entry %s: %v", file.Name, err)
		}
		entries[file.Name] = data
	}
	return entries
}

func assertEntries(t *testing.T, entries map[string][]byte, want []string) {
	t.Helper()
	if len(entries) != len(want) {
		t.Fatalf("archive has %d entries, want %d (%v)", len(entries), len(want), entries)
	}
	for _, name := range want {
		if _, ok := entries[name]; !ok {
			t.Fatalf("archive is missing %s (%v)", name, entries)
		}
	}
}
