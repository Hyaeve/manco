package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hyaeve/manco/internal/model"
)

const bangumiFeedCacheTTL = 10 * time.Minute

type bangumiFeedDefinition struct {
	ID        string
	Name      string
	Type      int
	Sort      string
	Tag       string
	RatingMin float64
	Calendar  bool
	Month     bool
	Year      bool
}

var bangumiFeedDefinitions = []bangumiFeedDefinition{
	{ID: "recent", Name: "近期注目", Type: 1, Sort: "heat"},
	{ID: "airing", Name: "连载中", Type: 1, Sort: "heat", Tag: "连载中"},
	{ID: "monthly", Name: "本月新作", Type: 1, Sort: "heat", Month: true},
	{ID: "yearly", Name: "今年新作", Type: 1, Sort: "heat", Year: true},
	{ID: "popular", Name: "收藏最多", Type: 1, Sort: "heat"},
	{ID: "ranking", Name: "排行榜", Type: 1, Sort: "rank"},
	{ID: "manga", Name: "高分漫画", Type: 1, Sort: "score", Tag: "漫画", RatingMin: 7.5},
	{ID: "novel", Name: "高分小说", Type: 1, Sort: "score", Tag: "小说", RatingMin: 7.5},
}

type bangumiFeedPayload struct {
	Data  []bangumiCalendarSubject `json:"data"`
	Total int                      `json:"total"`
}

type bangumiFeedCacheEntry struct {
	items   []model.Comic
	total   int
	expires time.Time
}

func bangumiFeedByID(id string) (bangumiFeedDefinition, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, feed := range bangumiFeedDefinitions {
		if feed.ID == id {
			return feed, true
		}
	}
	return bangumiFeedDefinition{}, false
}

func (s *Server) handleBangumiDiscover(w http.ResponseWriter, r *http.Request) {
	feed, ok := bangumiFeedByID(r.URL.Query().Get("feed"))
	if !ok {
		writeError(w, http.StatusBadRequest, "未知的 Bangumi 内容源")
		return
	}
	page := intParam(r, "page", 1)
	if page < 1 {
		page = 1
	}
	limit := intParam(r, "limit", 42)
	if limit < 1 {
		limit = 42
	}
	if limit > 100 {
		limit = 100
	}
	items, total, err := s.bangumiFeedPage(r.Context(), feed, page, limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Bangumi 加载失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":   items,
		"page":    page,
		"total":   total,
		"hasMore": page*limit < total,
	})
}

