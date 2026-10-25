package api

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"path/filepath"
	"testing"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/sources"
	"github.com/hyaeve/manco/internal/store"
)

type subscriptionMatchSource struct {
	info  model.SourceInfo
	items []model.Comic
}

func (s *subscriptionMatchSource) Info() model.SourceInfo { return s.info }
func (s *subscriptionMatchSource) Search(context.Context, source.Account, string, int) (model.SearchResult, error) {
	return model.SearchResult{Items: s.items}, nil
}
func (s *subscriptionMatchSource) Browse(context.Context, source.Account, model.BrowseOptions, int) (model.SearchResult, error) {
	return model.SearchResult{}, nil
}
func (s *subscriptionMatchSource) Detail(context.Context, source.Account, string) (model.Comic, error) {
	return model.Comic{}, nil
}
func (s *subscriptionMatchSource) Chapters(context.Context, source.Account, string) ([]model.Chapter, error) {
	return nil, nil
}
func (s *subscriptionMatchSource) Pages(context.Context, source.Account, string, model.Chapter) ([]model.Page, error) {
	return nil, nil
}

func TestMatchBangumiSubscriptionSelectsBestContentSource(t *testing.T) {
	repository, err := store.Open(filepath.Join(t.TempDir(), "match.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	registry := sources.NewRegistry(nil, nil, repository)
	registry.Register(&subscriptionMatchSource{
		info: model.SourceInfo{ID: "match-comic", Name: "匹配漫画源", Kind: source.KindComic, CanSearch: true},
		items: []model.Comic{{
			SourceID: "match-comic", ID: "comic-42", Title: "葬送的芙莉莲", Author: "山田钟人",
		}},
	})
	hidden := map[string]bool{}
	for _, info := range registry.List() {
		if info.ID != "match-comic" {
			hidden[info.ID] = true
		}
	}
	rawHidden, err := json.Marshal(hidden)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.SetSetting(context.Background(), "hidden_sources", string(rawHidden)); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: repository, registry: registry, logger: log.New(io.Discard, "", 0)}
	ctx := context.Background()
	pending, err := repository.UpsertSubscription(ctx, model.Subscription{
		SourceID: "bangumi", ComicID: "42", OriginSourceID: "bangumi-manga", OriginComicID: "42",
		MatchKind: source.KindComic, MatchStatus: "pending", Title: "葬送的芙莉莲", Author: "山田钟人",
		DownloadDir: "/downloads", Enabled: true, AutoDownload: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	matched, ok, err := server.MatchSubscription(ctx, pending)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("subscription was not matched")
	}
	if matched.SourceID != "match-comic" || matched.ComicID != "comic-42" || matched.MatchStatus != "matched" {
		t.Fatalf("matched subscription = %#v", matched)
	}
	if matched.OriginSourceID != "bangumi-manga" || matched.OriginComicID != "42" {
		t.Fatalf("origin fields = %#v", matched)
	}
}
