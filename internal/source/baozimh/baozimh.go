package baozimh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type Source struct {
	client *http.Client
}

func New(client *http.Client) *Source { return &Source{client: client} }

const (
	sourceID    = "baozimh"
	defaultSite = "https://www.baozimh.com"
	perPage     = 30
)

// defaultSites lists the public baozimh mirrors used as fallbacks when a
// configured site is unreachable. The order is the preferred rotation order.
var defaultSites = []string{
	defaultSite,
	"https://cn.baozimh.com",
	"https://www.baozimhcn.com",
	"https://www.webmota.com",
	"https://www.kukuc.co",
	"https://www.twmanga.com",
	"https://www.dinnerku.com",
	"https://www.bzmgcn.com",
}

var baoziCategoryOptions = []model.FilterOption{
	{Value: "", Label: "全部"},
	{Value: "lianai", Label: "恋爱"},
	{Value: "chunai", Label: "纯爱"},
	{Value: "gufeng", Label: "古风"},
	{Value: "yineng", Label: "异能"},
	{Value: "xuanyi", Label: "悬疑"},
	{Value: "juqing", Label: "剧情"},
	{Value: "kehuan", Label: "科幻"},
	{Value: "qihuan", Label: "奇幻"},
	{Value: "xuanhuan", Label: "玄幻"},
	{Value: "chuanyue", Label: "穿越"},
	{Value: "mouxian", Label: "冒险"},
	{Value: "tuili", Label: "推理"},
	{Value: "wuxia", Label: "武侠"},
	{Value: "gedou", Label: "格斗"},
	{Value: "zhanzheng", Label: "战争"},
	{Value: "rexie", Label: "热血"},
	{Value: "gaoxiao", Label: "搞笑"},
	{Value: "danuzhu", Label: "大女主"},
	{Value: "dushi", Label: "都市"},
	{Value: "zongcai", Label: "总裁"},
	{Value: "hougong", Label: "后宫"},
	{Value: "richang", Label: "日常"},
	{Value: "hanman", Label: "韩漫"},
	{Value: "shaonian", Label: "少年"},
	{Value: "qita", Label: "其它"},
}

var baoziStateOptions = []model.FilterOption{
	{Value: "", Label: "全部"},
	{Value: "serial", Label: "连载中"},
	{Value: "pub", Label: "已完结"},
}

var baoziRegionOptions = []model.FilterOption{
	{Value: "", Label: "全部"},
	{Value: "cn", Label: "国漫"},
	{Value: "kr", Label: "韩漫"},
	{Value: "jp", Label: "日漫"},
	{Value: "en", Label: "美漫"},
}

var (
	comicPattern   = regexp.MustCompile(`/comic/([A-Za-z0-9_\-]+)`)
	chapterPattern = regexp.MustCompile(`/comic/chapter/([A-Za-z0-9_\-]+)`)
	imagePattern   = regexp.MustCompile(`(?i)(?:https?:)?//[^\s"'<>\\]+?\.(?:jpg|jpeg|png|webp|avif)(?:\?[^\s"'<>\\]*)?`)
)

func (s *Source) Info() model.SourceInfo {
	return model.SourceInfo{
		ID:          sourceID,
		Name:        "包子漫画",
		Kind:        source.KindComic,
		Description: "baozimh web source",
		Homepage:    defaultSite + "/",
		NeedsLogin:  false,
		CanSearch:   true,
		CanBrowse:   true,
		Icon:        "https://www.google.com/s2/favicons?domain=baozimh.com&sz=64",
		Sites:       append([]string(nil), defaultSites...),
		Filters: []model.FilterGroup{
			{Key: "category", Label: "题材", Options: baoziCategoryOptions},
			{Key: "region", Label: "地区", Options: baoziRegionOptions},
			{Key: "state", Label: "状态", Options: baoziStateOptions},
		},
	}
}

func (s *Source) Search(ctx context.Context, account source.Account, query string, page int) (model.SearchResult, error) {
	if page < 1 {
		page = 1
	}
	values := url.Values{
		"q":    {query},
		"page": {strconv.Itoa(page)},
	}
	html, base, err := s.get(ctx, account, "/search", values)
	if err != nil {
		return model.SearchResult{}, err
	}
	return parseComics(html, base, page), nil
}

