package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/store"
)

func TestSubscriptionsAPICombinesSearchWithPagingAndDirectory(t *testing.T) {
	repository, err := store.Open(filepath.Join(t.TempDir(), "subscriptions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	for _, fixture := range []model.Subscription{
		{SourceID: "fixture", ComicID: "a", Title: "匹配 A", DownloadDir: "/downloads/comics"},
		{SourceID: "fixture", ComicID: "b", Title: "匹配 B", DownloadDir: "/downloads/comics"},
		{SourceID: "fixture", ComicID: "c", Title: "匹配 C", DownloadDir: "/downloads/books"},
		{SourceID: "fixture", ComicID: "d", Title: "其他作品", DownloadDir: "/downloads/comics"},
	} {
		if _, err := repository.UpsertSubscription(t.Context(), fixture); err != nil {
			t.Fatal(err)
		}
	}
	server := &Server{store: repository}
	for _, test := range []struct {
		path  string
		total int
		comic string
	}{
		{path: "?limit=1&offset=1&sort=title&desc=0&dir=%2Fdownloads%2Fcomics&q=" + url.QueryEscape(" 匹配 "), total: 2, comic: "b"},
		{path: "?q=" + url.QueryEscape("其他"), total: 1, comic: "d"},
		{path: "?limit=60&q=" + url.QueryEscape("不存在")},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.handleListSubscriptions(response, httptest.NewRequest(http.MethodGet, "/api/subscriptions"+test.path, nil))
			var payload struct {
				Items     []model.Subscription `json:"items"`
				Total     int                  `json:"total"`
				KindCount map[string]int       `json:"kindCounts"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || payload.Total != test.total || payload.KindCount["all"] != 4 {
				t.Fatalf("unexpected response: status=%d body=%s", response.Code, response.Body.String())
			}
			if test.comic == "" {
				if len(payload.Items) != 0 {
					t.Fatalf("unexpected matches: %+v", payload.Items)
				}
			} else if len(payload.Items) != 1 || payload.Items[0].ComicID != test.comic {
				t.Fatalf("unexpected matches: %+v", payload.Items)
			}
		})
	}
}
