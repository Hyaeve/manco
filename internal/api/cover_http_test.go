package api

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hyaeve/manco/internal/config"
	"github.com/hyaeve/manco/internal/discovercache"
	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/store"
	"github.com/hyaeve/manco/internal/subscriptioncover"
)

func TestCachedCoverHTTPRoutesServeThumbnails(t *testing.T) {
	root := t.TempDir()
	repository, err := store.Open(filepath.Join(root, "manco.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	if err := repository.CreateUser(ctx, "fixture", "unused-password-hash"); err != nil {
		t.Fatal(err)
	}
	user, err := repository.UserByUsername(ctx, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateSession(ctx, hashToken("fixture-session"), user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	discover, err := discovercache.Open(filepath.Join(root, "discover"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	covers, err := subscriptioncover.Open(filepath.Join(root, "subscription-covers"))
	if err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 800, 1200))); err != nil {
		t.Fatal(err)
	}
	if err := discover.PutNamedWithCover(ctx, nil, "fixture", "Fixture", "comic", model.ComicDetail{
		Comic: model.Comic{ID: "work", SourceID: "fixture", Title: "Work"},
	}, func(context.Context, model.Comic) ([]byte, string, error) {
		return encoded.Bytes(), "image/png", nil
	}); err != nil {
		t.Fatal(err)
	}
	coverPath := filepath.Join(root, "cover.png")
	if err := os.WriteFile(coverPath, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := covers.Update(model.Subscription{SourceID: "fixture", ComicID: "work", Title: "Work"}, coverPath); err != nil {
		t.Fatal(err)
	}
	server := New(Options{
		Config: config.Config{DataDir: root}, Store: repository,
		Discover: discover, SubscriptionCovers: covers,
	})
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	for _, path := range []string{
		"/api/discover/cover?sourceId=fixture&comicId=work&kind=comic&w=160",
		"/api/subscription-cover?sourceId=fixture&comicId=work&w=160",
	} {
		request, _ := http.NewRequest(http.MethodGet, httpServer.URL+path, nil)
		request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "fixture-session"})
		response, err := httpServer.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != 200 {
			t.Fatalf("cover route %s: status=%d err=%v body=%s", path, response.StatusCode, err, data)
		}
		dimensions, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || format != "jpeg" || dimensions.Width != 160 || dimensions.Height != 240 {
			t.Fatalf("cover route %s: dimensions=%+v format=%s err=%v", path, dimensions, format, err)
		}
	}
}
