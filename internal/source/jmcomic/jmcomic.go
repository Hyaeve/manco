package jmcomic

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
)

type Source struct {
	client       *http.Client
	directClient *http.Client
	scrambleMu   sync.Mutex
	scrambleIDs  map[string]int
}

const (
	sourceID               = "jmcomic"
	defaultSite            = "https://www.cdnhjk.net"
	appVersion             = "2.1.7"
	appTokenSecret         = "185Hcomic3PAPP7R"
	appDataSecret          = "185Hcomic3PAPP7R"
	appScrambleTokenSecret = "18comicAPPContent"
	appUserAgent           = "Mozilla/5.0 (Linux; Android 9; V1938CT Build/PQ3A.190705.11211812; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/91.0.4472.114 Safari/537.36"
	pageSize               = 80
	defaultScrambleID      = 220980
)

var defaultSites = []string{
	"https://www.cdnhjk.net",
	"https://www.cdngwc.cc",
	"https://www.cdngwc.net",
	"https://www.cdngwc.club",
	"https://www.cdnutc.me",
}

var imageDomains = []string{
	"https://cdn-msp.jmapiproxy1.cc",
	"https://cdn-msp.jmapiproxy2.cc",
	"https://cdn-msp2.jmapiproxy2.cc",
	"https://cdn-msp3.jmapiproxy2.cc",
	"https://cdn-msp.jmapinodeudzn.net",
	"https://cdn-msp3.jmapinodeudzn.net",
}

var (
	scramblePattern = regexp.MustCompile(`var\s+scramble_id\s*=\s*(\d+)`)
	comicIDPattern  = regexp.MustCompile(`/(?:album|photo)/(\d+)`)
)

var jmCategoryOptions = []model.FilterOption{
	{Value: "0", Label: "全部"},
	{Value: "doujin", Label: "同人"},
	{Value: "single", Label: "单本"},
	{Value: "short", Label: "短篇"},
	{Value: "another", Label: "其他类"},
	{Value: "hanman", Label: "韩漫"},
	{Value: "meiman", Label: "美漫"},
	{Value: "doujin_cosplay", Label: "Cosplay"},
	{Value: "3D", Label: "3D"},
}

var jmSortOptions = []model.FilterOption{
	{Value: "mr", Label: "最新"},
	{Value: "mv", Label: "最多观看"},
	{Value: "mv_t", Label: "今日热门"},
	{Value: "mv_w", Label: "本周热门"},
	{Value: "mv_m", Label: "本月热门"},
	{Value: "tf", Label: "最多喜欢"},
	{Value: "mp", Label: "最多图片"},
}

func New(client *http.Client) *Source {
	return &Source{client: client, scrambleIDs: map[string]int{}}
}

func (s *Source) Info() model.SourceInfo {
	return model.SourceInfo{
		ID:          sourceID,
		Name:        "禁漫天堂",
		Kind:        source.KindComic,
		Description: "18comic mobile API source",
		Homepage:    defaultSite + "/",
		NeedsLogin:  false,
		CanSearch:   true,
		CanBrowse:   true,
		Icon:        "/source-icons/jmcomic.png",
		Sites:       append([]string(nil), defaultSites...),
		Filters: []model.FilterGroup{
			{Key: "category", Label: "分类", Default: "0", Options: jmCategoryOptions},
			{Key: "sort", Label: "排序", Default: "mr", Options: jmSortOptions},
		},
	}
}

