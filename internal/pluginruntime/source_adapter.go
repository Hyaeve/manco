package pluginruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
)

const pageSize = 30

type Adapter struct {
	manager      *Manager
	item         model.CustomSource
	config       Config
	info         model.SourceInfo
	filterMu     sync.Mutex
	filtersReady bool
	filters      []model.FilterGroup
	tagGroups    map[string]map[string]map[string]any
}

func NewAdapter(manager *Manager, item model.CustomSource) (*Adapter, error) {
	if manager == nil {
		return nil, errors.New("Kototoro 插件运行时不可用")
	}
	var config Config
	if len(item.Config) > 0 {
		if err := json.Unmarshal(item.Config, &config); err != nil {
			return nil, fmt.Errorf("解析插件来源配置: %w", err)
		}
	}
	if strings.TrimSpace(config.Runtime) != RuntimeKototoro {
		return nil, errors.New("不是 Kototoro 解析器来源")
	}
	if strings.TrimSpace(config.PluginID) == "" || strings.TrimSpace(config.SourceName) == "" {
		return nil, errors.New("插件来源缺少 pluginId 或 sourceName")
	}
	info := model.SourceInfo{
		ID:          strings.ToLower(strings.TrimSpace(item.ID)),
		Name:        strings.TrimSpace(item.Name),
		Kind:        KindForContentType(config.ContentType),
		Description: strings.TrimSpace(item.Description),
		Homepage:    strings.TrimSpace(item.Homepage),
		CanSearch:   true,
		CanBrowse:   true,
		Icon:        strings.TrimSpace(item.Icon),
		Editable:    true,
		Sites:       []string{strings.TrimSpace(item.Homepage)},
	}
	return &Adapter{
		manager:   manager,
		item:      item,
		config:    config,
		info:      info,
		tagGroups: map[string]map[string]map[string]any{},
	}, nil
}

func (a *Adapter) Info() model.SourceInfo { return a.info }

func (a *Adapter) Search(ctx context.Context, _ source.Account, query string, page int) (model.SearchResult, error) {
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * pageSize
	filter := map[string]any{"query": strings.TrimSpace(query)}
	result, err := a.call(ctx, "list", map[string]any{
		"sourceName": a.config.SourceName,
		"offset":     offset,
		"filter":     filter,
	})
	if err != nil {
		return model.SearchResult{}, err
	}
	return a.searchResult(result, page)
}

func (a *Adapter) Browse(ctx context.Context, _ source.Account, options model.BrowseOptions, page int) (model.SearchResult, error) {
	if page < 1 {
		page = 1
	}
	_, _ = a.Filters(ctx)
	filter := map[string]any{}
	if strings.TrimSpace(options.Category) != "" {
		filter["types"] = []string{strings.TrimSpace(options.Category)}
	}
	if strings.TrimSpace(options.State) != "" {
		filter["states"] = []string{strings.ToUpper(strings.TrimSpace(options.State))}
	}
	if strings.TrimSpace(options.Region) != "" {
		filter["originalLocale"] = strings.TrimSpace(options.Region)
	}
	for key, value := range options.Extra {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		switch key {
		case "demographic":
			filter["demographics"] = []string{strings.ToUpper(value)}
		case "rating":
			filter["contentRating"] = []string{strings.ToUpper(value)}
		case "region":
			filter["originalLocale"] = value
		default:
			if strings.HasPrefix(key, "tag:") {
				if tag := a.tagGroups[key][value]; tag != nil {
					filter["tags"] = []any{tag}
				}
			}
		}
	}
	fields := map[string]any{
		"sourceName": a.config.SourceName,
		"offset":     (page - 1) * pageSize,
		"filter":     filter,
	}
	if strings.TrimSpace(options.Sort) != "" {
		fields["order"] = strings.ToUpper(strings.TrimSpace(options.Sort))
	}
	result, err := a.call(ctx, "list", fields)
	if err != nil {
		return model.SearchResult{}, err
	}
	return a.searchResult(result, page)
}

