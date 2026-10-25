package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	bangumiAPIRoot            = "https://api.bgm.tv"
	bangumiCacheTTL           = 30 * time.Minute
	bangumiItemLimit          = 10
	bangumiCoverMaxSize       = 4 << 20
	bangumiCoverCacheMaxBytes = 16 << 20
)

var bangumiUserAgent = "Manco/" + appVersion + " (https://github.com/Hyaeve/manco)"

type bangumiLoginItem struct {
	ID      int
	Title   string
	Summary string
	URL     string
	Cover   string
}

type bangumiCalendarDay struct {
	Weekday struct {
		ID int `json:"id"`
	} `json:"weekday"`
	Items []bangumiCalendarSubject `json:"items"`
}

type bangumiCalendarSubject struct {
	ID      int    `json:"id"`
	URL     string `json:"url"`
	Name    string `json:"name"`
	NameCN  string `json:"name_cn"`
	Summary string `json:"summary"`
	Type    int    `json:"type"`
	Date    string `json:"date"`
	Eps     int    `json:"eps"`
	Images  struct {
		Large  string `json:"large"`
		Common string `json:"common"`
	} `json:"images"`
	Rating struct {
		Score float64 `json:"score"`
		Rank  int     `json:"rank"`
	} `json:"rating"`
	Tags []struct {
		Name string `json:"name"`
	} `json:"tags"`
	Collection struct {
		Wish    int `json:"wish"`
		Collect int `json:"collect"`
		Doing   int `json:"doing"`
	} `json:"collection"`
	CalendarDayRank int `json:"-"`
}

type bangumiCoverCacheEntry struct {
	data        []byte
	contentType string
	expires     time.Time
}

func (s *Server) handleLoginBangumi(w http.ResponseWriter, r *http.Request) {
	items, err := s.loginBangumiItems(r.Context())
	if err != nil {
		s.logger.Printf("api: load Bangumi login background: %v", err)
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{
			"id":      item.ID,
			"title":   item.Title,
			"summary": item.Summary,
			"url":     item.URL,
			"cover":   fmt.Sprintf("/api/login/bangumi/cover?subject=%d", item.ID),
		})
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	writeJSON(w, http.StatusOK, map[string]any{"items": result})
}

func (s *Server) handleLoginBangumiCover(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.proxySetting(r.Context())) == "" {
		http.NotFound(w, r)
		return
	}
	subjectID, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("subject")))
	if err != nil || subjectID <= 0 {
		http.NotFound(w, r)
		return
	}
	data, contentType, err := s.loginBangumiCover(r.Context(), subjectID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = "image/jpeg"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(data)
}

func (s *Server) loginBangumiItems(ctx context.Context) ([]bangumiLoginItem, error) {
	if strings.TrimSpace(s.proxySetting(ctx)) == "" {
		return nil, nil
	}
	s.loginBangumiMu.Lock()
	if len(s.loginBangumiCacheItems) > 0 && time.Since(s.loginBangumiAt) < bangumiCacheTTL {
		items := append([]bangumiLoginItem(nil), s.loginBangumiCacheItems...)
		s.loginBangumiMu.Unlock()
		return sampleBangumiLoginItems(items, bangumiItemLimit), nil
	}
	s.loginBangumiMu.Unlock()

	subjects, err := s.randomBangumiSubjects(ctx, bangumiItemLimit*2, 20)
	if err != nil {
		return nil, err
	}
	items := make([]bangumiLoginItem, 0, len(subjects))
	for _, subject := range subjects {
		if item, ok := bangumiLoginSubjectItem(subject); ok {
			items = append(items, item)
		}
	}
	items = s.enrichBangumiItems(ctx, s.bangumiHTTPClient(), items)
	if len(items) == 0 {
		return nil, nil
	}
	s.loginBangumiMu.Lock()
	s.loginBangumiCacheItems = append([]bangumiLoginItem(nil), items...)
	s.loginBangumiAt = time.Now()
	s.loginBangumiCovers = make(map[int]string, len(items))
	for _, item := range items {
		s.loginBangumiCovers[item.ID] = item.Cover
	}
	s.loginBangumiMu.Unlock()
	return sampleBangumiLoginItems(items, bangumiItemLimit), nil
}

