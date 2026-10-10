package configstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/store"
)

const (
	settingsFile      = "settings.json"
	sourcesFile       = "sources.json"
	repositoriesFile  = "repositories.json"
	subscriptionsFile = "subscriptions.json"
)

// Files persists user-facing configuration separately from the operational
// SQLite database. Each module is written atomically so a partial write cannot
// corrupt the other configuration files.
type Files struct {
	dir string
}

type sourcesDocument struct {
	Custom []model.CustomSource `json:"customSources"`
	Users  []accountDocument    `json:"accounts"`
}

type accountDocument struct {
	SourceID     string          `json:"sourceId"`
	Username     string          `json:"username,omitempty"`
	SecretCipher string          `json:"secretCipher,omitempty"`
	TokenCipher  string          `json:"tokenCipher,omitempty"`
	CookieCipher string          `json:"cookieCipher,omitempty"`
	HomeURL      string          `json:"homeUrl,omitempty"`
	Extra        json.RawMessage `json:"extra,omitempty"`
}

func New(dir string) (*Files, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("config directory is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create config directory: %w", err)
	}
	return &Files{dir: dir}, nil
}

// Restore imports the module files when they exist. Existing SQLite rows are
// updated so a mounted /app/config directory remains the durable source of
// user-facing configuration.
func (f *Files) Restore(ctx context.Context, repository *store.Store) error {
	if f == nil || repository == nil {
		return nil
	}
	if err := f.restoreSettings(ctx, repository); err != nil {
		return err
	}
	if err := f.restoreSources(ctx, repository); err != nil {
		return err
	}
	if err := f.restoreRepositories(ctx, repository); err != nil {
		return err
	}
	return f.restoreSubscriptions(ctx, repository)
}

func (f *Files) PersistSettings(ctx context.Context, repository *store.Store) error {
	if f == nil || repository == nil {
		return nil
	}
	values, err := repository.ListSettings(ctx)
	if err != nil {
		return err
	}
	return f.writeJSON(settingsFile, values)
}

func (f *Files) PersistSources(ctx context.Context, repository *store.Store) error {
	if f == nil || repository == nil {
		return nil
	}
	custom, err := repository.ListCustomSources(ctx)
	if err != nil {
		return err
	}
	accounts, err := repository.ListSourceAccounts(ctx)
	if err != nil {
		return err
	}
	document := sourcesDocument{Custom: custom}
	for _, account := range accounts {
		document.Users = append(document.Users, accountDocument{
			SourceID:     account.SourceID,
			Username:     account.Username,
			SecretCipher: account.SecretCipher,
			TokenCipher:  account.TokenCipher,
			CookieCipher: account.CookieCipher,
			HomeURL:      account.HomeURL,
			Extra:        account.Extra,
		})
	}
	return f.writeJSON(sourcesFile, document)
}

func (f *Files) PersistRepositories(ctx context.Context, repository *store.Store) error {
	if f == nil || repository == nil {
		return nil
	}
	items, err := repository.ListExtensionRepositories(ctx)
	if err != nil {
		return err
	}
	return f.writeJSON(repositoriesFile, items)
}

func (f *Files) PersistSubscriptions(ctx context.Context, repository *store.Store) error {
	if f == nil || repository == nil {
		return nil
	}
	items, err := repository.ListAllSubscriptions(ctx)
	if err != nil {
		return err
	}
	return f.writeJSON(subscriptionsFile, items)
}

func (f *Files) PersistAll(ctx context.Context, repository *store.Store) error {
	if err := f.PersistSettings(ctx, repository); err != nil {
		return err
	}
	if err := f.PersistSources(ctx, repository); err != nil {
		return err
	}
	if err := f.PersistRepositories(ctx, repository); err != nil {
		return err
	}
	return f.PersistSubscriptions(ctx, repository)
}

func (f *Files) restoreSettings(ctx context.Context, repository *store.Store) error {
	var values map[string]string
	if err := f.readJSON(settingsFile, &values); err != nil {
		return err
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := repository.SetSetting(ctx, key, values[key]); err != nil {
			return err
		}
	}
	return nil
}

func (f *Files) restoreSources(ctx context.Context, repository *store.Store) error {
	var document sourcesDocument
	if err := f.readJSON(sourcesFile, &document); err != nil {
		return err
	}
	for _, item := range document.Custom {
		if _, err := repository.UpsertCustomSource(ctx, item); err != nil {
			return err
		}
	}
	for _, item := range document.Users {
		if err := repository.UpsertSourceAccount(ctx, model.SourceAccount{
			SourceID:     item.SourceID,
			Username:     item.Username,
			SecretCipher: item.SecretCipher,
			TokenCipher:  item.TokenCipher,
			CookieCipher: item.CookieCipher,
			HomeURL:      item.HomeURL,
			Extra:        item.Extra,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (f *Files) restoreRepositories(ctx context.Context, repository *store.Store) error {
	var items []model.ExtensionRepository
	if err := f.readJSON(repositoriesFile, &items); err != nil {
		return err
	}
	for _, item := range items {
		if _, err := repository.UpsertExtensionRepository(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (f *Files) restoreSubscriptions(ctx context.Context, repository *store.Store) error {
	var items []model.Subscription
	if err := f.readJSON(subscriptionsFile, &items); err != nil {
		return err
	}
	for _, item := range items {
		if _, err := repository.ImportSubscription(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (f *Files) readJSON(name string, target any) error {
	if f == nil {
		return nil
	}
	path := filepath.Join(f.dir, name)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("parse %s: %w", name, err)
	}
	return nil
}

func (f *Files) writeJSON(name string, value any) error {
	if f == nil {
		return nil
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", name, err)
	}
	finalPath := filepath.Join(f.dir, name)
	tempPath := finalPath + ".tmp"
	if err := os.WriteFile(tempPath, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("replace %s: %w", name, err)
	}
	return nil
}
