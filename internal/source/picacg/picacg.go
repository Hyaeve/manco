package picacg

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
)

const (
	apiKey    = "C69BAF41DA5ABD1FFEDC6D2FEA56B"
	secretKey = "~d}$Q7$eIni=V)9\\RK/P.RM4;9[7|@/CA}b~OW!3?EV`:<>M7pddUBL5n|0/*Cn"
	nonce     = "4ce7a7aa759b40f794d189a88b84aba8"
)

type Source struct {
	client *http.Client
}

func New(client *http.Client) *Source { return &Source{client: client} }

func (s *Source) Info() model.SourceInfo {
	return model.SourceInfo{
		ID:          "picacg",
		Name:        "哔咔漫画",
		Description: "Pica Comic API source",
		Homepage:    "https://picaapi.go2778.com/",
		NeedsLogin:  true,
		CanSearch:   true,
		CanBrowse:   true,
	}
}

func (s *Source) Login(ctx context.Context, account source.Account) (source.LoginResult, error) {
	if strings.TrimSpace(account.Username) == "" || account.Password == "" {
		return source.LoginResult{}, errors.New("哔咔账号和密码不能为空")
	}
	body := map[string]any{"email": account.Username, "password": account.Password}
	var response struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := s.request(ctx, account, http.MethodPost, "/auth/sign-in", nil, body, &response); err != nil {
		return source.LoginResult{}, err
	}
	if response.Data.Token == "" {
		return source.LoginResult{}, errors.New("哔咔接口未返回 token")
	}
	return source.LoginResult{Token: response.Data.Token, Username: account.Username}, nil
}

func (s *Source) Search(ctx context.Context, account source.Account, query string, page int) (model.SearchResult, error) {
	if page < 1 {
		page = 1
	}
	queryValues := url.Values{"page": {strconv.Itoa(page)}}
	body := map[string]any{"keyword": query, "sort": "dd"}
	var response map[string]any
	if err := s.request(ctx, account, http.MethodPost, "/comics/advanced-search", queryValues, body, &response); err != nil {
		return model.SearchResult{}, err
	}
	return s.parseComicPage(response, page)
}

func (s *Source) Browse(ctx context.Context, account source.Account, kind string, page int) (model.SearchResult, error) {
	if page < 1 {
		page = 1
	}
	query := url.Values{"page": {strconv.Itoa(page)}, "s": {"dd"}}
	if strings.TrimSpace(kind) != "" && kind != "latest" {
		query.Set("c", kind)
	}
	var response map[string]any
	if err := s.request(ctx, account, http.MethodGet, "/comics", query, nil, &response); err != nil {
		return model.SearchResult{}, err
	}
	return s.parseComicPage(response, page)
}

func (s *Source) Detail(ctx context.Context, account source.Account, comicID string) (model.Comic, error) {
	var response map[string]any
	if err := s.request(ctx, account, http.MethodGet, "/comics/"+url.PathEscape(comicID), nil, nil, &response); err != nil {
		return model.Comic{}, err
	}
	data := object(response["data"])
	return comicFromMap(object(data["comic"]), account.HomeURL), nil
}

