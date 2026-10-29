package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hyaeve/manco/internal/diskcache"
	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/sources"
	"github.com/hyaeve/manco/internal/store"
)

func TestBangumiSubjectChapterCount(t *testing.T) {
	for _, test := range []struct {
		name   string
		fields string
		want   int
	}{
		{name: "declared count", fields: `"eps":123,"total_episodes":10`, want: 123},
		{name: "catalog count fallback", fields: `"eps":0,"total_episodes":18`, want: 18},
		{name: "missing counts", fields: `"volumes":12`},
		{name: "negative counts", fields: `"eps":-1,"total_episodes":-2`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var subject bangumiCalendarSubject
			raw := fmt.Sprintf(`{"id":7,"type":1,"name":"示例漫画","images":{"large":"https://lain.bgm.tv/7.jpg"},%s}`, test.fields)
			if err := json.Unmarshal([]byte(raw), &subject); err != nil {
				t.Fatal(err)
			}
			comic, ok := bangumiSubjectComic(subject, bangumiFeedDefinitions[6])
			if !ok || comic.ChapterCount != test.want {
				t.Fatalf("chapter count=%d, accepted=%v, want %d", comic.ChapterCount, ok, test.want)
			}
		})
	}
}

func TestBangumiDetailShowsCountWithoutReadableChapters(t *testing.T) {
	root := t.TempDir()
	repository, err := store.Open(filepath.Join(root, "manco.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	cache, err := diskcache.Open(filepath.Join(root, "cache"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	client := &http.Client{Transport: bangumiRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.Path != "/v0/subjects/7" {
			t.Fatalf("unexpected content or chapter request: %s", request.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"id":7,"type":1,"name":"示例漫画","eps":123,"total_episodes":10,
				"images":{"large":"https://lain.bgm.tv/7.jpg"}
			}`)),
		}, nil
	})}
	server := &Server{store: repository, registry: sources.NewRegistry(client, nil, nil), cache: cache}
	for range 2 {
		request := httptest.NewRequest(http.MethodGet, "/api/sources/bangumi-manga/comics/7", nil)
		request.SetPathValue("id", "bangumi-manga")
		request.SetPathValue("comicId", "7")
		recorder := httptest.NewRecorder()
		server.handleComic(recorder, request)
		var detail model.ComicDetail
		if err := json.Unmarshal(recorder.Body.Bytes(), &detail); err != nil {
			t.Fatal(err)
		}
		if recorder.Code != http.StatusOK || detail.Comic.ChapterCount != 123 || len(detail.Chapters) != 0 {
			t.Fatalf("unexpected detail: status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		if detail.Comic.SourceURL != "https://bgm.tv/subject/7" {
			t.Fatalf("original page link=%q", detail.Comic.SourceURL)
		}
	}
	if requests != 1 {
		t.Fatalf("upstream requests=%d, want one cached subject request", requests)
	}
}
