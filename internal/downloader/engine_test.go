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

// staticSource returns a fixed chapter list and page list.
type staticSource struct {
	pages map[string][]model.Page
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
	return model.Comic{SourceID: "stub", ID: "1", Title: "测试作品"}, nil
}

func (s *staticSource) Chapters(context.Context, source.Account, string) ([]model.Chapter, error) {
	return nil, nil
}

func (s *staticSource) Pages(_ context.Context, _ source.Account, _ string, chapter model.Chapter) ([]model.Page, error) {
	return s.pages[chapter.ID], nil
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
		"/1.png": pngImage(t, 0x11),
		"/2.png": pngImage(t, 0x22),
		"/3.png": pngImage(t, 0x33),
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
	registry.Register(&staticSource{pages: map[string][]model.Page{
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
	engine := NewEngine(registry, store, downloadDir, 2, 2, log.New(io.Discard, "", 0))

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
	assertEntries(t, first, []string{"001.png", "002.png", "003.png"})
	if !bytes.Equal(first["001.png"], images["/2.png"]) {
		t.Fatal("chapter 1 page order was not preserved")
	}
	if !bytes.Equal(first["003.png"], images["/3.png"]) {
		t.Fatal("chapter 1 last page mismatch")
	}

	second := readZip(t, filepath.Join(downloadDir, "测试作品", "第2话.cbz"))
	assertEntries(t, second, []string{"001.png", "002.png"})
	if !bytes.Equal(second["001.png"], images["/3.png"]) {
		t.Fatal("chapter 2 first page mismatch")
	}

	if _, err := os.Stat(filepath.Join(downloadDir, ".tmp", "job-1")); !os.IsNotExist(err) {
		t.Fatalf("temporary directory was not cleaned up: %v", err)
	}

	job, _ := store.find(1)
	if job.TotalPages != 3 || job.CompletedPages != 3 {
		t.Fatalf("job progress = %d/%d, want 3/3", job.CompletedPages, job.TotalPages)
	}
	if !strings.HasSuffix(job.FilePath, filepath.Join("测试作品", "第1话.cbz")) {
		t.Fatalf("job file path = %q", job.FilePath)
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