func parseAmpComicList(raw string, page int) (model.SearchResult, error) {
	var payload struct {
		Items []struct {
			ComicID  string `json:"comic_id"`
			Name     string `json:"name"`
			Author   string `json:"author"`
			TopicImg string `json:"topic_img"`
		} `json:"items"`
		Total int `json:"total"`
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return model.SearchResult{}, err
	}
	items := make([]model.Comic, 0, len(payload.Items))
	for _, item := range payload.Items {
		comicID := strings.TrimSpace(item.ComicID)
		if comicID == "" {
			continue
		}
		cover := coverAddress(strings.TrimSpace(item.TopicImg))
		items = append(items, model.Comic{
			SourceID: sourceID,
			ID:       comicID,
			Title:    source.FirstNonEmpty(strings.TrimSpace(item.Name), comicID),
			Cover:    cover,
			Author:   strings.TrimSpace(item.Author),
		})
	}
	limit := payload.Limit
	if limit <= 0 {
		limit = perPage
	}
	return model.SearchResult{
		Items:   items,
		Page:    page,
		Total:   payload.Total,
		HasMore: len(items) >= limit && (payload.Total == 0 || payload.Total > page*limit),
	}, nil
}

func (s *Source) Browse(ctx context.Context, account source.Account, options model.BrowseOptions, page int) (model.SearchResult, error) {
	if page < 1 {
		page = 1
	}
	category := source.FirstNonEmpty(strings.TrimSpace(options.Category), "all")
	if category == "latest" {
		category = "all"
	}
	region := source.FirstNonEmpty(strings.TrimSpace(options.Region), "all")
	state := source.FirstNonEmpty(strings.TrimSpace(options.State), "all")
	apiValues := url.Values{
		"filter": {"*"},
		"region": {region},
		"type":   {category},
		"state":  {state},
		"limit":  {strconv.Itoa(perPage)},
		"page":   {strconv.Itoa(page)},
	}
	if raw, _, err := s.get(ctx, account, "/api/bzmhq/amp_comic_list", apiValues); err == nil {
		if result, parseErr := parseAmpComicList(raw, page); parseErr == nil {
			return result, nil
		}
	}

	path := "/list"
	if category != "all" {
		path = "/list/" + url.PathEscape(category)
	}
	values := url.Values{}
	if page > 1 {
		values.Set("page", strconv.Itoa(page))
	}
	html, base, err := s.get(ctx, account, path, values)
	if err != nil {
		return model.SearchResult{}, err
	}
	return parseComics(html, base, page), nil
}

func (s *Source) Detail(ctx context.Context, account source.Account, comicID string) (model.Comic, error) {
	slug := normalizeComicID(comicID)
	if slug == "" {
		return model.Comic{}, errors.New("无效的包子漫画作品 ID")
	}
	html, _, err := s.get(ctx, account, "/comic/"+slug, nil)
	if err != nil {
		return model.Comic{}, err
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return model.Comic{}, err
	}
	comic := model.Comic{
		SourceID: sourceID,
		ID:       slug,
		Title: source.FirstNonEmpty(
			metaContent(document, "og:novel:book_name"),
			metaContent(document, "og:title"),
			strings.TrimSpace(document.Find("h1").First().Text()),
		),
		Cover: coverAddress(source.FirstNonEmpty(
			metaContent(document, "og:image"),
			imageFrom(document.Find(".comic-cover, .cover, .book-cover")),
		)),
		Tags:   splitList(source.FirstNonEmpty(metaContent(document, "og:novel:category"), listFrom(document, ".tags a, .tag, .categories a"))),
		Status: source.FirstNonEmpty(metaContent(document, "og:novel:status"), strings.TrimSpace(document.Find(".status, .book-status").First().Text())),
	}
	if comic.Title == "" {
		comic.Title = strings.TrimSpace(document.Find("title").First().Text())
	}
	if comic.Title == "" {
		comic.Title = "包子漫画 " + slug
	}
	comic.Author = source.FirstNonEmpty(
		metaContent(document, "og:novel:author"),
		listFrom(document, ".author a, .author, .book-author"),
	)
	comic.Description = source.FirstNonEmpty(
		metaContent(document, "og:description"),
		strings.TrimSpace(document.Find(".description, .book-description, .intro").First().Text()),
	)
	comic.ChapterCount = document.Find("a[href*='/comic/chapter/'], a[href*='/user/page_direct']").Length()
	return comic, nil
}