func (s *Server) handleBangumiSearch(w http.ResponseWriter, r *http.Request) {
	keyword := strings.TrimSpace(r.URL.Query().Get("q"))
	if keyword == "" {
		writeError(w, http.StatusBadRequest, "请输入 Bangumi 搜索关键词")
		return
	}
	page := intParam(r, "page", 1)
	if page < 1 {
		page = 1
	}
	limit := intParam(r, "limit", 42)
	if limit < 1 {
		limit = 42
	}
	if limit > 100 {
		limit = 100
	}
	subjects, total, err := s.searchBangumiSubjects(r.Context(), keyword, "match", map[string]any{
		"type": []int{1},
		"nsfw": false,
	}, page, limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Bangumi 搜索失败："+err.Error())
		return
	}
	feed := bangumiFeedDefinition{ID: "search", Name: "Bangumi 搜索"}
	items := make([]model.Comic, 0, len(subjects))
	for _, subject := range subjects {
		if item, ok := bangumiSubjectComic(subject, feed); ok {
			items = append(items, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":   items,
		"page":    page,
		"total":   total,
		"hasMore": page*limit < total,
	})
}

func (s *Server) bangumiFeedPage(ctx context.Context, feed bangumiFeedDefinition, page, limit int) ([]model.Comic, int, error) {
	cacheKey := fmt.Sprintf("%s:%d:%d", feed.ID, page, limit)
	s.bangumiFeedMu.Lock()
	if cached, ok := s.bangumiFeedCache[cacheKey]; ok && time.Now().Before(cached.expires) {
		items := append([]model.Comic(nil), cached.items...)
		total := cached.total
		s.bangumiFeedMu.Unlock()
		return items, total, nil
	}
	s.bangumiFeedMu.Unlock()

	var subjects []bangumiCalendarSubject
	total := 0
	var err error
	if feed.Calendar {
		subjects, err = s.bangumiCalendarSubjects(ctx)
		if err == nil {
			subjects = filterBangumiCalendarFeed(subjects, feed.ID)
			total = len(subjects)
			subjects = sliceBangumiSubjects(subjects, page, limit)
		}
	} else {
		subjects, total, err = s.searchBangumiFeed(ctx, feed, page, limit)
	}
	if err != nil {
		return nil, 0, err
	}
	items := make([]model.Comic, 0, len(subjects))
	for _, subject := range subjects {
		item, ok := bangumiSubjectComic(subject, feed)
		if ok {
			items = append(items, item)
		}
	}
	if s.bangumiFeedCache == nil {
		s.bangumiFeedCache = map[string]bangumiFeedCacheEntry{}
	}
	s.bangumiFeedMu.Lock()
	s.bangumiFeedCache[cacheKey] = bangumiFeedCacheEntry{
		items: append([]model.Comic(nil), items...), total: total, expires: time.Now().Add(bangumiFeedCacheTTL),
	}
	s.bangumiFeedMu.Unlock()
	return items, total, nil
}

func (s *Server) searchBangumiFeed(ctx context.Context, feed bangumiFeedDefinition, page, limit int) ([]bangumiCalendarSubject, int, error) {
	now := time.Now()
	filter := map[string]any{
		"type": []int{feed.Type},
		"nsfw": false,
	}
	if strings.TrimSpace(feed.Tag) != "" {
		filter["tag"] = []string{feed.Tag}
	}
	if feed.RatingMin > 0 {
		filter["rating"] = []string{fmt.Sprintf(">=%.1f", feed.RatingMin)}
	}
	if feed.Month {
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		end := start.AddDate(0, 1, 0)
		filter["air_date"] = []string{">=" + start.Format("2006-01-02"), "<" + end.Format("2006-01-02")}
	}
	if feed.Year {
		start := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
		end := start.AddDate(1, 0, 0)
		filter["air_date"] = []string{">=" + start.Format("2006-01-02"), "<" + end.Format("2006-01-02")}
	}
	return s.searchBangumiSubjects(ctx, "", feed.Sort, filter, page, limit)
}

func (s *Server) searchBangumiSubjects(
	ctx context.Context,
	keyword string,
	sortMode string,
	filter map[string]any,
	page int,
	limit int,
) ([]bangumiCalendarSubject, int, error) {
	body, err := json.Marshal(map[string]any{
		"keyword": keyword,
		"sort":    sortMode,
		"filter":  filter,
	})
	if err != nil {
		return nil, 0, err
	}
	address := fmt.Sprintf("%s/v0/search/subjects?limit=%d&offset=%d", bangumiAPIRoot, limit, (page-1)*limit)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, strings.NewReader(string(body)))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("User-Agent", bangumiUserAgent)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := s.bangumiHTTPClient().Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, 0, fmt.Errorf("Bangumi 搜索返回 HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return nil, 0, err
	}
	var payload bangumiFeedPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, 0, err
	}
	return payload.Data, payload.Total, nil
}

func (s *Server) bangumiCalendarSubjects(ctx context.Context) ([]bangumiCalendarSubject, error) {
	s.bangumiCalendarMu.Lock()
	defer s.bangumiCalendarMu.Unlock()
	if len(s.bangumiCalendarCache) > 0 && time.Since(s.bangumiCalendarAt) < bangumiFeedCacheTTL {
		return append([]bangumiCalendarSubject(nil), s.bangumiCalendarCache...), nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, bangumiAPIRoot+"/calendar", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", bangumiUserAgent)
	request.Header.Set("Accept", "application/json")
	response, err := s.bangumiHTTPClient().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Bangumi 日历返回 HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var days []bangumiCalendarDay
	if err := json.Unmarshal(raw, &days); err != nil {
		return nil, err
	}
	today := int(time.Now().Weekday())
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
	subjects := make([]bangumiCalendarSubject, 0, 21*7)
	for dayIndex, day := range ordered {
		for _, subject := range day.Items {
			subject.CalendarDayRank = dayIndex
			subjects = append(subjects, subject)
		}
	}
	s.bangumiCalendarCache = append([]bangumiCalendarSubject(nil), subjects...)
	s.bangumiCalendarAt = time.Now()
	return subjects, nil
}

func (s *Server) randomBangumiSubjects(ctx context.Context, count, perFeed int) ([]bangumiCalendarSubject, error) {
	if count < 1 {
		count = 10
	}
	if perFeed < count {
		perFeed = count
	}
	type feedResult struct {
		subjects []bangumiCalendarSubject
		err      error
	}
	results := make([]feedResult, len(bangumiFeedDefinitions))
	sem := make(chan struct{}, 4)
	var wait sync.WaitGroup
	for index, feed := range bangumiFeedDefinitions {
		wait.Add(1)
		go func(index int, feed bangumiFeedDefinition) {
			defer wait.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[index].err = ctx.Err()
				return
			}
			if feed.Calendar {
				subjects, err := s.bangumiCalendarSubjects(ctx)
				if err == nil {
					subjects = filterBangumiCalendarFeed(subjects, feed.ID)
				}
				results[index] = feedResult{subjects: subjects, err: err}
				return
			}
			subjects, _, err := s.searchBangumiFeed(ctx, feed, 1, perFeed)
			results[index] = feedResult{subjects: subjects, err: err}
		}(index, feed)
	}
	wait.Wait()

	seen := map[int]bool{}
	subjects := make([]bangumiCalendarSubject, 0, count*len(bangumiFeedDefinitions))
	var firstErr error
	for _, result := range results {
		if result.err != nil && firstErr == nil {
			firstErr = result.err
		}
		for _, subject := range result.subjects {
			if subject.ID <= 0 || seen[subject.ID] {
				continue
			}
			seen[subject.ID] = true
			subjects = append(subjects, subject)
		}
	}
	if len(subjects) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, fmt.Errorf("Bangumi 没有返回可展示的条目")
	}
	rand.Shuffle(len(subjects), func(i, j int) {
		subjects[i], subjects[j] = subjects[j], subjects[i]
	})
	if len(subjects) > count {
		subjects = subjects[:count]
	}
	return subjects, nil
}

