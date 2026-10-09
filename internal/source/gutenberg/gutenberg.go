// Package gutenberg implements a book source backed by the public Project
// Gutenberg catalogue (gutendex.com) so Manco can also fetch books alongside
// comics. Each book is exposed as a single "chapter" whose download is one
// text file.
package gutenberg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
)

const (
	sourceID     = "gutenberg"
	apiBase      = "https://gutendex.com"
	defaultSite  = "https://www.gutenberg.org"
	catalogueURL = apiBase + "/books"
)

var languageOptions = []model.FilterOption{
	{Value: "", Label: "全部"},
	{Value: "en", Label: "英文"},
	{Value: "zh", Label: "中文"},
	{Value: "fr", Label: "法语"},
	{Value: "de", Label: "德语"},
	{Value: "es", Label: "西班牙语"},
}

var sortOptions = []model.FilterOption{
	{Value: "popular", Label: "最热门"},
	{Value: "descending", Label: "最新"},
	{Value: "ascending", Label: "最早"},
}

var errNoPages = errors.New("书籍源不支持图片下载")

type Source struct {
	client *http.Client
}

func New(client *http.Client) *Source { return &Source{client: client} }

func (s *Source) Info() model.SourceInfo {
	return model.SourceInfo{
		ID:          sourceID,
		Name:        "Project Gutenberg",
		Kind:        source.KindBook,
		Description: "公版书籍，按作品整本下载为文本",
		Homepage:    defaultSite + "/",
		NeedsLogin:  false,
		CanSearch:   true,
		CanBrowse:   true,
		Icon:        "https://www.google.com/s2/favicons?domain=gutenberg.org&sz=64",
		Sites:       []string{apiBase, defaultSite},
		Filters: []model.FilterGroup{
			{Key: "category", Label: "语言", Options: languageOptions},
			{Key: "sort", Label: "排序", Default: "popular", Options: sortOptions},
		},
	}
}

type book struct {
	ID            int               `json:"id"`
	Title         string            `json:"title"`
	Authors       []author          `json:"authors"`
	Summaries     []string          `json:"summaries"`
	Subjects      []string          `json:"subjects"`
	Bookshelves   []string          `json:"bookshelves"`
	Languages     []string          `json:"languages"`
	DownloadCount int               `json:"download_count"`
	Formats       map[string]string `json:"formats"`
}

type author struct {
	Name string `json:"name"`
}

type listing struct {
	Count   int    `json:"count"`
	Next    string `json:"next"`
	Results []book `json:"results"`
}

func (s *Source) Search(ctx context.Context, account source.Account, query string, page int) (model.SearchResult, error) {
	if page < 1 {
		page = 1
	}
	values := url.Values{
		"search": {query},
		"page":   {strconv.Itoa(page)},
	}
	return s.fetchListing(ctx, values, page)
}

func (s *Source) Browse(ctx context.Context, account source.Account, options model.BrowseOptions, page int) (model.SearchResult, error) {
	if page < 1 {
		page = 1
	}
	values := url.Values{"page": {strconv.Itoa(page)}}
	if language := strings.TrimSpace(options.Category); language != "" {
		values.Set("languages", language)
	}
	if sort := strings.TrimSpace(options.Sort); sort != "" {
		values.Set("sort", sort)
	}
	return s.fetchListing(ctx, values, page)
}

func (s *Source) fetchListing(ctx context.Context, values url.Values, page int) (model.SearchResult, error) {
	address := catalogueURL + "?" + values.Encode()
	raw, err := source.FetchText(ctx, s.client, address, map[string]string{"Accept": "application/json"})
	if err != nil {
		return model.SearchResult{}, err
	}
	var payload listing
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return model.SearchResult{}, fmt.Errorf("解析 Gutenberg 列表失败: %w", err)
	}
	items := make([]model.Comic, 0, len(payload.Results))
	for _, entry := range payload.Results {
		items = append(items, comicFromBook(entry, false))
	}
	return model.SearchResult{
		Items:   items,
		Page:    page,
		Total:   payload.Count,
		HasMore: strings.TrimSpace(payload.Next) != "",
	}, nil
}

