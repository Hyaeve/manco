package api

import (
	"archive/zip"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var localThumbnailSlots = make(chan struct{}, 2)

// Grid requests only need series summaries; read chapter entries on demand.
func (s *Server) handleLocalItem(w http.ResponseWriter, r *http.Request) {
	value := r.URL.Query().Get("path")
	target, err := s.resolveLocalTarget(r.Context(), value)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	root := filepath.Dir(target)
	allowed := false
	for _, candidate := range s.localRoots(r.Context()) {
		absolute, _ := filepath.Abs(candidate)
		if root == absolute {
			allowed = true
			root = candidate
			break
		}
	}
	info, statErr := os.Stat(target)
	if !allowed || statErr != nil || !info.IsDir() {
		writeError(w, http.StatusNotFound, "本地作品目录不存在")
		return
	}
	item := s.scanLocalDir(root, filepath.Base(target))
	item.Path = value
	item.ChapterCount = len(item.Files)
	if item.Cover != "" {
		item.Cover = localPath(root, item.Cover)
	}
	for index := range item.Files {
		item.Files[index].Path = localPath(root, item.Files[index].Path)
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": item})
}

// The browser lazily requests one thumbnail, rather than first asking for the
// complete archive directory of every chapter in a series.
func (s *Server) handleLocalCBZThumbnail(w http.ResponseWriter, r *http.Request) {
	target, err := s.resolveLocalTarget(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	extension := strings.ToLower(filepath.Ext(target))
	if extension != ".cbz" && extension != ".zip" {
		writeError(w, http.StatusBadRequest, "无效的章节压缩包")
		return
	}
	select {
	case localThumbnailSlots <- struct{}{}:
		defer func() { <-localThumbnailSlots }()
	case <-r.Context().Done():
		return
	}
	archive, err := zip.OpenReader(target)
	if err != nil {
		writeError(w, http.StatusNotFound, "无法读取章节封面")
		return
	}
	defer archive.Close()
	var first *zip.File
	for _, file := range archive.File {
		if file.FileInfo().IsDir() || strings.Contains(file.Name, "..") || !isImageArchiveEntry(file.Name) {
			continue
		}
		if first == nil || file.Name < first.Name {
			first = file
		}
	}
	if first == nil || first.UncompressedSize64 > 32<<20 {
		writeError(w, http.StatusNotFound, "章节没有可用封面")
		return
	}
	reader, err := first.Open()
	if err != nil {
		writeError(w, http.StatusBadGateway, "无法读取章节封面")
		return
	}
	width := requestedThumbWidth(r)
	if width <= 0 {
		width = 240
	}
	data, thumbErr := buildThumbnailFromReader(io.LimitReader(reader, 32<<20), width)
	_ = reader.Close()
	if thumbErr == nil {
		writeThumbBytes(w, r, data)
		return
	}
	reader, err = first.Open()
	if err != nil {
		writeError(w, http.StatusBadGateway, "无法读取章节封面")
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(first.Name)))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = io.Copy(w, io.LimitReader(reader, 32<<20))
}