func (s *Source) Chapters(ctx context.Context, account source.Account, comicID string) ([]model.Chapter, error) {
	slug := normalizeComicID(comicID)
	if slug == "" {
		return nil, errors.New("无效的包子漫画作品 ID")
	}
	chapters := make([]model.Chapter, 0, 128)
	seen := map[string]bool{}
	if html, base, err := s.get(ctx, account, "/comic/"+slug, nil); err == nil {
		if document, err := goquery.NewDocumentFromReader(strings.NewReader(html)); err == nil {
			// baozimh renders a newest-first "latest chapters" teaser and a
			// recommendation block above the real catalogue, so scope the parse
			// to the two catalogue containers, which list chapters ascending.
			selector := "#chapter-items a[href*='/user/page_direct'], #chapters_other_list a[href*='/user/page_direct'], #chapter-items a[href*='/comic/chapter/'], #chapters_other_list a[href*='/comic/chapter/']"
			if document.Find(selector).Length() == 0 {
				selector = "a[href*='/comic/chapter/'], a[href*='/user/page_direct']"
			}
			document.Find(selector).Each(func(_ int, selection *goquery.Selection) {
				href, ok := selection.Attr("href")
				if !ok {
					return
				}
				href = chapterHref(href)
				if href == "" {
					return
				}
				chapterID := chapterSlug(href)
				key := chapterKey(chapterID)
				if chapterID == "" || key == "" || seen[key] {
					return
				}
				seen[key] = true
				title := source.FirstNonEmpty(
					strings.TrimSpace(selection.AttrOr("title", "")),
					strings.TrimSpace(selection.Find(".chapter-title, .chapter-name, span, p").First().Text()),
					strings.TrimSpace(selection.Text()),
					chapterID,
				)
				chapters = append(chapters, model.Chapter{
					ID:      chapterID,
					ComicID: slug,
					Title:   title,
					Order:   float64(len(chapters) + 1),
					URL:     source.Absolutize(base, href),
				})
			})
		}
	}
	if len(chapters) == 0 {
		if viaAPI, err := s.chaptersFromApp(ctx, slug); err == nil && len(viaAPI) > 0 {
			return viaAPI, nil
		}
	}
	orderChapters(chapters)
	return chapters, nil
}

// orderChapters normalises the raw chapter list into oldest-to-newest order
// with strictly increasing order values, which the scheduler relies on.
func orderChapters(chapters []model.Chapter) {
	if len(chapters) == 0 {
		return
	}
	if listedNewestFirst(chapters) {
		for i, j := 0, len(chapters)-1; i < j; i, j = i+1, j-1 {
			chapters[i], chapters[j] = chapters[j], chapters[i]
		}
	}
	for index := range chapters {
		chapters[index].Order = float64(index + 1)
	}
}

// listedNewestFirst reports whether the comic page printed the newest chapter
// first. Explicit chapter numbers win when both ends of the list carry one;
// otherwise we fall back to the site's newest-first layout.
func listedNewestFirst(chapters []model.Chapter) bool {
	firstIndex, firstNumber, okFirst := firstNumbered(chapters)
	lastIndex, lastNumber, okLast := lastNumbered(chapters)
	if okFirst && okLast && firstIndex != lastIndex && firstNumber != lastNumber {
		return firstNumber > lastNumber
	}
	return true
}

func firstNumbered(chapters []model.Chapter) (int, float64, bool) {
	for index := range chapters {
		if number, ok := chapterNumber(chapters[index]); ok {
			return index, number, true
		}
	}
	return 0, 0, false
}

func lastNumbered(chapters []model.Chapter) (int, float64, bool) {
	for index := len(chapters) - 1; index >= 0; index-- {
		if number, ok := chapterNumber(chapters[index]); ok {
			return index, number, true
		}
	}
	return 0, 0, false
}

var chapterNumberPatterns = []*regexp.Regexp{
	regexp.MustCompile(`第\s*([0-9]+(?:\.[0-9]+)?)`),
	regexp.MustCompile(`^\s*([0-9]+(?:\.[0-9]+)?)\s*(?:话|話|回|章|集|卷|节|節|期)`),
	regexp.MustCompile(`(?i)(?:chapter|chap|ch)\.?\s*([0-9]+(?:\.[0-9]+)?)`),
}

func chapterNumber(chapter model.Chapter) (float64, bool) {
	for _, value := range []string{chapter.Title, chapter.ID} {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		for _, pattern := range chapterNumberPatterns {
			matches := pattern.FindStringSubmatch(value)
			if len(matches) < 2 {
				continue
			}
			number, err := strconv.ParseFloat(matches[1], 64)
			if err != nil {
				continue
			}
			return number, true
		}
	}
	return 0, false
}