func (s *Source) Chapters(ctx context.Context, account source.Account, comicID string) ([]model.Chapter, error) {
	chapters := make([]model.Chapter, 0, 32)
	for page := 1; page <= 100; page++ {
		query := url.Values{"page": {strconv.Itoa(page)}}
		var response map[string]any
		if err := s.request(ctx, account, http.MethodGet, "/comics/"+url.PathEscape(comicID)+"/eps", query, nil, &response); err != nil {
			return nil, err
		}
		data := object(response["data"])
		eps := object(data["eps"])
		docs := array(eps["docs"])
		for _, item := range docs {
			row := object(item)
			chapters = append(chapters, model.Chapter{
				ID:      text(row["_id"]),
				ComicID: comicID,
				Title:   source.FirstNonEmpty(text(row["title"]), "第 "+text(row["order"])+" 话"),
				Order:   number(row["order"]),
			})
		}
		pages := int(number(eps["pages"]))
		if pages <= page || len(docs) == 0 {
			break
		}
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
	order := int(chapter.Order)
	if order <= 0 {
		order = 1
	}
	pages := make([]model.Page, 0, 32)
	for page := 1; page <= 100; page++ {
		query := url.Values{"page": {strconv.Itoa(page)}}
		path := fmt.Sprintf("/comics/%s/order/%d/pages", url.PathEscape(comicID), order)
		var response map[string]any
		if err := s.request(ctx, account, http.MethodGet, path, query, nil, &response); err != nil {
			return nil, err
		}
		data := object(response["data"])
		pageData := object(data["pages"])
		docs := array(pageData["docs"])
		for _, item := range docs {
			row := object(item)
			media := object(row["media"])
			fileServer := text(media["fileServer"])
			filePath := text(media["path"])
			pageURL := normalizeMediaURL(fileServer, filePath)
			if pageURL == "" {
				continue
			}
			pages = append(pages, model.Page{
				URL:          pageURL,
				Referer:      strings.TrimRight(source.FirstNonEmpty(account.HomeURL, "https://picaapi.go2778.com"), "/") + "/",
				FileName:     source.FirstNonEmpty(text(media["originalName"]), filepath.Base(filePath)),
				Alternatives: mediaAlternatives(fileServer, filePath),
			})
		}
		pagesTotal := int(number(pageData["pages"]))
		if pagesTotal <= page || len(docs) == 0 {
			break
		}
	}
	return pages, nil
}

func (s *Source) request(ctx context.Context, account source.Account, method, path string, query url.Values, body any, out any) error {
	if method != http.MethodPost && method != http.MethodGet {
		return errors.New("unsupported method")
	}
	if account.Token == "" && path != "/auth/sign-in" {
		return source.ErrAuthRequired
	}
	base := strings.TrimSpace(account.HomeURL)
	if base == "" {
		base = "https://picaapi.go2778.com"
	}
	address := source.BuildURL(base, path, query)
	signaturePath := path
	if len(query) > 0 {
		signaturePath += "?" + query.Encode()
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := sign(signaturePath, timestamp, method)
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	request, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		return err
	}
	setHeaders(request.Header)
	request.Header.Set("time", timestamp)
	request.Header.Set("signature", signature)
	request.Header.Set("authorization", account.Token)
	request.Header.Set("image-quality", "original")
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var payload map[string]any
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&payload); err != nil {
		return fmt.Errorf("decode Pica response: %w", err)
	}
	code := int(number(payload["code"]))
	if response.StatusCode < 200 || response.StatusCode >= 300 || (code != 0 && code != 200) {
		message := source.FirstNonEmpty(text(payload["message"]), response.Status)
		return fmt.Errorf("Pica API: %s (code %d)", message, code)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func setHeaders(headers http.Header) {
	headers.Set("accept", "application/vnd.picacomic.com.v1+json")
	headers.Set("User-Agent", "okhttp/3.8.1")
	headers.Set("Content-Type", "application/json; charset=UTF-8")
	headers.Set("api-key", apiKey)
	headers.Set("app-build-version", "45")
	headers.Set("app-platform", "android")
	headers.Set("app-uuid", "defaultUuid")
	headers.Set("app-version", "2.2.1.3.3.4")
	headers.Set("nonce", nonce)
	headers.Set("app-channel", "1")
}

func sign(path, timestamp, method string) string {
	key := strings.ToLower(path + timestamp + nonce + method + apiKey)
	mac := hmac.New(sha256.New, []byte(secretKey))
	_, _ = mac.Write([]byte(key))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Source) parseComicPage(response map[string]any, page int) (model.SearchResult, error) {
	data := object(response["data"])
	comics := object(data["comics"])
	docs := array(comics["docs"])
	items := make([]model.Comic, 0, len(docs))
	for _, item := range docs {
		items = append(items, comicFromMap(object(item), ""))
	}
	pages := int(number(comics["pages"]))
	return model.SearchResult{Items: items, Page: page, Pages: pages, Total: int(number(comics["total"])), HasMore: pages > page && len(items) > 0}, nil
}

func comicFromMap(row map[string]any, home string) model.Comic {
	return model.Comic{
		SourceID:     "picacg",
		ID:           source.FirstNonEmpty(text(row["_id"]), text(row["id"])),
		Title:        source.FirstNonEmpty(text(row["title"]), "未命名"),
		Cover:        normalizeMediaMap(object(row["thumb"])),
		Author:       text(row["author"]),
		Description:  text(row["description"]),
		Tags:         stringsFrom(row["tags"]),
		Status:       boolStatus(row["finished"]),
		ChapterCount: int(number(row["epsCount"])),
		UpdatedAt:    text(row["updated_at"]),
	}
}

func normalizeMediaMap(media map[string]any) string {
	return normalizeMediaURL(text(media["fileServer"]), text(media["path"]))
}

func normalizeMediaURL(fileServer, filePath string) string {
	fileServer = strings.TrimRight(strings.TrimSpace(fileServer), "/")
	filePath = strings.TrimLeft(strings.TrimSpace(filePath), "/")
	if fileServer == "" || filePath == "" {
		return ""
	}
	if strings.Contains(fileServer, "static") {
		return fileServer + "/" + filePath
	}
	return fileServer + "/static/" + filePath
}

func mediaAlternatives(fileServer, filePath string) []string {
	primary := normalizeMediaURL(fileServer, filePath)
	proxy := strings.Replace(primary, "picacomic", "go2778", 1)
	if proxy == primary {
		proxy = strings.Replace(primary, "go2778", "picacomic", 1)
	}
	if proxy == "" || proxy == primary {
		return nil
	}
	return []string{proxy}
}

func object(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return map[string]any{}
}

func array(value any) []any {
	if typed, ok := value.([]any); ok {
		return typed
	}
	return nil
}

func text(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func number(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	case string:
		parsed, _ := strconv.ParseFloat(typed, 64)
		return parsed
	default:
		return 0
	}
}

func stringsFrom(value any) []string {
	rows := array(value)
	result := make([]string, 0, len(rows))
	for _, row := range rows {
		if value := strings.TrimSpace(text(row)); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func boolStatus(value any) string {
	if typed, ok := value.(bool); ok {
		if typed {
			return "已完结"
		}
		return "连载中"
	}
	return ""
}

var _ source.Source = (*Source)(nil)
var _ source.LoginSource = (*Source)(nil)
