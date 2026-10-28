package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hyaeve/manco/internal/diskcache"
	"github.com/hyaeve/manco/internal/sources"
)

func bangumiCacheFixture(t *testing.T, transport bangumiRoundTripFunc) *Server {
	t.Helper()
	cache, err := diskcache.Open(filepath.Join(t.TempDir(), "cache"), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{cache: cache, registry: sources.NewRegistry(&http.Client{Transport: transport}, nil, nil)}
}

func bangumiResponseFixture(status int, header http.Header, data string) *http.Response {
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(data))}
}

func TestBangumiRequestsCoalesce(t *testing.T) {
	var requests atomic.Int32
	started, release := make(chan struct{}, 1), make(chan struct{})
	server := bangumiCacheFixture(t, func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		started <- struct{}{}
		select {
		case <-release:
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
		return bangumiResponseFixture(200, http.Header{}, `{"data":[],"total":0}`), nil
	})
	spec := bangumiRequest{Path: "/v0/search/subjects?limit=20&offset=0", Body: json.RawMessage(`{"keyword":""}`), TTL: bangumiHotTTL}
	var wait sync.WaitGroup
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := server.bangumiJSON(context.Background(), spec, true); err != nil {
				t.Errorf("coalesced request: %v", err)
			}
		}()
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("request did not start")
	}
	close(release)
	wait.Wait()
	if requests.Load() != 1 {
		t.Fatalf("upstream requests = %d, want 1", requests.Load())
	}
}

func TestBangumiStaleReturnsImmediatelyAndRevalidates(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	server := bangumiCacheFixture(t, func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("If-None-Match") != `"revision-1"` {
			t.Error("missing conditional ETag")
		}
		started <- struct{}{}
		<-release
		return bangumiResponseFixture(304, http.Header{}, ""), nil
	})
	spec := bangumiRequest{Path: "/v0/subjects/7", TTL: bangumiStableTTL}
	key := bangumiRequestKey(spec)
	old := bangumiResponse{Data: json.RawMessage(`{"id":7,"type":1}`), FreshUntil: time.Now().Add(-time.Hour), ETag: `"revision-1"`}
	if err := server.cache.SetTTL(key, old, bangumiStaleTTL); err != nil {
		t.Fatal(err)
	}
	data, err := server.bangumiJSON(context.Background(), spec, false)
	if err != nil || string(data) != string(old.Data) {
		close(release)
		t.Fatalf("stale result = %s, err = %v", data, err)
	}
	<-started
	server.bangumiRequests.mu.Lock()
	call := server.bangumiRequests.pending[key]
	server.bangumiRequests.mu.Unlock()
	close(release)
	<-call.done
	if call.err != nil || !server.readBangumiResponse(key).FreshUntil.After(time.Now().Add(23*time.Hour)) {
		t.Fatalf("304 did not renew cache: %v", call.err)
	}
}

func TestBangumiAutomaticRefreshSurvivesRestart(t *testing.T) {
	var requests atomic.Int32
	server := bangumiCacheFixture(t, func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		return bangumiResponseFixture(200, http.Header{}, `{"data":[],"total":0}`), nil
	})
	spec := bangumiRequest{Path: "/v0/search/subjects?limit=20&offset=0", Body: json.RawMessage(`{"keyword":""}`), TTL: bangumiHotTTL}
	key := bangumiRequestKey(spec)
	now := time.Now()
	expired := bangumiResponse{Data: json.RawMessage(`{"data":[],"total":0}`), FreshUntil: now.Add(-time.Hour)}
	if err := server.cache.SetTTL(key, expired, bangumiStaleTTL); err != nil {
		t.Fatal(err)
	}
	spec.LastUsed, spec.NextCheck = now, now.Add(-time.Minute)
	if err := server.cache.SetTTL(bangumiRefreshIndex, map[string]bangumiRequest{key: spec}, bangumiStaleTTL); err != nil {
		t.Fatal(err)
	}
	restarted := &Server{cache: server.cache, registry: server.registry}
	restarted.refreshBangumiRequests(context.Background(), now)
	restarted.refreshBangumiRequests(context.Background(), now.Add(time.Minute))
	if requests.Load() != 1 {
		t.Fatalf("automatic requests = %d, want 1", requests.Load())
	}
	if !restarted.readBangumiResponse(key).FreshUntil.After(now.Add(5 * time.Hour)) {
		t.Fatal("background refresh did not update persistent response")
	}
}

func TestBangumiRateLimitStopsOtherRequests(t *testing.T) {
	var requests atomic.Int32
	server := bangumiCacheFixture(t, func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return bangumiResponseFixture(429, http.Header{"Retry-After": []string{"600"}}, ""), nil
	})
	for _, id := range []int{1, 2} {
		_, err := server.bangumiJSON(context.Background(), bangumiRequest{Path: fmt.Sprintf("/v0/subjects/%d", id), TTL: bangumiStableTTL}, false)
		if err == nil {
			t.Fatal("expected a rate limit error")
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("cooldown allowed %d upstream requests", requests.Load())
	}
}

