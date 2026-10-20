package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

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
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|%d", sourcePath, info.Size(), info.ModTime().UnixNano(), width)))
	name := hex.EncodeToString(sum[:12]) + ".jpg"
	directory := filepath.Join(s.cfg.DataDir, thumbCacheFolder)
	target := filepath.Join(directory, name)
	if data, readErr := os.ReadFile(target); readErr == nil && len(data) > 0 {
		writeThumbBytes(w, r, data)
		return true
	}
	data, err := buildThumbnail(sourcePath, width)
	if err != nil {
		return false
	}
	if err := os.MkdirAll(directory, 0o755); err == nil {
		temporary := target + ".part"
		if err := os.WriteFile(temporary, data, 0o644); err == nil {
			_ = os.Rename(temporary, target)
		}
	}
	writeThumbBytes(w, r, data)
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
