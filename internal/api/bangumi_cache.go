package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	bangumiHotTTL       = 6 * time.Hour
	bangumiStableTTL    = 24 * time.Hour
	bangumiSearchTTL    = 30 * time.Minute
	bangumiStaleTTL     = 7 * 24 * time.Hour
	bangumiRefreshIndex = "bangumi-refresh:v1"
)

type bangumiRequest struct {
	Path      string          `json:"path"`
	Body      json.RawMessage `json:"body,omitempty"`
	TTL       time.Duration   `json:"ttl"`
	LastUsed  time.Time       `json:"lastUsed"`
	NextCheck time.Time       `json:"nextCheck"`
}

type bangumiResponse struct {
	Data         json.RawMessage `json:"data"`
	FreshUntil   time.Time       `json:"freshUntil"`
	ETag         string          `json:"etag,omitempty"`
	LastModified string          `json:"lastModified,omitempty"`
}

type bangumiCall struct {
	done chan struct{}
	data []byte
	err  error
}

type bangumiRequestCache struct {
	mu         sync.Mutex
	loaded     bool
	requests   map[string]bangumiRequest
	pending    map[string]*bangumiCall
	slots      chan struct{}
	retryAfter time.Time
	parent     context.Context
}

func bangumiRequestKey(request bangumiRequest) string {
	sum := sha256.Sum256(append([]byte(request.Path+"\n"), request.Body...))
	return "bangumi-response:v3:" + hex.EncodeToString(sum[:])
}

func (s *Server) initBangumiCacheLocked() {
	cache := &s.bangumiRequests
	if cache.loaded {
		return
	}
	cache.loaded = true
	cache.requests = map[string]bangumiRequest{}
	cache.pending = map[string]*bangumiCall{}
	cache.slots = make(chan struct{}, 2)
	if raw, _, ok := s.cache.Get(bangumiRefreshIndex); ok {
		_ = json.Unmarshal(raw, &cache.requests)
	}
	if cache.requests == nil {
		cache.requests = map[string]bangumiRequest{}
	}
}

func (s *Server) readBangumiResponse(key string) bangumiResponse {
	var response bangumiResponse
	if raw, _, ok := s.cache.Get(key); ok {
		_ = json.Unmarshal(raw, &response)
	}
	return response
}

func (s *Server) rememberBangumiRequest(key string, request bangumiRequest) {
	cache := &s.bangumiRequests
	cache.mu.Lock()
	defer cache.mu.Unlock()
	s.initBangumiCacheLocked()
	now := time.Now()
	if previous, ok := cache.requests[key]; ok && now.Sub(previous.LastUsed) < time.Hour {
		return
	}
	request.LastUsed = now
	if previous, ok := cache.requests[key]; ok {
		request.NextCheck = previous.NextCheck
	}
	cache.requests[key] = request
	for existing, item := range cache.requests {
		if now.Sub(item.LastUsed) > bangumiStaleTTL {
			delete(cache.requests, existing)
		}
	}
	if len(cache.requests) > 64 {
		oldestKey := ""
		var oldest time.Time
		for existing, item := range cache.requests {
			if oldestKey == "" || item.LastUsed.Before(oldest) {
				oldestKey, oldest = existing, item.LastUsed
			}
		}
		delete(cache.requests, oldestKey)
	}
	_ = s.cache.SetTTL(bangumiRefreshIndex, cache.requests, bangumiStaleTTL)
}

func (s *Server) bangumiJSON(ctx context.Context, request bangumiRequest, automatic bool) ([]byte, error) {
	key := bangumiRequestKey(request)
	if automatic {
		s.rememberBangumiRequest(key, request)
	}
	cached := s.readBangumiResponse(key)
	if len(cached.Data) > 0 && time.Now().Before(cached.FreshUntil) {
		return cached.Data, nil
	}
	call := s.startBangumiRequest(request, cached)
	if len(cached.Data) > 0 {
		return cached.Data, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-call.done:
		return call.data, call.err
	}
}

