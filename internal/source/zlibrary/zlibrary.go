// Package zlibrary implements the built-in Z-Library ebook source. It uses
// the public EAPI endpoints and keeps the original ebook file returned by the
// service instead of converting books to plain text.
package zlibrary

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
)

const (
	sourceID       = "zlibrary"
	defaultSite    = "https://1lib.sk"
	loginPath      = "/eapi/user/login"
	searchPath     = "/eapi/book/search"
	perPage        = 30
	maxBookSize    = 256 << 20
	sessionPrefix  = "zlib:"
	defaultBookExt = ".epub"
)

var defaultSites = []string{
	defaultSite,
	"https://z-library.sk",
}

var categoryOptions = []model.FilterOption{
	{Value: "popular", Label: "热门"},
	{Value: "recent", Label: "最近"},
	{Value: "chinese", Label: "中文"},
	{Value: "english", Label: "英文"},
	{Value: "japanese", Label: "日文"},
	{Value: "korean", Label: "韩文"},
	{Value: "epub", Label: "EPUB"},
	{Value: "pdf", Label: "PDF"},
	{Value: "mobi", Label: "MOBI"},
	{Value: "azw3", Label: "AZW3"},
}

var sortOptions = []model.FilterOption{
	{Value: "popular", Label: "热门"},
	{Value: "recent", Label: "最新"},
}

type Source struct {
	client       *http.Client
	directClient *http.Client
	sessionMu    sync.Mutex
	jar          http.CookieJar
	jarKey       [32]byte
	jarBases     map[string]bool
}

type session struct {
	UserID  string `json:"userId"`
	UserKey string `json:"userKey"`
}

func New(client *http.Client) *Source {
	return &Source{client: client}
}

func (s *Source) Info() model.SourceInfo {
	return model.SourceInfo{
		ID:          sourceID,
		Name:        "Z-Library",
		Kind:        source.KindBook,
		Description: "电子书检索与原始文件下载，支持账号登录",
		Homepage:    defaultSite + "/",
		NeedsLogin:  true,
		CanSearch:   true,
		CanBrowse:   true,
		Icon:        "/source-icons/zlibrary.png",
		Sites:       append([]string(nil), defaultSites...),
		Filters: []model.FilterGroup{
			{Key: "category", Label: "分类", Options: categoryOptions},
			{Key: "sort", Label: "排序", Default: "popular", Options: sortOptions},
		},
	}
}

func (s *Source) Login(ctx context.Context, account source.Account) (source.LoginResult, error) {
	email := strings.TrimSpace(account.Username)
	if email == "" || account.Password == "" {
		return source.LoginResult{}, errors.New("Z-Library 邮箱和密码不能为空")
	}
	payload := map[string]any{}
	_, err := s.requestJSON(ctx, account, http.MethodPost, loginPath, nil, url.Values{
		"email":    {email},
		"password": {account.Password},
	}, &payload)
	if err != nil {
		return source.LoginResult{}, err
	}
	if message := payloadError(payload); message != "" {
		return source.LoginResult{}, errors.New(message)
	}
	user := objectValue(payload["user"])
	if len(user) == 0 {
		if response := objectValue(payload["response"]); len(response) > 0 {
			user = objectValue(response["user"])
			if len(user) == 0 {
				user = response
			}
		}
	}
	if len(user) == 0 {
		return source.LoginResult{}, errors.New("Z-Library 登录响应缺少用户信息")
	}
	userID := firstValue(user["id"], user["user_id"], user["userId"], payload["remix_userid"])
	userKey := firstValue(user["remix_userkey"], user["remixUserKey"], payload["remix_userkey"])
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(userKey) == "" {
		return source.LoginResult{}, errors.New("Z-Library 登录未返回 remix_userid/remix_userkey")
	}
	token, err := encodeSession(strings.TrimSpace(userID), strings.TrimSpace(userKey))
	if err != nil {
		return source.LoginResult{}, err
	}
	username := firstValue(user["email"], user["name"], payload["email"])
	if strings.TrimSpace(username) == "" {
		username = email
	}
	return source.LoginResult{Token: token, Username: username}, nil
}

