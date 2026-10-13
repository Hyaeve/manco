package jmcomic

import (
	"context"
	"crypto/md5"
	"encoding/hex"
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
	client       *http.Client
	directClient *http.Client
}

var jmCategoryOptions = []model.FilterOption{
	{Value: "", Label: "全部"},
	{Value: "doujin", Label: "同人"},
	{Value: "single", Label: "单本"},
	{Value: "short", Label: "短篇"},
	{Value: "another", Label: "其他类"},
	{Value: "hanman", Label: "韩漫"},
	{Value: "meiman", Label: "美漫"},
	{Value: "another_cosplay", Label: "Cosplay"},
	{Value: "3D", Label: "3D"},
	{Value: "禁漫漢化組", Label: "禁漫汉化组"},
}

var jmSortOptions = []model.FilterOption{
	{Value: "mr", Label: "最新"},
	{Value: "mv", Label: "最多观看"},
	{Value: "tf", Label: "最多喜欢"},
	{Value: "mp", Label: "最多图片"},
}

func New(client *http.Client) *Source { return &Source{client: client} }

const (
	sourceID      = "jmcomic"
	defaultSite   = "https://www.cdnhjk.net"
	defaultOrigin = "https://cdn-msp.jmapinodeudzn.net"
)

// defaultSites lists the public 18comic mirrors used as fallbacks when a
// configured site is unreachable. The order is the preferred rotation order.
var defaultSites = []string{
	"https://www.cdnhjk.net",
	"https://www.cdngwc.cc",
	"https://www.cdngwc.net",
	"https://www.cdngwc.club",
	"https://www.cdnutc.me",
}

// defaultUserAgent is used when a source account has no custom UA override.
const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

var (
	albumPattern           = regexp.MustCompile(`/album/(\d+)`)
	photoPattern           = regexp.MustCompile(`/photo/(\d+)`)
	pageArrayPattern       = regexp.MustCompile(`var\s+page_arr\s*=\s*(\[[^\]]*\])`)
	scramblePattern        = regexp.MustCompile(`var\s+scramble_id\s*=\s*(\d+)`)
	albumIDPattern         = regexp.MustCompile(`var\s+(?:album_id|aid)\s*=\s*(\d+)`)
	domainPattern          = regexp.MustCompile(`var\s+data_original_domain\s*=\s*["']([^"']+)["']`)
	imageHostPattern       = regexp.MustCompile(`(?:https?:)?//([a-z0-9.-]+\.(?:18comic|jmapiproxy[0-9]*|jmapinodeudzn|cdngwc|cdnhjk|cdnutc)\.(?:vip|org|cc|site|club|net|me|info))`)
	backgroundImagePattern = regexp.MustCompile(`url\([\'"]?([^\'")]+)[\'"]?\)`)
)

func (s *Source) Info() model.SourceInfo {
	return model.SourceInfo{
		ID:          sourceID,
		Name:        "禁漫天堂",
		Kind:        source.KindComic,
		Description: "18comic album source",
		Homepage:    defaultSite + "/",
		NeedsLogin:  false,
		CanSearch:   true,
		CanBrowse:   true,
		Icon:        "/source-icons/jmcomic.png",
		Sites:       append([]string(nil), defaultSites...),
		Filters: []model.FilterGroup{
			{Key: "category", Label: "分类", Options: jmCategoryOptions},
			{Key: "sort", Label: "排序", Default: "mr", Options: jmSortOptions},
		},
	}
}

func (s *Source) Search(ctx context.Context, account source.Account, query string, page int) (model.SearchResult, error) {
	if page < 1 {
		page = 1
	}
	values := url.Values{
		"search_query": {query},
		"page":         {strconv.Itoa(page)},
	}
	html, base, err := s.getMatching(ctx, account, []string{"/search", "/search/photos"}, values, hasAlbumLinks)
	if err != nil {
		return model.SearchResult{}, err
	}
	return parseSearch(html, base, page)
}

