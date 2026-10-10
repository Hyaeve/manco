// Package shencou implements a Chinese light-novel book source backed by the
// 神凑轻小说文库 (jieqi CMS) so Manco can also browse and download books. Each
// chapter is downloaded as its own text file.
package shencou

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
)

const (
	sourceID    = "shencou"
	defaultSite = "https://www.wowenku.com"
)

var defaultSites = []string{
	defaultSite,
	"https://www.shencou.com",
}

var categoryOptions = []model.FilterOption{
	{Value: "", Label: "全部"},
	{Value: "1", Label: "电击文库"},
	{Value: "2", Label: "富士见文库"},
	{Value: "3", Label: "角川文库"},
	{Value: "4", Label: "MF文库J"},
	{Value: "5", Label: "FAMI通文库"},
	{Value: "6", Label: "GA文库"},
	{Value: "7", Label: "HJ文库"},
	{Value: "8", Label: "一迅社文库"},
	{Value: "9", Label: "集英社文库"},
	{Value: "12", Label: "讲谈社文库"},
}

var (
	bookPattern     = regexp.MustCompile(`/books/read_(\d+)\.html`)
	chapterPattern  = regexp.MustCompile(`(\d+)\.html`)
	progressPattern = regexp.MustCompile(`写作进度：\s*([^<\s]+)`)
	introPattern    = regexp.MustCompile(`内容简介：([\s\S]*?)(?:本书公告：|</td>|</div>)`)
	goMarker        = "<!--go-->"
	overMarker      = "<!--over-->"
	tagPattern      = regexp.MustCompile(`<[^>]+>`)
)

var errNoPages = errors.New("书籍源不支持图片下载")

type Source struct {
	client *http.Client
}

func New(client *http.Client) *Source { return &Source{client: client} }

func (s *Source) Info() model.SourceInfo {
	return model.SourceInfo{
		ID:          sourceID,
		Name:        "神凑轻小说",
		Kind:        source.KindBook,
		Description: "轻小说文库，按章节下载为文本",
		Homepage:    defaultSite + "/",
		NeedsLogin:  false,
		CanSearch:   true,
		CanBrowse:   true,
		Icon:        "/source-icons/shencou.png",
		Sites:       append([]string(nil), defaultSites...),
		Filters: []model.FilterGroup{
			{Key: "category", Label: "文库", Options: categoryOptions},
		},
	}
}

func (s *Source) Search(ctx context.Context, account source.Account, query string, page int) (model.SearchResult, error) {
	if page < 1 {
		page = 1
	}
	values := url.Values{
		"searchkey": {query},
		"page":      {strconv.Itoa(page)},
	}
	html, base, err := s.get(ctx, account, "/modules/article/search.php", values)
	if err != nil {
		return model.SearchResult{}, err
	}
	return parseList(html, base, page), nil
}

func (s *Source) Browse(ctx context.Context, account source.Account, options model.BrowseOptions, page int) (model.SearchResult, error) {
	if page < 1 {
		page = 1
	}
	values := url.Values{"page": {strconv.Itoa(page)}}
	if category := strings.TrimSpace(options.Category); category != "" {
		values.Set("sortid", category)
	}
	html, base, err := s.get(ctx, account, "/modules/article/articlelist.php", values)
	if err != nil {
		return model.SearchResult{}, err
	}
	return parseList(html, base, page), nil
}

func (s *Source) Detail(ctx context.Context, account source.Account, comicID string) (model.Comic, error) {
	id := normalizeID(comicID)
	if id == "" {
		return model.Comic{}, errors.New("无效的书籍 ID")
	}
	html, base, err := s.get(ctx, account, "/books/read_"+id+".html", nil)
	if err != nil {
		return model.Comic{}, err
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return model.Comic{}, err
	}
	comic := model.Comic{
		SourceID: sourceID,
		ID:       id,
		Title:    strings.TrimSpace(document.Find("h1").First().Text()),
		Cover:    coverURL(base, id),
		Status:   strings.TrimSpace(firstMatch(progressPattern, html)),
	}
	if titleParts := splitDocumentTitle(document); len(titleParts) > 0 {
		if comic.Title == "" {
			comic.Title = titleParts[0]
		}
		if len(titleParts) > 1 {
			comic.Author = titleParts[1]
		}
	}
	if comic.Title == "" {
		comic.Title = "神凑 " + id
	}
	if intro := firstMatch(introPattern, html); intro != "" {
		comic.Description = cleanText(intro)
	}
	comic.Tags = bookTags(document)
	if chapters, err := s.Chapters(ctx, account, id); err == nil {
		comic.ChapterCount = len(chapters)
	}
	return comic, nil
}

