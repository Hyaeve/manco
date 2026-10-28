package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hyaeve/manco/internal/config"
)

func TestThumbnailRejectsHugeImageBeforePixelDecode(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	data := encoded.Bytes()
	binary.BigEndian.PutUint32(data[16:20], 20000)
	binary.BigEndian.PutUint32(data[20:24], 20000)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	if _, err := buildThumbnailFromReader(bytes.NewReader(data), 240); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("huge declared image dimensions were not rejected: %v", err)
	}
}

func TestThumbnailCacheCoalescesAndPersists(t *testing.T) {
	server := &Server{cfg: config.Config{DataDir: t.TempDir()}}
	var builds atomic.Int32
	var wait sync.WaitGroup
	for range 12 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			data, err := server.cachedThumbnail(context.Background(), "same-image", func() ([]byte, error) {
				builds.Add(1)
				time.Sleep(10 * time.Millisecond)
				return []byte("thumbnail"), nil
			})
			if err != nil || string(data) != "thumbnail" {
				t.Errorf("thumbnail result = %s, %v", data, err)
			}
		}()
	}
	wait.Wait()
	reopened := &Server{cfg: server.cfg}
	if _, err := reopened.cachedThumbnail(context.Background(), "same-image", func() ([]byte, error) {
		builds.Add(1)
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if builds.Load() != 1 {
		t.Fatalf("thumbnail builds = %d, want 1", builds.Load())
	}
}

func TestThumbnailGenerationHasSharedConcurrencyLimit(t *testing.T) {
	server := &Server{cfg: config.Config{DataDir: t.TempDir()}}
	var active, peak atomic.Int32
	var wait sync.WaitGroup
	for index := range 10 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := server.cachedThumbnail(context.Background(), filepath.Join("image", string(rune('a'+index))), func() ([]byte, error) {
				current := active.Add(1)
				for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
				}
				defer active.Add(-1)
				time.Sleep(10 * time.Millisecond)
				return []byte("thumbnail"), nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	if peak.Load() != 2 {
		t.Fatalf("peak thumbnail concurrency = %d, want 2", peak.Load())
	}
}

func TestRemoteThumbnailReusesDiskCache(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 500, 700))); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(encoded.Bytes())
	}))
	defer upstream.Close()
	server := &Server{cfg: config.Config{DataDir: t.TempDir()}}
	for range 2 {
		request := httptest.NewRequest(http.MethodGet, "/api/proxy/image?w=160", nil)
		imageRequest, _ := http.NewRequestWithContext(request.Context(), http.MethodGet, upstream.URL, nil)
		recorder := httptest.NewRecorder()
		if !server.serveRemoteThumbnail(recorder, request, imageRequest, upstream.Client(), 160) {
			t.Fatal("remote thumbnail was not served")
		}
		if recorder.Header().Get("Content-Type") != "image/jpeg" {
			t.Fatalf("thumbnail content type = %s", recorder.Header().Get("Content-Type"))
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("remote cover requested %d times, want 1", requests.Load())
	}
}

func TestRemoteThumbnailDoesNotRetryFailedFetchInSameRequest(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()
	server := &Server{cfg: config.Config{DataDir: t.TempDir()}}
	request := httptest.NewRequest(http.MethodGet, "/api/proxy/image?w=160", nil)
	imageRequest, _ := http.NewRequestWithContext(request.Context(), http.MethodGet, upstream.URL, nil)
	recorder := httptest.NewRecorder()
	if !server.serveRemoteThumbnail(recorder, request, imageRequest, upstream.Client(), 160) ||
		recorder.Code != http.StatusBadGateway || requests.Load() != 1 {
		t.Fatalf("failed cover: status=%d requests=%d", recorder.Code, requests.Load())
	}
}

func TestThumbnailCanceledWaitDoesNotDecode(t *testing.T) {
	thumbnailSlots <- struct{}{}
	thumbnailSlots <- struct{}{}
	defer func() { <-thumbnailSlots; <-thumbnailSlots }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := &Server{cfg: config.Config{DataDir: t.TempDir()}}
	_, err := server.cachedThumbnail(ctx, "canceled-image", func() ([]byte, error) {
		t.Fatal("canceled request decoded an image")
		return nil, nil
	})
	if err != context.Canceled {
		t.Fatalf("canceled thumbnail error = %v", err)
	}
}

func TestBuildThumbnailFromReaderScalesDown(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 800, 1200))
	for x := 0; x < 800; x++ {
		for y := 0; y < 1200; y++ {
			source.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 200, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatalf("encode source: %v", err)
	}

	thumb, err := buildThumbnailFromReader(bytes.NewReader(encoded.Bytes()), 200)
	if err != nil {
		t.Fatalf("buildThumbnailFromReader() error = %v", err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(thumb))
	if err != nil {
		t.Fatalf("decode thumbnail: %v", err)
	}
	if got, want := decoded.Bounds().Dx(), 200; got != want {
		t.Fatalf("缩略图宽度 = %d, want %d", got, want)
	}
	if got, want := decoded.Bounds().Dy(), 300; got != want {
		t.Fatalf("缩略图高度 = %d, want %d（等比缩放）", got, want)
	}

	// 原图本来就不比目标宽时返回错误，调用方会直接回退到原图。
	if _, err := buildThumbnailFromReader(bytes.NewReader(encoded.Bytes()), 2000); err == nil {
		t.Fatal("原图更窄时应返回错误以便回退原图")
	}
	// 不是图片的字节流同样返回错误，不会让请求失败。
	if _, err := buildThumbnailFromReader(bytes.NewReader([]byte("not an image")), 320); err == nil {
		t.Fatal("非图片内容应返回错误")
	}
}