func (s *Source) Browse(ctx context.Context, account source.Account, options model.BrowseOptions, page int) (model.SearchResult, error) {
	if page < 1 {
		page = 1
	}
	values := url.Values{
		"page":  {strconv.Itoa(page)},
		"order": {source.FirstNonEmpty(strings.TrimSpace(options.Sort), "mr")},
	}
	category := strings.TrimSpace(options.Category)
	if category != "" && category != "0" {
		values.Set("category", category)
	}
	paths := []string{"/albums.html", "/albums"}
	if page > 1 {
		paths = append(paths, "/albums-index-page-"+strconv.Itoa(page)+".html")
	}
	html, base, err := s.getMatching(ctx, account, paths, values, hasAlbumLinks)
	if err != nil {
		return model.SearchResult{}, err
	}
	return parseSearch(html, base, page)
}

func (s *Source) Detail(ctx context.Context, account source.Account, comicID string) (model.Comic, error) {
	comicID = normalizeComicID(comicID)
	if comicID == "" {
		return model.Comic{}, errors.New("无效的禁漫作品 ID")
	}
	html, base, err := s.getMatching(ctx, account, []string{"/album/" + comicID}, nil, hasAlbumLinks)
	if err != nil {
		return model.Comic{}, err
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return model.Comic{}, err
	}
	comic := model.Comic{
		SourceID: sourceID,
		ID:       comicID,
		Title:    source.FirstNonEmpty(attr(document, "meta[property='og:title']", "content"), strings.TrimSpace(document.Find("h1").First().Text())),
		Cover:    source.Absolutize(base, attr(document, "meta[property='og:image']", "content")),
		Tags:     splitList(listFrom(document, ".tags .tag, .tag-list a, a.tag")),
		Status:   strings.TrimSpace(document.Find(".label, .status").First().Text()),
	}
	if comic.Title == "" {
		comic.Title = "禁漫 " + comicID
	}
	comic.Author = listFrom(document, ".author a, .author, a[href*='/author/']")
	comic.Description = strings.TrimSpace(document.Find(".intro, .description, #intro, .col-md-9 p").First().Text())
	comic.ChapterCount = document.Find("a[href*='/photo/']").Length()
	return comic, nil
}