func sampleBangumiLoginItems(items []bangumiLoginItem, count int) []bangumiLoginItem {
	if len(items) <= count {
		result := append([]bangumiLoginItem(nil), items...)
		rand.Shuffle(len(result), func(i, j int) {
			result[i], result[j] = result[j], result[i]
		})
		return result
	}
	indexes := rand.Perm(len(items))
	result := make([]bangumiLoginItem, 0, count)
	for _, index := range indexes[:count] {
		result = append(result, items[index])
	}
	return result
}

func bangumiLoginSubjectItem(subject bangumiCalendarSubject) (bangumiLoginItem, bool) {
	title := strings.TrimSpace(subject.NameCN)
	if title == "" {
		title = strings.TrimSpace(subject.Name)
	}
	cover := strings.TrimSpace(subject.Images.Large)
	if cover == "" {
		cover = strings.TrimSpace(subject.Images.Common)
	}
	cover, validCover := normalizeBangumiImageURL(cover)
	if subject.ID <= 0 || title == "" || !validCover {
		return bangumiLoginItem{}, false
	}
	return bangumiLoginItem{
		ID:      subject.ID,
		Title:   title,
		Summary: strings.TrimSpace(subject.Summary),
		URL:     fmt.Sprintf("https://bgm.tv/subject/%d", subject.ID),
		Cover:   cover,
	}, true
}

func parseBangumiCalendar(raw []byte, now time.Time) ([]bangumiLoginItem, error) {
	var days []bangumiCalendarDay
	if err := json.Unmarshal(raw, &days); err != nil {
		return nil, err
	}
	today := int(now.Weekday())
	if today == 0 {
		today = 7
	}
	ordered := make([]bangumiCalendarDay, 0, len(days))
	for _, day := range days {
		if day.Weekday.ID == today {
			ordered = append(ordered, day)
		}
	}
	for _, day := range days {
		if day.Weekday.ID != today {
			ordered = append(ordered, day)
		}
	}
	seen := map[int]bool{}
	items := make([]bangumiLoginItem, 0, bangumiItemLimit)
	for _, day := range ordered {
		sort.SliceStable(day.Items, func(i, j int) bool {
			left := day.Items[i].Collection.Doing*3 + day.Items[i].Collection.Collect + day.Items[i].Collection.Wish
			right := day.Items[j].Collection.Doing*3 + day.Items[j].Collection.Collect + day.Items[j].Collection.Wish
			return left > right
		})
		for _, subject := range day.Items {
			if subject.ID <= 0 || subject.Type != 2 || seen[subject.ID] {
				continue
			}
			title := strings.TrimSpace(subject.NameCN)
			if title == "" {
				title = strings.TrimSpace(subject.Name)
			}
			cover := strings.TrimSpace(subject.Images.Large)
			if cover == "" {
				cover = strings.TrimSpace(subject.Images.Common)
			}
			cover, validCover := normalizeBangumiImageURL(cover)
			summary := strings.TrimSpace(subject.Summary)
			if title == "" || !validCover {
				continue
			}
			seen[subject.ID] = true
			items = append(items, bangumiLoginItem{
				ID:      subject.ID,
				Title:   title,
				Summary: summary,
				URL:     fmt.Sprintf("https://bgm.tv/subject/%d", subject.ID),
				Cover:   cover,
			})
			if len(items) >= bangumiItemLimit {
				return items, nil
			}
		}
	}
	return items, nil
}

func isBangumiImageURL(raw string) bool {
	_, ok := normalizeBangumiImageURL(raw)
	return ok
}

func normalizeBangumiImageURL(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "bgm.tv" && !strings.HasSuffix(host, ".bgm.tv") {
		return "", false
	}
	parsed.Scheme = "https"
	return parsed.String(), true
}

