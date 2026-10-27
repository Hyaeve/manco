package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/hyaeve/manco/internal/config"
	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/store"
)

func TestResolveBangumiSubscriptionDownloadDirFollowsMatchedSource(t *testing.T) {
	repository, err := store.Open(filepath.Join(t.TempDir(), "manco.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()

	sourceDefault := t.TempDir()
	customDir := t.TempDir()
	extra, err := json.Marshal(map[string]any{
		"downloadSettings": map[string]any{"defaultDownloadDir": sourceDefault},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.UpsertSourceAccount(context.Background(), model.SourceAccount{
		SourceID: "matched-source",
		Extra:    extra,
	}); err != nil {
		t.Fatal(err)
	}

	server := &Server{
		store: repository,
		cfg:   config.Config{DownloadDir: t.TempDir()},
	}
	following := model.Subscription{
		SourceID:       "matched-source",
		OriginSourceID: "bangumi-manga",
		DownloadDir:    "",
	}
	got, err := server.ResolveSubscriptionDownloadDir(context.Background(), following)
	if err != nil {
		t.Fatal(err)
	}
	if got != sourceDefault {
		t.Fatalf("followed directory = %q, want %q", got, sourceDefault)
	}

	custom := following
	custom.DownloadDir = customDir
	got, err = server.ResolveSubscriptionDownloadDir(context.Background(), custom)
	if err != nil {
		t.Fatal(err)
	}
	if got != customDir {
		t.Fatalf("custom directory = %q, want %q", got, customDir)
	}

	pendingDir, err := server.ResolveSubscriptionDownloadDir(context.Background(), model.Subscription{
		SourceID:       "bangumi",
		OriginSourceID: "bangumi-manga",
		DownloadDir:    "",
	})
	if err != nil {
		t.Fatal(err)
	}
	if pendingDir != "" {
		t.Fatalf("pending directory = %q, want empty until matched", pendingDir)
	}
}

func TestSubscriptionWithCoversExposesFollowedDirectory(t *testing.T) {
	repository, err := store.Open(filepath.Join(t.TempDir(), "manco.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	sourceDefault := t.TempDir()
	extra, _ := json.Marshal(map[string]any{
		"downloadSettings": map[string]any{"defaultDownloadDir": sourceDefault},
	})
	if err := repository.UpsertSourceAccount(context.Background(), model.SourceAccount{
		SourceID: "matched-source",
		Extra:    extra,
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: repository, cfg: config.Config{DownloadDir: t.TempDir()}}
	view := server.subscriptionWithCovers(context.Background(), model.Subscription{
		SourceID:       "matched-source",
		OriginSourceID: "bangumi-manga",
		Title:          "测试作品",
		DownloadDir:    "",
	})
	if !view.FollowSourceDownloadDir {
		t.Fatal("followSourceDownloadDir = false, want true")
	}
	if view.EffectiveDownloadDir != sourceDefault {
		t.Fatalf("effectiveDownloadDir = %q, want %q", view.EffectiveDownloadDir, sourceDefault)
	}
}
