package downloader

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
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

func (m *memoryJobs) FailedDownloadJobsByComic(_ context.Context, sourceID, comicID string) ([]model.DownloadJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var failed []model.DownloadJob
	for _, job := range m.jobs {
		if job.SourceID != sourceID || job.ComicID != comicID {
			continue
		}
		if job.Status == "failed" || job.Status == "canceled" {
			failed = append(failed, job)
		}
	}
	return failed, nil
}

// staticSource returns a fixed chapter list and page list.
type staticSource struct {
	detail model.Comic
	pages  map[string][]model.Page
}

type otherSource struct {
	*staticSource
}

func (s *otherSource) Info() model.SourceInfo {
	info := s.staticSource.Info()
	info.ID = "other-stub"
	info.Name = "其他测试源"
	info.Kind = source.KindOther
	return info
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

type unsupportedMergedBookSource struct {
	*staticSource
}

func (s *unsupportedMergedBookSource) Info() model.SourceInfo {
	info := s.staticSource.Info()
	info.ID = "plain-book"
	info.Name = "普通书籍源"
	info.Kind = source.KindBook
	return info
}

type textBookSource struct {
	*staticSource
	text string
}

func (s *textBookSource) Info() model.SourceInfo {
	info := s.staticSource.Info()
	info.ID = "text-book"
	info.Name = "文本书籍源"
	info.Kind = source.KindBook
	return info
}

func (s *textBookSource) ChapterContent(context.Context, source.Account, string, model.Chapter) (string, error) {
	return s.text, nil
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

type archiveBookSource struct {
	detail      model.Comic
	archive     source.ChapterArchive
	archives    map[string]source.ChapterArchive
	imageData   map[string][]byte
	coverData   []byte
	fallbackTxt string
}

func (s *archiveBookSource) Info() model.SourceInfo {
	return model.SourceInfo{ID: "archive-book", Name: "插件书籍", Kind: source.KindBook, CanSearch: true, CanBrowse: true}
}

func (s *archiveBookSource) Search(context.Context, source.Account, string, int) (model.SearchResult, error) {
	return model.SearchResult{}, nil
}

func (s *archiveBookSource) Browse(context.Context, source.Account, model.BrowseOptions, int) (model.SearchResult, error) {
	return model.SearchResult{}, nil
}

func (s *archiveBookSource) Detail(context.Context, source.Account, string) (model.Comic, error) {
	return s.detail, nil
}

func (s *archiveBookSource) Chapters(context.Context, source.Account, string) ([]model.Chapter, error) {
	return nil, nil
}

func (s *archiveBookSource) Pages(context.Context, source.Account, string, model.Chapter) ([]model.Page, error) {
	return nil, nil
}

func (s *archiveBookSource) ChapterArchive(_ context.Context, _ source.Account, _ string, chapter model.Chapter) (source.ChapterArchive, error) {
	if archive, ok := s.archives[chapter.ID]; ok {
		return archive, nil
	}
	return s.archive, nil
}

func (s *archiveBookSource) ChapterContent(context.Context, source.Account, string, model.Chapter) (string, error) {
	return s.fallbackTxt, nil
}

func (s *archiveBookSource) PageImage(_ context.Context, _ source.Account, _ string, _ model.Chapter, page model.Page) ([]byte, string, error) {
	data, ok := s.imageData[page.URL]
	if !ok {
		return nil, "", fmt.Errorf("unknown image %q", page.URL)
	}
	return data, "image/png", nil
}

func (s *archiveBookSource) CoverImage(context.Context, source.Account, model.Comic) ([]byte, string, error) {
	return s.coverData, "image/png", nil
}

type ebookFileSource struct {
	*archiveBookSource
	data     []byte
	filename string
}

func (s *ebookFileSource) ChapterFile(context.Context, source.Account, string, model.Chapter) ([]byte, string, error) {
	return s.data, s.filename, nil
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
	// Cache cleanup runs asynchronously after the job finishes, so give it a
	// brief window instead of asserting immediately.
	waitForMissingPath(t, filepath.Join(cacheDir, "job-1"))

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
	for _, value := range []string{"<PageCount>", "<Pages>", "<Status>"} {
		if bytes.Contains(nfo, []byte(value)) {
			t.Fatalf("standalone ComicInfo.xml contains chapter-only %q: %s", value, nfo)
		}
	}
	embedded, ok := first["ComicInfo.xml"]
	if !ok {
		t.Fatal("CBZ is missing embedded ComicInfo.xml")
	}
	for _, value := range []string{
		"<Series>测试作品</Series>",
		"<PageCount>3</PageCount>",
		`<Pages>`,
		`<Page Image="0" Type="Story"></Page>`,
		"<Manga>YesAndRightToLeft</Manga>",
		"<LanguageISO>zh-Hans</LanguageISO>",
		"<Genre>测试,漫画</Genre>",
		"<Tags>测试,漫画</Tags>",
	} {
		if !bytes.Contains(embedded, []byte(value)) {
			t.Fatalf("embedded ComicInfo.xml is missing %q: %s", value, embedded)
		}
	}
	if bytes.Contains(embedded, []byte("<Status>")) {
		t.Fatalf("ComicInfo.xml contains a non-v2.1 Status element: %s", embedded)
	}

	// KMRS reads the series summary only from series.json. Keep it strict enough
	// for its serde model even when numeric/string fields are empty or zero.
	seriesPath := filepath.Join(downloadDir, "测试作品", "series.json")
	if !validMylarSeriesJSON(seriesPath) {
		t.Fatalf("series.json does not match KMRS's Mylar schema")
	}
	seriesPayload, err := os.ReadFile(seriesPath)
	if err != nil {
		t.Fatalf("read series.json: %v", err)
	}
	var series MylarSeries
	if err := json.Unmarshal(seriesPayload, &series); err != nil {
		t.Fatalf("parse series.json: %v", err)
	}
	if series.Metadata.Name != "测试作品" || series.Metadata.DescriptionText != "测试简介" {
		t.Fatalf("unexpected series metadata: %+v", series.Metadata)
	}
	// description_formatted 必须和纯文本一致，不能包 <p>，否则 KMRS 会把标签一起显示。
	if series.Metadata.DescriptionFormatted != "测试简介" {
		t.Fatalf("description_formatted should stay plain text: %q", series.Metadata.DescriptionFormatted)
	}
	if series.Metadata.Status != "Continuing" || series.Metadata.TotalIssues != 2 || series.Metadata.ComicImage != "cover.png" {
		t.Fatalf("series identity/status mismatch: %+v", series.Metadata)
	}
	for _, obsolete := range []string{"metadata.opf", "book.json"} {
		if _, err := os.Stat(filepath.Join(downloadDir, "测试作品", obsolete)); !os.IsNotExist(err) {
			t.Fatalf("obsolete metadata sidecar %s should not exist: %v", obsolete, err)
		}
	}
}

func TestOtherSourcePreservesProvidedContentType(t *testing.T) {
	media := []byte("video-bytes-with-mp4-content-type")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(media)
	}))
	defer server.Close()

	registry := sources.NewRegistry(server.Client(), nil, nil)
	registry.Register(&otherSource{staticSource: &staticSource{
		detail: model.Comic{SourceID: "other-stub", ID: "video-1", Title: "其他作品", Author: "作者"},
		pages: map[string][]model.Page{
			"chapter-1": {{URL: server.URL + "/stream", ContentType: "video/mp4"}},
		},
	}})
	store := &memoryJobs{}
	downloadDir := t.TempDir()
	engine := NewEngine(registry, store, downloadDir, 1, 1, log.New(io.Discard, "", 0))
	job, err := store.CreateDownloadJob(context.Background(), model.DownloadJob{
		SourceID:     "other-stub",
		ComicID:      "video-1",
		ComicTitle:   "其他作品",
		ChapterID:    "chapter-1",
		ChapterTitle: "第1话",
		ChapterOrder: 1,
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := engine.download(context.Background(), job); err != nil {
		t.Fatalf("download other source: %v", err)
	}
	output := filepath.Join(downloadDir, "其他作品", "第1话.mp4")
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read downloaded media: %v", err)
	}
	if !bytes.Equal(data, media) {
		t.Fatalf("downloaded media changed: %q", data)
	}
	stored, _ := store.find(job.ID)
	if stored.Status != "completed" || stored.FilePath != output {
		t.Fatalf("stored job = %+v", stored)
	}
	if matches, _ := filepath.Glob(filepath.Join(downloadDir, "其他作品", "*.cbz")); len(matches) != 0 {
		t.Fatalf("other source was incorrectly wrapped as CBZ: %v", matches)
	}
}