func (s *Source) Chapters(ctx context.Context, account source.Account, comicID string) ([]model.Chapter, error) {
	comicID = normalizeComicID(comicID)
	if comicID == "" {
		return nil, errors.New("无效的禁漫作品 ID")
	}
	html, base, err := s.getMatching(ctx, account, []string{"/album/" + comicID}, nil, hasAlbumLinks)
	if err != nil {
		return nil, err
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	chapters := make([]model.Chapter, 0, 64)
	seen := map[string]bool{}
	document.Find("a[href*='/photo/']").Each(func(_ int, selection *goquery.Selection) {
		href, ok := selection.Attr("href")
		if !ok {
			return
		}
		matches := photoPattern.FindStringSubmatch(href)
		if len(matches) < 2 || seen[matches[1]] {
			return
		}
		seen[matches[1]] = true
		title := source.FirstNonEmpty(
			strings.TrimSpace(selection.Find("span").Last().Text()),
			strings.TrimSpace(selection.Text()),
			"第 "+matches[1]+" 话",
		)
		order := float64(len(chapters) + 1)
		if parsed, parseErr := strconv.ParseFloat(strings.TrimSpace(selection.Find("span").Last().Text()), 64); parseErr == nil && parsed > 0 {
			order = parsed
		}
		chapters = append(chapters, model.Chapter{
			ID:      matches[1],
			ComicID: comicID,
			Title:   title,
			Order:   order,
			URL:     source.Absolutize(base, href),
		})
	})
	if len(chapters) == 0 {
		return []model.Chapter{}, nil
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
	html, base, err := s.getMatching(ctx, account, []string{"/photo/" + photoID}, nil, func(page string) bool {
		_, ok := pageArray(page)
		return ok
	})
	if err != nil {
		return nil, err
	}
	names, ok := pageArray(html)
	if !ok {
		return nil, errors.New("禁漫页面未返回图片列表，可能需要配置源 Cookie 或更换镜像域名")
	}
	albumID := intValue(firstMatch(albumIDPattern, html))
	if albumID == 0 {
		albumID = intValue(comicID)
	}
	scrambleID := intValue(firstMatch(scramblePattern, html))
	parts := 0
	if len(names) > 0 {
		parts = scrambleParts(scrambleID, albumID, names[0])
	}
	domains := imageDomains(html)
	referer := source.BuildURL(base, "/photo/"+photoID, nil)
	pages := make([]model.Page, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		urls := make([]string, 0, len(domains))
		for _, domain := range domains {
			urls = append(urls, fmt.Sprintf("%s/media/photos/%s/%s", strings.TrimRight(domain, "/"), photoID, name))
		}
		if len(urls) == 0 {
			continue
		}
		pages = append(pages, model.Page{
			URL:             urls[0],
			Alternatives:    urls[1:],
			Referer:         referer,
			FileName:        name,
			DescrambleParts: parts,
			AssetID:         photoID,
		})
	}
	if len(pages) == 0 {
		return nil, errors.New("禁漫章节没有可下载的图片")
	}
	return pages, nil
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
	return source.FetchTextFallback(ctx, source.SourceClient(s.client, &s.directClient, account), s.bases(account), path, values, func(base string) map[string]string {
		headers := map[string]string{
			"Referer":                   base + "/",
			"Accept-Language":           "zh-CN,zh;q=0.9,en;q=0.6",
			"Upgrade-Insecure-Requests": "1",
		}
		return jmHeaders(account, base, headers)
	})
}

// getMatching walks every mirror and candidate path until one returns a page
// that satisfies ok. 18comic mirrors frequently rotate to parked or
// challenge pages that answer HTTP 200 with no album markup, so plain status
// checks would silently return empty results from a dead mirror.
func (s *Source) getMatching(ctx context.Context, account source.Account, paths []string, values url.Values, ok func(string) bool) (string, string, error) {
	client := source.SourceClient(s.client, &s.directClient, account)
	var lastErr error
	for _, base := range s.bases(account) {
		for _, path := range paths {
			address := source.BuildURL(base, path, values)
			headers := map[string]string{
				"Referer":                   base + "/",
				"Accept-Language":           "zh-CN,zh;q=0.9,en;q=0.6",
				"Upgrade-Insecure-Requests": "1",
			}
			headers = jmHeaders(account, base, headers)
			html, err := source.FetchText(ctx, client, address, headers)
			if err != nil {
				lastErr = err
				continue
			}
			if ok != nil && !ok(html) {
				lastErr = fmt.Errorf("%s 未返回有效内容", base+path)
				continue
			}
			return html, base, nil
		}
	}
	if lastErr == nil {
		lastErr = errors.New("禁漫镜像均不可用")
	}
	return "", "", lastErr
}

// hasAlbumLinks reports whether a page really lists albums.
func hasAlbumLinks(html string) bool {
	return albumPattern.MatchString(html)
}

// jmHeaders merges the account User-Agent and Cookie into the request headers.
// 18comic mirrors increasingly reject blank or library-default agents, so a
// custom UA can be configured per source account.
func jmHeaders(account source.Account, base string, headers map[string]string) map[string]string {
	ua := strings.TrimSpace(account.UserAgent)
	if ua == "" {
		ua = defaultUserAgent
	}
	headers["User-Agent"] = ua
	for key, value := range source.HeaderCookie(account.Cookie) {
		headers[key] = value
	}
	return headers
}

func parseSearch(html, base string, page int) (model.SearchResult, error) {
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return model.SearchResult{}, err
	}
	items := parseAlbumCards(document, base)
	if len(items) == 0 {
		items = parseSearchFallback(document, base)
	}
	hasMore := document.Find("a[rel='next'], .pagination .next, a[href*='page="+strconv.Itoa(page+1)+"']").Length() > 0
	return model.SearchResult{Items: items, Page: page, Pages: 0, Total: len(items), HasMore: hasMore}, nil
}

// albumCardSelectors matches the containers 18comic uses for album tiles.
// Multiple mirrors render slightly different markup, so several class names
// are accepted. The anchor itself is the final fallback.
var albumCardSelectors = []string{
	".video-item", ".video-card", ".card", ".thumb", ".hentai-item",
	".img_span", "li", "article", "div",
}

// parseAlbumCards walks every album anchor, promotes it to the enclosing
// tile, and reads title and cover from that tile. Older parsing only inspected
// the anchor itself, so lazy-loaded covers (data-original on a sibling image)
// and titles living in the tile header were missed and the card degraded to
// "禁漫 <id>" with no artwork.
func parseAlbumCards(document *goquery.Document, base string) []model.Comic {
	items := make([]model.Comic, 0, 30)
	seen := map[string]bool{}
	document.Find("a[href*='/album/'], a[href*='/albums/']").Each(func(_ int, anchor *goquery.Selection) {
		href, ok := anchor.Attr("href")
		if !ok {
			return
		}
		matches := albumPattern.FindStringSubmatch(href)
		if len(matches) < 2 || seen[matches[1]] {
			return
		}
		seen[matches[1]] = true
		card := albumCard(anchor)
		title := source.FirstNonEmpty(
			strings.TrimSpace(anchor.AttrOr("title", "")),
			cardTitle(card),
			normalizeText(anchor.Text()),
		)
		cover := source.Absolutize(base, imageFrom(card))
		if isPlaceholderTitle(title, matches[1]) {
			title = "禁漫 " + matches[1]
		}
		items = append(items, model.Comic{
			SourceID: sourceID,
			ID:       matches[1],
			Title:    title,
			Cover:    cover,
		})
	})
	return items
}

func isPlaceholderTitle(title, albumID string) bool {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" || trimmed == albumID {
		return true
	}
	return strings.EqualFold(trimmed, "禁漫 "+albumID)
}