func (s *Source) Pages(ctx context.Context, account source.Account, comicID string, chapter model.Chapter) ([]model.Page, error) {
	path := chapterPath(chapter)
	if path == "" {
		return nil, errors.New("无效的包子漫画章节 ID")
	}
	// The reader endpoint only returns the real page with the .html suffix;
	// without it the mirror answers 200 with a tiny placeholder shell.
	readerPath := path
	if !strings.HasSuffix(readerPath, ".html") {
		readerPath += ".html"
	}
	html, base, err := s.get(ctx, account, "/comic/chapter/"+readerPath, nil)
	if err != nil {
		return nil, err
	}
	referer := source.BuildURL(base, "/comic/chapter/"+readerPath, nil)
	seen := map[string]bool{}
	pages := make([]model.Page, 0, 64)
	add := func(raw string) {
		raw = strings.TrimSpace(strings.ReplaceAll(raw, `\/`, `/`))
		if raw == "" {
			return
		}
		address := source.Absolutize(base, raw)
		if !strings.HasPrefix(address, "http") {
			return
		}
		if !isImageAddress(address) || isCoverImage(address) {
			return
		}
		key := imageKey(address)
		if seen[key] {
			return
		}
		seen[key] = true
		pages = append(pages, model.Page{
			URL:      address,
			Referer:  referer,
			FileName: fileNameFromURL(address),
		})
	}
	if document, err := goquery.NewDocumentFromReader(strings.NewReader(html)); err == nil {
		images := document.Find(".comic-contain img, .comic-contain__item img, img.chapter-img")
		if images.Length() == 0 {
			images = document.Find("img")
		}
		images.Each(func(_ int, selection *goquery.Selection) {
			class, _ := selection.Attr("class")
			if strings.Contains(class, "avatar") || strings.Contains(class, "logo") {
				return
			}
			add(source.FirstNonEmpty(
				selection.AttrOr("data-img", ""),
				selection.AttrOr("data-original", ""),
				selection.AttrOr("data-src", ""),
				selection.AttrOr("data-lazy-src", ""),
				selection.AttrOr("src", ""),
			))
		})
	}
	if len(pages) == 0 {
		for _, match := range imagePattern.FindAllString(html, -1) {
			add(match)
		}
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("包子漫画章节没有解析到图片（镜像 %s，页面 %d 字节），站点可能启用了更强的 Cloudflare 校验，请在源设置里更新 Cookie", base, len(html))
	}
	return pages, nil
}

// coverHost is the image CDN baozimh serves from. The site's previous cover
// host (static-tw.baozimh.com) sits behind Cloudflare and frequently 403s,
// while the same files are publicly served by s1/s2.bzcdn.net.
const coverHost = "https://s1.bzcdn.net"

// coverAddress normalises the different cover shapes baozimh returns so
// protocol-relative and root-relative paths still resolve to real images.
func coverAddress(raw string) string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, `\/`, `/`))
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	raw = strings.NewReplacer(
		"static-tw.baozimh.com", "s1.bzcdn.net",
		"static.baozimh.com", "s1.bzcdn.net",
	).Replace(raw)
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	if strings.HasPrefix(raw, "/") {
		return coverHost + raw
	}
	if strings.Contains(raw, "/") {
		return coverHost + "/" + strings.TrimLeft(raw, "/")
	}
	return coverHost + "/cover/" + strings.TrimLeft(raw, "/")
}

// chapterKey collapses the mirror-specific comic id so the same chapter that
// is listed twice (main list plus the "other chapters" section) is counted
// only once.
func chapterKey(chapterID string) string {
	chapterID = strings.TrimSpace(chapterID)
	if index := strings.LastIndex(chapterID, "/"); index >= 0 {
		return chapterID[index+1:]
	}
	return chapterID
}

