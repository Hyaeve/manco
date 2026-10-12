package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLegacyConfigCopiesMissingFilesAndPreservesCurrent(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	configDir := filepath.Join(root, "config")
	legacyDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(legacyDir, "settings.json"), `{"legacy":true}`)
	writeTestFile(t, filepath.Join(legacyDir, "subscriptions.json"), `[{"id":7}]`)
	writeTestFile(t, filepath.Join(dataDir, ".secret"), "legacy-secret\n")
	writeTestFile(t, filepath.Join(configDir, "settings.json"), `{"current":true}`)

	if err := migrateLegacyConfig(dataDir, configDir); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, filepath.Join(configDir, "settings.json")); got != `{"current":true}` {
		t.Fatalf("current settings were overwritten: %q", got)
	}
	if got := readTestFile(t, filepath.Join(configDir, "subscriptions.json")); got != `[{"id":7}]` {
		t.Fatalf("legacy subscriptions were not copied: %q", got)
	}
	if got := readTestFile(t, filepath.Join(configDir, "secret.key")); got != "legacy-secret\n" {
		t.Fatalf("legacy secret was not copied: %q", got)
	}
}

func TestMigrateLegacyConfigIgnoresSameDirectory(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "settings.json"), `{"x":1}`)
	if err := migrateLegacyConfig(root, root); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, filepath.Join(root, "settings.json")); got != `{"x":1}` {
		t.Fatalf("same-directory migration modified file: %q", got)
	}
}

func writeTestFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