func filterBangumiCalendarFeed(subjects []bangumiCalendarSubject, feedID string) []bangumiCalendarSubject {
	seen := map[int]bool{}
	result := make([]bangumiCalendarSubject, 0, len(subjects))
	for _, subject := range subjects {
		if subject.ID <= 0 || seen[subject.ID] {
			continue
		}
		seen[subject.ID] = true
		result = append(result, subject)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if feedID == "recent" && result[i].CalendarDayRank != result[j].CalendarDayRank {
			return result[i].CalendarDayRank < result[j].CalendarDayRank
		}
		return bangumiSubjectHeat(result[i]) > bangumiSubjectHeat(result[j])
	})
	if feedID == "recent" && len(result) > 21 {
		result = result[:21]
	}
	return result
}

func sliceBangumiSubjects(subjects []bangumiCalendarSubject, page, limit int) []bangumiCalendarSubject {
	start := (page - 1) * limit
	if start >= len(subjects) {
		return nil
	}
	end := start + limit
	if end > len(subjects) {
		end = len(subjects)
	}
	return subjects[start:end]
}

func bangumiSubjectHeat(subject bangumiCalendarSubject) int {
	return subject.Collection.Doing*3 + subject.Collection.Collect + subject.Collection.Wish
}

func bangumiSubjectComic(subject bangumiCalendarSubject, feed bangumiFeedDefinition) (model.Comic, bool) {
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
		return model.Comic{}, false
	}
	status := feed.Name
	if subject.Rating.Score > 0 {
		status = fmt.Sprintf("Bangumi %.1f 分", subject.Rating.Score)
	}
	if subject.Rating.Rank > 0 && subject.Rating.Score > 0 {
		status += fmt.Sprintf(" · 排名 %d", subject.Rating.Rank)
	}
	tags := make([]string, 0, len(subject.Tags))
	for _, tag := range subject.Tags {
		if name := strings.TrimSpace(tag.Name); name != "" {
			tags = append(tags, name)
		}
	}
	sourceID := "bangumi-" + feed.ID
	if feed.ID != "manga" && feed.ID != "novel" {
		if bangumiSubjectLooksNovel(tags) {
			sourceID = "bangumi-novel"
		} else {
			sourceID = "bangumi-manga"
		}
	}
	return model.Comic{
		SourceID:    sourceID,
		ID:          strconv.Itoa(subject.ID),
		Title:       title,
		Cover:       cover,
		SourceURL:   fmt.Sprintf("https://bgm.tv/subject/%d", subject.ID),
		Description: strings.TrimSpace(subject.Summary),
		Tags:        tags,
		Status:      status,
		UpdatedAt:   strings.TrimSpace(subject.Date),
	}, true
}

func bangumiSubjectLooksNovel(tags []string) bool {
	for _, tag := range tags {
		normalized := strings.ToLower(strings.TrimSpace(tag))
		if normalized == "小说" || normalized == "轻小说" || strings.Contains(normalized, "小说") {
			return true
		}
	}
	return false
}

func (s *Server) bangumiHTTPClient() *http.Client {
	if s.registry != nil {
		return s.registry.Client()
	}
	return http.DefaultClient
}