func TestMergedComicDownloadWritesOneOrderedCBZ(t *testing.T) {
	images := map[string][]byte{
		"/cover.png":  pngImage(t, 0x10),
		"/c1-p1.png":  pngImage(t, 0x21),
		"/c1-p2.png":  pngImage(t, 0x22),
		"/c2-p1.png":  pngImage(t, 0x31),
		"/c3-p1.png":  pngImage(t, 0x41),
		"/c3-p2.png":  pngImage(t, 0x42),
		"/unused.png": pngImage(t, 0x55),
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

	item := &staticSource{
		detail: model.Comic{
			SourceID:    "stub",
			ID:          "merged-comic",
			Title:       "合并漫画",
			Author:      "测试作者",
			Description: "合并下载测试",
			Cover:       server.URL + "/cover.png",
			Tags:        []string{"合并", "漫画"},
		},
		pages: map[string][]model.Page{
			"1": {
				{URL: server.URL + "/c1-p1.png"},
				{URL: server.URL + "/c1-p2.png"},
			},
			"2": {
				{URL: server.URL + "/c2-p1.png"},
			},
			"3": {
				{URL: server.URL + "/c3-p1.png"},
				{URL: server.URL + "/c3-p2.png"},
			},
		},
	}
	registry := sources.NewRegistry(server.Client(), nil, nil)
	registry.Register(item)

	store := &memoryJobs{}
	downloadDir := t.TempDir()
	engine := NewEngine(registry, store, downloadDir, 1, 2, log.New(io.Discard, "", 0))
	engine.SetCacheDir(t.TempDir())
	job, err := store.CreateDownloadJob(context.Background(), model.DownloadJob{
		SourceID:     "stub",
		ComicID:      "merged-comic",
		ComicTitle:   "合并漫画",
		ComicAuthor:  "测试作者",
		ChapterID:    "merge-test",
		ChapterTitle: "第1话 - 第3话",
		ChapterOrder: 1,
		DownloadDir:  downloadDir,
		MergeChapters: []model.Chapter{
			{ID: "1", Title: "第1话", Order: 1},
			{ID: "2", Title: "第2话", Order: 2},
			{ID: "3", Title: "第3话", Order: 3},
		},
	})
	if err != nil {
		t.Fatalf("create merged comic job: %v", err)
	}
	if err := engine.download(context.Background(), job); err != nil {
		t.Fatalf("download merged comic: %v", err)
	}

	archives := listCBZ(t, downloadDir)
	if len(archives) != 1 {
		t.Fatalf("merged comic produced %d CBZ files, want 1 (%v)", len(archives), archives)
	}
	archivePath := filepath.Join(downloadDir, "合并漫画", "第1话 - 第3话.cbz")
	archive := readZip(t, archivePath)
	assertEntries(t, archive, []string{
		"001-0001.png",
		"001-0002.png",
		"002-0001.png",
		"003-0001.png",
		"003-0002.png",
		"ComicInfo.xml",
	})
	order := []struct {
		entry string
		want  string
	}{
		{"001-0001.png", "/c1-p1.png"},
		{"001-0002.png", "/c1-p2.png"},
		{"002-0001.png", "/c2-p1.png"},
		{"003-0001.png", "/c3-p1.png"},
		{"003-0002.png", "/c3-p2.png"},
	}
	for _, check := range order {
		if !bytes.Equal(archive[check.entry], images[check.want]) {
			t.Fatalf("merged entry %s does not match %s", check.entry, check.want)
		}
	}
	if !bytes.Contains(archive["ComicInfo.xml"], []byte("<PageCount>5</PageCount>")) {
		t.Fatalf("merged ComicInfo.xml has wrong page count: %s", archive["ComicInfo.xml"])
	}
	completed, ok := store.find(job.ID)
	if !ok || completed.Status != "completed" || completed.TotalPages != 5 || completed.CompletedPages != 5 {
		t.Fatalf("merged comic job state = %+v", completed)
	}
}

func TestMergedBookDownloadWritesOneEPUB(t *testing.T) {
	firstImage := pngImage(t, 0x61)
	secondImage := pngImage(t, 0x72)
	item := &archiveBookSource{
		detail: model.Comic{
			SourceID:    "archive-book",
			ID:          "merged-book",
			Title:       "合并小说",
			Author:      "测试作者",
			Description: "合并书籍测试",
			Cover:       "cover",
			Tags:        []string{"小说", "合并"},
		},
		archives: map[string]source.ChapterArchive{
			"chapter-1": {
				HTML:   `<p>第一章</p><img src="chapter-1-image">`,
				Images: []model.Page{{URL: "chapter-1-image"}},
			},
			"chapter-2": {
				HTML:   `<p>第二章</p><img src="chapter-2-image">`,
				Images: []model.Page{{URL: "chapter-2-image"}},
			},
		},
		imageData: map[string][]byte{
			"chapter-1-image": firstImage,
			"chapter-2-image": secondImage,
		},
		coverData: pngImage(t, 0x73),
	}
	registry := sources.NewRegistry(source.NewHTTPClient(), nil, nil)
	registry.Register(item)

	store := &memoryJobs{}
	downloadDir := t.TempDir()
	engine := NewEngine(registry, store, downloadDir, 1, 1, log.New(io.Discard, "", 0))
	engine.SetCacheDir(t.TempDir())
	job, err := store.CreateDownloadJob(context.Background(), model.DownloadJob{
		SourceID:     "archive-book",
		ComicID:      "merged-book",
		ComicTitle:   "合并小说",
		ChapterID:    "merge-book-test",
		ChapterTitle: "第1章 - 第2章",
		ChapterOrder: 1,
		DownloadDir:  downloadDir,
		MergeChapters: []model.Chapter{
			{ID: "chapter-1", Title: "第1章", Order: 1},
			{ID: "chapter-2", Title: "第2章", Order: 2},
		},
	})
	if err != nil {
		t.Fatalf("create merged book job: %v", err)
	}
	if err := engine.download(context.Background(), job); err != nil {
		t.Fatalf("download merged book: %v", err)
	}

	if books := listEPUB(t, downloadDir); len(books) != 1 {
		t.Fatalf("merged book produced %d EPUB files, want 1 (%v)", len(books), books)
	}
	if archives := listCBZ(t, downloadDir); len(archives) != 0 {
		t.Fatalf("merged book unexpectedly produced CBZ files: %v", archives)
	}
	archivePath := filepath.Join(downloadDir, "合并小说", "第1章 - 第2章.epub")
	archive := readZip(t, archivePath)
	assertEntries(t, archive, []string{
		"mimetype",
		"META-INF/container.xml",
		"OEBPS/toc.ncx",
		"OEBPS/content.opf",
		"OEBPS/nav.xhtml",
		"OEBPS/style.css",
		"OEBPS/chapter-0001.xhtml",
		"OEBPS/chapter-0002.xhtml",
		"OEBPS/images/001-0001.png",
		"OEBPS/images/002-0001.png",
	})
	if string(archive["mimetype"]) != "application/epub+zip" {
		t.Fatalf("unexpected EPUB mimetype: %q", archive["mimetype"])
	}
	if !bytes.Equal(archive["OEBPS/images/001-0001.png"], firstImage) || !bytes.Equal(archive["OEBPS/images/002-0001.png"], secondImage) {
		t.Fatal("merged book image bytes mismatch")
	}
	if !strings.Contains(string(archive["OEBPS/chapter-0001.xhtml"]), `src="images/001-0001.png"`) ||
		!strings.Contains(string(archive["OEBPS/chapter-0002.xhtml"]), `src="images/002-0001.png"`) {
		t.Fatal("merged book XHTML did not reference EPUB-local images")
	}
	packageDocument := string(archive["OEBPS/content.opf"])
	for _, value := range []string{"<dc:title>合并小说</dc:title>", `<itemref idref="chapter-1"/>`, `<itemref idref="chapter-2"/>`} {
		if !strings.Contains(packageDocument, value) {
			t.Fatalf("EPUB package is missing %q: %s", value, packageDocument)
		}
	}
	if !firstZipEntryIsMimetype(t, archivePath) {
		t.Fatal("EPUB mimetype must be the first ZIP entry and stored without compression")
	}
}

func TestMergedBookDownloadRejectsSourceWithoutChapterArchive(t *testing.T) {
	registry := sources.NewRegistry(source.NewHTTPClient(), nil, nil)
	registry.Register(&unsupportedMergedBookSource{
		staticSource: &staticSource{detail: model.Comic{SourceID: "plain-book", ID: "book", Title: "普通书籍"}},
	})
	store := &memoryJobs{}
	engine := NewEngine(registry, store, t.TempDir(), 1, 1, log.New(io.Discard, "", 0))
	if err := engine.download(context.Background(), model.DownloadJob{
		SourceID:     "plain-book",
		ComicID:      "book",
		ComicTitle:   "普通书籍",
		ChapterID:    "merge",
		ChapterTitle: "第1章 - 第2章",
		DownloadDir:  t.TempDir(),
		MergeChapters: []model.Chapter{
			{ID: "1", Title: "第1章", Order: 1},
			{ID: "2", Title: "第2章", Order: 2},
		},
	}); err == nil || !strings.Contains(err.Error(), "不支持合并下载") {
		t.Fatalf("unsupported merged book error = %v", err)
	}
}

func TestPluginBookChapterDownloadsAsEPUB(t *testing.T) {
	firstImage := pngImage(t, 0x41)
	secondImage := pngImage(t, 0x52)
	item := &archiveBookSource{
		detail: model.Comic{
			SourceID:    "archive-book",
			ID:          "book-1",
			Title:       "插件小说",
			Author:      "测试作者",
			Description: "章节简介",
			Cover:       "cover",
			Tags:        []string{"小说", "测试"},
		},
		archive: source.ChapterArchive{
			HTML: `<p>正文</p><img src="illustration-1"><img data-src="illustration-2">`,
			Images: []model.Page{
				{URL: "illustration-1"},
				{URL: "illustration-2"},
			},
		},
		imageData: map[string][]byte{
			"illustration-1": firstImage,
			"illustration-2": secondImage,
		},
		coverData:   pngImage(t, 0x63),
		fallbackTxt: "这个回退文本不应落盘",
	}
	registry := sources.NewRegistry(source.NewHTTPClient(), nil, nil)
	registry.Register(item)

	store := &memoryJobs{}
	downloadDir := t.TempDir()
	engine := NewEngine(registry, store, downloadDir, 1, 1, log.New(io.Discard, "", 0))
	job, err := store.CreateDownloadJob(context.Background(), model.DownloadJob{
		SourceID:     "archive-book",
		ComicID:      "book-1",
		ComicTitle:   "插件小说",
		ChapterID:    "chapter-1",
		ChapterTitle: "第1章",
		ChapterOrder: 1,
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := engine.download(context.Background(), job); err != nil {
		t.Fatalf("download plugin book: %v", err)
	}

	archivePath := filepath.Join(downloadDir, "插件小说", "第1章.epub")
	archive := readZip(t, archivePath)
	htmlName := "OEBPS/chapter-0001.xhtml"
	firstImageName := "OEBPS/images/00000000_00010001.png"
	secondImageName := "OEBPS/images/00000000_00010002.png"
	assertEntries(t, archive, []string{
		"mimetype",
		"META-INF/container.xml",
		"OEBPS/toc.ncx",
		"OEBPS/content.opf",
		"OEBPS/nav.xhtml",
		"OEBPS/style.css",
		htmlName,
		firstImageName,
		secondImageName,
	})
	html := string(archive[htmlName])
	if !strings.Contains(html, `src="images/00000000_00010001.png"`) || !strings.Contains(html, `src="images/00000000_00010002.png"`) {
		t.Fatalf("chapter XHTML was not rewritten to EPUB-local images: %s", html)
	}
	if !bytes.Equal(archive[firstImageName], firstImage) || !bytes.Equal(archive[secondImageName], secondImage) {
		t.Fatal("chapter EPUB image bytes mismatch")
	}
	if _, err := os.Stat(filepath.Join(downloadDir, "插件小说", "第1章.txt")); !os.IsNotExist(err) {
		t.Fatalf("fallback text file should not exist: %v", err)
	}
	// Book downloads keep the same single sidecar format as comics.
	if _, err := os.Stat(filepath.Join(downloadDir, "插件小说", "book.json")); !os.IsNotExist(err) {
		t.Fatalf("book.json should not be written any more: %v", err)
	}
	seriesInfo, err := os.ReadFile(filepath.Join(downloadDir, "插件小说", "ComicInfo.xml"))
	if err != nil {
		t.Fatalf("read series ComicInfo.xml: %v", err)
	}
	for _, value := range []string{"<Series>插件小说</Series>", "<Writer>测试作者</Writer>", "<Manga>No</Manga>"} {
		if !bytes.Contains(seriesInfo, []byte(value)) {
			t.Fatalf("series ComicInfo.xml is missing %q: %s", value, seriesInfo)
		}
	}
}

func TestBookFileContentSourceKeepsNativeEPUB(t *testing.T) {
	item := &ebookFileSource{
		archiveBookSource: &archiveBookSource{
			detail: model.Comic{
				SourceID: "archive-book",
				ID:       "book-2",
				Title:    "原格式书籍",
			},
			archive: source.ChapterArchive{
				HTML: "<p>不应使用归档</p>",
			},
		},
		data:     []byte("epub-data"),
		filename: "book.epub",
	}
	registry := sources.NewRegistry(source.NewHTTPClient(), nil, nil)
	registry.Register(item)

	store := &memoryJobs{}
	downloadDir := t.TempDir()
	engine := NewEngine(registry, store, downloadDir, 1, 1, log.New(io.Discard, "", 0))
	job, err := store.CreateDownloadJob(context.Background(), model.DownloadJob{
		SourceID:     "archive-book",
		ComicID:      "book-2",
		ComicTitle:   "原格式书籍",
		ChapterID:    "chapter-1",
		ChapterTitle: "第1章",
		ChapterOrder: 1,
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := engine.download(context.Background(), job); err != nil {
		t.Fatalf("download ebook file: %v", err)
	}

	outputPath := filepath.Join(downloadDir, "原格式书籍", "book.epub")
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read original ebook: %v", err)
	}
	if string(data) != "epub-data" {
		t.Fatalf("original ebook data = %q", data)
	}
	if archives := listCBZ(t, downloadDir); len(archives) != 0 {
		t.Fatalf("original ebook was unexpectedly repackaged: %v", archives)
	}
}

func TestBookFileContentSourceRejectsNonEPUB(t *testing.T) {
	item := &ebookFileSource{
		archiveBookSource: &archiveBookSource{
			detail: model.Comic{
				SourceID: "archive-book",
				ID:       "book-pdf",
				Title:    "PDF 书籍",
			},
		},
		data:     []byte("%PDF-1.7"),
		filename: "book.pdf",
	}
	registry := sources.NewRegistry(source.NewHTTPClient(), nil, nil)
	registry.Register(item)

	downloadDir := t.TempDir()
	store := &memoryJobs{}
	engine := NewEngine(registry, store, downloadDir, 1, 1, log.New(io.Discard, "", 0))
	job, err := store.CreateDownloadJob(context.Background(), model.DownloadJob{
		SourceID:     "archive-book",
		ComicID:      "book-pdf",
		ComicTitle:   "PDF 书籍",
		ChapterID:    "chapter-1",
		ChapterTitle: "书籍",
		ChapterOrder: 1,
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := engine.download(context.Background(), job); err == nil || !strings.Contains(err.Error(), "仅支持 EPUB") {
		t.Fatalf("non-EPUB book error = %v", err)
	}
	if files := listEPUB(t, downloadDir); len(files) != 0 {
		t.Fatalf("non-EPUB source produced EPUB files: %v", files)
	}
}

func TestPlainTextBookDownloadsAsEPUB(t *testing.T) {
	item := &textBookSource{
		staticSource: &staticSource{detail: model.Comic{
			SourceID: "text-book",
			ID:       "text-1",
			Title:    "文本小说",
			Author:   "测试作者",
		}},
		text: "第一段正文\n\n第二段正文",
	}
	registry := sources.NewRegistry(source.NewHTTPClient(), nil, nil)
	registry.Register(item)

	downloadDir := t.TempDir()
	store := &memoryJobs{}
	engine := NewEngine(registry, store, downloadDir, 1, 1, log.New(io.Discard, "", 0))
	job, err := store.CreateDownloadJob(context.Background(), model.DownloadJob{
		SourceID:     "text-book",
		ComicID:      "text-1",
		ComicTitle:   "文本小说",
		ChapterID:    "chapter-1",
		ChapterTitle: "第1章",
		ChapterOrder: 1,
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := engine.download(context.Background(), job); err != nil {
		t.Fatalf("download text book: %v", err)
	}
	outputPath := filepath.Join(downloadDir, "文本小说", "第1章.epub")
	archive := readZip(t, outputPath)
	assertEntries(t, archive, []string{
		"mimetype",
		"META-INF/container.xml",
		"OEBPS/toc.ncx",
		"OEBPS/content.opf",
		"OEBPS/nav.xhtml",
		"OEBPS/style.css",
		"OEBPS/chapter-0001.xhtml",
	})
	if !strings.Contains(string(archive["OEBPS/chapter-0001.xhtml"]), "第一段正文") ||
		!strings.Contains(string(archive["OEBPS/chapter-0001.xhtml"]), "第二段正文") {
		t.Fatalf("plain-text EPUB is missing chapter content: %s", archive["OEBPS/chapter-0001.xhtml"])
	}
	if files := listCBZ(t, downloadDir); len(files) != 0 {
		t.Fatalf("plain-text book unexpectedly produced CBZ files: %v", files)
	}
}

func TestBookFileContentSourceWrapsPlainTextAsEPUB(t *testing.T) {
	item := &ebookFileSource{
		archiveBookSource: &archiveBookSource{
			detail: model.Comic{
				SourceID: "archive-book",
				ID:       "book-txt",
				Title:    "文本文件书籍",
				Author:   "测试作者",
			},
		},
		data:     []byte("第一行\n第二行"),
		filename: "book.txt",
	}
	registry := sources.NewRegistry(source.NewHTTPClient(), nil, nil)
	registry.Register(item)

	downloadDir := t.TempDir()
	store := &memoryJobs{}
	engine := NewEngine(registry, store, downloadDir, 1, 1, log.New(io.Discard, "", 0))
	job, err := store.CreateDownloadJob(context.Background(), model.DownloadJob{
		SourceID:     "archive-book",
		ComicID:      "book-txt",
		ComicTitle:   "文本文件书籍",
		ChapterID:    "chapter-1",
		ChapterTitle: "第1章",
		ChapterOrder: 1,
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := engine.download(context.Background(), job); err != nil {
		t.Fatalf("download text file book: %v", err)
	}
	outputPath := filepath.Join(downloadDir, "文本文件书籍", "第1章.epub")
	archive := readZip(t, outputPath)
	if !strings.Contains(string(archive["OEBPS/chapter-0001.xhtml"]), "第一行") ||
		!strings.Contains(string(archive["OEBPS/chapter-0001.xhtml"]), "第二行") {
		t.Fatalf("text-file EPUB is missing chapter content: %s", archive["OEBPS/chapter-0001.xhtml"])
	}
	if _, err := os.Stat(filepath.Join(downloadDir, "文本文件书籍", "book.txt")); !os.IsNotExist(err) {
		t.Fatalf("plain text source should not be stored as .txt: %v", err)
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
	// Older Manco versions left multiple metadata formats behind, and some
	// series.json files omitted required empty/zero fields. A repair must
	// rewrite the JSON and remove only the obsolete OPF/Manco sidecars.
	for _, legacy := range []string{"series.json", "metadata.opf", "book.json"} {
		if err := os.WriteFile(filepath.Join(seriesDir, legacy), []byte("legacy"), 0o644); err != nil {
			t.Fatal(err)
		}
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
	if !validMylarSeriesJSON(filepath.Join(seriesDir, "series.json")) {
		t.Fatalf("repair did not write a valid KMRS series.json")
	}
	seriesJSON, err := os.ReadFile(filepath.Join(seriesDir, "series.json"))
	if err != nil {
		t.Fatalf("read repaired series.json: %v", err)
	}
	var repairedSeries MylarSeries
	if err := json.Unmarshal(seriesJSON, &repairedSeries); err != nil {
		t.Fatalf("parse repaired series.json: %v", err)
	}
	if repairedSeries.Metadata.TotalIssues != 1 {
		t.Fatalf("repaired total_issues = %d, want 1", repairedSeries.Metadata.TotalIssues)
	}
	for _, obsolete := range []string{"metadata.opf", "book.json"} {
		if _, err := os.Stat(filepath.Join(seriesDir, obsolete)); !os.IsNotExist(err) {
			t.Fatalf("obsolete sidecar %s should have been removed: %v", obsolete, err)
		}
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

func TestMarshalComicInfoV21SchemaOrder(t *testing.T) {
	payload, err := marshalComicInfo(ComicInfo{
		Title:               "第1话",
		Series:              "测试作品",
		Number:              "1",
		Count:               12,
		Volume:              1,
		AlternateSeries:     "别名",
		AlternateNumber:     "A1",
		AlternateCount:      2,
		Summary:             "测试简介",
		Notes:               "Manco source=stub comicId=1",
		Year:                2026,
		Month:               10,
		Day:                 16,
		Writer:              "作者",
		Penciller:           "铅笔",
		Inker:               "墨线",
		Colorist:            "上色",
		Letterer:            "嵌字",
		CoverArtist:         "封面",
		Editor:              "编辑",
		Translator:          "翻译",
		Publisher:           "出版社",
		Imprint:             "副牌",
		Genre:               "漫画,测试",
		Tags:                "测试,标签",
		Web:                 "https://example.com/comic/1",
		PageCount:           2,
		LanguageISO:         "zh-Hans",
		Format:              "Digital",
		BlackAndWhite:       "No",
		Manga:               "YesAndRightToLeft",
		Characters:          "角色",
		Teams:               "团队",
		Locations:           "地点",
		ScanInformation:     "扫描",
		StoryArc:            "篇章",
		StoryArcNumber:      "1",
		SeriesGroup:         "系列组",
		AgeRating:           "Adults Only 18+",
		Pages:               &ComicInfoPages{Page: []ComicInfoPage{{Image: 0, Type: "Story"}, {Image: 1, Type: "Story"}}},
		CommunityRating:     "4.5",
		MainCharacterOrTeam: "主角",
		Review:              "评论",
		GTIN:                "9781234567897",
	})
	if err != nil {
		t.Fatalf("marshal ComicInfo: %v", err)
	}
	if bytes.Contains(payload, []byte("<Status>")) {
		t.Fatalf("ComicInfo v2.1 must not contain Status: %s", payload)
	}
	sequence := []string{
		"Title", "Series", "Number", "Count", "Volume", "AlternateSeries",
		"AlternateNumber", "AlternateCount", "Summary", "Notes", "Year", "Month",
		"Day", "Writer", "Penciller", "Inker", "Colorist", "Letterer",
		"CoverArtist", "Editor", "Translator", "Publisher", "Imprint", "Genre",
		"Tags", "Web", "PageCount", "LanguageISO", "Format", "BlackAndWhite",
		"Manga", "Characters", "Teams", "Locations", "ScanInformation",
		"StoryArc", "StoryArcNumber", "SeriesGroup", "AgeRating", "Pages",
		"CommunityRating", "MainCharacterOrTeam", "Review", "GTIN",
	}
	last := -1
	for _, name := range sequence {
		tag := []byte("<" + name + ">")
		index := bytes.Index(payload, tag)
		if index < 0 {
			t.Fatalf("ComicInfo.xml is missing %s: %s", tag, payload)
		}
		if index <= last {
			t.Fatalf("ComicInfo.xml field %s is out of v2.1 schema order: %s", tag, payload)
		}
		last = index
	}
}

func TestMylarSeriesJSONIncludesRequiredZeroFields(t *testing.T) {
	dir := t.TempDir()
	if err := writeSeriesMetadataFiles(model.Comic{}, model.SourceInfo{}, dir); err != nil {
		t.Fatalf("write series.json: %v", err)
	}
	path := filepath.Join(dir, "series.json")
	if !validMylarSeriesJSON(path) {
		t.Fatalf("minimal series.json does not match KMRS's Mylar schema")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Metadata map[string]json.RawMessage `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("parse series.json: %v", err)
	}
	for _, field := range []string{
		"type",
		"publisher",
		"name",
		"comicid",
		"year",
		"booktype",
		"comic_image",
		"total_issues",
		"publication_run",
		"status",
	} {
		if _, ok := payload.Metadata[field]; !ok {
			t.Fatalf("required KMRS field %q is missing from series.json: %s", field, raw)
		}
	}
}

func TestComicInfoAgeRatingUsesV21Enums(t *testing.T) {
	comic := model.Comic{Tags: []string{"成人"}}
	if got := comicInfoAgeRating(comic, comic.Tags); got != "Adults Only 18+" {
		t.Fatalf("ComicInfo age rating = %q, want Adults Only 18+", got)
	}
}

func TestRetryFailedByComicResetsRetryBudget(t *testing.T) {
	nextRetry := time.Now().Add(time.Hour)
	jobs := &memoryJobs{jobs: []model.DownloadJob{
		{ID: 1, SourceID: "stub", ComicID: "1", Status: "failed", RetryCount: 2, NextRetryAt: &nextRetry},
		{ID: 2, SourceID: "stub", ComicID: "1", Status: "completed", RetryCount: 2, NextRetryAt: &nextRetry},
		{ID: 3, SourceID: "other", ComicID: "1", Status: "failed", RetryCount: 2, NextRetryAt: &nextRetry},
	}}
	engine := NewEngine(sources.NewRegistry(nil, nil, nil), jobs, t.TempDir(), 1, 1, log.New(io.Discard, "", 0))

	retried, err := engine.RetryFailedByComic(context.Background(), "stub", "1")
	if err != nil {
		t.Fatalf("RetryFailedByComic() error = %v", err)
	}
	if retried != 1 {
		t.Fatalf("RetryFailedByComic() retried = %d, want 1", retried)
	}
	job, ok := jobs.find(1)
	if !ok {
		t.Fatal("failed job disappeared")
	}
	if job.Status != "queued" || job.RetryCount != 0 || job.NextRetryAt != nil {
		t.Fatalf("reset job = %#v, want queued with a fresh retry budget", job)
	}
	other, _ := jobs.find(3)
	if other.Status != "failed" || other.RetryCount != 2 {
		t.Fatalf("unrelated job changed = %#v", other)
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

func waitForMissingPath(t *testing.T, path string) {
	t.Helper()
	waitFor(t, 3*time.Second, func() bool {
		_, err := os.Stat(path)
		return os.IsNotExist(err)
	})
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

func listEPUB(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(path), ".epub") {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return found
}

func firstZipEntryIsMimetype(t *testing.T, path string) bool {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer reader.Close()
	if len(reader.File) == 0 {
		return false
	}
	first := reader.File[0]
	if first.Name != "mimetype" || first.Method != zip.Store {
		return false
	}
	handle, err := first.Open()
	if err != nil {
		t.Fatalf("open mimetype: %v", err)
	}
	defer handle.Close()
	data, err := io.ReadAll(handle)
	if err != nil {
		t.Fatalf("read mimetype: %v", err)
	}
	return string(data) == "application/epub+zip"
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

// 回归：全局章节并发一度被写成「各内容源上限之和」，来源一多就等于没有上限。
func TestGlobalChapterConcurrencyIsNotMultipliedBySources(t *testing.T) {
	engine := NewEngine(nil, nil, t.TempDir(), 2, 4, log.New(io.Discard, "", 0))
	engine.SetSourceConcurrency(map[string]int{"a": 1, "b": 1, "c": 1})

	claimed := 0
	sources := []string{"a", "b", "c", "d"}
	for index, sourceID := range sources {
		if engine.claim(model.DownloadJob{ID: int64(index + 1), SourceID: sourceID}) {
			claimed++
		}
	}
	if claimed != 2 {
		t.Fatalf("同时认领 %d 个章节，全局上限是 2，不应随内容源数量放大", claimed)
	}
}

func TestSetConcurrencyUpdatesGlobalLimitImmediately(t *testing.T) {
	engine := NewEngine(nil, nil, t.TempDir(), 1, 4, log.New(io.Discard, "", 0))
	first := model.DownloadJob{ID: 1, SourceID: "a"}
	second := model.DownloadJob{ID: 2, SourceID: "b"}

	if !engine.claim(first) {
		t.Fatal("第一个章节应该被认领")
	}
	if engine.claim(second) {
		t.Fatal("全局上限为 1 时第二个章节不应被认领")
	}

	engine.SetConcurrency(3, 8)
	if !engine.claim(second) {
		t.Fatal("提高全局上限后第二个章节应被认领")
	}
	// 再提到上限：第三个章节仍然受全局上限约束。
	if !engine.claim(model.DownloadJob{ID: 3, SourceID: "c"}) {
		t.Fatal("全局上限为 3 时第三个章节应被认领")
	}
	if engine.claim(model.DownloadJob{ID: 4, SourceID: "d"}) {
		t.Fatal("全局上限为 3 时第四个章节不应被认领")
	}

	engine.release(first)
	engine.release(second)
}
