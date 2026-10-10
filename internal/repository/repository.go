package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
)

var supportedKinds = map[string]bool{
	"json":        true,
	"jar":         true,
	"mihon":       true,
	"aniyomi":     true,
	"ireader":     true,
	"cloudstream": true,
	"tsundoku":    true,
}

var slugPattern = regexp.MustCompile(`[^a-z0-9_-]+`)

type client interface {
	Do(*http.Request) (*http.Response, error)
}

func NormalizeKind(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if supportedKinds[value] {
		return value
	}
	return "json"
}

func SupportedKinds() []string {
	return []string{"json", "jar", "mihon", "aniyomi", "ireader", "cloudstream", "tsundoku"}
}

// Sync downloads a repository index and converts common Kototoro, Mihon and
// generic JSON descriptions into the source metadata Manco can persist.
func Sync(ctx context.Context, httpClient client, repo model.ExtensionRepository) ([]model.RepositoryExtension, error) {
	address := strings.TrimSpace(repo.URL)
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("仓库地址无效")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("仓库只支持 http 或 https 地址")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json, application/octet-stream, text/plain")
	request.Header.Set("User-Agent", source.DefaultUserAgent)
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("仓库返回 HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 12<<20))
	if err != nil {
		return nil, err
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("仓库清单不是可识别的 JSON：%w", err)
	}
	items := parsePayload(payload, parsed, NormalizeKind(repo.Kind))
	if len(items) == 0 {
		return nil, fmt.Errorf("仓库中没有可识别的扩展或配置源")
	}
	return items, nil
}

func parsePayload(payload any, base *url.URL, repoKind string) []model.RepositoryExtension {
	switch value := payload.(type) {
	case []any:
		out := make([]model.RepositoryExtension, 0, len(value))
		for index, entry := range value {
			object, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if nested := nestedExtensions(object, base, repoKind); len(nested) > 0 {
				out = append(out, nested...)
				continue
			}
			out = append(out, extensionFromMap(object, base, repoKind, index))
		}
		return compact(out)
	case map[string]any:
		if nested := nestedExtensions(value, base, repoKind); len(nested) > 0 {
			return compact(nested)
		}
		return compact([]model.RepositoryExtension{extensionFromMap(value, base, repoKind, 0)})
	default:
		return nil
	}
}

func nestedExtensions(object map[string]any, base *url.URL, repoKind string) []model.RepositoryExtension {
	var out []model.RepositoryExtension
	for _, key := range []string{"extensions", "extensionList", "packages", "plugins"} {
		list, ok := object[key].([]any)
		if !ok {
			continue
		}
		for index, entry := range list {
			child, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, extensionFromMap(child, base, repoKind, index))
		}
	}
	if sources, ok := object["sources"].([]any); ok {
		parent := object
		for index, entry := range sources {
			child, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			merged := copyMap(parent)
			delete(merged, "sources")
			for key, value := range child {
				merged[key] = value
			}
			merged["kind"] = sourceKind(child)
			out = append(out, extensionFromMap(merged, base, repoKind, index))
		}
	}
	return out
}

func extensionFromMap(object map[string]any, base *url.URL, repoKind string, index int) model.RepositoryExtension {
	name := firstString(object, "name", "title", "label")
	packageName := firstString(object, "packageName", "pkg", "package", "id")
	if name == "" {
		name = packageName
	}
	if name == "" {
		name = fmt.Sprintf("扩展 %d", index+1)
	}
	version := firstString(object, "versionName", "version")
	if version == "" {
		version = numberString(object["versionCode"])
	}
	if version == "" {
		version = numberString(object["code"])
	}
	kind := sourceKind(object)
	homepage := absolute(base, firstString(object, "homepage", "homeUrl", "website", "site"))
	icon := absolute(base, firstString(object, "icon", "iconUrl", "logo", "pic"))
	installURL := absolute(base, firstString(object, "jarUrl", "apkUrl", "installUrl", "downloadUrl", "apk", "jar"))
	if installURL == "" {
		if resources, ok := object["resources"].(map[string]any); ok {
			installURL = absolute(base, firstString(resources, "jarUrl", "apkUrl", "url"))
			if icon == "" {
				icon = absolute(base, firstString(resources, "iconUrl", "icon"))
			}
		}
	}
	if homepage == "" {
		if sources, ok := object["sources"].([]any); ok && len(sources) > 0 {
			if child, ok := sources[0].(map[string]any); ok {
				homepage = absolute(base, firstString(child, "homeUrl", "homepage", "url"))
			}
		}
	}
	config := configFromMap(object)
	installable := len(config) > 0 && homepage != ""
	idSource := firstNonEmpty(packageName, name) + "|" + firstNonEmpty(version, strconv.Itoa(index))
	return model.RepositoryExtension{
		ID:          stableID(idSource),
		Name:        name,
		PackageName: packageName,
		Version:     version,
		Kind:        kind,
		PluginType:  repoKind,
		Description: firstString(object, "description", "summary"),
		Homepage:    homepage,
		Icon:        icon,
		InstallURL:  installURL,
		Installable: installable,
		Config:      config,
		Raw:         marshalRaw(object),
	}
}

func configFromMap(object map[string]any) json.RawMessage {
	config := map[string]any{}
	for _, key := range []string{
		"searchUrl", "browseUrl", "itemSelector", "titleSelector", "coverSelector", "linkSelector",
		"detailTitleSelector", "authorSelector", "descriptionSelector", "chapterSelector",
		"chapterTitleSelector", "chapterLinkSelector", "pageImageSelector", "contentSelector",
	} {
		if value := strings.TrimSpace(firstString(object, key)); value != "" {
			config[key] = value
		}
	}
	if nested, ok := object["config"].(map[string]any); ok {
		for key, value := range nested {
			config[key] = value
		}
	}
	if hosts, ok := object["allowedHosts"].([]any); ok {
		values := make([]string, 0, len(hosts))
		for _, entry := range hosts {
			if text := strings.TrimSpace(fmt.Sprint(entry)); text != "" {
				values = append(values, text)
			}
		}
		if len(values) > 0 {
			config["allowedHosts"] = values
		}
	}
	if len(config) == 0 {
		return nil
	}
	raw, _ := json.Marshal(config)
	return raw
}

func sourceKind(object map[string]any) string {
	value := strings.ToLower(firstString(object, "kind", "type", "contentType", "mediaType"))
	if strings.Contains(value, "book") || strings.Contains(value, "novel") || value == "text" {
		return "book"
	}
	return "comic"
}

func compact(items []model.RepositoryExtension) []model.RepositoryExtension {
	out := make([]model.RepositoryExtension, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		if item.Name == "" {
			continue
		}
		key := item.PackageName + "|" + item.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

func firstString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := object[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return strings.TrimSpace(typed)
			}
		case json.Number:
			if typed.String() != "" {
				return typed.String()
			}
		}
	}
	return ""
}

func numberString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func absolute(base *url.URL, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	if parsed.IsAbs() {
		return parsed.String()
	}
	return base.ResolveReference(parsed).String()
}

func stableID(value string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(value))))
	return "repo-" + hex.EncodeToString(sum[:8])
}

func copyMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func marshalRaw(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return raw
}

func Slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = slugPattern.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return stableID(value)
	}
	if len(value) > 56 {
		value = value[:56]
	}
	return value
}