func (s *Server) startBangumiRequest(request bangumiRequest, cached bangumiResponse) *bangumiCall {
	cache := &s.bangumiRequests
	key := bangumiRequestKey(request)
	cache.mu.Lock()
	s.initBangumiCacheLocked()
	if pending := cache.pending[key]; pending != nil {
		cache.mu.Unlock()
		return pending
	}
	call := &bangumiCall{done: make(chan struct{})}
	now := time.Now()
	if current := s.readBangumiResponse(key); len(current.Data) > 0 && now.Before(current.FreshUntil) {
		call.data = current.Data
		close(call.done)
		cache.mu.Unlock()
		return call
	}
	if now.Before(cache.retryAfter) || len(cache.pending) >= 8 {
		call.err = fmt.Errorf("Bangumi 请求冷却中，请稍后重试")
		close(call.done)
		cache.mu.Unlock()
		return call
	}
	if tracked, ok := cache.requests[key]; ok && len(cached.Data) > 0 && now.Before(tracked.NextCheck) {
		call.err = fmt.Errorf("Bangumi 请求稍后自动重试")
		close(call.done)
		cache.mu.Unlock()
		return call
	}
	cache.pending[key] = call
	cache.mu.Unlock()
	go func() {
		defer func() {
			cache.mu.Lock()
			delete(cache.pending, key)
			if tracked, ok := cache.requests[key]; ok {
				tracked.NextCheck = time.Now().Add(5 * time.Minute)
				if call.err == nil {
					tracked.NextCheck = s.readBangumiResponse(key).FreshUntil
				}
				cache.requests[key] = tracked
				_ = s.cache.SetTTL(bangumiRefreshIndex, cache.requests, bangumiStaleTTL)
			}
			close(call.done)
			cache.mu.Unlock()
		}()
		cache.mu.Lock()
		parent := cache.parent
		cache.mu.Unlock()
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithTimeout(parent, 45*time.Second)
		defer cancel()
		select {
		case cache.slots <- struct{}{}:
			defer func() { <-cache.slots }()
		case <-ctx.Done():
			call.err = ctx.Err()
			return
		}
		// A queued request must observe a rate limit received by another worker.
		cache.mu.Lock()
		blocked := time.Now().Before(cache.retryAfter)
		cache.mu.Unlock()
		if blocked {
			call.err = fmt.Errorf("Bangumi 请求冷却中，请稍后重试")
			return
		}
		call.data, call.err = s.fetchBangumiJSON(ctx, request, cached)
	}()
	return call
}

func (s *Server) fetchBangumiJSON(ctx context.Context, spec bangumiRequest, cached bangumiResponse) ([]byte, error) {
	method := http.MethodGet
	if len(spec.Body) > 0 {
		method = http.MethodPost
	}
	request, err := http.NewRequestWithContext(ctx, method, bangumiAPIRoot+spec.Path, bytes.NewReader(spec.Body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", bangumiUserAgent)
	request.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	if cached.ETag != "" {
		request.Header.Set("If-None-Match", cached.ETag)
	}
	if cached.LastModified != "" {
		request.Header.Set("If-Modified-Since", cached.LastModified)
	}
	s.applyBangumiAuthorization(ctx, request)
	response, err := s.bangumiHTTPClient().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		until := bangumiRetryTime(response.Header.Get("Retry-After"), time.Now())
		s.bangumiRequests.mu.Lock()
		if until.After(s.bangumiRequests.retryAfter) {
			s.bangumiRequests.retryAfter = until
		}
		s.bangumiRequests.mu.Unlock()
		return nil, bangumiRequestError("Bangumi", response.StatusCode)
	}
	if response.StatusCode == http.StatusNotModified && len(cached.Data) > 0 {
		cached.FreshUntil = bangumiFreshUntil(response.Header, spec.TTL, time.Now())
	} else {
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, bangumiRequestError("Bangumi", response.StatusCode)
		}
		raw, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
		if err != nil || len(raw) > 4<<20 || !json.Valid(raw) {
			return nil, fmt.Errorf("Bangumi 返回的数据无效或过大")
		}
		if err := validateBangumiJSON(spec.Path, raw); err != nil {
			return nil, err
		}
		cached = bangumiResponse{
			Data: raw, FreshUntil: bangumiFreshUntil(response.Header, spec.TTL, time.Now()),
			ETag: response.Header.Get("ETag"), LastModified: response.Header.Get("Last-Modified"),
		}
	}
	_ = s.cache.SetTTL(bangumiRequestKey(spec), cached, bangumiStaleTTL)
	return cached.Data, nil
}

func validateBangumiJSON(path string, raw []byte) error {
	if strings.HasPrefix(path, "/v0/subjects/") {
		var subject bangumiCalendarSubject
		id, _ := strconv.Atoi(strings.TrimPrefix(path, "/v0/subjects/"))
		if json.Unmarshal(raw, &subject) != nil || subject.ID != id {
			return fmt.Errorf("Bangumi 返回的作品 ID 不匹配")
		}
	}
	if strings.HasPrefix(path, "/v0/search/subjects") {
		var payload map[string]json.RawMessage
		if json.Unmarshal(raw, &payload) != nil || payload["data"] == nil {
			return fmt.Errorf("Bangumi 返回的搜索数据无效")
		}
	}
	return nil
}