func TestBangumiCanonicalBlocksDoNotSkipSubjects(t *testing.T) {
	var requests atomic.Int32
	server := bangumiCacheFixture(t, func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(request.URL.Query().Get("offset"))
		if limit != 20 {
			t.Errorf("upstream limit = %d, want 20", limit)
		}
		data := make([]map[string]int, 0, 20)
		for index := offset; index < min(offset+20, 100); index++ {
			data = append(data, map[string]int{"id": index + 1, "type": 1})
		}
		raw, _ := json.Marshal(map[string]any{"data": data, "total": 100})
		return bangumiResponseFixture(200, http.Header{}, string(raw)), nil
	})
	for page := 1; page <= 2; page++ {
		items, total, err := server.searchBangumiSubjects(context.Background(), "", "heat", map[string]any{"type": []int{1}}, page, 35)
		if err != nil || len(items) != 35 || total != 100 {
			t.Fatalf("page %d: count=%d total=%d err=%v", page, len(items), total, err)
		}
		for index, item := range items {
			if item.ID != (page-1)*35+index+1 {
				t.Fatalf("page %d skipped a subject at index %d: %d", page, index, item.ID)
			}
		}
	}
	if requests.Load() != 4 {
		t.Fatalf("block requests = %d, want 4 with shared boundary block", requests.Load())
	}
}

func TestBangumiCacheCadenceAndRetryAfter(t *testing.T) {
	now := time.Now()
	if got := bangumiFreshUntil(http.Header{}, bangumiHotTTL, now); !got.Equal(now.Add(6 * time.Hour)) {
		t.Fatalf("hot-feed cadence = %v", got.Sub(now))
	}
	if got := bangumiFreshUntil(http.Header{"Cache-Control": []string{"public, max-age=86400"}}, bangumiHotTTL, now); !got.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("upstream freshness not respected: %v", got.Sub(now))
	}
	if got := bangumiRetryTime(now.Add(time.Hour).UTC().Format(http.TimeFormat), now); got.Sub(now) < 59*time.Minute {
		t.Fatalf("HTTP-date Retry-After not respected: %v", got.Sub(now))
	}
}

func TestBangumiRequestsShareGlobalConcurrencyLimit(t *testing.T) {
	var active, peak atomic.Int32
	server := bangumiCacheFixture(t, func(request *http.Request) (*http.Response, error) {
		current := active.Add(1)
		for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
		}
		defer active.Add(-1)
		time.Sleep(10 * time.Millisecond)
		id := strings.TrimPrefix(request.URL.Path, "/v0/subjects/")
		return bangumiResponseFixture(200, http.Header{}, `{"id":`+id+`,"type":1}`), nil
	})
	var wait sync.WaitGroup
	for id := 1; id <= 6; id++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := server.bangumiSubjectByID(context.Background(), id); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	if peak.Load() != 2 {
		t.Fatalf("Bangumi peak concurrency = %d, want 2", peak.Load())
	}
}

func TestBangumiRequestsCancelWithService(t *testing.T) {
	started := make(chan struct{})
	server := bangumiCacheFixture(t, func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server.bangumiRequests.parent = ctx
	done := make(chan error, 1)
	go func() {
		_, err := server.bangumiSubjectByID(context.Background(), 7)
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("service cancellation was ignored")
		}
	case <-time.After(time.Second):
		t.Fatal("Bangumi request outlived service cancellation")
	}
}

func TestBangumiPeriodRolloverKeepsFiltersAndDoesNotExtendActivity(t *testing.T) {
	lastUsed := time.Date(2026, 10, 31, 12, 0, 0, 0, time.Local)
	spec := bangumiRequest{LastUsed: lastUsed, TTL: bangumiHotTTL, Body: json.RawMessage(
		`{"keyword":"","sort":"heat","filter":{"type":[1],"nsfw":false,"air_date":[">=2026-10-01","<2026-11-01"]}}`,
	)}
	now := lastUsed.Add(24 * time.Hour)
	updated, rolled := rollBangumiRequest(spec, now)
	if !rolled || !strings.Contains(string(updated.Body), "2026-11-01") ||
		!strings.Contains(string(updated.Body), "2026-12-01") ||
		!strings.Contains(string(updated.Body), `"type":[1]`) ||
		!updated.LastUsed.Equal(lastUsed) {
		t.Fatalf("period rollover = %s, active since %v", updated.Body, updated.LastUsed)
	}
	if bangumiRequestActive(updated, now.Add(8*24*time.Hour)) {
		t.Fatal("unused feed was kept active indefinitely")
	}
}