func (s *Source) Search(ctx context.Context, account source.Account, query string, page int) (model.SearchResult, error) {
	if page < 1 {
		page = 1
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return model.SearchResult{}, errors.New("搜索关键词不能为空")
	}
	payload, _, err := s.apiJSON(ctx, account, "/search", url.Values{
		"main_tag":     {"0"},
		"search_query": {query},
		"page":         {strconv.Itoa(page)},
		"o":            {"mr"},
		"t":            {""},
	}, appTokenSecret)
	if err != nil {
		return model.SearchResult{}, err
	}
	var envelope apiSearchPage
	if err := decodeJSON(payload, &envelope); err != nil {
		return model.SearchResult{}, err
	}
	if redirectID := normalizeComicID(textValue(envelope.RedirectAID)); redirectID != "" {
		comic, detailErr := s.Detail(ctx, account, redirectID)
		if detailErr != nil {
			return model.SearchResult{}, detailErr
		}
		return model.SearchResult{
			Items:   []model.Comic{comic},
			Page:    page,
			Pages:   1,
			Total:   1,
			HasMore: false,
		}, nil
	}
	return parseSearchResult(envelope, page)
}

func (s *Source) Browse(ctx context.Context, account source.Account, options model.BrowseOptions, page int) (model.SearchResult, error) {
	if page < 1 {
		page = 1
	}
	category := normalizeCategory(options.Category)
	order := normalizeSort(options.Sort)
	payload, _, err := s.apiJSON(ctx, account, "/categories/filter", url.Values{
		"page":  {strconv.Itoa(page)},
		"order": {""},
		"c":     {category},
		"o":     {order},
	}, appTokenSecret)
	if err != nil {
		return model.SearchResult{}, err
	}
	var envelope apiSearchPage
	if err := decodeJSON(payload, &envelope); err != nil {
		return model.SearchResult{}, err
	}
	return parseSearchResult(envelope, page)
}

func (s *Source) Detail(ctx context.Context, account source.Account, comicID string) (model.Comic, error) {
	comicID = normalizeComicID(comicID)
	if comicID == "" {
		return model.Comic{}, errors.New("无效的禁漫作品 ID")
	}
	payload, _, err := s.apiJSON(ctx, account, "/album", url.Values{"id": {comicID}}, appTokenSecret)
	if err != nil {
		return model.Comic{}, err
	}
	var album apiAlbumDetail
	if err := decodeJSON(payload, &album); err != nil {
		return model.Comic{}, err
	}
	if strings.TrimSpace(album.Name) == "" {
		return model.Comic{}, fmt.Errorf("禁漫作品 %s 不存在或没有返回数据", comicID)
	}
	tags := append([]string{}, stringList(album.Tags)...)
	tags = appendUnique(tags, stringList(album.Works)...)
	tags = appendUnique(tags, stringList(album.Actors)...)
	chapterCount := len(album.Series)
	if chapterCount == 0 {
		chapterCount = 1
	}
	return model.Comic{
		SourceID:     sourceID,
		ID:           source.FirstNonEmpty(textValue(album.ID), comicID),
		Title:        strings.TrimSpace(album.Name),
		Cover:        coverURL(comicID),
		Author:       strings.Join(stringList(album.Author), " / "),
		Description:  textValue(album.Description),
		Tags:         tags,
		ChapterCount: chapterCount,
		UpdatedAt:    unixText(album.AddTime),
	}, nil
}

