package downloader

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/sources"
)

type resolvedVideoSource struct {
	*otherSource
	page       model.Page
	imageCalls atomic.Int32
}

func (s *resolvedVideoSource) ResolveMedia(context.Context, source.Account, model.Page) (model.Page, error) {
	return s.page, nil
}

func (s *resolvedVideoSource) PageFile(context.Context, source.Account, string, model.Chapter, model.Page) (io.ReadCloser, string, string, error) {
	s.imageCalls.Add(1)
	return nil, "", "", os.ErrInvalid
}

func TestVideoDownloadResolvesOutsideImageStore(t *testing.T) {
	payload := []byte("native-mp4-payload")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "runtime=accepted" || r.Header.Get("Referer") != "https://source.example/" {
			http.Error(w, "headers lost", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	fixture := &resolvedVideoSource{
		otherSource: &otherSource{staticSource: &staticSource{
			detail: model.Comic{Title: "视频", SourceID: "other-stub"},
			pages:  map[string][]model.Page{"episode": {{URL: "https://source.example/unresolved", AssetID: "runtime-page"}}},
		}},
		page: model.Page{URL: server.URL + "/stream", Headers: map[string]string{
			"Cookie": "runtime=accepted", "Referer": "https://source.example/",
		}},
	}
	registry := sources.NewRegistry(server.Client(), nil, nil)
	registry.Register(fixture)
	jobs := &memoryJobs{}
	engine := NewEngine(registry, jobs, t.TempDir(), 1, 1, log.New(io.Discard, "", 0))
	engine.SetCacheDir(t.TempDir())
	engine.SetSourceSettings(map[string]model.SourceDownloadSettings{"other-stub": {BatchSize: 2, BatchIntervalMinutes: 5}})
	job, err := jobs.CreateDownloadJob(context.Background(), model.DownloadJob{
		SourceID: "other-stub", ComicID: "video", ComicTitle: "视频", ChapterID: "episode", ChapterTitle: "第1集",
	})
	if err != nil {
		t.Fatal(err)
	}
	engine.runJob(context.Background(), job)
	stored, _ := jobs.find(job.ID)
	data, err := os.ReadFile(stored.FilePath)
	if err != nil || !bytes.Equal(data, payload) || !strings.HasSuffix(stored.FilePath, ".mp4") {
		t.Fatalf("native video was not preserved: path=%s err=%v data=%q", stored.FilePath, err, data)
	}
	if fixture.imageCalls.Load() != 0 {
		t.Fatal("video passed through image materialization")
	}
	if engine.inCooldown("other-stub") {
		t.Fatal("one completed chapter counted twice toward batch pause")
	}
}

func TestVideoStreamExceedsImageCacheLimit(t *testing.T) {
	const totalBytes = 65 << 20
	chunk := make([]byte, 64<<10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		for written := 0; written < totalBytes; written += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	fixture := &resolvedVideoSource{
		otherSource: &otherSource{staticSource: &staticSource{}},
		page:        model.Page{URL: server.URL + "/large.mp4"},
	}
	path, extension, _, err := downloadOtherPage(context.Background(), server.Client(), source.Account{}, fixture, "work", model.Chapter{}, model.Page{AssetID: "runtime-page"}, t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != totalBytes || extension != ".mp4" {
		t.Fatalf("large video was truncated: extension=%s err=%v", extension, err)
	}
	if fixture.imageCalls.Load() != 0 {
		t.Fatal("large video entered the image cache")
	}
}

func TestMediaCommandHeadersProxyAndProtocols(t *testing.T) {
	client := source.NewHTTPClientDirect()
	page := model.Page{URL: "https://media.example/master.m3u8", Headers: map[string]string{"Cookie": "runtime=1", "Referer": "https://source.example/"}}
	args, err := mediaCommandArgs(client, source.Account{Cookie: "stale=1"}, page, "video.mkv.part")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\n")
	if strings.Contains(joined, "stale=1") || !strings.Contains(joined, "Cookie: runtime=1") {
		t.Fatal("runtime cookie was overwritten")
	}
	if !strings.Contains(joined, "http,https,httpproxy,tcp,tls,crypto") || strings.Contains(joined, "file,") {
		t.Fatal("unsafe media protocol set")
	}
	for index := range args {
		if args[index] == "-http_proxy" && args[index+1] != "" {
			t.Fatal("direct source inherited proxy")
		}
	}
	if err := source.SetHTTPClientProxy(client, "http://127.0.0.1:9999"); err != nil {
		t.Fatal(err)
	}
	args, err = mediaCommandArgs(client, source.Account{}, page, "video.mkv.part")
	if err != nil || !strings.Contains(strings.Join(args, "\n"), "http://127.0.0.1:9999") {
		t.Fatalf("selected source proxy missing: %v", err)
	}
	page.Headers["Referer"] = "https://source.example/\r\nInjected: value"
	if _, err := mediaCommandArgs(client, source.Account{}, page, "video.mkv.part"); err == nil {
		t.Fatal("newline in media headers was accepted")
	}
}

func TestManifestDetection(t *testing.T) {
	for _, test := range []struct {
		mime, url, sample string
		want              bool
	}{
		{"application/octet-stream", "https://cdn.example/master.m3u8?token=x", "", true},
		{"application/vnd.apple.mpegurl", "https://cdn.example/stream", "", true},
		{"application/octet-stream", "https://cdn.example/stream", "#EXTM3U\n", true},
		{"application/dash+xml", "https://cdn.example/manifest", "", true},
		{"video/mp4", "https://cdn.example/movie.mp4", "", false},
	} {
		if got := isMediaManifest(test.mime, test.url, []byte(test.sample)); got != test.want {
			t.Fatalf("manifest detection %s/%s = %v", test.mime, test.url, got)
		}
	}
}

func TestPageWorkersBoundGoroutinesAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	baseline := runtime.NumGoroutine()
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	done := make(chan struct{})
	var calls atomic.Int32
	go func() {
		runPageWorkers(ctx, 10000, 2, func(int) {
			calls.Add(1)
			entered <- struct{}{}
			<-release
		})
		close(done)
	}()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	if delta := runtime.NumGoroutine() - baseline; delta > 8 {
		t.Errorf("10000 pages spawned %d goroutines", delta)
	}
	cancel()
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("workers ignored cancellation")
	}
	if calls.Load() != 2 {
		t.Fatalf("ran %d pages after cancellation", calls.Load())
	}
}

func TestDownloadDetailCacheHasHardLimit(t *testing.T) {
	engine := NewEngine(nil, &memoryJobs{}, t.TempDir(), 1, 1, log.New(io.Discard, "", 0))
	fixture := &staticSource{detail: model.Comic{Title: "作品", Tags: []string{"标签"}}}
	for index := 0; index < maxDetailCacheSize*3; index++ {
		if _, err := engine.cachedSourceDetail(context.Background(), fixture, source.Account{}, filepath.Base(t.TempDir())); err != nil {
			t.Fatal(err)
		}
	}
	if len(engine.details) > maxDetailCacheSize {
		t.Fatalf("cache retained %d entries", len(engine.details))
	}
}

// Runs against a local HLS fixture when FFmpeg is installed; no source website
// is contacted and the source's authentication headers are required.
func TestHLSDownloadIntegration(t *testing.T) {
	binary, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg not installed")
	}
	dir := t.TempDir()
	generate := exec.Command(binary, "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=black:s=64x64:r=10", "-t", "2", "-c:v", "mpeg2video", "-hls_time", "1", "-hls_list_size", "0", "-f", "hls", filepath.Join(dir, "media.m3u8"))
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate HLS fixture: %v: %s", err, output)
	}
	files := http.FileServer(http.Dir(dir))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "runtime=1" {
			http.Error(w, "missing runtime cookie", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/master.m3u8" {
			_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=100000\nmedia.m3u8\n")
			return
		}
		files.ServeHTTP(w, r)
	}))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "video.mkv")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := downloadMediaManifest(ctx, source.NewHTTPClientDirect(), source.Account{}, model.Page{URL: server.URL + "/master.m3u8", Headers: map[string]string{"Cookie": "runtime=1"}}, target); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || len(data) < 4 || !bytes.Equal(data[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}) {
		t.Fatalf("not a local Matroska video: %v", err)
	}
	if _, err := os.Stat(target + ".part"); !os.IsNotExist(err) {
		t.Fatal("partial file was left behind")
	}
}
