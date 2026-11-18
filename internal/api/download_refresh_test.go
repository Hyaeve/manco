package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/hyaeve/manco/internal/config"
	"github.com/hyaeve/manco/internal/downloader"
	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/scheduler"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/sources"
	"github.com/hyaeve/manco/internal/store"
)

type refreshSource struct {
	lazyFilterFixture
	chapters []model.Chapter
	err      error
}

func (f *refreshSource) Chapters(context.Context, source.Account, string) ([]model.Chapter, error) {
	return f.chapters, f.err
}

func TestFullRefreshPreflightsAndPreservesDownloadedFile(t *testing.T) {
	ctx := context.Background()
	repository, err := store.Open(filepath.Join(t.TempDir(), "manco.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	root := t.TempDir()
	seriesDir := filepath.Join(root, "Work")
	if err := os.Mkdir(seriesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(seriesDir, "Old chapter.cbz")
	if err := os.WriteFile(archive, []byte("existing chapter"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture := &refreshSource{lazyFilterFixture: lazyFilterFixture{info: model.SourceInfo{ID: "refresh-source"}}}
	registry := sources.NewRegistry(nil, nil, nil)
	registry.Register(fixture)
	engine := downloader.NewEngine(registry, repository, root, 1, 1, nil)
	queue := struct {
		*downloader.Engine
		*store.Store
	}{engine, repository}
	schedule := scheduler.New(registry, repository, queue, time.Minute, nil)
	server := &Server{store: repository, engine: engine, scheduler: schedule, cfg: config.Config{DownloadDir: root}}
	schedule.SetDownloadDirResolver(server.ResolveSubscriptionDownloadDir)
	subscription, err := repository.UpsertSubscription(ctx, model.Subscription{
		SourceID: "refresh-source", ComicID: "work", Title: "Work", Enabled: true,
		LastChapterID: "complete", LastChapterOrder: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	old := make(map[string]model.DownloadJob)
	for _, chapter := range []struct{ id, status, path string }{
		{"complete", "completed", archive}, {"active", "running", ""},
		{"pending", "queued", ""}, {"missing-from-catalog", "completed", ""},
	} {
		job, err := repository.CreateDownloadJob(ctx, model.DownloadJob{
			SourceID: subscription.SourceID, ComicID: subscription.ComicID, ChapterID: chapter.id,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := repository.UpdateDownloadJob(ctx, job.ID, chapter.status, 5, 3, chapter.path, ""); err != nil {
			t.Fatal(err)
		}
		old[chapter.id] = job
	}
	refresh := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/subscriptions/download-all", nil)
		request.SetPathValue("id", strconv.FormatInt(subscription.ID, 10))
		response := httptest.NewRecorder()
		server.handleDownloadAllSubscription(response, request)
		return response
	}
	checkArchive := func() {
		t.Helper()
		data, err := os.ReadFile(archive)
		if err != nil || string(data) != "existing chapter" {
			t.Fatalf("existing archive = %q, error = %v", data, err)
		}
	}
	fixture.err = errors.New("source unavailable")
	if response := refresh(); response.Code != http.StatusBadGateway {
		t.Fatalf("unavailable source status = %d, body = %s", response.Code, response.Body)
	}
	if _, err := server.BotFullDownload(ctx, subscription.ID); !errors.Is(err, fixture.err) {
		t.Fatalf("Telegram full refresh error = %v", err)
	}
	fixture.err = nil
	fixture.chapters = []model.Chapter{{ID: " "}}
	if response := refresh(); response.Code != http.StatusNotFound {
		t.Fatalf("unusable catalog status = %d, body = %s", response.Code, response.Body)
	}
	checkArchive()
	completed, err := repository.DownloadJob(ctx, old["complete"].ID)
	if err != nil || completed.Status != "completed" || completed.FilePath != archive {
		t.Fatalf("completed chapter after preflight failure = %+v, error = %v", completed, err)
	}

	fixture.chapters = []model.Chapter{
		{ID: "complete", Title: "Renamed chapter", Order: 1}, {ID: "active", Order: 2},
		{ID: "pending", Order: 3}, {ID: "new", Order: 4}, {ID: "new", Order: 4},
	}
	response := refresh()
	var counts map[string]any
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &counts) != nil {
		t.Fatalf("valid catalog status = %d, body = %s", response.Code, response.Body)
	}
	if counts["queued"] != float64(2) || counts["skipped"] != float64(2) || counts["clearedJobs"] != float64(1) || counts["removedFiles"] != float64(0) {
		t.Fatalf("full refresh counts = %v", counts)
	}
	checkArchive()
	updated, err := repository.DownloadJobByChapter(ctx, subscription.SourceID, subscription.ComicID, "complete")
	if err != nil || updated.ID == completed.ID || updated.Status != "queued" || updated.CompletedPages != 0 || updated.FilePath != archive || updated.DownloadDir != root {
		t.Fatalf("replacement job = %+v, error = %v", updated, err)
	}
	for id, status := range map[string]string{"active": "running", "pending": "queued", "missing-from-catalog": "completed"} {
		job, err := repository.DownloadJob(ctx, old[id].ID)
		if err != nil || job.Status != status || job.CompletedPages != 3 {
			t.Fatalf("preserved chapter %q = %+v, error = %v", id, job, err)
		}
	}
}
