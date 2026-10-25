package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/pluginruntime"
	"github.com/hyaeve/manco/internal/secret"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/source/baozimh"
	"github.com/hyaeve/manco/internal/source/biquge"
	"github.com/hyaeve/manco/internal/source/generic"
	"github.com/hyaeve/manco/internal/source/jmcomic"
	"github.com/hyaeve/manco/internal/source/picacg"
	"github.com/hyaeve/manco/internal/source/zlibrary"
	"github.com/hyaeve/manco/internal/store"
)

// isPluginRuntime reports whether a persisted source config points at one of
// the JVM runtimes hosted by the embedded sidecar.
func isPluginRuntime(value string) bool {
	value = strings.TrimSpace(value)
	return value == pluginruntime.RuntimeKototoro || value == pluginruntime.RuntimeMihon
}

type Registry struct {
	client        *http.Client
	box           *secret.Box
	store         *store.Store
	byID          map[string]source.Source
	builtins      map[string]bool
	custom        map[string]bool
	pluginSources map[string]bool
	runtime       *pluginruntime.Manager
}

func NewRegistry(client *http.Client, box *secret.Box, repository *store.Store, runtimes ...*pluginruntime.Manager) *Registry {
	var runtimeManager *pluginruntime.Manager
	if len(runtimes) > 0 {
		runtimeManager = runtimes[0]
	}
	items := []source.Source{
		picacg.New(client),
		jmcomic.New(client),
		baozimh.New(client),
		zlibrary.New(client),
		biquge.New(client),
	}
	registry := &Registry{
		client:        client,
		box:           box,
		store:         repository,
		byID:          make(map[string]source.Source, len(items)),
		builtins:      map[string]bool{"picacg": true, "jmcomic": true, "baozimh": true, "zlibrary": true, "biquge": true},
		custom:        map[string]bool{},
		pluginSources: map[string]bool{},
		runtime:       runtimeManager,
	}
	for _, item := range items {
		registry.byID[item.Info().ID] = item
	}
	if repository != nil {
		_ = registry.ReloadCustomSources(context.Background())
	}
	return registry
}

func (r *Registry) List() []model.SourceInfo {
	items := make([]model.SourceInfo, 0, len(r.byID))
	for _, item := range r.byID {
		info := item.Info()
		info.Builtin = r.builtins[info.ID]
		info.Editable = r.custom[info.ID]
		if r.pluginSources[info.ID] {
			info.PluginSource = true
		}
		items = append(items, info)
	}
	order := map[string]int{"baozimh": 0, "picacg": 1, "jmcomic": 2, "biquge": 3, "zlibrary": 4}
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

// ReloadCustomSources rebuilds the non-built-in part of the registry from the
// database. Built-in adapters cannot be replaced by a custom source with the
// same ID.
func (r *Registry) ReloadCustomSources(ctx context.Context) error {
	if r.store == nil {
		return nil
	}
	for id := range r.custom {
		delete(r.byID, id)
	}
	r.custom = map[string]bool{}
	r.pluginSources = map[string]bool{}
	items, err := r.store.ListCustomSources(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		id := strings.ToLower(strings.TrimSpace(item.ID))
		if id == "" || r.builtins[id] {
			continue
		}
		var adapter source.Source
		var err error
		var config pluginruntime.Config
		if json.Unmarshal(item.Config, &config) == nil && isPluginRuntime(config.Runtime) {
			adapter, err = pluginruntime.NewAdapter(r.runtime, item)
			if err == nil {
				r.pluginSources[id] = true
			}
		} else {
			adapter, err = generic.New(r.client, item)
		}
		if err != nil {
			continue
		}
		r.byID[id] = adapter
		r.custom[id] = true
	}
	return nil
}

func (r *Registry) IsBuiltin(id string) bool {
	return r.builtins[strings.ToLower(strings.TrimSpace(id))]
}

// Runtime exposes the JVM sidecar manager. It is nil when the current build
// has no plugin runtime, and callers must handle that.
func (r *Registry) Runtime() *pluginruntime.Manager {
	if r == nil {
		return nil
	}
	return r.runtime
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
			account := source.Account{}
			source.ApplyDefaultAccountUseProxy(id, &account)
			return account, nil
		}
		return source.Account{}, err
	}
	account, err := source.AccountFromModel(r.box, stored)
	if err != nil {
		return source.Account{}, err
	}
	source.ApplyDefaultAccountUseProxy(id, &account)
	return account, nil
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

