package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

var thumbnailSlots = make(chan struct{}, 2)

type thumbnailCall struct {
	done chan struct{}
	data []byte
	err  error
}

type thumbnailFetchError struct{}

func (thumbnailFetchError) Error() string { return "cover fetch failed" }

// 封面一律下缩略图：源站封面动辄几千像素，浏览器解码一张就是几十 MB 位图，
// 网格里几十张直接把内存和滚动帧率拖垮。这里按需缩放并按「文件+尺寸」缓存。
const (
	thumbMaxWidth    = 480
	thumbCacheFolder = "thumbnails"
)

// requestedThumbWidth 解析 ?w=，非法或未请求时返回 0。
func requestedThumbWidth(r *http.Request) int {
	value := strings.TrimSpace(r.URL.Query().Get("w"))
	if value == "" {
		return 0
	}
	width, err := strconv.Atoi(value)
	if err != nil || width <= 0 {
		return 0
	}
	if width > thumbMaxWidth {
		width = thumbMaxWidth
	}
	return width
}

// serveThumbnail 尝试把 sourcePath 缩放成 JPEG 后直接写响应。返回 false 表示
// 这次请求不要缩略图、原图本来就够小，或解码失败——调用方继续按原图发送即可。
func (s *Server) serveThumbnail(w http.ResponseWriter, r *http.Request, sourcePath string, width int) bool {
	if width <= 0 || s == nil {
		return false
	}
	info, err := os.Stat(sourcePath)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return false
	}
	data, err := s.cachedThumbnail(r.Context(), fmt.Sprintf("%s|%d|%d|%d", sourcePath, info.Size(), info.ModTime().UnixNano(), width), func() ([]byte, error) {
		return buildThumbnail(sourcePath, width)
	})
	if err != nil {
		return false
	}
	writeThumbBytes(w, r, data)
	return true
}

// The slot is acquired before reading compressed bytes, not just before decoding.
func (s *Server) cachedThumbnail(ctx context.Context, key string, build func() ([]byte, error)) ([]byte, error) {
	sum := sha256.Sum256([]byte(key))
	name := hex.EncodeToString(sum[:12]) + ".jpg"
	directory := filepath.Join(s.cfg.DataDir, thumbCacheFolder)
	target := filepath.Join(directory, name)
	if data, readErr := os.ReadFile(target); readErr == nil && len(data) > 0 {
		return data, nil
	}
	s.thumbnailMu.Lock()
	if call := s.thumbnailJobs[name]; call != nil {
		s.thumbnailMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-call.done:
			return call.data, call.err
		}
	}
	if s.thumbnailJobs == nil {
		s.thumbnailJobs = map[string]*thumbnailCall{}
	}
	call := &thumbnailCall{done: make(chan struct{})}
	s.thumbnailJobs[name] = call
	s.thumbnailMu.Unlock()
	defer func() {
		s.thumbnailMu.Lock()
		delete(s.thumbnailJobs, name)
		close(call.done)
		s.thumbnailMu.Unlock()
	}()
	select {
	case thumbnailSlots <- struct{}{}:
		defer func() { <-thumbnailSlots }()
	case <-ctx.Done():
		call.err = ctx.Err()
		return nil, call.err
	}
	// Another request may have completed between the initial lookup and registration.
	if call.data, call.err = os.ReadFile(target); call.err == nil && len(call.data) > 0 {
		return call.data, nil
	}
	call.data, call.err = build()
	if call.err == nil && s.cfg.DataDir != "" {
		if err := os.MkdirAll(directory, 0o755); err == nil {
			if file, err := os.CreateTemp(directory, ".thumb-*"); err == nil {
				temp := file.Name()
				_, writeErr := file.Write(call.data)
				closeErr := file.Close()
				if writeErr == nil && closeErr == nil {
					_ = os.Rename(temp, target)
				}
				_ = os.Remove(temp)
			}
		}
		s.pruneThumbnails(directory)
	}
	return call.data, call.err
}