func (s *Source) Chapters(ctx context.Context, account source.Account, comicID string) ([]model.Chapter, error) {
	comicID = normalizeComicID(comicID)
	if comicID == "" {
		return nil, errors.New("无效的禁漫作品 ID")
	}
	payload, base, err := s.apiJSON(ctx, account, "/album", url.Values{"id": {comicID}}, appTokenSecret)
	if err != nil {
		return nil, err
	}
	var album apiAlbumDetail
	if err := decodeJSON(payload, &album); err != nil {
		return nil, err
	}
	if len(album.Series) == 0 {
		title := source.FirstNonEmpty(strings.TrimSpace(album.Name), "禁漫 "+comicID)
		pageCount := intValue(album.TotalPhotos)
		return []model.Chapter{{
			ID:        comicID,
			ComicID:   comicID,
			Title:     title,
			Order:     1,
			PageCount: pageCount,
			URL:       source.BuildURL(base, "/album/"+comicID, nil),
		}}, nil
	}
	chapters := make([]model.Chapter, 0, len(album.Series))
	for index, item := range album.Series {
		chapterID := normalizeComicID(textValue(item.ID))
		if chapterID == "" {
			continue
		}
		order := float64(intValue(item.Sort))
		if order <= 0 {
			order = float64(index + 1)
		}
		title := strings.TrimSpace(item.Name)
		if title == "" {
			title = "第 " + strconv.Itoa(index+1) + " 话"
		}
		chapters = append(chapters, model.Chapter{
			ID:      chapterID,
			ComicID: comicID,
			Title:   title,
			Order:   order,
			URL:     source.BuildURL(base, "/photo/"+chapterID, nil),
		})
	}
	sort.SliceStable(chapters, func(i, j int) bool {
		if chapters[i].Order == chapters[j].Order {
			return chapters[i].ID < chapters[j].ID
		}
		return chapters[i].Order < chapters[j].Order
	})
	return chapters, nil
}

func (s *Source) Pages(ctx context.Context, account source.Account, comicID string, chapter model.Chapter) ([]model.Page, error) {
	photoID := normalizeComicID(chapter.ID)
	if photoID == "" {
		photoID = normalizeComicID(chapter.URL)
	}
	if photoID == "" {
		return nil, errors.New("无效的禁漫章节 ID")
	}
	comicID = normalizeComicID(comicID)
	if comicID == "" {
		comicID = photoID
	}
	payload, base, err := s.apiJSON(ctx, account, "/chapter", url.Values{"id": {photoID}}, appTokenSecret)
	if err != nil {
		return nil, err
	}
	var detail apiChapterDetail
	if err := decodeJSON(payload, &detail); err != nil {
		return nil, err
	}
	names := append([]string(nil), detail.Images...)
	albumID := intValue(detail.SeriesID)
	if len(names) == 0 {
		// Some older/single albums expose the list only on /album. Keep that
		// fallback so a valid album is still downloadable after an API change.
		albumPayload, _, albumErr := s.apiJSON(ctx, account, "/album", url.Values{"id": {source.FirstNonEmpty(comicID, photoID)}}, appTokenSecret)
		if albumErr == nil {
			var album apiAlbumDetail
			if decodeJSON(albumPayload, &album) == nil {
				names = append(names, album.Images...)
				if albumID == 0 {
					albumID = intValue(album.ID)
				}
			}
		}
	}
	if len(names) == 0 {
		return nil, errors.New("禁漫章节没有可下载的图片")
	}
	if albumID == 0 {
		albumID = intValue(comicID)
	}
	if albumID == 0 {
		albumID = intValue(photoID)
	}
	scrambleID := s.scrambleID(ctx, account, photoID, strconv.Itoa(albumID))
	parts := scrambleParts(scrambleID, albumID, strings.TrimSpace(names[0]))
	referer := source.BuildURL(base, "/", nil)
	headers := map[string]string{
		"X-Requested-With": "com.JMComic3.app",
		"Accept-Language":  "zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7",
	}
	pages := make([]model.Page, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		addresses := make([]string, 0, len(imageDomains))
		for _, domain := range imageDomains {
			addresses = append(addresses, fmt.Sprintf("%s/media/photos/%s/%s", strings.TrimRight(domain, "/"), photoID, name))
		}
		if len(addresses) == 0 {
			continue
		}
		pages = append(pages, model.Page{
			URL:             addresses[0],
			Alternatives:    addresses[1:],
			Referer:         referer,
			Headers:         headers,
			FileName:        name,
			DescrambleParts: parts,
			AssetID:         strconv.Itoa(albumID),
		})
	}
	if len(pages) == 0 {
		return nil, errors.New("禁漫章节没有可下载的图片")
	}
	return pages, nil
}

type apiSearchPage struct {
	Total       any             `json:"total"`
	Content     []apiSearchItem `json:"content"`
	RedirectAID any             `json:"redirect_aid"`
}

