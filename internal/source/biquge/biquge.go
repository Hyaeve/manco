// Package biquge implements a built-in Chinese web-novel source. It follows
// the same one-file-per-chapter contract as the other book sources.
package biquge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
)

const (
	sourceID    = "biquge"
	defaultSite = "https://www.piquge.com"
)

var defaultSites = []string{
	defaultSite,
	"https://piquge.com",
}

var categoryOptions = []model.FilterOption{
	{Value: "", Label: "全部"},
	{Value: "1", Label: "玄幻小说"},
	{Value: "2", Label: "仙侠小说"},
	{Value: "3", Label: "都市小说"},
	{Value: "4", Label: "历史小说"},
	{Value: "5", Label: "科幻小说"},
	{Value: "6", Label: "游戏小说"},
	{Value: "7", Label: "女生小说"},
	{Value: "8", Label: "其他小说"},
}

var (
	bookPattern            = regexp.MustCompile(`(?i)/(\d+)/(\d+)/`)
	rawBookPattern         = regexp.MustCompile(`^(\d+)/(\d+)$`)
	chapterPattern         = regexp.MustCompile(`(?i)/(\d+)/(\d+)/(\d+)(?:_\d+)?\.html`)
	nextChapterPagePattern = regexp.MustCompile(`(?is)const\s+next_page\s*=\s*"([^"]+)"`)
	markerPattern          = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>`)
	tagPattern             = regexp.MustCompile(`(?is)<[^>]+>`)
)

var errNoPages = errors.New("书籍源不支持图片下载")

type Source struct {
	client       *http.Client
	directClient *http.Client
}

func New(client *http.Client) *Source { return &Source{client: client} }

func (s *Source) Info() model.SourceInfo {
	return model.SourceInfo{
		ID:          sourceID,
		Name:        "笔趣阁",
		Kind:        source.KindBook,
		Description: "笔趣阁网页小说，按章节下载为文本",
		Homepage:    defaultSite + "/",
		NeedsLogin:  false,
		CanSearch:   true,
		CanBrowse:   true,
		Icon:        "/source-icons/biquge345.ico",
		Sites:       append([]string(nil), defaultSites...),
		Filters: []model.FilterGroup{
			{Key: "category", Label: "分类", Options: categoryOptions},
		},
	}
}

func (s *Source) Search(ctx context.Context, account source.Account, query string, page int) (model.SearchResult, error) {
	if page < 1 {
		page = 1
	}
	values := url.Values{
		"searchkey": {strings.TrimSpace(query)},
		"order":     {"0"},
		"page":      {strconv.Itoa(page)},
	}
	html, base, err := s.fetch(ctx, account, "/search/", values)
	if err != nil {
		return model.SearchResult{}, err
	}
	return parseSearch(html, base, page), nil
}

func (s *Source) Browse(ctx context.Context, account source.Account, options model.BrowseOptions, page int) (model.SearchResult, error) {
	if page < 1 {
		page = 1
	}
	category := strings.TrimSpace(options.Category)
	if category == "" {
		category = "1"
	}
	if _, err := strconv.Atoi(category); err != nil {
		category = "1"
	}
	html, base, err := s.fetch(ctx, account, fmt.Sprintf("/list%s/%d/", category, page), nil)
	if err != nil {
		return model.SearchResult{}, err
	}
	return parseList(html, base, page), nil
}

func (s *Source) Detail(ctx context.Context, account source.Account, comicID string) (model.Comic, error) {
	id := normalizeBookID(comicID)
	if id == "" {
		return model.Comic{}, errors.New("无效的笔趣阁书籍 ID")
	}
	html, base, err := s.fetch(ctx, account, "/"+id+"/", nil)
	if err != nil {
		return model.Comic{}, err
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return model.Comic{}, err
	}
	book := model.Comic{
		SourceID:  sourceID,
		ID:        id,
		Title:     source.FirstNonEmpty(meta(document, "og:novel:book_name"), meta(document, "og:title"), document.Find("h1").First().Text()),
		Author:    source.FirstNonEmpty(meta(document, "og:novel:author"), meta(document, "author"), document.Find(".bookdes a[href*='/author/']").First().Text()),
		Cover:     source.Absolutize(base, meta(document, "og:image")),
		Status:    meta(document, "og:novel:status"),
		UpdatedAt: detailUpdatedAt(document),
	}
	if book.Title == "" {
		book.Title = "笔趣阁 " + id
	}
	book.Description = strings.TrimSpace(source.FirstNonEmpty(meta(document, "og:description"), document.Find(".bookintro").Text()))
	if category := strings.TrimSpace(meta(document, "og:novel:category")); category != "" {
		book.Tags = []string{category}
	}
	if book.Cover == "" || strings.Contains(book.Cover, "/public/img/") {
		if cover := documentCover(document, base); cover != "" {
			book.Cover = cover
		}
	}
	if chapters, err := s.Chapters(ctx, account, id); err == nil {
		book.ChapterCount = len(chapters)
	}
	return book, nil
}

func (s *Source) Chapters(ctx context.Context, account source.Account, comicID string) ([]model.Chapter, error) {
	id := normalizeBookID(comicID)
	if id == "" {
		return nil, errors.New("无效的笔趣阁书籍 ID")
	}
	html, base, err := s.fetch(ctx, account, "/"+id+"/", nil)
	if err != nil {
		return nil, err
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	bookParts := strings.Split(id, "/")
	if len(bookParts) != 2 {
		return nil, errors.New("无效的笔趣阁书籍 ID")
	}
	type chapterRow struct {
		id    string
		title string
		url   string
	}
	seen := map[string]bool{}
	rows := make([]chapterRow, 0, 256)
	document.Find("a[href]").Each(func(_ int, selection *goquery.Selection) {
		href, _ := selection.Attr("href")
		matches := chapterPattern.FindStringSubmatch(href)
		if len(matches) < 4 || matches[1] != bookParts[0] || matches[2] != bookParts[1] || seen[matches[3]] {
			return
		}
		title := strings.TrimSpace(source.FirstNonEmpty(selection.AttrOr("title", ""), selection.Text()))
		if title == "" {
			return
		}
		seen[matches[3]] = true
		rows = append(rows, chapterRow{
			id:    matches[3],
			title: title,
			url:   source.Absolutize(base, href),
		})
	})
	sort.SliceStable(rows, func(i, j int) bool {
		left, _ := strconv.ParseInt(rows[i].id, 10, 64)
		right, _ := strconv.ParseInt(rows[j].id, 10, 64)
		if left == right {
			return i < j
		}
		return left < right
	})
	chapters := make([]model.Chapter, 0, len(rows))
	for index, row := range rows {
		chapters = append(chapters, model.Chapter{
			ID:      row.id,
			ComicID: id,
			Title:   row.title,
			Order:   float64(index + 1),
			URL:     row.url,
		})
	}
	return chapters, nil
}

func (s *Source) Pages(context.Context, source.Account, string, model.Chapter) ([]model.Page, error) {
	return nil, errNoPages
}

func (s *Source) ChapterContent(ctx context.Context, account source.Account, bookID string, chapter model.Chapter) (string, error) {
	id := normalizeBookID(bookID)
	chapterID := normalizeChapterID(chapter.ID)
	if id == "" || chapterID == "" {
		return "", errors.New("无效的笔趣阁章节")
	}
	path := fmt.Sprintf("/%s/%s.html", id, chapterID)
	parts := make([]string, 0, 64)
	for page := 0; page < 30; page++ {
		html, _, err := s.fetch(ctx, account, path, nil)
		if err != nil {
			return "", err
		}
		parts = append(parts, parseChapterParagraphs(html)...)
		nextPath := nextChapterPage(html, id, chapterID)
		if nextPath == "" || nextPath == path {
			break
		}
		path = nextPath
	}
	content := strings.TrimSpace(strings.Join(parts, "\n"))
	if content == "" {
		return "", errors.New("未解析到笔趣阁章节正文")
	}
	return content, nil
}

func (s *Source) bases(account source.Account) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(defaultSites)+1)
	for _, candidate := range append([]string{account.HomeURL}, defaultSites...) {
		candidate = canonicalBase(candidate)
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		result = append(result, candidate)
	}
	return result
}

func canonicalBase(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return value
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "biquge345.com", "www.biquge345.com":
		return defaultSite
	}
	return value
}

func (s *Source) fetch(ctx context.Context, account source.Account, path string, values url.Values) (string, string, error) {
	client := source.SourceClient(s.client, &s.directClient, account)
	var failures []string
	for _, base := range s.bases(account) {
		address := source.BuildURL(base, path, values)
		text, err := s.getText(ctx, client, address, base, account.Cookie)
		if err == nil {
			return text, base, nil
		}
		failures = append(failures, fmt.Sprintf("%s: %v", base, err))
	}
	return "", "", fmt.Errorf("all mirror sites failed: %s", strings.Join(failures, "; "))
}

func (s *Source) getText(ctx context.Context, client *http.Client, address, base, cookie string) (string, error) {
	text, err := s.requestText(ctx, client, address, base, cookie)
	if err != nil {
		return "", err
	}
	if !isBrowserCheck(text) {
		return text, nil
	}
	if strings.Contains(cookie, "__sc_clearance=") {
		return "", errors.New("笔趣阁浏览器校验未通过")
	}
	clearedCookie := strings.TrimSpace(strings.Trim(cookie, ";") + "; __sc_clearance=1")
	text, err = s.requestText(ctx, client, address, base, clearedCookie)
	if err != nil {
		return "", err
	}
	if isBrowserCheck(text) {
		return "", errors.New("笔趣阁浏览器校验未通过")
	}
	return text, nil
}

func (s *Source) requestText(ctx context.Context, client *http.Client, address, base, cookie string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", source.DefaultUserAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.7")
	request.Header.Set("Referer", strings.TrimRight(base, "/")+"/")
	if strings.TrimSpace(cookie) != "" {
		request.Header.Set("Cookie", cookie)
	}
	if client == nil {
		client = source.NewHTTPClient()
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 12<<20))
	if err != nil {
		return "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return string(raw), nil
}

func isBrowserCheck(html string) bool {
	return strings.Contains(html, "__sc_clearance") || strings.Contains(html, "正在检查您的浏览器")
}

func parseSearch(html, base string, page int) model.SearchResult {
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return model.SearchResult{Page: page}
	}
	items := make([]model.Comic, 0, 30)
	seen := map[string]bool{}
	document.Find(".ranklist .content dl, .ranklist dl").Each(func(_ int, selection *goquery.Selection) {
		if item, ok := cardComic(selection, base); ok && !seen[item.ID] {
			seen[item.ID] = true
			items = append(items, item)
		}
	})
	hasMore := strings.Contains(html, fmt.Sprintf("page=%d", page+1))
	return model.SearchResult{Items: items, Page: page, Total: len(items), HasMore: hasMore}
}

func parseList(html, base string, page int) model.SearchResult {
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return model.SearchResult{Page: page}
	}
	items := make([]model.Comic, 0, 40)
	seen := map[string]bool{}
	document.Find(".ranklist .content dl, .ranklist dl").Each(func(_ int, selection *goquery.Selection) {
		if item, ok := cardComic(selection, base); ok && !seen[item.ID] {
			seen[item.ID] = true
			items = append(items, item)
		}
	})
	document.Find("ul.update li").Each(func(_ int, selection *goquery.Selection) {
		var link *goquery.Selection
		selection.Find("a[href]").EachWithBreak(func(_ int, candidate *goquery.Selection) bool {
			href, _ := candidate.Attr("href")
			if normalizeBookID(href) == "" {
				return true
			}
			link = candidate
			return false
		})
		if link == nil {
			return
		}
		href, _ := link.Attr("href")
		id := normalizeBookID(href)
		if id == "" || seen[id] {
			return
		}
		title := strings.TrimSpace(source.FirstNonEmpty(link.AttrOr("title", ""), link.Text()))
		if title == "" {
			return
		}
		seen[id] = true
		items = append(items, model.Comic{
			SourceID: sourceID,
			ID:       id,
			Title:    title,
			Author:   strings.TrimSpace(selection.Find("span").Eq(3).Find("a").First().Text()),
		})
	})
	hasMore := document.Find(fmt.Sprintf(`a[href$="/%d/"]`, page+1)).Length() > 0
	return model.SearchResult{Items: items, Page: page, Total: len(items), HasMore: hasMore}
}

func cardComic(container *goquery.Selection, base string) (model.Comic, bool) {
	link := container.Find("a.cover[href]").First()
	if link.Length() == 0 {
		container.Find("a[href]").EachWithBreak(func(_ int, candidate *goquery.Selection) bool {
			href, _ := candidate.Attr("href")
			if normalizeBookID(href) == "" {
				return true
			}
			link = candidate
			return false
		})
	}
	if link.Length() == 0 {
		return model.Comic{}, false
	}
	href, _ := link.Attr("href")
	id := normalizeBookID(href)
	if id == "" {
		return model.Comic{}, false
	}
	title := strings.TrimSpace(source.FirstNonEmpty(
		link.AttrOr("title", ""),
		container.Find("dt a").First().AttrOr("title", ""),
		container.Find("dt a").First().Text(),
		link.Text(),
	))
	if title == "" {
		return model.Comic{}, false
	}
	return model.Comic{
		SourceID: sourceID,
		ID:       id,
		Title:    title,
		Author:   strings.TrimSpace(container.Find("dd").Last().Find("a").First().Text()),
		Cover:    nearbyCover(link, base),
	}, true
}

func parseChapterParagraphs(html string) []string {
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil
	}
	parts := make([]string, 0, 64)
	document.Find("#chaptercontent p").Each(func(_ int, selection *goquery.Selection) {
		if selection.Closest("#morecontent").Length() > 0 {
			return
		}
		text := cleanText(selection.Text())
		if text == "" || isChapterNoise(text) {
			return
		}
		parts = append(parts, text)
	})
	if len(parts) == 0 {
		text := cleanText(document.Find("#chaptercontent").Text())
		if text != "" && !isChapterNoise(text) {
			parts = append(parts, text)
		}
	}
	return parts
}

func nextChapterPage(html, bookID, chapterID string) string {
	matches := nextChapterPagePattern.FindStringSubmatch(html)
	if len(matches) < 2 {
		return ""
	}
	next := strings.TrimSpace(matches[1])
	parsed, err := url.Parse(next)
	if err == nil && parsed.Path != "" {
		next = parsed.Path
	}
	prefix := fmt.Sprintf("/%s/%s_", bookID, chapterID)
	if strings.HasPrefix(next, prefix) && strings.HasSuffix(next, ".html") {
		return next
	}
	return ""
}

func isChapterNoise(text string) bool {
	for _, marker := range []string{
		"看后求收藏",
		"piquge.com",
		"更多内容加载中",
		"本站只支持手机浏览器访问",
		"本章未完，请点击下一章",
		"关闭阅读模式",
		"关闭广告屏蔽功能",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func meta(document *goquery.Document, name string) string {
	value := strings.TrimSpace(document.Find(fmt.Sprintf(`meta[property="%s"]`, name)).First().AttrOr("content", ""))
	if value != "" {
		return value
	}
	return strings.TrimSpace(document.Find(fmt.Sprintf(`meta[name="%s"]`, name)).First().AttrOr("content", ""))
}

func documentCover(document *goquery.Document, base string) string {
	for _, selector := range []string{
		".book .cover img",
		".bookimg img",
		".book-img img",
		"#fmimg img",
		".cover img",
		".pic img",
		".zhutu img",
		"img.cover",
	} {
		if value := imageAddress(document.Find(selector).First(), base); value != "" {
			return value
		}
	}
	return ""
}

func nearbyCover(selection *goquery.Selection, base string) string {
	candidates := []*goquery.Selection{
		selection.Find("img").First(),
		selection.Parent().Find("img").First(),
		selection.Closest("dl, li, .item, .book, .book-item, .bookbox, .hot, .recommend, .grid").Find("img").First(),
	}
	for _, image := range candidates {
		if value := imageAddress(image, base); value != "" {
			return value
		}
	}
	return ""
}

func imageAddress(image *goquery.Selection, base string) string {
	if image == nil || image.Length() == 0 {
		return ""
	}
	for _, attribute := range []string{
		"data-original",
		"data-src",
		"data-lazy-src",
		"data-echo",
		"data-url",
		"src",
	} {
		value := strings.TrimSpace(image.AttrOr(attribute, ""))
		if value == "" || strings.HasPrefix(strings.ToLower(value), "data:") || strings.Contains(value, "/public/img/nocover") {
			continue
		}
		return source.Absolutize(base, value)
	}
	if srcset := strings.TrimSpace(image.AttrOr("srcset", "")); srcset != "" {
		candidate := strings.TrimSpace(strings.Split(srcset, ",")[0])
		if fields := strings.Fields(candidate); len(fields) > 0 {
			return source.Absolutize(base, fields[0])
		}
	}
	return ""
}

func detailUpdatedAt(document *goquery.Document) string {
	return source.NormalizeTimestamp(source.FirstNonEmpty(
		meta(document, "og:novel:update_time"),
		meta(document, "article:modified_time"),
		meta(document, "og:updated_time"),
	))
}

func normalizeBookID(value string) string {
	value = strings.TrimSpace(value)
	if matches := chapterPattern.FindStringSubmatch(value); len(matches) > 2 {
		return matches[1] + "/" + matches[2]
	}
	if matches := bookPattern.FindStringSubmatch(value); len(matches) > 2 {
		return matches[1] + "/" + matches[2]
	}
	value = strings.Trim(value, "/")
	if matches := rawBookPattern.FindStringSubmatch(value); len(matches) > 2 {
		return matches[1] + "/" + matches[2]
	}
	return ""
}

func normalizeChapterID(value string) string {
	value = strings.TrimSpace(value)
	if matches := chapterPattern.FindStringSubmatch(value); len(matches) > 3 {
		return matches[3]
	}
	if _, err := strconv.ParseInt(value, 10, 64); err == nil {
		return value
	}
	return ""
}

func cleanText(raw string) string {
	raw = strings.ReplaceAll(raw, "\u00a0", " ")
	raw = strings.ReplaceAll(raw, "&nbsp;", " ")
	raw = strings.ReplaceAll(raw, "&amp;", "&")
	raw = strings.ReplaceAll(raw, "&lt;", "<")
	raw = strings.ReplaceAll(raw, "&gt;", ">")
	raw = strings.ReplaceAll(raw, "&quot;", "\"")
	raw = markerPattern.ReplaceAllString(raw, "")
	raw = tagPattern.ReplaceAllString(raw, "")
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

var _ source.Source = (*Source)(nil)
var _ source.ContentSource = (*Source)(nil)