// chapterHref normalises a baozimh chapter anchor into the canonical reader
// path. The server-rendered comic page links chapters through
// /user/page_direct?comic_id=<id>&section_slot=<s>&chapter_slot=<c>, whose real
// reader URL is /comic/chapter/<id>/<s>_<c>.html.
func chapterHref(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if !strings.Contains(href, "/user/page_direct") {
		if strings.Contains(href, "/comic/chapter/") {
			return href
		}
		return ""
	}
	parsed, err := url.Parse(href)
	if err != nil {
		return ""
	}
	query := parsed.Query()
	comicID := strings.TrimSpace(query.Get("comic_id"))
	section := strings.TrimSpace(query.Get("section_slot"))
	chapter := strings.TrimSpace(query.Get("chapter_slot"))
	if comicID == "" || chapter == "" {
		return ""
	}
	if section == "" {
		section = "0"
	}
	return "/comic/chapter/" + comicID + "/" + section + "_" + chapter + ".html"
}

// chapterSlug extracts the chapter identifier from a /comic/chapter/ link.
func chapterSlug(href string) string {
	return chapterPathValue(href)
}

// chapterPath resolves the reader path for a chapter, accepting an id or url.
func chapterPath(chapter model.Chapter) string {
	for _, value := range []string{chapter.URL, chapter.ID} {
		if path := chapterPathValue(value); path != "" {
			return path
		}
	}
	return ""
}

func chapterPathValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if index := strings.Index(value, "/comic/chapter/"); index >= 0 {
		value = value[index+len("/comic/chapter/"):]
	} else if parsed, err := url.Parse(value); err == nil && parsed.IsAbs() {
		value = parsed.Path
	}
	if cut := strings.IndexAny(value, "?#"); cut >= 0 {
		value = value[:cut]
	}
	value = strings.TrimSuffix(value, ".html")
	return strings.Trim(value, "/")
}

// chaptersFromApp uses the baozimh app chapter API, which the web comic page
// relies on for its client-rendered chapter list.
func (s *Source) chaptersFromApp(ctx context.Context, comicID string) ([]model.Chapter, error) {
	address := "https://appcn.baozimh.com/baozimhapp/comic/chapter/" + comicID
	raw, err := source.FetchText(ctx, s.client, address, map[string]string{
		"Referer":         "https://www.baozimh.com/",
		"Accept":          "application/json, text/plain, */*",
		"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.6",
	})
	if err != nil {
		return nil, err
	}
	return parseAppChapters(raw, comicID), nil
}

type appChapter struct {
	id    string
	title string
	order float64
}

// parseAppChapters walks the app API payload generically, so it keeps working
// when the API nests its chapter list differently across mirrors.
func parseAppChapters(raw, comicID string) []model.Chapter {
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}
	collected := make([]appChapter, 0, 64)
	seen := map[string]bool{}
	var walk func(node any)
	walk = func(node any) {
		switch value := node.(type) {
		case map[string]any:
			id := jsonString(value, "chapterid", "chapter_id", "chapterId")
			if id != "" && !seen[id] {
				seen[id] = true
				collected = append(collected, appChapter{
					id:    id,
					title: jsonString(value, "chapter_title", "chaptertitle", "chapter_name", "name", "title"),
					order: jsonNumber(value, "chapterindex", "chapter_index", "index", "sort", "order"),
				})
			}
			for _, child := range value {
				walk(child)
			}
		case []any:
			for _, child := range value {
				walk(child)
			}
		}
	}
	walk(payload)
	if len(collected) == 0 {
		return nil
	}
	hasOrder := false
	for _, item := range collected {
		if item.order > 0 {
			hasOrder = true
			break
		}
	}
	if hasOrder {
		sort.SliceStable(collected, func(i, j int) bool {
			if collected[i].order == collected[j].order {
				return collected[i].id < collected[j].id
			}
			return collected[i].order < collected[j].order
		})
	}
	chapters := make([]model.Chapter, 0, len(collected))
	for index, item := range collected {
		chapters = append(chapters, model.Chapter{
			ID:      item.id,
			ComicID: comicID,
			Title:   source.FirstNonEmpty(item.title, item.id),
			Order:   float64(index + 1),
		})
	}
	return chapters
}

func jsonString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if raw, ok := value[key]; ok {
			switch typed := raw.(type) {
			case string:
				if trimmed := strings.TrimSpace(typed); trimmed != "" {
					return trimmed
				}
			case float64:
				if typed != 0 {
					return strconv.FormatFloat(typed, 'f', -1, 64)
				}
			}
		}
	}
	return ""
}

func jsonNumber(value map[string]any, keys ...string) float64 {
	for _, key := range keys {
		switch typed := value[key].(type) {
		case float64:
			return typed
		case string:
			if number, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
				return number
			}
		}
	}
	return 0
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