func (s *Source) Chapters(ctx context.Context, account source.Account, comicID string) ([]model.Chapter, error) {
	id := normalizeID(comicID)
	if id == "" {
		return nil, errors.New("无效的书籍 ID")
	}
	html, base, err := s.get(ctx, account, "/read/0/"+id+"/index.html", nil)
	if err != nil {
		return nil, err
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	chapters := make([]model.Chapter, 0, 128)
	seen := map[string]bool{}
	document.Find(".zjlist4 a, .zjlist a, .chapterlist a, td.ccss a, .zjbox a[href]").Each(func(_ int, selection *goquery.Selection) {
		href, ok := selection.Attr("href")
		if !ok {
			return
		}
		matches := chapterPattern.FindStringSubmatch(href)
		if len(matches) < 2 || seen[matches[1]] {
			return
		}
		title := source.FirstNonEmpty(strings.TrimSpace(selection.Text()), matches[1])
		seen[matches[1]] = true
		chapters = append(chapters, model.Chapter{
			ID:      matches[1],
			ComicID: id,
			Title:   title,
			Order:   float64(len(chapters) + 1),
			URL:     source.Absolutize(base, "/read/0/"+id+"/"+matches[1]+".html"),
		})
	})
	return chapters, nil
}

func (s *Source) Pages(ctx context.Context, account source.Account, comicID string, chapter model.Chapter) ([]model.Page, error) {
	return nil, errNoPages
}

func (s *Source) ChapterContent(ctx context.Context, account source.Account, bookID string, chapter model.Chapter) (string, error) {
	path := "/read/0/" + normalizeID(bookID) + "/" + normalizeID(source.FirstNonEmpty(chapter.ID, chapter.URL)) + ".html"
	html, _, err := s.get(ctx, account, path, nil)
	if err != nil {
		return "", err
	}
	text := between(html, goMarker, overMarker)
	if strings.TrimSpace(text) == "" {
		return "", errors.New("未解析到章节正文")
	}
	return cleanText(text), nil
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

func (s *Source) get(ctx context.Context, account source.Account, path string, values url.Values) (string, string, error) {
	return source.FetchTextFallback(ctx, s.client, s.bases(account), path, values, func(base string) map[string]string {
		headers := map[string]string{
			"Referer":         base + "/",
			"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.6",
		}
		for key, value := range source.HeaderCookie(account.Cookie) {
			headers[key] = value
		}
		return headers
	})
}

func parseList(html, base string, page int) model.SearchResult {
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return model.SearchResult{Page: page}
	}
	items := make([]model.Comic, 0, 30)
	seen := map[string]bool{}
	document.Find("a[href*='/books/read_']").Each(func(_ int, selection *goquery.Selection) {
		href, ok := selection.Attr("href")
		if !ok {
			return
		}
		matches := bookPattern.FindStringSubmatch(href)
		if len(matches) < 2 || seen[matches[1]] {
			return
		}
		title := source.FirstNonEmpty(strings.TrimSpace(selection.AttrOr("title", "")), strings.TrimSpace(selection.Text()))
		if title == "" {
			return
		}
		seen[matches[1]] = true
		items = append(items, model.Comic{
			SourceID: sourceID,
			ID:       matches[1],
			Title:    title,
			Cover:    coverURL(base, matches[1]),
		})
	})
	hasMore := document.Find("a[href*='page="+strconv.Itoa(page+1)+"']").Length() > 0
	return model.SearchResult{Items: items, Page: page, Total: len(items), HasMore: hasMore}
}

func coverURL(base, id string) string {
	number, err := strconv.Atoi(id)
	if err != nil {
		return ""
	}
	return source.BuildURL(base, fmt.Sprintf("/files/article/image/%d/%s/%sl.jpg", number/1000, id, id), nil)
}

func splitDocumentTitle(document *goquery.Document) []string {
	raw := strings.TrimSpace(document.Find("title").First().Text())
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "-")
	result := make([]string, 0, 2)
	for _, part := range parts {
		part = strings.TrimSpace(strings.TrimSuffix(part, "_神凑小说网"))
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func bookTags(document *goquery.Document) []string {
	values := make([]string, 0, 6)
	document.Find("a[href*='/modules/article/toplist.php'], a[href*='sortid=']").Each(func(_ int, selection *goquery.Selection) {
		value := strings.TrimSpace(selection.Text())
		if value != "" && len(values) < 6 {
			values = append(values, value)
		}
	})
	return values
}

func normalizeID(value string) string {
	value = strings.TrimSpace(value)
	if matches := chapterPattern.FindStringSubmatch(value); len(matches) >= 2 {
		return matches[1]
	}
	if matches := bookPattern.FindStringSubmatch(value); len(matches) >= 2 {
		return matches[1]
	}
	if _, err := strconv.Atoi(value); err != nil {
		return ""
	}
	return value
}

func between(value, start, end string) string {
	index := strings.Index(value, start)
	if index < 0 {
		return ""
	}
	value = value[index+len(start):]
	if end != "" {
		if stop := strings.Index(value, end); stop >= 0 {
			value = value[:stop]
		}
	}
	return value
}

func firstMatch(pattern *regexp.Regexp, text string) string {
	matches := pattern.FindStringSubmatch(text)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func cleanText(raw string) string {
	raw = strings.ReplaceAll(raw, "<br />", "\n")
	raw = strings.ReplaceAll(raw, "<br/>", "\n")
	raw = strings.ReplaceAll(raw, "<br>", "\n")
	raw = strings.ReplaceAll(raw, "</p>", "\n")
	raw = tagPattern.ReplaceAllString(raw, "")
	raw = strings.ReplaceAll(raw, "&nbsp;", " ")
	raw = strings.ReplaceAll(raw, "&amp;", "&")
	raw = strings.ReplaceAll(raw, "&lt;", "<")
	raw = strings.ReplaceAll(raw, "&gt;", ">")
	raw = strings.ReplaceAll(raw, "&quot;", "\"")
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

var _ source.Source = (*Source)(nil)
var _ source.ContentSource = (*Source)(nil)