// albumCard returns the innermost tile-like ancestor of an album anchor.
func albumCard(anchor *goquery.Selection) *goquery.Selection {
	for _, selector := range albumCardSelectors {
		if ancestor := anchor.Closest(selector); ancestor.Length() > 0 {
			return ancestor
		}
	}
	return anchor
}

// cardTitle extracts the first meaningful title string from an album tile.
func cardTitle(card *goquery.Selection) string {
	selectors := []string{
		".video-title", ".title", ".name", ".card-title",
		"[data-title]", ".comic-name", ".comix_title",
		"h3", "h2", "h1", "p",
	}
	for _, selector := range selectors {
		var found string
		card.Find(selector).EachWithBreak(func(_ int, node *goquery.Selection) bool {
			if value := strings.TrimSpace(node.AttrOr("content", "")); value != "" {
				found = value
				return false
			}
			if value := normalizeText(node.Text()); value != "" {
				found = value
				return false
			}
			return true
		})
		if found != "" {
			return found
		}
	}
	if value := strings.TrimSpace(card.AttrOr("data-title", "")); value != "" {
		return value
	}
	return normalizeText(card.Text())
}

func normalizeText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func parseSearchFallback(document *goquery.Document, base string) []model.Comic {
	items := make([]model.Comic, 0, 30)
	seen := map[string]bool{}
	document.Find("a").Each(func(_ int, selection *goquery.Selection) {
		href, ok := selection.Attr("href")
		if !ok {
			return
		}
		matches := albumPattern.FindStringSubmatch(href)
		if len(matches) < 2 || seen[matches[1]] {
			return
		}
		seen[matches[1]] = true
		title := source.FirstNonEmpty(strings.TrimSpace(selection.AttrOr("title", "")), normalizeText(selection.Text()))
		cover := source.Absolutize(base, imageFrom(selection.Closest(".video-item, .video-card, .card, li, article, div").First()))
		if cover == "" {
			cover = source.Absolutize(base, imageFrom(selection))
		}
		if isPlaceholderTitle(title, matches[1]) {
			title = "禁漫 " + matches[1]
		}
		items = append(items, model.Comic{
			SourceID: sourceID,
			ID:       matches[1],
			Title:    title,
			Cover:    cover,
		})
	})
	return items
}