func (a *Adapter) Detail(ctx context.Context, _ source.Account, comicID string) (model.Comic, error) {
	content, err := decodeObject(comicID)
	if err != nil {
		return model.Comic{}, err
	}
	result, err := a.call(ctx, "details", map[string]any{
		"content":   content,
		"fetchMode": "FORCE_REFRESH",
	})
	if err != nil {
		return model.Comic{}, err
	}
	item, err := objectField(result, "content")
	if err != nil {
		return model.Comic{}, err
	}
	return a.toComic(item), nil
}

func (a *Adapter) Chapters(ctx context.Context, account source.Account, comicID string) ([]model.Chapter, error) {
	comic, chapters, err := a.detailAndChapters(ctx, comicID)
	_ = comic
	if err == nil {
		return chapters, nil
	}
	detail, detailErr := a.Detail(ctx, account, comicID)
	if detailErr != nil {
		return nil, err
	}
	_ = detail
	return nil, err
}

func (a *Adapter) Pages(ctx context.Context, _ source.Account, _ string, chapter model.Chapter) ([]model.Page, error) {
	raw, err := decodeObject(chapter.ID)
	if err != nil {
		return nil, err
	}
	result, err := a.call(ctx, "pages", map[string]any{"chapter": raw})
	if err != nil {
		return nil, err
	}
	values, err := arrayField(result, "pages")
	if err != nil {
		return nil, err
	}
	pages := make([]model.Page, 0, len(values))
	for _, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		id, _ := encodeObject(item)
		page := model.Page{
			URL:      stringValue(item["url"]),
			AssetID:  id,
			Referer:  a.info.Homepage,
			Headers:  stringMap(item["headers"]),
			FileName: "",
		}
		pages = append(pages, page)
	}
	return pages, nil
}

// PageImage materializes a parser page through the sidecar. Parser plugins
// often descramble or sign images, so bypassing the plugin HTTP stack would
// produce corrupt files.
func (a *Adapter) PageImage(ctx context.Context, _ source.Account, _ string, _ model.Chapter, page model.Page) ([]byte, string, error) {
	raw, err := decodeObject(page.AssetID)
	if err != nil {
		return nil, "", err
	}
	result, err := a.call(ctx, "image", map[string]any{"page": raw})
	if err != nil {
		return nil, "", err
	}
	return a.readArtifact(result)
}

func (a *Adapter) CoverImage(ctx context.Context, _ source.Account, comic model.Comic) ([]byte, string, error) {
	raw, err := decodeObject(comic.ID)
	if err != nil {
		return nil, "", err
	}
	result, err := a.call(ctx, "cover", map[string]any{"content": raw, "large": true})
	if err != nil {
		return nil, "", err
	}
	return a.readArtifact(result)
}

func (a *Adapter) ChapterContent(ctx context.Context, _ source.Account, _ string, chapter model.Chapter) (string, error) {
	raw, err := decodeObject(chapter.ID)
	if err != nil {
		return "", err
	}
	result, err := a.call(ctx, "chapterContent", map[string]any{"chapter": raw})
	if err != nil {
		return "", err
	}
	content, ok := result["content"].(map[string]any)
	if !ok || content == nil {
		return "", errors.New("该章节没有正文内容")
	}
	return stringValue(content["html"]), nil
}

func (a *Adapter) call(ctx context.Context, operation string, fields map[string]any) (map[string]any, error) {
	return a.manager.Call(ctx, operation, fields)
}