func (s *Server) pruneThumbnails(directory string) {
	s.thumbnailMu.Lock()
	if time.Since(s.thumbnailPruned) < time.Hour {
		s.thumbnailMu.Unlock()
		return
	}
	s.thumbnailPruned = time.Now()
	s.thumbnailMu.Unlock()
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	type candidate struct {
		name string
		size int64
		at   time.Time
	}
	candidates := make([]candidate, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jpg") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{entry.Name(), info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].at.Before(candidates[j].at) })
	remaining := len(candidates)
	for _, item := range candidates {
		if remaining <= 2048 && total <= 128<<20 && time.Since(item.at) < 7*24*time.Hour {
			break
		}
		if os.Remove(filepath.Join(directory, item.name)) == nil {
			total -= item.size
			remaining--
		}
	}
}

func (s *Server) serveRemoteThumbnail(w http.ResponseWriter, r *http.Request, request *http.Request, client *http.Client, width int) bool {
	headers, _ := json.Marshal(request.Header)
	// Remote covers can change without changing their URL; rotate the disk key daily.
	key := fmt.Sprintf("remote|%s|%s|%d|%s", request.URL.String(), headers, width, time.Now().UTC().Format("2006-01-02"))
	var original []byte
	var contentType string
	data, err := s.cachedThumbnail(r.Context(), key, func() ([]byte, error) {
		response, err := client.Do(request)
		if err != nil {
			return nil, thumbnailFetchError{}
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, thumbnailFetchError{}
		}
		original, err = io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
		if err != nil || len(original) > 16<<20 {
			original = nil
			return nil, fmt.Errorf("image exceeds thumbnail input limit")
		}
		contentType = response.Header.Get("Content-Type")
		return buildThumbnailFromReader(bytes.NewReader(original), width)
	})
	if err == nil {
		writeThumbBytes(w, r, data)
		return true
	}
	if r.Context().Err() != nil {
		return true
	}
	if _, failed := err.(thumbnailFetchError); failed {
		writeError(w, http.StatusBadGateway, "封面加载失败，请稍后重试")
		return true
	}
	if len(original) == 0 {
		return false
	}
	if contentType == "" {
		contentType = http.DetectContentType(original)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(original)
	return true
}

func buildThumbnail(path string, width int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return buildThumbnailFromReader(file, width)
}

// buildThumbnailFromReader 就地把一张图缩成 JPEG；原图本来就不宽时返回错误，
// 让调用方继续按原图发送。
func buildThumbnailFromReader(reader io.Reader, width int) ([]byte, error) {
	var prefix bytes.Buffer
	configuration, _, err := image.DecodeConfig(io.TeeReader(io.LimitReader(reader, 1<<20), &prefix))
	if err != nil {
		return nil, err
	}
	if configuration.Width <= 0 || configuration.Height <= 0 ||
		int64(configuration.Width)*int64(configuration.Height) > 24_000_000 {
		return nil, fmt.Errorf("image too large for thumbnail decoding")
	}
	if configuration.Width <= width {
		return nil, fmt.Errorf("already small")
	}
	source, _, err := image.Decode(io.MultiReader(bytes.NewReader(prefix.Bytes()), reader))
	if err != nil {
		return nil, err
	}
	bounds := source.Bounds()
	if bounds.Dx() <= width {
		// 原图本来就不宽，没必要再转一次。
		return nil, fmt.Errorf("already small")
	}
	height := bounds.Dy() * width / bounds.Dx()
	if height < 1 {
		height = 1
	}
	target := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(target, target.Bounds(), source, bounds, draw.Over, nil)
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, target, &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func writeThumbBytes(w http.ResponseWriter, r *http.Request, data []byte) {
	w.Header().Set("Content-Type", "image/jpeg")
	// 封面文件名里已经带上了原图的修改时间，可以放心长缓存。
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, "thumb.jpg", time.Time{}, bytes.NewReader(data))
}
