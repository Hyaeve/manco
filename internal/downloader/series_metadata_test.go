package downloader

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/sources"
)

func TestDownloadsPreserveSeriesComicInfo(t *testing.T) {
	image := pngImage(t, 0x42)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(image)
	}))
	defer server.Close()

	for _, kind := range []string{source.KindComic, source.KindBook} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			base := &staticSource{
				detail: model.Comic{
					ID: "1", Title: "Series", Author: "Original author",
					Description: "Original summary", Cover: server.URL + "/cover.png",
				},
				pages: map[string][]model.Page{
					"1": {{URL: server.URL + "/page.png"}},
					"2": {{URL: server.URL + "/page.png"}},
				},
			}
			var item source.Source = base
			if kind == source.KindBook {
				item = &textBookSource{staticSource: base, text: "Chapter text"}
			}
			base.detail.SourceID = item.Info().ID
			registry := sources.NewRegistry(server.Client(), nil, nil)
			registry.Register(item)
			jobs := &memoryJobs{}
			root := t.TempDir()
			newEngine := func() *Engine {
				engine := NewEngine(registry, jobs, root, 1, 1, log.New(io.Discard, "", 0))
				engine.SetCacheDir(t.TempDir())
				return engine
			}
			engine := newEngine()
			download := func(order int) string {
				t.Helper()
				chapter := strconv.Itoa(order)
				job, err := jobs.CreateDownloadJob(ctx, model.DownloadJob{
					SourceID: item.Info().ID, ComicID: "1", ComicTitle: "Series",
					ChapterID: chapter, ChapterTitle: "Chapter " + chapter, ChapterOrder: float64(order),
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := engine.download(ctx, job); err != nil {
					t.Fatal(err)
				}
				stored, _ := jobs.find(job.ID)
				if stored.Status != "completed" {
					t.Fatalf("download status = %q", stored.Status)
				}
				return stored.FilePath
			}

			firstArchive := download(1)
			dir := filepath.Join(root, "Series")
			path := filepath.Join(dir, "ComicInfo.xml")
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			fixedTime := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
			if err := os.Chtimes(path, fixedTime, fixedTime); err != nil {
				t.Fatal(err)
			}
			stamp, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			assertPreserved := func() {
				t.Helper()
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(raw, original) || !info.ModTime().Equal(stamp.ModTime()) {
					t.Fatal("existing series ComicInfo.xml was rewritten")
				}
			}

			base.detail.Author = "Updated author"
			base.detail.Description = "Updated summary"
			// A fresh engine also verifies that preservation survives a restart.
			engine = newEngine()
			secondArchive := download(2)
			assertPreserved()
			archives := []string{firstArchive, secondArchive}
			archiveBytes := make([][]byte, len(archives))
			for index, archive := range archives {
				if _, exists := readZip(t, archive)["ComicInfo.xml"]; exists {
					t.Fatal("chapter archive still contains ComicInfo.xml")
				}
				archiveBytes[index], err = os.ReadFile(archive)
				if err != nil {
					t.Fatal(err)
				}
			}
			raw, err := os.ReadFile(filepath.Join(dir, "series.json"))
			if err != nil {
				t.Fatal(err)
			}
			var series MylarSeries
			if err := json.Unmarshal(raw, &series); err != nil {
				t.Fatal(err)
			}
			if series.Metadata.TotalIssues != 2 {
				t.Fatalf("total_issues = %d, want 2", series.Metadata.TotalIssues)
			}

			subscription := model.Subscription{
				SourceID: item.Info().ID, ComicID: "1", Title: "Series", DownloadDir: root,
			}
			// Repairing other sidecars must not overwrite an intact series XML.
			for _, name := range []string{"cover.png", "series.json"} {
				if err := os.Remove(filepath.Join(dir, name)); err != nil {
					t.Fatal(err)
				}
			}
			if repaired, err := engine.EnsureSeriesMetadata(ctx, subscription); err != nil || !repaired {
				t.Fatalf("repair = %v, %v", repaired, err)
			}
			assertPreserved()
			if refreshed, err := engine.ScrapeSeriesMetadata(ctx, subscription); err != nil || !refreshed {
				t.Fatalf("scrape = %v, %v", refreshed, err)
			}
			raw, err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var info ComicInfo
			if err := xml.Unmarshal(raw, &info); err != nil {
				t.Fatal(err)
			}
			if info.Writer != "Updated author" || info.Summary != "Updated summary" ||
				info.Title != "Series" || info.Series != "Series" ||
				info.Number != "" || info.PageCount != 0 || info.Pages != nil {
				t.Fatalf("unexpected scraped series metadata: %+v", info)
			}
			for index, archive := range archives {
				raw, err := os.ReadFile(archive)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(raw, archiveBytes[index]) {
					t.Fatalf("scrape modified existing archive %s", archive)
				}
			}
		})
	}
}