// Filters returns the filter controls advertised by the Kototoro parser.
func (a *Adapter) Filters(ctx context.Context) ([]model.FilterGroup, error) {
	a.filterMu.Lock()
	defer a.filterMu.Unlock()
	if a.filtersReady {
		return append([]model.FilterGroup(nil), a.filters...), nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	result, err := a.call(ctx, "filters", map[string]any{"sourceName": a.config.SourceName})
	if err != nil {
		return nil, err
	}
	// Kototoro 的排序方式在 describe 描述符中声明，与 filters 操作分开返回。
	// 这里始终读取一次描述符，把内容源的排序能力合并进前端筛选栏。
	describeResult, _ := a.call(ctx, "describe", map[string]any{"sourceName": a.config.SourceName})
	options, _ := result["options"].(map[string]any)
	if options == nil {
		return nil, errors.New("解析器没有返回筛选配置")
	}
	a.filters = nil
	a.tagGroups = map[string]map[string]map[string]any{}
	a.filtersReady = true
	filters := make([]model.FilterGroup, 0, 8)
	if sortGroups := sortFilterGroups(describeResult); len(sortGroups) > 0 {
		filters = append(filters, sortGroups...)
	}
	addEnumFilter := func(key, label, field string) {
		items := stringValues(options[field])
		if len(items) == 0 {
			return
		}
		filter := model.FilterGroup{Key: key, Label: label, Default: ""}
		filter.Options = append(filter.Options, model.FilterOption{Value: "", Label: "全部"})
		for _, item := range items {
			filter.Options = append(filter.Options, model.FilterOption{Value: item, Label: enumLabel(item)})
		}
		filters = append(filters, filter)
	}
	addEnumFilter("category", "类型", "availableContentTypes")
	addEnumFilter("state", "状态", "availableStates")
	addEnumFilter("region", "地区", "availableLocales")
	addEnumFilter("demographic", "受众", "availableDemographics")
	addEnumFilter("rating", "分级", "availableContentRating")
	tagGroups := map[string]map[string]map[string]any{}
	groups, _ := options["effectiveTagGroups"].([]any)
	for index, value := range groups {
		group, ok := value.(map[string]any)
		if !ok {
			continue
		}
		label := firstNonEmpty(stringValue(group["title"]), fmt.Sprintf("标签 %d", index+1))
		key := fmt.Sprintf("tag:%d", index)
		filter := model.FilterGroup{Key: key, Label: label, Default: ""}
		filter.Options = append(filter.Options, model.FilterOption{Value: "", Label: "全部"})
		byKey := map[string]map[string]any{}
		tags, _ := group["tags"].([]any)
		for _, tagValue := range tags {
			tag, ok := tagValue.(map[string]any)
			if !ok {
				continue
			}
			keyValue := firstNonEmpty(stringValue(tag["key"]), stringValue(tag["title"]))
			if keyValue == "" {
				continue
			}
			byKey[keyValue] = tag
			filter.Options = append(filter.Options, model.FilterOption{Value: keyValue, Label: firstNonEmpty(stringValue(tag["title"]), keyValue)})
		}
		if len(byKey) == 0 {
			continue
		}
		tagGroups[key] = byKey
		filters = append(filters, filter)
	}
	a.filters = filters
	a.tagGroups = tagGroups
	return append([]model.FilterGroup(nil), a.filters...), nil
}

// sortFilterGroups converts the parser descriptor sortOrders into a filter group
// that the discover page can render as the sort selector.
func sortFilterGroups(result map[string]any) []model.FilterGroup {
	if result == nil {
		return nil
	}
	descriptor, _ := result["descriptor"].(map[string]any)
	values, _ := result["sortOrders"].([]any)
	defaultValue := stringValue(result["defaultSortOrder"])
	if descriptor != nil {
		if len(values) == 0 {
			values, _ = descriptor["sortOrders"].([]any)
		}
		if defaultValue == "" {
			defaultValue = stringValue(descriptor["defaultSortOrder"])
		}
	}
	group := model.FilterGroup{Key: "sort", Label: "排序", Default: defaultValue}
	seen := map[string]bool{}
	for _, raw := range values {
		value := stringValue(raw)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		group.Options = append(group.Options, model.FilterOption{Value: value, Label: sortOrderLabel(value)})
	}
	if len(group.Options) == 0 {
		return nil
	}
	return []model.FilterGroup{group}
}

func sortOrderLabel(value string) string {
	labels := map[string]string{
		"UPDATED": "最新", "LATEST": "最新", "NEWEST": "最新", "DATE": "最新",
		"POPULARITY": "热度", "POPULAR": "热度", "TRENDING": "热度",
		"POPULARITY_HOUR": "今日热门", "POPULARITY_TODAY": "今日热门", "TODAY": "今日热门",
		"POPULARITY_WEEK": "本周热门", "WEEK": "本周热门",
		"POPULARITY_MONTH": "本月热门", "MONTH": "本月热门",
		"POPULARITY_YEAR": "年度热门", "YEAR": "年度热门",
		"RATING": "评分", "SCORE": "评分",
		"NAME": "名称", "TITLE": "名称", "ALPHABETICAL": "名称",
	}
	if label, ok := labels[strings.ToUpper(strings.TrimSpace(value))]; ok {
		return label
	}
	return value
}

func stringValues(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, item := range values {
		text := strings.TrimSpace(stringValue(item))
		if text == "" || seen[text] {
			continue
		}
		seen[text] = true
		result = append(result, text)
	}
	return result
}

func enumLabel(value string) string {
	labels := map[string]string{
		"MANGA": "漫画", "MANHWA": "韩漫", "MANHUA": "国漫", "COMIC": "漫画",
		"ONGOING": "连载中", "FINISHED": "已完结", "HIATUS": "休刊", "ABANDONED": "已弃坑",
		"SAFE": "全年龄", "SUGGESTIVE": "建议谨慎", "MATURE": "成人", "ADULT": "成人",
		"SHOUNEN": "少年", "SHOUJO": "少女", "SEINEN": "青年", "JOSEI": "女性",
	}
	if label, ok := labels[strings.ToUpper(value)]; ok {
		return label
	}
	return value
}

func (a *Adapter) detailAndChapters(ctx context.Context, comicID string) (model.Comic, []model.Chapter, error) {
	content, err := decodeObject(comicID)
	if err != nil {
		return model.Comic{}, nil, err
	}
	result, err := a.call(ctx, "details", map[string]any{
		"content":   content,
		"fetchMode": "FORCE_REFRESH",
	})
	if err != nil {
		return model.Comic{}, nil, err
	}
	item, err := objectField(result, "content")
	if err != nil {
		return model.Comic{}, nil, err
	}
	comic := a.toComic(item)
	values, _ := arrayField(item, "chapters")
	chapters := make([]model.Chapter, 0, len(values))
	for _, value := range values {
		child, ok := value.(map[string]any)
		if !ok {
			continue
		}
		id, _ := encodeObject(child)
		number := floatValue(child["number"])
		chapters = append(chapters, model.Chapter{
			ID:        id,
			ComicID:   comicID,
			Title:     chapterTitle(child),
			Order:     number,
			UpdatedAt: unixTime(child["uploadDate"]),
			URL:       stringValue(child["url"]),
		})
	}
	normalizeChapters(chapters)
	return comic, chapters, nil
}

func (a *Adapter) searchResult(result map[string]any, page int) (model.SearchResult, error) {
	values, err := arrayField(result, "content")
	if err != nil {
		return model.SearchResult{}, err
	}
	items := make([]model.Comic, 0, len(values))
	for _, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		items = append(items, a.toComic(item))
	}
	return model.SearchResult{
		Items:   items,
		Page:    page,
		Pages:   page + boolInt(len(items) >= pageSize),
		HasMore: len(items) >= pageSize,
	}, nil
}