func (s *Source) Search(ctx context.Context, account source.Account, query string, page int) (model.SearchResult, error) {
	if page < 1 {
		page = 1
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return model.SearchResult{}, errors.New("搜索关键词不能为空")
	}
	payload := map[string]any{}
	base, err := s.requestJSON(ctx, account, http.MethodPost, searchPath, nil, url.Values{
		"message": {query},
		"page":    {strconv.Itoa(page)},
		"limit":   {strconv.Itoa(perPage)},
	}, &payload)
	if err != nil {
		return model.SearchResult{}, err
	}
	return parseListing(payload, base, page), nil
}

func (s *Source) Browse(ctx context.Context, account source.Account, options model.BrowseOptions, page int) (model.SearchResult, error) {
	if page < 1 {
		page = 1
	}
	category := strings.ToLower(strings.TrimSpace(options.Category))
	sortValue := strings.ToLower(strings.TrimSpace(options.Sort))
	payload := map[string]any{}
	var (
		base string
		err  error
	)
	switch {
	case category == "recent" || (category == "" && (sortValue == "recent" || sortValue == "newest")):
		base, err = s.requestJSON(ctx, account, http.MethodGet, "/eapi/book/recently", url.Values{
			"page":  {strconv.Itoa(page)},
			"limit": {strconv.Itoa(perPage)},
		}, nil, &payload)
	case category == "" || category == "popular":
		base, err = s.requestJSON(ctx, account, http.MethodGet, "/eapi/book/most-popular", url.Values{
			"page":  {strconv.Itoa(page)},
			"limit": {strconv.Itoa(perPage)},
		}, nil, &payload)
	case isLanguage(category) || isExtension(category):
		values := url.Values{
			"page":  {strconv.Itoa(page)},
			"limit": {strconv.Itoa(perPage)},
			"order": {apiOrder(sortValue)},
		}
		if isLanguage(category) {
			values.Set("languages[]", category)
		} else {
			values.Set("extensions[]", category)
		}
		base, err = s.requestJSON(ctx, account, http.MethodPost, searchPath, nil, values, &payload)
	default:
		return model.SearchResult{}, fmt.Errorf("不支持 Z-Library 筛选：%s", category)
	}
	if err != nil {
		return model.SearchResult{}, err
	}
	return parseListing(payload, base, page), nil
}

func (s *Source) Detail(ctx context.Context, account source.Account, comicID string) (model.Comic, error) {
	bookID, hash, err := splitBookID(comicID)
	if err != nil {
		return model.Comic{}, err
	}
	payload := map[string]any{}
	base, err := s.requestJSON(ctx, account, http.MethodGet, bookPath(bookID, hash), nil, nil, &payload)
	if err != nil {
		return model.Comic{}, err
	}
	book := bookObject(payload)
	if len(book) == 0 {
		return model.Comic{}, errors.New("Z-Library 未返回书籍详情")
	}
	comic := bookFromRow(book, base)
	if comic.ID == "" {
		return model.Comic{}, errors.New("Z-Library 书籍详情缺少 ID")
	}
	return comic, nil
}

func (s *Source) Chapters(ctx context.Context, account source.Account, comicID string) ([]model.Chapter, error) {
	detail, err := s.DetailWithChapters(ctx, account, comicID)
	if err != nil {
		return nil, err
	}
	return detail.Chapters, nil
}

// DetailWithChapters reuses the detail response for the single-file ebook
// chapter instead of issuing a second, easily rate-limited EAPI request.
func (s *Source) DetailWithChapters(ctx context.Context, account source.Account, comicID string) (model.ComicDetail, error) {
	book, err := s.Detail(ctx, account, comicID)
	if err != nil {
		return model.ComicDetail{}, err
	}
	title := source.FirstNonEmpty(book.Title, "电子书")
	chapters := []model.Chapter{{
		ID:      comicID,
		ComicID: comicID,
		Title:   title,
		Order:   1,
	}}
	return model.ComicDetail{Comic: book, Chapters: chapters}, nil
}

func (s *Source) Pages(context.Context, source.Account, string, model.Chapter) ([]model.Page, error) {
	return nil, errors.New("书籍源不支持图片下载")
}

