package source

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/secret"
)

var ErrAuthRequired = errors.New("source account is not connected")

// Source kinds. Comic sources yield image chapters packed into CBZ files;
// book sources yield per-chapter text files instead.
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

type LoginResult struct {
	Token    string
	Username string
	Extra    map[string]any
}

// ContentSource is implemented by book sources whose chapter is plain text.
// The downloader persists one file per chapter. Sources that expose a real
// ebook file should implement FileContentSource, which takes precedence.
type ContentSource interface {
	ChapterContent(ctx context.Context, account Account, bookID string, chapter model.Chapter) (string, error)
}

// FileContentSource is implemented by book sources that expose a real binary
// file (for example EPUB, PDF, MOBI or AZW3). The downloader prefers this
// interface over ContentSource so the original ebook is persisted unchanged.
type FileContentSource interface {
	ChapterFile(ctx context.Context, account Account, bookID string, chapter model.Chapter) (data []byte, filename string, err error)
}

// ImageSource is implemented by sources that materialize page and cover
// bytes through a source-specific runtime instead of a plain HTTP GET.
type ImageSource interface {
	PageImage(ctx context.Context, account Account, comicID string, chapter model.Chapter, page model.Page) ([]byte, string, error)
	CoverImage(ctx context.Context, account Account, comic model.Comic) ([]byte, string, error)
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