func (s *Source) Detail(ctx context.Context, account source.Account, comicID string) (model.Comic, error) {
	entry, err := s.book(ctx, comicID)
	if err != nil {
		return model.Comic{}, err
	}
	return comicFromBook(entry, true), nil
}

// Chapters exposes the whole book as a single downloadable chapter, matching
// the "one file per chapter" convention used across Manco.
func (s *Source) Chapters(ctx context.Context, account source.Account, comicID string) ([]model.Chapter, error) {
	entry, err := s.book(ctx, comicID)
	if err != nil {
		return nil, err
	}
	return []model.Chapter{{
		ID:      strconv.Itoa(entry.ID),
		ComicID: strconv.Itoa(entry.ID),
		Title:   entry.Title,
		Order:   1,
	}}, nil
}

// Pages is unused for book sources; downloads go through ChapterContent.
func (s *Source) Pages(ctx context.Context, account source.Account, comicID string, chapter model.Chapter) ([]model.Page, error) {
	return nil, errNoPages
}

func (s *Source) ChapterContent(ctx context.Context, account source.Account, bookID string, chapter model.Chapter) (string, error) {
	entry, err := s.book(ctx, bookID)
	if err != nil {
		return "", err
	}
	address := pickTextFormat(entry.Formats)
	if address == "" {
		return "", fmt.Errorf("《%s》没有可下载的纯文本格式", entry.Title)
	}
	return source.FetchText(ctx, s.client, address, nil)
}

func (s *Source) book(ctx context.Context, comicID string) (book, error) {
	id := strings.TrimSpace(comicID)
	if id == "" {
		return book{}, fmt.Errorf("无效的 Gutenberg 书籍 ID")
	}
	raw, err := source.FetchText(ctx, s.client, catalogueURL+"/"+url.PathEscape(id), map[string]string{"Accept": "application/json"})
	if err != nil {
		return book{}, err
	}
	var entry book
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		return book{}, fmt.Errorf("解析 Gutenberg 书籍失败: %w", err)
	}
	if entry.ID == 0 {
		return book{}, fmt.Errorf("Gutenberg 未返回该书籍")
	}
	return entry, nil
}

func comicFromBook(entry book, detailed bool) model.Comic {
	comic := model.Comic{
		SourceID:     sourceID,
		ID:           strconv.Itoa(entry.ID),
		Title:        strings.TrimSpace(entry.Title),
		Author:       authorNames(entry.Authors),
		Cover:        pickImageFormat(entry.Formats),
		Status:       "已完结",
		ChapterCount: 1,
	}
	if comic.Title == "" {
		comic.Title = "Gutenberg " + comic.ID
	}
	if detailed {
		comic.Description = strings.Join(entry.Summaries, "\n\n")
		comic.Tags = append(limitTags(entry.Subjects), limitTags(entry.Bookshelves)...)
	}
	return comic
}

func authorNames(authors []author) string {
	names := make([]string, 0, len(authors))
	for _, item := range authors {
		if name := strings.TrimSpace(item.Name); name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, " / ")
}

func limitTags(values []string) []string {
	result := make([]string, 0, 6)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		result = append(result, value)
		if len(result) >= 6 {
			break
		}
	}
	return result
}

func pickImageFormat(formats map[string]string) string {
	for _, key := range []string{"image/jpeg", "image/png"} {
		if value := strings.TrimSpace(formats[key]); value != "" {
			return value
		}
	}
	return ""
}

func pickTextFormat(formats map[string]string) string {
	for _, key := range []string{
		"text/plain; charset=utf-8",
		"text/plain; charset=us-ascii",
		"text/plain",
	} {
		if value := strings.TrimSpace(formats[key]); value != "" {
			return value
		}
	}
	for key, value := range formats {
		if strings.HasPrefix(strings.ToLower(key), "text/plain") && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var _ source.Source = (*Source)(nil)
var _ source.ContentSource = (*Source)(nil)