func (s *Source) ChapterFile(ctx context.Context, account source.Account, bookID string, chapter model.Chapter) ([]byte, string, error) {
	comicID := source.FirstNonEmpty(bookID, chapter.ComicID)
	id, hash, err := splitBookID(comicID)
	if err != nil {
		return nil, "", err
	}
	payload := map[string]any{}
	base, err := s.requestJSON(ctx, account, http.MethodGet, bookPath(id, hash)+"/file", nil, nil, &payload)
	if err != nil {
		return nil, "", err
	}
	file := objectValue(payload["file"])
	if len(file) == 0 {
		if data := objectValue(payload["data"]); len(data) > 0 {
			file = objectValue(data["file"])
		}
	}
	if len(file) == 0 {
		return nil, "", errors.New("Z-Library 未返回文件信息")
	}
	link := firstValue(file["downloadLink"], file["download_link"], file["url"])
	if strings.TrimSpace(link) == "" {
		return nil, "", errors.New("Z-Library 文件信息缺少下载地址")
	}
	address := source.Absolutize(base, link)
	data, err := s.downloadBytes(ctx, account, base, address)
	if err != nil {
		return nil, "", err
	}
	filename := firstValue(file["description"], file["name"], file["title"])
	author := firstValue(file["author"])
	extension := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(firstValue(file["extension"]))), ".")
	if extension == "" {
		extension = extensionFromAddress(address)
	}
	if filename == "" {
		filename = source.FirstNonEmpty(chapter.Title, "电子书")
	}
	if !strings.HasSuffix(strings.ToLower(filename), "."+extension) && extension != "" {
		filename += "." + extension
	}
	if author != "" && !strings.Contains(filename, author) {
		baseName := strings.TrimSuffix(filename, "."+extension)
		filename = baseName + " (" + author + ")." + extension
	}
	return data, filename, nil
}

func (s *Source) bases(account source.Account) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(defaultSites)+1)
	add := func(value string) {
		value = strings.TrimRight(strings.TrimSpace(value), "/")
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		result = append(result, value)
	}
	add(account.HomeURL)
	for _, site := range defaultSites {
		add(site)
	}
	return result
}

func (s *Source) requestJSON(ctx context.Context, account source.Account, method, requestPath string, query, form url.Values, out any) (string, error) {
	var failures []string
	for _, base := range s.bases(account) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		raw, err := s.request(ctx, account, base, method, requestPath, query, form)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", base, err))
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err == nil {
			if message := payloadError(payload); message != "" {
				return "", fmt.Errorf("Z-Library: %s", message)
			}
		}
		if err := json.Unmarshal(raw, out); err != nil {
			failures = append(failures, fmt.Sprintf("%s: API 返回了无效的 JSON 数据", base))
			continue
		}
		return base, nil
	}
	if len(failures) == 0 {
		return "", errors.New("Z-Library 没有可用站点")
	}
	return "", fmt.Errorf("Z-Library 请求失败: %s", strings.Join(failures, "; "))
}

