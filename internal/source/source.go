package source

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/secret"
)

var ErrAuthRequired = errors.New("source account is not connected")

// DefaultReloginDays is the keep-alive window applied to login sources that
// have not stored their own re-login interval.
const DefaultReloginDays = 7

// Source kinds. Comic sources yield image chapters packed into CBZ files;
// book sources prefer generated CBZ and otherwise preserve native EPUB/PDF;
// other sources preserve the native file type reported by the source.
const (
	KindComic = "comic"
	KindBook  = "book"
	KindOther = "other"
)

type Account struct {
	Username  string
	Password  string
	Token     string
	Cookie    string
	UserAgent string
	HomeURL   string
	Extra     map[string]any
}

// AccountReloginDays returns the configured keep-alive window in days. A
// missing or non-positive value falls back to fallback.
func AccountReloginDays(account Account, fallback int) int {
	if account.Extra != nil {
		switch value := account.Extra["reloginDays"].(type) {
		case float64:
			if value > 0 {
				return int(value)
			}
		case int:
			if value > 0 {
				return value
			}
		case int64:
			if value > 0 {
				return int(value)
			}
		case json.Number:
			if parsed, err := value.Int64(); err == nil && parsed > 0 {
				return int(parsed)
			}
		case string:
			if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && parsed > 0 {
				return parsed
			}
		}
	}
	return fallback
}

// AccountLastLogin reports when the account credentials were last refreshed.
func AccountLastLogin(account Account) time.Time {
	if account.Extra == nil {
		return time.Time{}
	}
	raw, _ := account.Extra["lastLoginAt"].(string)
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// SetAccountReloginDays stores the keep-alive window in the account extras.
func SetAccountReloginDays(account *Account, days int) {
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	account.Extra["reloginDays"] = days
}

// MarkAccountLoggedIn records the current time as the last successful login.
func MarkAccountLoggedIn(account *Account, now time.Time) {
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	account.Extra["lastLoginAt"] = now.UTC().Format(time.RFC3339)
}

func AccountFromModel(box *secret.Box, stored model.SourceAccount) (Account, error) {
	password, err := box.Decrypt(stored.SecretCipher)
	if err != nil {
		return Account{}, err
	}
	token, err := box.Decrypt(stored.TokenCipher)
	if err != nil {
		return Account{}, err
	}
	cookie, err := box.Decrypt(stored.CookieCipher)
	if err != nil {
		return Account{}, err
	}
	userAgent, err := box.Decrypt(stored.UserAgentCipher)
	if err != nil {
		return Account{}, err
	}
	extra := map[string]any{}
	if len(stored.Extra) > 0 {
		_ = json.Unmarshal(stored.Extra, &extra)
	}
	return Account{
		Username:  stored.Username,
		Password:  password,
		Token:     token,
		Cookie:    cookie,
		UserAgent: userAgent,
		HomeURL:   stored.HomeURL,
		Extra:     extra,
	}, nil
}

type Source interface {
	Info() model.SourceInfo
	Search(ctx context.Context, account Account, query string, page int) (model.SearchResult, error)
	Browse(ctx context.Context, account Account, options model.BrowseOptions, page int) (model.SearchResult, error)
	Detail(ctx context.Context, account Account, comicID string) (model.Comic, error)
	Chapters(ctx context.Context, account Account, comicID string) ([]model.Chapter, error)
	Pages(ctx context.Context, account Account, comicID string, chapter model.Chapter) ([]model.Page, error)
}

// DetailWithChaptersSource is implemented by sources that can return the
// comic details and chapter list from one upstream operation. This avoids a
// second detail request for books and plugin sources whose detail endpoint is
// rate-limited or comparatively expensive.
type DetailWithChaptersSource interface {
	DetailWithChapters(ctx context.Context, account Account, comicID string) (model.ComicDetail, error)
}

// ComicPageURLSource resolves a browser-openable work page from the opaque
// source item identifier without making another upstream request.
type ComicPageURLSource interface {
	ComicPageURL(comicID string) string
}

type LoginResult struct {
	Token    string
	Username string
	Extra    map[string]any
}

// ContentSource is implemented by book sources whose chapter is plain text.
// The downloader packages each chapter as CBZ. Sources that expose a real
// ebook file should implement FileContentSource, which takes precedence.
type ContentSource interface {
	ChapterContent(ctx context.Context, account Account, bookID string, chapter model.Chapter) (string, error)
}

// ChapterArchive is a rich book chapter payload intended for CBZ packaging.
// Plugin runtimes can return HTML plus source-specific image pages whose bytes
// are materialized through ImageSource before the archive is written.
type ChapterArchive struct {
	HTML   string
	Images []model.Page
}

// ChapterArchiveSource is implemented by book sources whose text and images
// should be persisted as CBZ. FileContentSource still takes precedence for
// native ebook binaries.
type ChapterArchiveSource interface {
	ChapterArchive(ctx context.Context, account Account, bookID string, chapter model.Chapter) (ChapterArchive, error)
}

// FileContentSource is implemented by book sources that expose a real binary
// file. The downloader accepts CBZ/EPUB/PDF and prefers those native files over
// generated text packaging.
type FileContentSource interface {
	ChapterFile(ctx context.Context, account Account, bookID string, chapter model.Chapter) (data []byte, filename string, err error)
}

// ImageSource is implemented by sources that materialize page and cover
// bytes through a source-specific runtime instead of a plain HTTP GET.
type ImageSource interface {
	PageImage(ctx context.Context, account Account, comicID string, chapter model.Chapter, page model.Page) ([]byte, string, error)
	CoverImage(ctx context.Context, account Account, comic model.Comic) ([]byte, string, error)
}

// PageFileSource streams source page bytes without loading large media into
// memory. It is preferred over ImageSource when downloading other-source
// chapters such as video, audio, PDF or native archives.
type PageFileSource interface {
	PageFile(ctx context.Context, account Account, comicID string, chapter model.Chapter, page model.Page) (io.ReadCloser, string, string, error)
}

// MediaSource resolves a playable resource and its runtime-owned headers.
// Video bytes must not pass through image validation or image size limits.
type MediaSource interface {
	ResolveMedia(ctx context.Context, account Account, page model.Page) (model.Page, error)
}

// FilterSource is implemented by sources that discover their filter controls
// lazily instead of exposing a static list from Info.
type FilterSource interface {
	Filters(ctx context.Context) ([]model.FilterGroup, error)
}

// KindOf reports the source kind, defaulting to comic when unspecified.
func KindOf(info model.SourceInfo) string {
	if info.Kind == KindBook {
		return KindBook
	}
	if info.Kind == KindOther {
		return KindOther
	}
	return KindComic
}

type LoginSource interface {
	Login(ctx context.Context, account Account) (LoginResult, error)
}

type Clock interface {
	Now() time.Time
}