// KeepAliveLogins re-signs login-based sources whose keep-alive window has
// elapsed so browsing and downloads keep working without manual logins. It
// returns human-readable messages for the caller to write to the run log.
func (r *Registry) KeepAliveLogins(ctx context.Context) []string {
	if r == nil || r.store == nil || r.box == nil {
		return nil
	}
	stored, err := r.store.ListSourceAccounts(ctx)
	if err != nil {
		return []string{fmt.Sprintf("list source accounts: %v", err)}
	}
	now := time.Now().UTC()
	messages := make([]string, 0, len(stored))
	for _, record := range stored {
		item, ok := r.byID[record.SourceID]
		if !ok {
			continue
		}
		loginSource, ok := item.(source.LoginSource)
		if !ok {
			continue
		}
		account, err := source.AccountFromModel(r.box, record)
		if err != nil {
			messages = append(messages, fmt.Sprintf("%s: keep-alive decode account: %v", record.SourceID, err))
			continue
		}
		fallback := 0
		if record.SourceID == "picacg" {
			fallback = source.DefaultReloginDays
		}
		days := source.AccountReloginDays(account, fallback)
		if days <= 0 || strings.TrimSpace(account.Username) == "" || account.Password == "" {
			continue
		}
		if last := source.AccountLastLogin(account); !last.IsZero() && now.Sub(last) < time.Duration(days)*24*time.Hour {
			continue
		}
		result, err := loginSource.Login(ctx, account)
		if err != nil {
			messages = append(messages, fmt.Sprintf("%s: keep-alive login failed: %v", record.SourceID, err))
			continue
		}
		if result.Token != "" {
			account.Token = result.Token
		}
		for key, value := range result.Extra {
			if account.Extra == nil {
				account.Extra = map[string]any{}
			}
			account.Extra[key] = value
		}
		source.MarkAccountLoggedIn(&account, now)
		if err := r.saveAccount(ctx, record, account); err != nil {
			messages = append(messages, fmt.Sprintf("%s: keep-alive save credentials: %v", record.SourceID, err))
			continue
		}
		messages = append(messages, fmt.Sprintf("%s: keep-alive login refreshed (%d day interval)", record.SourceID, days))
	}
	return messages
}

func (r *Registry) saveAccount(ctx context.Context, record model.SourceAccount, account source.Account) error {
	passwordCipher, err := r.box.Encrypt(account.Password)
	if err != nil {
		return err
	}
	tokenCipher, err := r.box.Encrypt(account.Token)
	if err != nil {
		return err
	}
	cookieCipher, err := r.box.Encrypt(account.Cookie)
	if err != nil {
		return err
	}
	userAgentCipher, err := r.box.Encrypt(account.UserAgent)
	if err != nil {
		return err
	}
	extra := account.Extra
	if extra == nil {
		extra = map[string]any{}
	}
	raw, err := json.Marshal(extra)
	if err != nil {
		return err
	}
	return r.store.UpsertSourceAccount(ctx, model.SourceAccount{
		SourceID:        record.SourceID,
		Username:        account.Username,
		SecretCipher:    passwordCipher,
		TokenCipher:     tokenCipher,
		CookieCipher:    cookieCipher,
		UserAgentCipher: userAgentCipher,
		HomeURL:         account.HomeURL,
		Extra:           raw,
	})
}

func (r *Registry) Client() *http.Client { return r.client }

// ClientForAccount returns the HTTP client a source account should use. When
// the account opts out of the configured proxy the request bypasses both the
// runtime proxy and the environment proxy.
func (r *Registry) ClientForAccount(account source.Account) *http.Client {
	if r == nil {
		return nil
	}
	return source.SourceClient(r.client, nil, account)
}

// AllowedImageHost reports whether an image host belongs to one of the
// configured comic sources. The image proxy only forwards requests to these
// hosts so it cannot be abused as an open proxy.
func (r *Registry) AllowedImageHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, suffix := range []string{
		".bgm.tv",
		".18comic.vip", ".18comic.org", ".18comic.cc", ".18mh.org",
		".jmapiproxy.cc", ".jmapiproxy1.cc", ".jmapiproxy2.cc", ".jmapiproxy3.cc",
		".jmapinodeudzn.net",
		".jm-comic.me", ".jm-comic.group", ".jmcomic.me", ".jmcomic.rocks", ".jmcomic1.rocks", ".jmcomic2.rocks", ".jm-comic1.rocks", ".jm-comic2.rocks",
		".baozimh.com", ".baozimh.org", ".baozimhcn.com", ".baozicdn.com", ".bzmgcn.com", ".bzcdn.net",
		".wowenku.com", ".shencou.com", ".wenruya.com",
		".gutenberg.org",
		".biquge345.com",
		".1lib.sk", ".z-library.sk", ".z-lib.io", ".singlelogin.re", ".z-lib.org", ".zlibrary.to",
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
		items, err := r.store.ListCustomSources(context.Background())
		if err == nil {
			for _, item := range items {
				if parsed, err := url.Parse(item.Homepage); err == nil && strings.EqualFold(parsed.Hostname(), host) {
					return true
				}
				var config generic.Config
				if json.Unmarshal(item.Config, &config) == nil {
					for _, allowed := range config.AllowedHosts {
						allowed = strings.ToLower(strings.TrimSpace(allowed))
						if host == strings.TrimPrefix(allowed, ".") || strings.HasSuffix(host, "."+strings.TrimPrefix(allowed, ".")) {
							return true
						}
					}
				}
			}
		}
	}
	return false
}