func (s *Source) request(ctx context.Context, account source.Account, base, method, requestPath string, query, form url.Values) ([]byte, error) {
	if requestPath != loginPath && strings.TrimSpace(account.Token) == "" && strings.TrimSpace(account.Cookie) == "" {
		return nil, errors.New("Z-Library 尚未登录，请先在资源仓库中登录")
	}
	address := strings.TrimRight(base, "/") + requestPath
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", source.FirstNonEmpty(account.UserAgent, source.DefaultUserAgent))
	request.Header.Set("Accept", "application/json,text/plain,*/*")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.7")
	request.Header.Set("Referer", strings.TrimRight(base, "/")+"/")
	request.Header.Set("Origin", strings.TrimRight(base, "/"))
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	client, err := s.requestClient(account, base, false)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if err := responseError(response, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func responseError(response *http.Response, raw []byte) error {
	text := strings.ToLower(string(raw[:min(len(raw), 8192)]))
	html := strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/html") ||
		strings.HasPrefix(strings.TrimSpace(text), "<")
	if response.StatusCode == 513 || html && (strings.Contains(text, "diamwall") ||
		strings.Contains(text, "cf-chl-") || strings.Contains(text, "checking your browser") ||
		strings.Contains(text, "verifying your browser")) {
		return verificationError()
	}
	if response.StatusCode == http.StatusUnauthorized {
		return errors.New("Z-Library 登录已失效，请在资源拓展中重新登录或更新 Cookie")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, http.StatusText(response.StatusCode))
	}
	if html {
		return errors.New("Z-Library 返回了网页而不是 API 数据，请核对网站地址、登录状态和浏览器验证 Cookie")
	}
	return nil
}

func verificationError() error {
	return errors.New("Z-Library 需要浏览器验证，请在资源拓展中打开该网站完成验证，保存同一浏览器的 Cookie 和 User-Agent；浏览器与 Manco 应使用相同网络或代理出口")
}

// Scope the jar to the selected site, including during redirects to file hosts.
type siteCookieJar struct {
	http.CookieJar
	host string
}

func (j siteCookieJar) Cookies(address *url.URL) []*http.Cookie {
	if !strings.EqualFold(address.Host, j.host) {
		return nil
	}
	return j.CookieJar.Cookies(address)
}

func (j siteCookieJar) SetCookies(address *url.URL, cookies []*http.Cookie) {
	if strings.EqualFold(address.Host, j.host) {
		j.CookieJar.SetCookies(address, cookies)
	}
}

func (s *Source) requestClient(account source.Account, base string, download bool) (*http.Client, error) {
	address, err := url.Parse(base)
	if err != nil || address.Host == "" || (address.Scheme != "https" && address.Scheme != "http") || address.User != nil {
		return nil, errors.New("Z-Library 网站地址无效")
	}
	fingerprint, _ := json.Marshal([]any{
		account.Username, account.Password, account.Token, account.Cookie,
		account.UserAgent, account.HomeURL, source.AccountUsesProxy(account),
	})
	key := sha256.Sum256(fingerprint)
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if s.jar == nil || s.jarKey != key {
		jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
		if err != nil {
			return nil, err
		}
		s.jar, s.jarKey, s.jarBases = jar, key, map[string]bool{}
	}
	if !s.jarBases[base] {
		cookies := &http.Request{Header: http.Header{"Cookie": {cookieHeader(account)}}}
		s.jar.SetCookies(address, cookies.Cookies())
		s.jarBases[base] = true
	}
	// Copy the client, retaining its transport/proxy without changing other sources.
	client := *source.SourceClient(s.client, &s.directClient, account)
	client.Jar = siteCookieJar{CookieJar: s.jar, host: address.Host}
	if client.Timeout == 0 {
		client.Timeout = 60 * time.Second
	}
	parentCheck := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return verificationError()
		}
		if request.URL.Scheme != "http" && request.URL.Scheme != "https" ||
			(via[0].URL.Scheme == "https" && request.URL.Scheme != "https") {
			return errors.New("Z-Library 拒绝不安全的重定向")
		}
		if !strings.EqualFold(request.URL.Host, address.Host) {
			if !download {
				return errors.New("Z-Library API 跳转至其他域名，请确认网站地址后在内容源设置中更新")
			}
			request.Header.Del("Cookie")
			request.Header.Del("Authorization")
		}
		if parentCheck != nil {
			return parentCheck(request, via)
		}
		return nil
	}
	return &client, nil
}

func (s *Source) downloadBytes(ctx context.Context, account source.Account, base, address string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", source.FirstNonEmpty(account.UserAgent, source.DefaultUserAgent))
	request.Header.Set("Accept", "*/*")
	request.Header.Set("Referer", strings.TrimRight(base, "/")+"/")
	client, err := s.requestClient(account, base, true)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/html") {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, verificationError()
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("下载电子书失败，HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBookSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBookSize {
		return nil, errors.New("电子书文件超过 256MB 上限")
	}
	if len(data) == 0 {
		return nil, errors.New("电子书文件为空")
	}
	if err := responseError(response, data); err != nil {
		return nil, err
	}
	return data, nil
}