func TestEnsureSeriesMetadataRepairsInvalidComicInfo(t *testing.T) {
	for _, kind := range []string{source.KindComic, source.KindBook} {
		for name, raw := range map[string]string{
			"missing":       "",
			"empty":         "",
			"truncated":     "<ComicInfo><Series>Series",
			"wrong-root":    "<Other><Series>Series</Series></Other>",
			"wrong-title":   "<ComicInfo><Title>Chapter</Title><Series>Series</Series></ComicInfo>",
			"wrong-series":  "<ComicInfo><Title>Series</Title><Series>Old</Series></ComicInfo>",
			"chapter":       "<ComicInfo><Title>Series</Title><Series>Series</Series><Number>2</Number><PageCount>1</PageCount></ComicInfo>",
			"chapter-pages": "<ComicInfo><Title>Series</Title><Series>Series</Series><Pages><Page Image=\"0\"/></Pages></ComicInfo>",
			"legacy-html":   "<ComicInfo><Title>Series</Title><Series>Series</Series><Summary>&lt;p&gt;Summary&lt;/p&gt;</Summary></ComicInfo>",
		} {
			t.Run(kind+"/"+name, func(t *testing.T) {
				base := &staticSource{detail: model.Comic{ID: "1", Title: "Series", Description: "Summary"}}
				var item source.Source = base
				if kind == source.KindBook {
					item = &textBookSource{staticSource: base}
				}
				base.detail.SourceID = item.Info().ID
				registry := sources.NewRegistry(source.NewHTTPClient(), nil, nil)
				registry.Register(item)
				root := t.TempDir()
				dir := filepath.Join(root, "Series")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				for _, file := range []string{"Chapter.cbz", "cover.png"} {
					if err := os.WriteFile(filepath.Join(dir, file), []byte("existing"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if err := writeSeriesMetadataFiles(base.detail, item.Info(), dir); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, "ComicInfo.xml")
				if name != "missing" {
					if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if seriesMetadataCurrent(dir, kind) {
					t.Fatal("invalid series ComicInfo.xml was treated as current")
				}
				engine := NewEngine(registry, &memoryJobs{}, root, 1, 1, log.New(io.Discard, "", 0))
				subscription := model.Subscription{SourceID: item.Info().ID, ComicID: "1", Title: "Series"}
				repaired, err := engine.EnsureSeriesMetadata(context.Background(), subscription)
				if err != nil || !repaired {
					t.Fatalf("repair = %v, %v", repaired, err)
				}
				payload, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var info ComicInfo
				if err := xml.Unmarshal(payload, &info); err != nil {
					t.Fatal(err)
				}
				if info.Title != "Series" || info.Series != "Series" || info.Summary != "Summary" ||
					info.Number != "" || info.PageCount != 0 || info.Pages != nil {
					t.Fatalf("unexpected repaired ComicInfo: %+v", info)
				}
				for _, file := range obsoleteMetadataFiles {
					if _, err := os.Stat(filepath.Join(dir, file)); !os.IsNotExist(err) {
						t.Fatalf("obsolete %s was not removed: %v", file, err)
					}
				}
				if repaired, err := engine.EnsureSeriesMetadata(context.Background(), subscription); err != nil || repaired {
					t.Fatalf("second repair = %v, %v", repaired, err)
				}
			})
		}
	}
}

func TestChapterMetadataEnsuresSeriesComicInfo(t *testing.T) {
	for _, kind := range []string{source.KindComic, source.KindBook} {
		for _, state := range []string{"missing", "corrupt", "intact", "unreadable"} {
			t.Run(kind+"/"+state, func(t *testing.T) {
				comic := model.Comic{ID: "1", Title: "Series", Author: "Author"}
				var item source.Source = &staticSource{detail: comic}
				if kind == source.KindBook {
					item = &textBookSource{staticSource: &staticSource{detail: comic}}
				}
				dir := t.TempDir()
				path := filepath.Join(dir, "ComicInfo.xml")
				var original []byte
				switch state {
				case "corrupt":
					if err := os.WriteFile(path, []byte("<ComicInfo>"), 0o644); err != nil {
						t.Fatal(err)
					}
				case "intact":
					var err error
					original, err = writeSeriesComicInfo(item, comic, dir)
					if err != nil {
						t.Fatal(err)
					}
					comic.Author = "Changed author"
				case "unreadable":
					if err := os.Mkdir(path, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				for _, name := range obsoleteMetadataFiles {
					if err := os.WriteFile(filepath.Join(dir, name), []byte("legacy"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				var err error
				if kind == source.KindBook {
					err = writeBookMetadata(context.Background(), nil, source.Account{}, item, comic, dir)
				} else {
					err = writeComicMetadata(context.Background(), nil, source.Account{}, item, comic, dir, 1)
				}
				if state == "unreadable" {
					if err == nil {
						t.Fatal("series ComicInfo.xml read error was ignored")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !seriesComicInfoCurrent(raw, "Series") {
					t.Fatal("chapter download did not create a valid series ComicInfo.xml")
				}
				if state == "intact" && !bytes.Equal(raw, original) {
					t.Fatal("chapter download overwrote existing series metadata")
				}
				for _, name := range obsoleteMetadataFiles {
					if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
						t.Fatalf("obsolete %s was not removed: %v", name, err)
					}
				}
			})
		}
	}
}