func pageArray(html string) ([]string, bool) {
	matches := pageArrayPattern.FindStringSubmatch(html)
	if len(matches) < 2 {
		return nil, false
	}
	raw := strings.ReplaceAll(matches[1], "'", "\"")
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		names = names[:0]
		regex := regexp.MustCompile(`"([^"]+)"`)
		for _, match := range regex.FindAllStringSubmatch(matches[1], -1) {
			names = append(names, match[1])
		}
	}
	return names, len(names) > 0
}

func imageDomains(html string) []string {
	domains := make([]string, 0, 8)
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if strings.HasPrefix(value, "//") {
			value = "https:" + value
		}
		value = strings.TrimRight(value, "/")
		if !strings.HasPrefix(value, "http") || seen[value] {
			return
		}
		seen[value] = true
		domains = append(domains, value)
	}
	for _, match := range domainPattern.FindAllStringSubmatch(html, -1) {
		add(match[1])
	}
	for _, match := range imageHostPattern.FindAllStringSubmatch(html, -1) {
		add("https://" + match[1])
	}
	add(defaultOrigin)
	for _, candidate := range []string{
		"https://cdn-msp.18comic.vip",
		"https://cdn-msp2.18comic.vip",
		"https://cdn-msp.18comic.org",
		"https://cdn-msp2.18comic.org",
		"https://cdn-msp.jmapinodeudzn.net",
		"https://cdn-msp2.jmapinodeudzn.net",
		"https://cdn-msp.jmapiproxy2.cc",
		"https://cdn-msp2.jmapiproxy2.cc",
	} {
		add(candidate)
	}
	return domains
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
	if matches := photoPattern.FindStringSubmatch(value); len(matches) >= 2 {
		return matches[1]
	}
	if matches := albumPattern.FindStringSubmatch(value); len(matches) >= 2 {
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

func firstMatch(pattern *regexp.Regexp, text string) string {
	matches := pattern.FindStringSubmatch(text)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

func intValue(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return parsed
}

func attr(document *goquery.Document, selector, name string) string {
	value, _ := document.Find(selector).First().Attr(name)
	return strings.TrimSpace(value)
}

func imageFrom(selection *goquery.Selection) string {
	if selection == nil || selection.Length() == 0 {
		return ""
	}
	image := selection
	if !selection.Is("img") && !selection.Is("amp-img") {
		image = selection.Find("img, amp-img").First()
	}
	if image.Length() > 0 {
		if value := imageAttrs(image); value != "" {
			return value
		}
	}
	if value := backgroundImageFrom(selection); value != "" {
		return value
	}
	return ""
}

// backgroundImageFrom reads CSS background-image declarations. Some mirrors
// no longer emit <img> for album tiles and instead style the tile directly.
func backgroundImageFrom(selection *goquery.Selection) string {
	candidates := selection
	if styled := selection.Find("[style*='background-image']").First(); styled.Length() > 0 {
		candidates = styled
	}
	if style := strings.TrimSpace(candidates.AttrOr("style", "")); style != "" {
		if match := backgroundImagePattern.FindStringSubmatch(style); len(match) >= 2 {
			return firstURL(match[1])
		}
	}
	return ""
}

func imageAttrs(image *goquery.Selection) string {
	for _, name := range []string{
		"data-original", "data-src", "data-lazy-src", "data-echo", "data-url",
		"data-srcset", "data-lazy", "src", "srcset",
	} {
		if value := firstURL(strings.TrimSpace(image.AttrOr(name, ""))); value != "" {
			return value
		}
	}
	return ""
}

// firstURL returns the first candidate from an attribute that may contain a
// srcset list ("url 1x, url2 2x"). 18comic lazy-loads grid covers through
// several of these attributes depending on the mirror.
func firstURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if index := strings.Index(value, ","); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	if fields := strings.Fields(value); len(fields) > 0 {
		value = fields[0]
	}
	return strings.TrimSpace(value)
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
