package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hyaeve/manco/internal/config"
	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/store"
)

func TestLibrarySummaryDoesNotSerializeOrMutateChapterLists(t *testing.T) {
	items := []localItemEntry{{
		Title: "作品", Path: "作品", TotalFiles: 3,
		Files: []localFileEntry{{Name: "1.cbz"}, {Name: "2.cbz"}},
	}}
	request := httptest.NewRequest(http.MethodGet, "/api/local?summary=1&limit=1", nil)
	response := httptest.NewRecorder()
	(&Server{}).writeLocalLibrary(response, request, items, nil)
	var payload struct{ Items []localItemEntry }
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 1 || payload.Items[0].ChapterCount != 2 || len(payload.Items[0].Files) != 0 {
		t.Fatalf("summary contains chapters or wrong count: %+v", payload.Items)
	}
	if len(items[0].Files) != 2 {
		t.Fatal("summary serialization mutated cached full chapter list")
	}
}

func TestLocalItemAndLazyChapterThumbnail(t *testing.T) {
	root := t.TempDir()
	repository, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	server := &Server{store: repository, cfg: config.Config{DownloadDir: root, DataDir: t.TempDir()}}
	dir := filepath.Join(root, "作品")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(dir, "第1话.cbz"))
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	entry, err := archive.Create("001.png")
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(entry, image.NewRGBA(image.Rect(0, 0, 640, 960))); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	value := localPath(root, "作品")
	response := httptest.NewRecorder()
	server.handleLocalItem(response, httptest.NewRequest(http.MethodGet, "/api/local/item?path="+url.QueryEscape(value), nil))
	var payload struct{ Item localItemEntry }
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(payload.Item.Files) != 1 || payload.Item.Files[0].Path != localPath(root, "作品/第1话.cbz") {
		t.Fatalf("invalid detail response: %s", response.Body.String())
	}
	response = httptest.NewRecorder()
	server.handleLocalCBZThumbnail(response, httptest.NewRequest(http.MethodGet, "/api/local/cbz/thumbnail?path="+url.QueryEscape(payload.Item.Files[0].Path)+"&w=120", nil))
	thumbnail, _, err := image.Decode(bytes.NewReader(response.Body.Bytes()))
	if err != nil || thumbnail.Bounds().Dx() != 120 {
		t.Fatalf("invalid thumbnail: status=%d err=%v", response.Code, err)
	}
	response = httptest.NewRecorder()
	server.handleLocalItem(response, httptest.NewRequest(http.MethodGet, "/api/local/item?path="+url.QueryEscape(localPath(root, "../escape")), nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("directory traversal returned %d", response.Code)
	}
}

func TestLocalReadingWithoutChapterComicInfo(t *testing.T) {
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 8, 12))); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"comic", "book"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			repository, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repository.Close()
			server := &Server{store: repository, cfg: config.Config{DownloadDir: root}}
			dir := filepath.Join(root, "Series")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			genre := "漫画"
			imageName := "001.png"
			pageName, pageKind := imageName, "image"
			if kind == "book" {
				genre = "书籍"
				imageName = "images/001.png"
				pageName, pageKind = "0001.xhtml", "document"
			}
			metadata := "<ComicInfo><Series>Series</Series><Writer>Author</Writer>" +
				"<Summary>Series summary</Summary><Genre>" + genre + "</Genre></ComicInfo>"
			if err := os.WriteFile(filepath.Join(dir, "ComicInfo.xml"), []byte(metadata), 0o644); err != nil {
				t.Fatal(err)
			}
			var archiveData bytes.Buffer
			archive := zip.NewWriter(&archiveData)
			entries := map[string][]byte{imageName: imageData.Bytes()}
			if kind == "book" {
				entries[pageName] = []byte(`<html><head><title>Chapter</title></head><body><p>Chapter body</p><img src="images/001.png"/></body></html>`)
				entries["style.css"] = []byte("body { color: black; }")
			}
			for name, raw := range entries {
				entry, err := archive.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := entry.Write(raw); err != nil {
					t.Fatal(err)
				}
			}
			if err := archive.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "Chapter.cbz"), archiveData.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			server.handleLocalItem(response, httptest.NewRequest(http.MethodGet,
				"/api/local/item?path="+url.QueryEscape(localPath(root, "Series")), nil))
			var detail struct{ Item localItemEntry }
			if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || detail.Item.Kind != kind ||
				detail.Item.Author != "Author" || detail.Item.Description != "Series summary" ||
				len(detail.Item.Files) != 1 {
				t.Fatalf("invalid series detail: %s", response.Body.String())
			}
			query := "?path=" + url.QueryEscape(detail.Item.Files[0].Path)
			response = httptest.NewRecorder()
			server.handleLocalCBZ(response, httptest.NewRequest(http.MethodGet, "/api/local/cbz"+query, nil))
			var listing struct {
				Pages []struct{ Name, Kind string }
			}
			if err := json.Unmarshal(response.Body.Bytes(), &listing); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || len(listing.Pages) != 1 ||
				listing.Pages[0].Name != pageName || listing.Pages[0].Kind != pageKind {
				t.Fatalf("invalid reading pages: %s", response.Body.String())
			}
			response = httptest.NewRecorder()
			server.handleLocalCBZFile(response, httptest.NewRequest(http.MethodGet,
				"/api/local/cbz/file"+query+"&entry="+url.QueryEscape(imageName), nil))
			if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), imageData.Bytes()) {
				t.Fatalf("image content changed: status=%d", response.Code)
			}
			if kind == "book" {
				response = httptest.NewRecorder()
				server.handleLocalCBZDocument(response, httptest.NewRequest(http.MethodGet,
					"/api/local/cbz/document"+query+"&entry="+url.QueryEscape(pageName), nil))
				var chapter struct{ HTML, Title string }
				if err := json.Unmarshal(response.Body.Bytes(), &chapter); err != nil {
					t.Fatal(err)
				}
				if response.Code != http.StatusOK || !strings.Contains(chapter.HTML, "Chapter body") ||
					!strings.Contains(chapter.HTML, "/api/local/cbz/file?path=") {
					t.Fatalf("invalid chapter document: %s", response.Body.String())
				}
			}
		})
	}
}

func TestCompactTaskPageSkipsLocalMetadataAndReturnsFilteredCounts(t *testing.T) {
	repository, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	job, err := repository.CreateDownloadJob(ctx, model.DownloadJob{
		SourceID: "source", ComicID: "work", ComicTitle: "作品", ChapterID: "chapter", ChapterTitle: "章节",
		ComicDescription: "不应随进度返回的作品简介",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: repository}
	response := httptest.NewRecorder()
	server.handleListDownloads(response, httptest.NewRequest(http.MethodGet, "/api/downloads?status=active&compact=1&filter=queued&q="+url.QueryEscape("作品"), nil))
	var payload struct {
		Items  []model.DownloadJob
		Total  int
		Counts store.DownloadJobCounts
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || payload.Total != 1 || payload.Counts.Queued != 1 || len(payload.Items) != 1 {
		t.Fatalf("unexpected compact response: %s", response.Body.String())
	}
	if payload.Items[0].ID != job.ID || payload.Items[0].ComicDescription != "" || payload.Items[0].ChapterID != "" {
		t.Fatalf("compact task contains unnecessary data: %+v", payload.Items[0])
	}
}
