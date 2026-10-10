// Package generic implements a configurable HTML source. It gives the resource
// library a safe way to add comic and book sites without loading executable
// parser bundles from third parties.
package generic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
)

type Config struct {
	SearchURL            string   `json:"searchUrl,omitempty"`
	BrowseURL            string   `json:"browseUrl,omitempty"`
	ItemSelector         string   `json:"itemSelector,omitempty"`
	TitleSelector        string   `json:"titleSelector,omitempty"`
	CoverSelector        string   `json:"coverSelector,omitempty"`
	LinkSelector         string   `json:"linkSelector,omitempty"`
	DetailTitleSelector  string   `json:"detailTitleSelector,omitempty"`
	AuthorSelector       string   `json:"authorSelector,omitempty"`
	DescriptionSelector  string   `json:"descriptionSelector,omitempty"`
	ChapterSelector      string   `json:"chapterSelector,omitempty"`
	ChapterTitleSelector string   `json:"chapterTitleSelector,omitempty"`
	ChapterLinkSelector  string   `json:"chapterLinkSelector,omitempty"`
	PageImageSelector    string   `json:"pageImageSelector,omitempty"`
	ContentSelector      string   `json:"contentSelector,omitempty"`
	AllowedHosts         []string `json:"allowedHosts,omitempty"`
}

type Source struct {
	client  client
	item    model.CustomSource
	config  Config
	homeURL string
}

type client interface {
	Do(*http.Request) (*http.Response, error)
}

var sourceIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,62}$`)

// New validates a persisted custom source and turns it into a source adapter.
func New(httpClient client, item model.CustomSource) (*Source, error) {
	id := strings.ToLower(strings.TrimSpace(item.ID))
	if !sourceIDPattern.MatchString(id) {
		return nil, fmt.Errorf("自定义源 ID 只能包含小写字母、数字、下划线和短横线")
	}
	homepage := strings.TrimSpace(item.Homepage)
	parsed, err := url.Parse(homepage)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("自定义源首页地址无效")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("自定义源只支持 http 或 https")
	}
	var config Config
	if len(item.Config) > 0 {
		if err := json.Unmarshal(item.Config, &config); err != nil {
			return nil, fmt.Errorf("解析自定义源配置: %w", err)
		}
	}
	if strings.TrimSpace(config.ItemSelector) == "" && strings.TrimSpace(config.SearchURL) == "" {
		return nil, fmt.Errorf("至少需要配置列表选择器或搜索地址")
	}
	return &Source{client: httpClient, item: item, config: config, homeURL: strings.TrimRight(homepage, "/")}, nil
}

func (s *Source) Info() model.SourceInfo {
	return model.SourceInfo{
		ID:          strings.ToLower(strings.TrimSpace(s.item.ID)),
		Name:        strings.TrimSpace(s.item.Name),
		Kind:        source.KindOf(model.SourceInfo{Kind: s.item.Kind}),
		Description: strings.TrimSpace(s.item.Description),
		Homepage:    s.homeURL,
		CanSearch:   strings.TrimSpace(s.config.SearchURL) != "",
		CanBrowse:   strings.TrimSpace(s.config.ItemSelector) != "",
		Icon:        strings.TrimSpace(s.item.Icon),
		Sites:       []string{s.homeURL},
		Editable:    true,
	}
}

func (s *Source) Search(ctx context.Context, account source.Account, query string, page int) (model.SearchResult, error) {
	if strings.TrimSpace(s.config.SearchURL) == "" {
		return model.SearchResult{}, fmt.Errorf("该资源未配置搜索地址")
	}
	if page < 1 {
		page = 1
	}
	address := templateURL(s.config.SearchURL, map[string]string{
		"query": url.QueryEscape(strings.TrimSpace(query)),
		"page":  strconv.Itoa(page),
		"base":  s.homeURL,
	})
	return s.list(ctx, account, address, page)
}

func (s *Source) Browse(ctx context.Context, account source.Account, _ model.BrowseOptions, page int) (model.SearchResult, error) {
	if strings.TrimSpace(s.config.BrowseURL) == "" {
		return model.SearchResult{}, fmt.Errorf("该资源未配置浏览地址")
	}
	if page < 1 {
		page = 1
	}
	address := templateURL(s.config.BrowseURL, map[string]string{"page": strconv.Itoa(page), "base": s.homeURL})
	return s.list(ctx, account, address, page)
}

func (s *Source) list(ctx context.Context, account source.Account, address string, page int) (model.SearchResult, error) {
	document, err := s.document(ctx, account, address)
	if err != nil {
		return model.SearchResult{}, err
	}
	selector := source.FirstNonEmpty(s.config.ItemSelector, "article, .comic-item, .book-item, li")
	items := make([]model.Comic, 0)
	document.Find(selector).Each(func(_ int, selection *goquery.Selection) {
		link := selection
		if strings.TrimSpace(s.config.LinkSelector) != "" {
			link = selection.Find(s.config.LinkSelector).First()
		}
		if link.Length() == 0 {
			link = selection.Find("a[href]").First()
		}
		href, _ := link.Attr("href")
		href = source.Absolutize(address, href)
		if href == "" {
			return
		}
		title := textFrom(selection, s.config.TitleSelector)
		if title == "" {
			title = strings.TrimSpace(link.Text())
		}
		if title == "" {
			return
		}
		cover := attributeFrom(selection, s.config.CoverSelector, "src", "data-src", "data-original", "data-lazy-src")
		cover = source.Absolutize(address, cover)
		items = append(items, model.Comic{
			SourceID: s.Info().ID,
			ID:       href,
			Title:    title,
			Cover:    cover,
		})
	})
	return model.SearchResult{Items: items, Page: page, Pages: page, HasMore: len(items) > 0}, nil
}

func (s *Source) Detail(ctx context.Context, account source.Account, comicID string) (model.Comic, error) {
	address := source.Absolutize(s.homeURL, comicID)
	document, err := s.document(ctx, account, address)
	if err != nil {
		return model.Comic{}, err
	}
	title := textFrom(document.Selection, s.config.DetailTitleSelector)
	if title == "" {
		title = document.Find("h1").First().Text()
	}
	if title == "" {
		title = meta(document, "og:title")
	}
	cover := attributeFrom(document.Selection, s.config.CoverSelector, "src", "data-src", "data-original")
	if cover == "" {
		cover = meta(document, "og:image")
	}
	return model.Comic{
		SourceID:    s.Info().ID,
		ID:          address,
		Title:       strings.TrimSpace(title),
		Cover:       source.Absolutize(address, cover),
		Author:      textFrom(document.Selection, s.config.AuthorSelector),
		Description: textFrom(document.Selection, s.config.DescriptionSelector),
	}, nil
}

func (s *Source) Chapters(ctx context.Context, account source.Account, comicID string) ([]model.Chapter, error) {
	address := source.Absolutize(s.homeURL, comicID)
	document, err := s.document(ctx, account, address)
	if err != nil {
		return nil, err
	}
	selector := source.FirstNonEmpty(s.config.ChapterSelector, "a[href]")
	chapters := make([]model.Chapter, 0)
	document.Find(selector).Each(func(index int, selection *goquery.Selection) {
		link := selection
		if strings.TrimSpace(s.config.ChapterLinkSelector) != "" {
			link = selection.Find(s.config.ChapterLinkSelector).First()
		}
		href, _ := link.Attr("href")
		href = source.Absolutize(address, href)
		if href == "" {
			return
		}
		title := textFrom(selection, s.config.ChapterTitleSelector)
		if title == "" {
			title = strings.TrimSpace(link.Text())
		}
		chapters = append(chapters, model.Chapter{
			ID:      href,
			ComicID: address,
			Title:   source.FirstNonEmpty(title, fmt.Sprintf("第%d话", index+1)),
			Order:   float64(index + 1),
			URL:     href,
		})
	})
	return chapters, nil
}

func (s *Source) Pages(ctx context.Context, account source.Account, _ string, chapter model.Chapter) ([]model.Page, error) {
	address := source.FirstNonEmpty(chapter.URL, chapter.ID)
	address = source.Absolutize(s.homeURL, address)
	document, err := s.document(ctx, account, address)
	if err != nil {
		return nil, err
	}
	selector := source.FirstNonEmpty(s.config.PageImageSelector, "img")
	pages := make([]model.Page, 0)
	document.Find(selector).Each(func(_ int, selection *goquery.Selection) {
		image := attributeFrom(selection, "", "src", "data-src", "data-original", "data-lazy-src")
		image = source.Absolutize(address, image)
		if image == "" {
			return
		}
		pages = append(pages, model.Page{URL: image, Referer: address})
	})
	return pages, nil
}

func (s *Source) ChapterContent(ctx context.Context, account source.Account, _ string, chapter model.Chapter) (string, error) {
	address := source.FirstNonEmpty(chapter.URL, chapter.ID)
	address = source.Absolutize(s.homeURL, address)
	document, err := s.document(ctx, account, address)
	if err != nil {
		return "", err
	}
	selector := source.FirstNonEmpty(s.config.ContentSelector, "article, main, .content, #content, body")
	text := strings.Join(strings.Fields(document.Find(selector).First().Text()), " ")
	if text == "" {
		return "", fmt.Errorf("未解析到正文内容")
	}
	return text, nil
}

func (s *Source) document(ctx context.Context, account source.Account, address string) (*goquery.Document, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", source.DefaultUserAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	if account.Cookie != "" {
		request.Header.Set("Cookie", account.Cookie)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return goquery.NewDocumentFromReader(io.LimitReader(response.Body, 12<<20))
}

func textFrom(root *goquery.Selection, selector string) string {
	if root == nil || strings.TrimSpace(selector) == "" {
		return ""
	}
	return strings.TrimSpace(root.Find(selector).First().Text())
}

func attributeFrom(root *goquery.Selection, selector string, names ...string) string {
	if root == nil {
		return ""
	}
	target := root
	if strings.TrimSpace(selector) != "" {
		target = root.Find(selector).First()
	}
	if target.Length() == 0 {
		return ""
	}
	for _, name := range names {
		if value, ok := target.Attr(name); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func meta(document *goquery.Document, name string) string {
	for _, selector := range []string{`meta[property="` + name + `"]`, `meta[name="` + name + `"]`} {
		if value, ok := document.Find(selector).First().Attr("content"); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func templateURL(template string, values map[string]string) string {
	result := strings.TrimSpace(template)
	for key, value := range values {
		result = strings.ReplaceAll(result, "{"+key+"}", value)
	}
	return result
}