type apiSearchItem struct {
	ID          any         `json:"id"`
	Name        string      `json:"name"`
	Author      string      `json:"author"`
	Description any         `json:"description"`
	AddDate     string      `json:"adddate"`
	UpdateAt    any         `json:"update_at"`
	Category    apiCategory `json:"category"`
	SubCategory apiCategory `json:"category_sub"`
}

type apiCategory struct {
	ID    any    `json:"id"`
	Title string `json:"title"`
}

type apiAlbumDetail struct {
	ID          any         `json:"id"`
	Name        string      `json:"name"`
	Author      []any       `json:"author"`
	Description any         `json:"description"`
	Tags        []any       `json:"tags"`
	Works       []any       `json:"works"`
	Actors      []any       `json:"actors"`
	Images      []string    `json:"images"`
	TotalPhotos any         `json:"total_photos"`
	AddTime     any         `json:"addtime"`
	Series      []apiSeries `json:"series"`
}

type apiChapterDetail struct {
	ID       any         `json:"id"`
	Name     string      `json:"name"`
	Images   []string    `json:"images"`
	SeriesID any         `json:"series_id"`
	Series   []apiSeries `json:"series"`
}

type apiSeries struct {
	ID   any    `json:"id"`
	Name string `json:"name"`
	Sort any    `json:"sort"`
}

func parseSearchResult(envelope apiSearchPage, page int) (model.SearchResult, error) {
	items := make([]model.Comic, 0, len(envelope.Content))
	for _, entry := range envelope.Content {
		comicID := normalizeComicID(textValue(entry.ID))
		if comicID == "" {
			continue
		}
		tags := make([]string, 0, 2)
		if title := strings.TrimSpace(entry.Category.Title); title != "" {
			tags = append(tags, title)
		}
		if title := strings.TrimSpace(entry.SubCategory.Title); title != "" && !containsText(tags, title) {
			tags = append(tags, title)
		}
		updatedAt := strings.TrimSpace(entry.AddDate)
		if updatedAt == "" {
			updatedAt = unixText(entry.UpdateAt)
		}
		items = append(items, model.Comic{
			SourceID:  sourceID,
			ID:        comicID,
			Title:     source.FirstNonEmpty(strings.TrimSpace(entry.Name), "禁漫 "+comicID),
			Cover:     coverURL(comicID),
			Author:    normalizeAuthor(entry.Author),
			Tags:      tags,
			UpdatedAt: updatedAt,
		})
	}
	total := intValue(envelope.Total)
	if total <= 0 {
		total = len(items)
	}
	pages := 0
	hasMore := len(items) >= pageSize
	if total > 0 {
		pages = (total + pageSize - 1) / pageSize
		hasMore = page < pages
	}
	return model.SearchResult{
		Items:   items,
		Page:    page,
		Pages:   pages,
		Total:   total,
		HasMore: hasMore,
	}, nil
}