func (s *Server) enrichBangumiItems(ctx context.Context, client *http.Client, items []bangumiLoginItem) []bangumiLoginItem {
	type detail struct {
		NameCN  string `json:"name_cn"`
		Name    string `json:"name"`
		Summary string `json:"summary"`
		Images  struct {
			Common string `json:"common"`
			Large  string `json:"large"`
		} `json:"images"`
	}
	enriched := make([]bangumiLoginItem, len(items))
	copy(enriched, items)
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for index := range enriched {
		if strings.TrimSpace(enriched[index].Summary) != "" {
			continue
		}
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodGet,
				fmt.Sprintf("%s/v0/subjects/%d", bangumiAPIRoot, enriched[index].ID), nil)
			if err != nil {
				return
			}
			request.Header.Set("User-Agent", bangumiUserAgent)
			request.Header.Set("Accept", "application/json")
			response, err := client.Do(request)
			if err != nil {
				return
			}
			raw, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
			_ = response.Body.Close()
			if readErr != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
				return
			}
			var payload detail
			if json.Unmarshal(raw, &payload) != nil {
				return
			}
			if title := strings.TrimSpace(payload.NameCN); title != "" {
				enriched[index].Title = title
			} else if title := strings.TrimSpace(payload.Name); title != "" {
				enriched[index].Title = title
			}
			enriched[index].Summary = strings.TrimSpace(payload.Summary)
			cover := strings.TrimSpace(payload.Images.Large)
			if cover == "" {
				cover = strings.TrimSpace(payload.Images.Common)
			}
			if normalized, ok := normalizeBangumiImageURL(cover); ok {
				enriched[index].Cover = normalized
			}
		}(index)
	}
	wg.Wait()
	return enriched
}

func (s *Server) loginBangumiCover(ctx context.Context, subjectID int) ([]byte, string, error) {
	s.loginBangumiMu.Lock()
	cached := s.loginBangumiCoversCache[subjectID]
	if len(cached.data) > 0 && time.Now().Before(cached.expires) {
		s.loginBangumiMu.Unlock()
		return append([]byte(nil), cached.data...), cached.contentType, nil
	}
	coverURL := strings.TrimSpace(s.loginBangumiCovers[subjectID])
	s.loginBangumiMu.Unlock()
	if coverURL == "" {
		if _, err := s.loginBangumiItems(ctx); err != nil {
			return nil, "", err
		}
		s.loginBangumiMu.Lock()
		coverURL = strings.TrimSpace(s.loginBangumiCovers[subjectID])
		s.loginBangumiMu.Unlock()
	}
	if coverURL == "" || !isBangumiImageURL(coverURL) {
		return nil, "", fmt.Errorf("Bangumi cover is unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, coverURL, nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("User-Agent", bangumiUserAgent)
	request.Header.Set("Referer", "https://bgm.tv/")
	client := http.DefaultClient
	if s.registry != nil {
		client = s.registry.Client()
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("Bangumi cover returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, bangumiCoverMaxSize+1))
	if err != nil || len(data) == 0 || len(data) > bangumiCoverMaxSize {
		return nil, "", fmt.Errorf("Bangumi cover size is invalid")
	}
	contentType := strings.TrimSpace(response.Header.Get("Content-Type"))
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return nil, "", fmt.Errorf("Bangumi cover is not an image")
	}
	s.loginBangumiMu.Lock()
	if s.loginBangumiCoversCache == nil {
		s.loginBangumiCoversCache = map[int]bangumiCoverCacheEntry{}
	}
	now := time.Now()
	totalBytes := len(data)
	for id, entry := range s.loginBangumiCoversCache {
		if !now.Before(entry.expires) {
			delete(s.loginBangumiCoversCache, id)
			continue
		}
		totalBytes += len(entry.data)
	}
	for id, entry := range s.loginBangumiCoversCache {
		if totalBytes <= bangumiCoverCacheMaxBytes {
			break
		}
		delete(s.loginBangumiCoversCache, id)
		totalBytes -= len(entry.data)
	}
	s.loginBangumiCoversCache[subjectID] = bangumiCoverCacheEntry{
		data: append([]byte(nil), data...), contentType: contentType, expires: now.Add(bangumiCacheTTL),
	}
	s.loginBangumiMu.Unlock()
	return data, contentType, nil
}
