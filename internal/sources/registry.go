package sources

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/secret"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/source/baozimh"
	"github.com/hyaeve/manco/internal/source/jmcomic"
	"github.com/hyaeve/manco/internal/source/picacg"
	"github.com/hyaeve/manco/internal/store"
)

type Registry struct {
	client *http.Client
	box    *secret.Box
	store  *store.Store
	byID   map[string]source.Source
}

func NewRegistry(client *http.Client, box *secret.Box, repository *store.Store) *Registry {
	items := []source.Source{
		picacg.New(client),
		jmcomic.New(client),
		baozimh.New(client),
	}
	registry := &Registry{client: client, box: box, store: repository, byID: make(map[string]source.Source, len(items))}
	for _, item := range items {
		registry.byID[item.Info().ID] = item
	}
	return registry
}

func (r *Registry) List() []model.SourceInfo {
	items := make([]model.SourceInfo, 0, len(r.byID))
	for _, item := range r.byID {
		items = append(items, item.Info())
	}
	order := map[string]int{"picacg": 0, "jmcomic": 1, "baozimh": 2}
	sort.SliceStable(items, func(i, j int) bool {
		left, leftOK := order[items[i].ID]
		right, rightOK := order[items[j].ID]
		if leftOK || rightOK {
			if !leftOK {
				return false
			}
			if !rightOK {
				return true
			}
			if left != right {
				return left < right
			}
		}
		return items[i].ID < items[j].ID
	})
	return items
}

// Register adds or replaces a source adapter. It is used by tests and by
// alternative builds that want to plug in extra sources.
func (r *Registry) Register(item source.Source) {
	info := item.Info()
	if strings.TrimSpace(info.ID) == "" {
		return
	}
	r.byID[info.ID] = item
}

func (r *Registry) Get(id string) (source.Source, error) {
	item, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("unknown source %q", id)
	}
	return item, nil
}

func (r *Registry) Account(ctx context.Context, id string) (source.Account, error) {
	if r.store == nil {
		return source.Account{}, nil
	}
	stored, err := r.store.SourceAccount(ctx, id)
	if err != nil {
		if err == store.ErrNotFound {
			return source.Account{}, nil
		}
		return source.Account{}, err
	}
	return source.AccountFromModel(r.box, stored)
}

func (r *Registry) Credentials(ctx context.Context, id string) (source.Account, error) {
	return r.Account(ctx, id)
}

func (r *Registry) RemoveAccount(ctx context.Context, id string) error {
	if r.store == nil {
		return nil
	}
	return r.store.DeleteSourceAccount(ctx, id)
}

func (r *Registry) Client() *http.Client { return r.client }

// AllowedImageHost reports whether an image host belongs to one of the
// configured comic sources. The image proxy only forwards requests to these
// hosts so it cannot be abused as an open proxy.
func (r *Registry) AllowedImageHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, suffix := range []string{
		".18comic.vip", ".18comic.org", ".18comic.cc",
		".jmapiproxy.cc", ".jmapiproxy1.cc", ".jmapiproxy2.cc",
		".jm-comic.me", ".jm-comic.group", ".jmcomic.me", ".jmcomic.rocks", ".jmcomic1.rocks", ".jmcomic2.rocks", ".jm-comic1.rocks", ".jm-comic2.rocks",
		".baozimh.com", ".baozimh.org", ".baozimhcn.com", ".baozicdn.com", ".bzmgcn.com",
		".webmota.com", ".kukuc.co", ".twmanga.com", ".dinnerku.com", ".twmanhua.com",
		".picacomic.com", ".go2778.com",
	} {
		if host == strings.TrimPrefix(suffix, ".") || strings.HasSuffix(host, suffix) {
			return true
		}
	}
	if r.store != nil {
		accounts, err := r.store.ListSourceAccounts(context.Background())
		if err == nil {
			for _, account := range accounts {
				if account.HomeURL == "" {
					continue
				}
				if parsed, err := url.Parse(account.HomeURL); err == nil && strings.EqualFold(parsed.Hostname(), host) {
					return true
				}
			}
		}
	}
	return false
}