func parseComics(html, base string, page int) model.SearchResult {
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return model.SearchResult{Page: page}
	}
	items := make([]model.Comic, 0, 30)
	seen := map[string]bool{}
	document.Find("a[href*='/comic/']").Each(func(_ int, selection *goquery.Selection) {
		href, ok := selection.Attr("href")
		if !ok || strings.Contains(href, "/comic/chapter/") {
			return
		}
		matches := comicPattern.FindStringSubmatch(href)
		if len(matches) < 2 || seen[matches[1]] {
			return
		}
		if selection.Find("img").Length() == 0 {
			return
		}
		seen[matches[1]] = true
		item := model.Comic{
			SourceID: sourceID,
			ID:       matches[1],
			Title: source.FirstNonEmpty(
				strings.TrimSpace(selection.AttrOr("title", "")),
				strings.TrimSpace(selection.Find(".comic-title, .title, h3, h2, p").First().Text()),
				strings.TrimSpace(selection.Text()),
			),
			Cover: coverAddress(imageFrom(selection)),
		}
		if item.Title == "" {
			item.Title = matches[1]
		}
		items = append(items, item)
	})
	hasMore := document.Find("a[rel='next'], .pagination .next, a[href*='page="+strconv.Itoa(page+1)+"']").Length() > 0
	return model.SearchResult{Items: items, Page: page, Total: len(items), HasMore: hasMore}
}

func normalizeComicID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if matches := comicPattern.FindStringSubmatch(value); len(matches) >= 2 {
		return matches[1]
	}
	value = strings.Trim(value, "/")
	if strings.ContainsAny(value, "?&=") {
		return ""
	}
	return value
}

func normalizeChapterID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if matches := chapterPattern.FindStringSubmatch(value); len(matches) >= 2 {
		return matches[1]
	}
	return value
}

func isImageAddress(address string) bool {
	lower := strings.ToLower(address)
	if strings.Contains(lower, "/static/") || strings.Contains(lower, "logo") || strings.Contains(lower, "avatar") {
		return false
	}
	for _, suffix := range []string{".jpg", ".jpeg", ".png", ".webp", ".avif"} {
		if strings.Contains(lower, suffix) {
			return true
		}
	}
	return strings.Contains(lower, "image") || strings.Contains(lower, "pic")
}

// isCoverImage reports whether an image belongs to a cover / promotion slot
// rather than the chapter reader.
func isCoverImage(address string) bool {
	lower := strings.ToLower(address)
	return strings.Contains(lower, "/cover/") || strings.Contains(lower, "default_cover")
}

// imageKey ignores query strings so cache-busting variants of the same file
// are not counted as separate pages.
func imageKey(address string) string {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" {
		return address
	}
	return parsed.Host + parsed.Path
}

func fileNameFromURL(address string) string {
	parsed, err := url.Parse(address)
	if err != nil {
		return ""
	}
	name := parsed.Path
	if index := strings.LastIndex(name, "/"); index >= 0 {
		name = name[index+1:]
	}
	return name
}

// metaContent reads an Open Graph-style meta tag. baozimh mirrors publish
// these with name="og:*" while other mirrors use property="og:*", so both
// spellings are accepted.
func metaContent(document *goquery.Document, name string) string {
	return source.FirstNonEmpty(
		attr(document, "meta[name='"+name+"']", "content"),
		attr(document, "meta[property='"+name+"']", "content"),
	)
}

func attr(document *goquery.Document, selector, name string) string {
	value, _ := document.Find(selector).First().Attr(name)
	return strings.TrimSpace(value)
}

func imageFrom(selection *goquery.Selection) string {
	image := selection
	if selection.Is("img") {
		image = selection
	} else {
		image = selection.Find("img").First()
	}
	if image.Length() == 0 {
		return ""
	}
	return source.FirstNonEmpty(
		strings.TrimSpace(image.AttrOr("data-original", "")),
		strings.TrimSpace(image.AttrOr("data-src", "")),
		strings.TrimSpace(image.AttrOr("data-lazy-src", "")),
		strings.TrimSpace(image.AttrOr("src", "")),
	)
}

func listFrom(document *goquery.Document, selector string) string {
	values := make([]string, 0, 8)
	document.Find(selector).Each(func(_ int, selection *goquery.Selection) {
		if value := strings.TrimSpace(selection.Text()); value != "" {
			values = append(values, value)
		}
	})
	return strings.Join(values, " / ")
}

func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, " / ")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

var _ source.Source = (*Source)(nil)
