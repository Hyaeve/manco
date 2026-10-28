package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hyaeve/manco/internal/diskcache"
	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/sources"
	"github.com/hyaeve/manco/internal/store"
)

type fastBrowseFixture struct {
	lazyFilterFixture
	release  chan struct{}
	started  chan struct{}
	chapters atomic.Int32
}

func (f *fastBrowseFixture) Browse(context.Context, source.Account, model.BrowseOptions, int) (model.SearchResult, error) {
	return model.SearchResult{Items: []model.Comic{{ID: "work", SourceID: "fast-browse", Title: "Work"}}}, nil
}

func (f *fastBrowseFixture) Detail(ctx context.Context, _ source.Account, _ string) (model.Comic, error) {
	close(f.started)
	select {
	case <-f.release:
		return model.Comic{ID: "work", Author: "Author"}, nil
	case <-ctx.Done():
		return model.Comic{}, ctx.Err()
	}
}

func (f *fastBrowseFixture) Chapters(context.Context, source.Account, string) ([]model.Chapter, error) {
	f.chapters.Add(1)
	return nil, nil
}

func TestBrowseRespondsBeforeDetailAndDoesNotFetchChapters(t *testing.T) {
	repository, err := store.Open(filepath.Join(t.TempDir(), "manco.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	cache, err := diskcache.Open(filepath.Join(t.TempDir(), "cache"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &fastBrowseFixture{
		lazyFilterFixture: lazyFilterFixture{info: model.SourceInfo{ID: "fast-browse", Name: "Fast browse"}},
		release:           make(chan struct{}), started: make(chan struct{}),
	}
	registry := sources.NewRegistry(nil, nil, nil)
	registry.Register(fixture)
	server := &Server{store: repository, registry: registry, cache: cache}
	request := httptest.NewRequest(http.MethodGet, "/api/sources/fast-browse/browse?page=1", nil)
	request.SetPathValue("id", "fast-browse")
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.handleBrowse(recorder, request)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		close(fixture.release)
		t.Fatal("browse response waited for detail enrichment")
	}
	if recorder.Code != http.StatusOK {
		close(fixture.release)
		t.Fatalf("browse status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	<-fixture.started
	close(fixture.release)
	deadline := time.Now().Add(3 * time.Second)
	for {
		server.enrichMu.Lock()
		pending := len(server.enriching)
		server.enrichMu.Unlock()
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("enrichment did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	if fixture.chapters.Load() != 0 {
		t.Fatal("list enrichment fetched a chapter directory")
	}
}
