package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var moduleConfigFiles = []string{
	"settings.json",
	"accounts.json",
	"sources.json",
	"repositories.json",
	"subscriptions.json",
}

// migrateLegacyConfig preserves configuration written by older container
// layouts. The operation is intentionally additive: a file that already exists
// in the mounted /app/config directory always wins.
func migrateLegacyConfig(dataDir, configDir string) error {
	configDir = filepath.Clean(strings.TrimSpace(configDir))
	if configDir == "" || configDir == "." {
		return nil
	}
	candidates := []string{
		filepath.Join(dataDir, "config"),
		"/app/data/config",
		"/data/config",
		filepath.Join(filepath.Dir(dataDir), "config"),
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		candidate = filepath.Clean(strings.TrimSpace(candidate))
		if candidate == "." || candidate == configDir || seen[candidate] {
			continue
		}
		seen[candidate] = true
		if info, err := os.Stat(candidate); err != nil || !info.IsDir() {
			continue
		}
		for _, name := range moduleConfigFiles {
			if err := copyFileIfMissing(filepath.Join(candidate, name), filepath.Join(configDir, name), 0o600); err != nil {
				return err
			}
		}
		if err := copyFileIfMissing(filepath.Join(candidate, "secret.key"), filepath.Join(configDir, "secret.key"), 0o600); err != nil {
			return err
		}
	}

	// The application secret historically lived beside the SQLite database.
	if err := copyFileIfMissing(filepath.Join(dataDir, ".secret"), filepath.Join(configDir, "secret.key"), 0o600); err != nil {
		return err
	}
	return nil
}

func copyFileIfMissing(source, target string, mode os.FileMode) error {
	if source == "" || target == "" || filepath.Clean(source) == filepath.Clean(target) {
		return nil
	}
	if _, err := os.Stat(target); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat config target %s: %w", target, err)
	}
	raw, err := os.ReadFile(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read legacy config %s: %w", source, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	temp := target + ".migrate.tmp"
	if err := os.WriteFile(temp, raw, mode); err != nil {
		return fmt.Errorf("write config migration %s: %w", target, err)
	}
	if err := os.Rename(temp, target); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("commit config migration %s: %w", target, err)
	}
	return nil
}