func (a *Adapter) toComic(item map[string]any) model.Comic {
	id, _ := encodeObject(item)
	comic := model.Comic{
		SourceID:    a.info.ID,
		ID:          id,
		Title:       firstNonEmpty(stringValue(item["title"]), stringValue(item["name"])),
		Cover:       firstNonEmpty(stringValue(item["largeCoverUrl"]), stringValue(item["coverUrl"])),
		Description: stringValue(item["description"]),
		Status:      stringValue(item["state"]),
		Author:      joinStrings(item["authors"]),
		Tags:        tagTitles(item["tags"]),
	}
	if values, ok := item["chapters"].([]any); ok {
		comic.ChapterCount = len(values)
	}
	return comic
}

func (a *Adapter) readArtifact(result map[string]any) ([]byte, string, error) {
	artifact, ok := result["artifact"].(map[string]any)
	if !ok {
		return nil, "", errors.New("解析器没有返回图片文件")
	}
	relativePath := stringValue(artifact["relativePath"])
	path := a.manager.ImagePath(relativePath)
	if path == "" {
		return nil, "", errors.New("解析器返回了不安全图片路径")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	return data, stringValue(artifact["contentType"]), nil
}

// KindForContentType follows Kototoro's content taxonomy: novels are books,
// comics/manhwa/manhua are comics, and all other media remain discoverable
// under the Other sources section instead of being mislabeled as comics.
func KindForContentType(contentType string) string {
	value := strings.ToUpper(strings.TrimSpace(contentType))
	switch {
	case strings.Contains(value, "NOVEL"):
		return source.KindBook
	case strings.Contains(value, "MANGA"),
		strings.Contains(value, "MANHWA"),
		strings.Contains(value, "MANHUA"),
		strings.Contains(value, "COMIC"):
		return source.KindComic
	case value == "":
		return source.KindComic
	default:
		return source.KindOther
	}
}

func encodeObject(value map[string]any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return "kx:" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeObject(value string) (map[string]any, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "kx:")
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("无效的解析器资源标识: %w", err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("解析资源标识: %w", err)
	}
	return result, nil
}

func objectField(result map[string]any, key string) (map[string]any, error) {
	value, ok := result[key].(map[string]any)
	if !ok || value == nil {
		return nil, fmt.Errorf("解析器响应缺少 %s", key)
	}
	return value, nil
}

func arrayField(result map[string]any, key string) ([]any, error) {
	value, ok := result[key].([]any)
	if !ok {
		return nil, fmt.Errorf("解析器响应缺少 %s", key)
	}
	return value, nil
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
}

func floatValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case json.Number:
		result, _ := typed.Float64()
		return result
	case string:
		result, _ := strconv.ParseFloat(typed, 64)
		return result
	default:
		return 0
	}
}