func (s *Source) apiJSON(ctx context.Context, account source.Account, path string, params url.Values, secret string) ([]byte, string, error) {
	client := source.SourceClient(s.client, &s.directClient, account)
	var lastErr error
	for _, base := range s.bases(account) {
		payload, err := s.apiJSONBase(ctx, client, account, base, path, params, secret)
		if err == nil {
			return payload, base, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("禁漫镜像均不可用")
	}
	return nil, "", fmt.Errorf("禁漫接口请求失败: %w", lastErr)
}

func (s *Source) apiJSONBase(ctx context.Context, client *http.Client, account source.Account, base, path string, params url.Values, secret string) ([]byte, error) {
	address := source.BuildURL(base, path, params)
	timestamp := time.Now().Unix()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	setAppHeaders(request, account, base, timestamp, secret)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s HTTP %d: %s", path, response.StatusCode, snippet(body))
	}
	var envelope struct {
		Code  int             `json:"code"`
		Data  json.RawMessage `json:"data"`
		Error string          `json:"errorMsg"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("%s 返回非 JSON: %w (%s)", path, err, snippet(body))
	}
	if envelope.Code != http.StatusOK {
		return nil, fmt.Errorf("%s code=%d: %s", path, envelope.Code, strings.TrimSpace(envelope.Error))
	}
	var encoded string
	if err := json.Unmarshal(envelope.Data, &encoded); err != nil {
		return nil, fmt.Errorf("%s 数据字段格式无效: %w", path, err)
	}
	plain, err := decryptAPIData(timestamp, encoded, secret)
	if err != nil {
		return nil, fmt.Errorf("%s 解密失败: %w", path, err)
	}
	return plain, nil
}

func (s *Source) scrambleID(ctx context.Context, account source.Account, photoID, albumID string) int {
	cacheKey := strings.TrimSpace(albumID)
	if cacheKey == "" || cacheKey == "0" {
		cacheKey = strings.TrimSpace(photoID)
	}
	s.scrambleMu.Lock()
	defer s.scrambleMu.Unlock()
	if s.scrambleIDs == nil {
		s.scrambleIDs = map[string]int{}
	}
	if cached, ok := s.scrambleIDs[cacheKey]; ok {
		return cached
	}
	client := source.SourceClient(s.client, &s.directClient, account)
	for _, base := range s.bases(account) {
		raw, err := s.rawScrambleRequest(ctx, client, account, base, photoID)
		if err != nil {
			continue
		}
		if match := scramblePattern.FindStringSubmatch(raw); len(match) >= 2 {
			if parsed, parseErr := strconv.Atoi(match[1]); parseErr == nil && parsed > 0 {
				s.scrambleIDs[cacheKey] = parsed
				return parsed
			}
		}
	}
	s.scrambleIDs[cacheKey] = defaultScrambleID
	return defaultScrambleID
}

func (s *Source) rawScrambleRequest(ctx context.Context, client *http.Client, account source.Account, base, photoID string) (string, error) {
	address := source.BuildURL(base, "/chapter_view_template", url.Values{
		"id":            {photoID},
		"mode":          {"vertical"},
		"page":          {"0"},
		"app_img_shunt": {"1"},
		"express":       {"off"},
		"v":             {strconv.FormatInt(time.Now().Unix(), 10)},
	})
	timestamp := time.Now().Unix()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", err
	}
	setAppHeaders(request, account, base, timestamp, appScrambleTokenSecret)
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("scramble HTTP %d", response.StatusCode)
	}
	return string(body), nil
}

func (s *Source) bases(account source.Account) []string {
	seen := map[string]bool{}
	order := make([]string, 0, len(defaultSites)+1)
	add := func(value string) {
		value = strings.TrimRight(strings.TrimSpace(value), "/")
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		order = append(order, value)
	}
	add(account.HomeURL)
	for _, site := range defaultSites {
		add(site)
	}
	return order
}

func setAppHeaders(request *http.Request, account source.Account, base string, timestamp int64, secret string) {
	userAgent := strings.TrimSpace(account.UserAgent)
	if userAgent == "" {
		userAgent = appUserAgent
	}
	timestampText := strconv.FormatInt(timestamp, 10)
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Token", md5Hex(timestampText+secret))
	request.Header.Set("Tokenparam", timestampText+","+appVersion)
	request.Header.Set("Referer", strings.TrimRight(base, "/")+"/")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7")
	if strings.TrimSpace(account.Cookie) != "" {
		request.Header.Set("Cookie", account.Cookie)
	}
}

func decryptAPIData(timestamp int64, encoded, secret string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("base64: %w", err)
	}
	if len(raw) == 0 || len(raw)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("invalid encrypted block length %d", len(raw))
	}
	key := md5Hex(strconv.FormatInt(timestamp, 10) + secret)
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(raw))
	for offset := 0; offset < len(raw); offset += aes.BlockSize {
		block.Decrypt(plain[offset:offset+aes.BlockSize], raw[offset:offset+aes.BlockSize])
	}
	plain, err = unpadPKCS7(plain)
	if err != nil {
		return nil, err
	}
	if !json.Valid(plain) {
		return nil, errors.New("解密结果不是有效 JSON")
	}
	return plain, nil
}

func unpadPKCS7(value []byte) ([]byte, error) {
	if len(value) == 0 {
		return nil, errors.New("empty padded value")
	}
	padding := int(value[len(value)-1])
	if padding < 1 || padding > aes.BlockSize || padding > len(value) {
		return nil, errors.New("invalid PKCS7 padding")
	}
	for _, item := range value[len(value)-padding:] {
		if int(item) != padding {
			return nil, errors.New("invalid PKCS7 padding")
		}
	}
	return value[:len(value)-padding], nil
}

func decodeJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func md5Hex(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

func textValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func intValue(value any) int {
	text := textValue(value)
	if text == "" {
		return 0
	}
	if parsed, err := strconv.Atoi(text); err == nil {
		return parsed
	}
	if parsed, err := strconv.ParseFloat(text, 64); err == nil {
		return int(parsed)
	}
	return 0
}

func stringList(values []any) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		text := strings.TrimSpace(textValue(value))
		if text == "" || strings.EqualFold(text, "N/A") {
			continue
		}
		result = append(result, text)
	}
	return result
}

func appendUnique(values []string, extra ...string) []string {
	for _, value := range extra {
		value = strings.TrimSpace(value)
		if value == "" || containsText(values, value) {
			continue
		}
		values = append(values, value)
	}
	return values
}

func containsText(values []string, value string) bool {
	for _, item := range values {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}

func normalizeAuthor(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "N/A") {
		return ""
	}
	return value
}

func normalizeCategory(value string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "", "0", "all":
		return "0"
	case "another_cosplay":
		return "doujin_cosplay"
	default:
		return value
	}
}

func normalizeSort(value string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "", "latest":
		return "mr"
	case "hot", "week", "weekly":
		return "mv_w"
	case "today", "daily":
		return "mv_t"
	default:
		return value
	}
}

func coverURL(comicID string) string {
	comicID = normalizeComicID(comicID)
	if comicID == "" {
		return ""
	}
	return "https://cdn-msp.jmapiproxy1.cc/media/albums/" + comicID + ".jpg"
}

func unixText(value any) string {
	seconds := int64(intValue(value))
	if seconds <= 0 {
		return ""
	}
	return time.Unix(seconds, 0).Format("2006-01-02")
}

func snippet(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > 220 {
		return text[:220]
	}
	return text
}

func scrambleParts(scrambleID, albumID int, fileName string) int {
	if scrambleID == 0 || albumID == 0 || albumID < scrambleID {
		return 0
	}
	if albumID < 268850 {
		return 10
	}
	base := 8
	if albumID < 421926 {
		base = 10
	}
	digest := md5.Sum([]byte(strconv.Itoa(albumID) + fileName))
	encoded := hex.EncodeToString(digest[:])
	last := encoded[len(encoded)-1]
	value, err := strconv.ParseInt(string(last), 16, 32)
	if err != nil {
		return 0
	}
	return int(value)%base*2 + 2
}

func normalizeComicID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if matches := comicIDPattern.FindStringSubmatch(value); len(matches) >= 2 {
		return matches[1]
	}
	value = strings.TrimSuffix(value, ".html")
	for _, index := range []int{strings.LastIndex(value, "/"), strings.LastIndex(value, "-")} {
		if index >= 0 && index < len(value)-1 {
			value = value[index+1:]
		}
	}
	if _, err := strconv.Atoi(value); err != nil {
		return ""
	}
	return value
}

var _ source.Source = (*Source)(nil)