func parseListing(payload map[string]any, base string, page int) model.SearchResult {
	rows := bookRows(payload)
	items := make([]model.Comic, 0, len(rows))
	for _, row := range rows {
		item := bookFromRow(objectValue(row), base)
		if item.ID != "" {
			items = append(items, item)
		}
	}
	pagination := objectValue(payload["pagination"])
	if len(pagination) == 0 {
		pagination = objectValue(payload["meta"])
	}
	total := intValue(pagination["total"])
	pages := intValue(pagination["pages"])
	if pages <= 0 && len(rows) >= perPage {
		pages = page + 1
	}
	return model.SearchResult{
		Items:   items,
		Page:    page,
		Pages:   pages,
		Total:   total,
		HasMore: pages > page || (pages == 0 && len(rows) >= perPage),
	}
}

func bookFromRow(row map[string]any, base string) model.Comic {
	if len(row) == 0 {
		return model.Comic{}
	}
	id := firstMapValue(row, "id", "book_id", "bookId")
	hash := firstMapValue(row, "hash", "hashid", "hash_id", "hashId")
	title := firstMapValue(row, "name", "title")
	author := firstMapValue(row, "author")
	if author == "" {
		author = strings.Join(stringSlice(row["authors"]), " / ")
	}
	cover := firstMapValue(row, "cover", "cover_url", "coverUrl", "image")
	description := firstMapValue(row, "description", "summary", "annotation")
	extension := strings.TrimPrefix(strings.ToLower(firstMapValue(row, "extension")), ".")
	language := firstMapValue(row, "language")
	year := firstMapValue(row, "year")
	var tags []string
	for _, value := range []string{extension, language, year} {
		if value = strings.TrimSpace(value); value != "" {
			tags = append(tags, value)
		}
	}
	if id == "" {
		return model.Comic{}
	}
	if hash == "" {
		hash = firstMapValue(row, "hash_id")
	}
	return model.Comic{
		SourceID:     sourceID,
		ID:           bookKey(id, hash),
		Title:        source.FirstNonEmpty(title, "Z-Library "+id),
		Cover:        source.Absolutize(base, cover),
		Author:       author,
		Description:  description,
		Tags:         tags,
		ChapterCount: 1,
		UpdatedAt:    year,
	}
}

func bookRows(payload map[string]any) []any {
	for _, key := range []string{"books", "items", "results"} {
		if rows := anySlice(payload[key]); len(rows) > 0 {
			return rows
		}
	}
	for _, key := range []string{"data", "response", "result"} {
		if child := objectValue(payload[key]); len(child) > 0 {
			if rows := bookRows(child); len(rows) > 0 {
				return rows
			}
		}
	}
	return nil
}

func bookObject(payload map[string]any) map[string]any {
	for _, key := range []string{"book", "data", "response", "result"} {
		if child := objectValue(payload[key]); len(child) > 0 {
			if nested := objectValue(child["book"]); len(nested) > 0 {
				return nested
			}
			if firstMapValue(child, "id", "book_id", "bookId") != "" {
				return child
			}
		}
	}
	return payload
}

func payloadError(payload map[string]any) string {
	if value, exists := payload["success"]; exists {
		failed := firstValue(value) == "0"
		if success, ok := value.(bool); ok {
			failed = !success
		}
		if failed {
			return source.FirstNonEmpty(firstMapValue(payload, "error", "message", "errorMessage"), "API 拒绝请求，请检查登录状态")
		}
	}
	return firstMapValue(payload, "error", "errorMessage")
}

func objectValue(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func anySlice(value any) []any {
	items, _ := value.([]any)
	return items
}

func stringSlice(value any) []string {
	rows := anySlice(value)
	result := make([]string, 0, len(rows))
	for _, row := range rows {
		if text := firstValue(row); strings.TrimSpace(text) != "" {
			result = append(result, text)
		}
	}
	if len(result) == 0 {
		if text := firstValue(value); strings.TrimSpace(text) != "" {
			result = append(result, text)
		}
	}
	return result
}

func firstMapValue(row map[string]any, keys ...string) string {
	for _, key := range keys {
		if text := firstValue(row[key]); strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func firstValue(values ...any) string {
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return strings.TrimSpace(typed)
			}
		case json.Number:
			if text := strings.TrimSpace(typed.String()); text != "" {
				return text
			}
		case float64:
			if typed == float64(int64(typed)) {
				return strconv.FormatInt(int64(typed), 10)
			}
			return strconv.FormatFloat(typed, 'f', -1, 64)
		case int:
			return strconv.Itoa(typed)
		case int64:
			return strconv.FormatInt(typed, 10)
		}
	}
	return ""
}