func bangumiFreshUntil(header http.Header, fallback time.Duration, now time.Time) time.Time {
	if fallback <= 0 {
		fallback = bangumiSearchTTL
	}
	// Never poll more often than the application's cadence or the upstream cache.
	for _, part := range strings.Split(header.Get("Cache-Control"), ",") {
		if value, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(part)), "max-age="); ok {
			if seconds, err := strconv.ParseInt(strings.Trim(value, `"`), 10, 64); err == nil && seconds > 0 {
				age, _ := strconv.ParseInt(header.Get("Age"), 10, 64)
				age = max(0, age)
				seconds = min(max(0, seconds-age), int64((7*24*time.Hour)/time.Second))
				fallback = max(fallback, time.Duration(seconds)*time.Second)
			}
		}
	}
	if expires, err := http.ParseTime(header.Get("Expires")); err == nil {
		fallback = max(fallback, min(expires.Sub(now), bangumiStaleTTL))
	}
	return now.Add(fallback)
}

func bangumiRetryTime(value string, now time.Time) time.Time {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
		return now.Add(time.Duration(min(max(seconds, 1), 86400)) * time.Second)
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return minTime(date, now.Add(24*time.Hour))
	}
	return now.Add(5 * time.Minute)
}

func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}

func (s *Server) refreshBangumiRequests(ctx context.Context, now time.Time) {
	cache := &s.bangumiRequests
	cache.mu.Lock()
	s.initBangumiCacheLocked()
	requests := make([]bangumiRequest, 0, len(cache.requests))
	changed := false
	for key, request := range cache.requests {
		updated, rolled := rollBangumiRequest(request, now)
		if rolled && now.Sub(request.LastUsed) < bangumiStaleTTL {
			delete(cache.requests, key)
			cache.requests[bangumiRequestKey(updated)] = updated
			request = updated
			changed = true
		}
		if bangumiRequestActive(request, now) && !now.Before(request.NextCheck) {
			requests = append(requests, request)
		}
	}
	if changed {
		_ = s.cache.SetTTL(bangumiRefreshIndex, cache.requests, bangumiStaleTTL)
	}
	cache.mu.Unlock()
	sort.Slice(requests, func(i, j int) bool { return requests[i].LastUsed.After(requests[j].LastUsed) })
	for _, request := range requests {
		if ctx.Err() != nil {
			return
		}
		key := bangumiRequestKey(request)
		cached := s.readBangumiResponse(key)
		if now.Before(cached.FreshUntil) {
			continue
		}
		call := s.startBangumiRequest(request, cached)
		select {
		case <-ctx.Done():
			return
		case <-call.done:
		}
	}
}

func rollBangumiRequest(request bangumiRequest, now time.Time) (bangumiRequest, bool) {
	var body map[string]any
	if json.Unmarshal(request.Body, &body) != nil {
		return request, false
	}
	filter, _ := body["filter"].(map[string]any)
	dates, _ := filter["air_date"].([]any)
	if len(dates) != 2 {
		return request, false
	}
	startText, _ := dates[0].(string)
	endText, _ := dates[1].(string)
	start, startErr := time.ParseInLocation("2006-01-02", strings.TrimPrefix(startText, ">="), now.Location())
	end, endErr := time.ParseInLocation("2006-01-02", strings.TrimPrefix(endText, "<"), now.Location())
	if startErr != nil || endErr != nil || now.Before(end) {
		return request, false
	}
	nextStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	nextEnd := nextStart.AddDate(0, 1, 0)
	if end.Sub(start) > 32*24*time.Hour {
		nextStart = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
		nextEnd = nextStart.AddDate(1, 0, 0)
	}
	filter["air_date"] = []string{">=" + nextStart.Format("2006-01-02"), "<" + nextEnd.Format("2006-01-02")}
	request.Body, _ = json.Marshal(body)
	request.NextCheck = time.Time{}
	return request, true
}

func bangumiRequestActive(request bangumiRequest, now time.Time) bool {
	if now.Sub(request.LastUsed) >= bangumiStaleTTL {
		return false
	}
	var body struct {
		Filter struct {
			Dates []string `json:"air_date"`
		} `json:"filter"`
	}
	if json.Unmarshal(request.Body, &body) == nil {
		for _, value := range body.Filter.Dates {
			if end, ok := strings.CutPrefix(value, "<"); ok {
				if date, err := time.ParseInLocation("2006-01-02", end, now.Location()); err == nil && !now.Before(date) {
					return false
				}
			}
		}
	}
	return true
}

func (s *Server) RunBangumiRefresh(ctx context.Context) {
	s.bangumiRequests.mu.Lock()
	s.bangumiRequests.parent = ctx
	s.bangumiRequests.mu.Unlock()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-timer.C:
			s.refreshBangumiRequests(ctx, now)
			timer.Reset(time.Minute)
		}
	}
}
