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
	defaultSite = "https://www.biquge345.com"
)

var defaultSites = []string{
	defaultSite,
	"https://biquge345.com",
}

var categoryOptions = []model.FilterOption{
	{Value: "", Label: "全部"},
	{Value: "1", Label: "玄幻魔法"},
	{Value: "2", Label: "仙侠修真"},
	{Value: "3", Label: "都市言情"},
	{Value: "4", Label: "网游动漫"},
	{Value: "5", Label: "科幻小说"},
	{Value: "6", Label: "恐怖灵异"},
	{Value: "7", Label: "历史军事"},
	{Value: "8", Label: "其他小说"},
}

var (
	bookPattern    = regexp.MustCompile(`/book/(\d+)/?`)
	chapterPattern = regexp.MustCompile(`/chapter/(\d+)/(\d+)\.html`)
	markerPattern  = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>`)
	tagPattern     = regexp.MustCompile(`(?is)<[^>]+>`)
)

var errNoPages = errors.New("书籍源不支持图片下载")

type Source struct {
	client *http.Client
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
		"s":      {strings.TrimSpace(query)},
		"type":   {"articlename"},
		"submit": {""},
	}
	var lastErr error
	for _, base := range s.bases(account) {
		html, err := s.postForm(ctx, base+"/s.php", values, base)
		if err != nil {
			lastErr = err
			continue
		}
		return parseSearch(html, base, page), nil
	}
	if lastErr == nil {
		lastErr = errors.New("笔趣阁没有可用的网站")
	}
	return model.SearchResult{}, lastErr
}

func (s *Source) Browse(ctx context.Context, account source.Account, options model.BrowseOptions, page int) (model.SearchResult, error) {
	if page < 1 {
		page = 1
	}
	category := strings.TrimSpace(options.Category)
	if category == "" {
		category = "1"
	}
	path := fmt.Sprintf("/sort/%s_%d/", category, page)
	html, base, err := source.FetchTextFallback(ctx, s.client, s.bases(account), path, nil, func(base string) map[string]string {
		return map[string]string{"Referer": base + "/"}
	})
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
	html, base, err := s.get(ctx, account, "/book/"+id+"/")
	if err != nil {
		return model.Comic{}, err
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return model.Comic{}, err
	}
	book := model.Comic{
		SourceID: sourceID,
		ID:       id,
		Title:    source.FirstNonEmpty(meta(document, "og:novel:book_name"), meta(document, "og:title"), document.Find("h1").First().Text()),
		Author:   source.FirstNonEmpty(meta(document, "og:novel:author"), meta(document, "author")),
		Cover:    source.Absolutize(base, meta(document, "og:image")),
		Status:   meta(document, "og:novel:status"),
	}
	if book.Title == "" {
		book.Title = "笔趣阁 " + id
	}
	book.Description = strings.TrimSpace(meta(document, "og:description"))
	if category := strings.TrimSpace(meta(document, "og:novel:category")); category != "" {
		book.Tags = []string{category}
	}
	if chapters, err := s.Chapters(ctx, account, id); err == nil {
		book.ChapterCount = len(chapters)
	}
	if book.Cover == "" {
		book.Cover = source.Absolutize(base, document.Find(".zhutu img").First().AttrOr("src", ""))
	}
	return book, nil
}

func (s *Source) Chapters(ctx context.Context, account source.Account, comicID string) ([]model.Chapter, error) {
	id := normalizeBookID(comicID)
	if id == "" {
		return nil, errors.New("无效的笔趣阁书籍 ID")
	}
	html, base, err := s.get(ctx, account, "/book/"+id+"/")
	if err != nil {
		return nil, err
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	type chapterRow struct {
		id    string
		title string
		url   string
	}
	seen := map[string]bool{}
	rows := make([]chapterRow, 0, 256)
	document.Find("a[href*='/chapter/']").Each(func(_ int, selection *goquery.Selection) {
		href, _ := selection.Attr("href")
		matches := chapterPattern.FindStringSubmatch(href)
		if len(matches) < 3 || matches[1] != id || seen[matches[2]] {
			return
		}
		title := strings.TrimSpace(selection.Text())
		if title == "" {
			return
		}
		seen[matches[2]] = true
		rows = append(rows, chapterRow{
			id:    matches[2],
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
	html, _, err := s.get(ctx, account, "/chapter/"+id+"/"+chapterID+".html")
	if err != nil {
		return "", err
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return "", err
	}
	var parts []string
	document.Find("#txt p, #txt br").Each(func(_ int, selection *goquery.Selection) {
		if goquery.NodeName(selection) == "br" {
			return
		}
		if text := cleanText(selection.Text()); text != "" {
			parts = append(parts, text)
		}
	})
	if len(parts) == 0 {
		parts = append(parts, cleanText(document.Find("#txt").Text()))
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
		candidate = strings.TrimRight(strings.TrimSpace(candidate), "/")
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		result = append(result, candidate)
	}
	return result
}

func (s *Source) get(ctx context.Context, account source.Account, path string) (string, string, error) {
	return source.FetchTextFallback(ctx, s.client, s.bases(account), path, nil, func(base string) map[string]string {
		headers := map[string]string{"Referer": base + "/"}
		for key, value := range source.HeaderCookie(account.Cookie) {
			headers[key] = value
		}
		return headers
	})
}

func (s *Source) postForm(ctx context.Context, address string, values url.Values, referer string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, strings.NewReader(values.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", source.DefaultUserAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.7")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Referer", strings.TrimRight(referer, "/")+"/")
	response, err := s.client.Do(request)
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

func parseSearch(html, base string, page int) model.SearchResult {
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return model.SearchResult{Page: page}
	}
	items := make([]model.Comic, 0, 30)
	seen := map[string]bool{}
	document.Find("ul.search li .name a[href*='/book/']").Each(func(_ int, selection *goquery.Selection) {
		href, _ := selection.Attr("href")
		id := normalizeBookID(href)
		if id == "" || seen[id] {
			return
		}
		title := strings.TrimSpace(selection.Text())
		if title == "" {
			return
		}
		seen[id] = true
		items = append(items, model.Comic{
			SourceID: sourceID,
			ID:       id,
			Title:    title,
			Author:   strings.TrimSpace(selection.Parent().Find(".zuo").Text()),
			Cover:    "",
		})
	})
	return model.SearchResult{Items: items, Page: page, Total: len(items), HasMore: len(items) >= 30}
}

func parseList(html, base string, page int) model.SearchResult {
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return model.SearchResult{Page: page}
	}
	items := make([]model.Comic, 0, 40)
	seen := map[string]bool{}
	document.Find("a[href*='/book/']").Each(func(_ int, selection *goquery.Selection) {
		href, _ := selection.Attr("href")
		id := normalizeBookID(href)
		if id == "" || seen[id] {
			return
		}
		title := strings.TrimSpace(selection.Text())
		if title == "" || !looksLikeBookTitle(title) {
			return
		}
		seen[id] = true
		items = append(items, model.Comic{
			SourceID: sourceID,
			ID:       id,
			Title:    title,
			Cover:    "",
		})
	})
	hasMore := document.Find(fmt.Sprintf("a[href*='_%d/']", page+1)).Length() > 0
	return model.SearchResult{Items: items, Page: page, Total: len(items), HasMore: hasMore}
}

func looksLikeBookTitle(title string) bool {
	title = strings.TrimSpace(title)
	return title != "" && title != "首页" && title != "上一页" && title != "下一页" && !strings.Contains(title, "更多")
}

func meta(document *goquery.Document, name string) string {
	value := strings.TrimSpace(document.Find(fmt.Sprintf(`meta[property="%s"]`, name)).First().AttrOr("content", ""))
	if value != "" {
		return value
	}
	return strings.TrimSpace(document.Find(fmt.Sprintf(`meta[name="%s"]`, name)).First().AttrOr("content", ""))
}

func normalizeBookID(value string) string {
	value = strings.TrimSpace(value)
	if matches := bookPattern.FindStringSubmatch(value); len(matches) > 1 {
		return matches[1]
	}
	if matches := chapterPattern.FindStringSubmatch(value); len(matches) > 1 {
		return matches[1]
	}
	if _, err := strconv.ParseInt(value, 10, 64); err == nil {
		return value
	}
	return ""
}

func normalizeChapterID(value string) string {
	value = strings.TrimSpace(value)
	if matches := chapterPattern.FindStringSubmatch(value); len(matches) > 2 {
		return matches[2]
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