func intValue(value any) int {
	text := firstValue(value)
	if text == "" {
		return 0
	}
	parsed, _ := strconv.Atoi(text)
	return parsed
}

func isLanguage(value string) bool {
	switch value {
	case "chinese", "english", "japanese", "korean", "french", "german", "spanish", "russian", "italian", "portuguese":
		return true
	default:
		return false
	}
}

func isExtension(value string) bool {
	switch value {
	case "epub", "pdf", "mobi", "azw", "azw3", "fb2", "djvu":
		return true
	default:
		return false
	}
}

func apiOrder(value string) string {
	switch value {
	case "recent", "newest", "date_created":
		return "date_created"
	default:
		return "popular"
	}
}

func bookKey(id, hash string) string {
	id = strings.TrimSpace(id)
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return id
	}
	return id + "~" + hash
}

func splitBookID(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", errors.New("无效的 Z-Library 书籍 ID")
	}
	parts := strings.SplitN(value, "~", 2)
	if strings.TrimSpace(parts[0]) == "" {
		return "", "", errors.New("无效的 Z-Library 书籍 ID")
	}
	if len(parts) == 1 || strings.TrimSpace(parts[1]) == "" {
		return parts[0], "", nil
	}
	return parts[0], parts[1], nil
}

func bookPath(id, hash string) string {
	pathValue := "/eapi/book/" + url.PathEscape(id)
	if strings.TrimSpace(hash) != "" {
		pathValue += "/" + url.PathEscape(hash)
	}
	return pathValue
}

func extensionFromAddress(address string) string {
	parsed, err := url.Parse(address)
	if err != nil {
		return defaultBookExt
	}
	extension := strings.TrimPrefix(strings.ToLower(path.Ext(parsed.Path)), ".")
	if extension == "" {
		return defaultBookExt
	}
	return extension
}

func encodeSession(userID, userKey string) (string, error) {
	if userID == "" || userKey == "" {
		return "", errors.New("Z-Library 会话信息为空")
	}
	raw, err := json.Marshal(session{UserID: userID, UserKey: userKey})
	if err != nil {
		return "", err
	}
	return sessionPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeSession(value string) (session, error) {
	if !strings.HasPrefix(value, sessionPrefix) {
		return session{}, errors.New("Z-Library 会话格式无效")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, sessionPrefix))
	if err != nil {
		return session{}, err
	}
	var result session
	if err := json.Unmarshal(raw, &result); err != nil {
		return session{}, err
	}
	if result.UserID == "" || result.UserKey == "" {
		return session{}, errors.New("Z-Library 会话不完整")
	}
	return result, nil
}

func cookieHeader(account source.Account) string {
	parts := make([]string, 0, 4)
	if strings.TrimSpace(account.Cookie) != "" {
		parts = append(parts, strings.TrimSpace(account.Cookie))
	}
	if sessionValue, err := decodeSession(account.Token); err == nil {
		if !cookieContains(account.Cookie, "remix_userid") {
			parts = append(parts, "remix_userid="+sessionValue.UserID)
		}
		if !cookieContains(account.Cookie, "remix_userkey") {
			parts = append(parts, "remix_userkey="+sessionValue.UserKey)
		}
	}
	if !cookieContains(account.Cookie, "siteLanguageV2") {
		parts = append(parts, "siteLanguageV2=en")
	}
	return strings.Join(parts, "; ")
}

func cookieContains(cookie, key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return false
	}
	for _, part := range strings.Split(cookie, ";") {
		name := strings.TrimSpace(strings.SplitN(part, "=", 2)[0])
		if strings.EqualFold(name, key) {
			return true
		}
	}
	return false
}