func unixTime(value any) string {
	seconds := int64(floatValue(value))
	if seconds <= 0 {
		return ""
	}
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}

func chapterTitle(item map[string]any) string {
	if value := stringValue(item["title"]); value != "" {
		return value
	}
	number := floatValue(item["number"])
	if number > 0 {
		return fmt.Sprintf("第%s话", strconv.FormatFloat(number, 'f', -1, 64))
	}
	return "章节"
}

func joinStrings(value any) string {
	values, ok := value.([]any)
	if !ok {
		return ""
	}
	items := make([]string, 0, len(values))
	for _, item := range values {
		if text := stringValue(item); text != "" {
			items = append(items, text)
		}
	}
	return strings.Join(items, " / ")
}

func tagTitles(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, item := range values {
		child, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if title := firstNonEmpty(stringValue(child["title"]), stringValue(child["key"])); title != "" {
			result = append(result, title)
		}
	}
	return result
}

func stringMap(value any) map[string]string {
	values, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, item := range values {
		if text := stringValue(item); text != "" {
			result[key] = text
		}
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func normalizeChapters(chapters []model.Chapter) {
	if len(chapters) < 2 {
		for index := range chapters {
			chapters[index].Order = float64(index + 1)
		}
		return
	}
	descending := chapters[0].Order > chapters[len(chapters)-1].Order
	if descending {
		for left, right := 0, len(chapters)-1; left < right; left, right = left+1, right-1 {
			chapters[left], chapters[right] = chapters[right], chapters[left]
		}
	}
	sort.SliceStable(chapters, func(i, j int) bool {
		if chapters[i].Order == chapters[j].Order {
			return chapters[i].UpdatedAt < chapters[j].UpdatedAt
		}
		return chapters[i].Order < chapters[j].Order
	})
	for index := range chapters {
		chapters[index].Order = float64(index + 1)
	}
}
