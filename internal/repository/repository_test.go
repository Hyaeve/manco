package repository

import (
	"encoding/json"
	"net/url"
	"testing"
)

// The Kototoro parsers index stores only the bare payload name (`plugin.jar`)
// while the file actually lives in the repository's `apk/` folder.
func TestLegacyKototoroIndexResolvesAPKFolder(t *testing.T) {
	base, err := url.Parse("https://raw.githubusercontent.com/skepsun/kototoro-parsers/repo/index.min.json")
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}
	raw := []byte(`[
  {"name":"Kototoro Parsers","pkg":"org.skepsun.kototoro.parsers","version":"1.0.137","code":137,"apk":"plugin.jar","lang":"all","nsfw":0}
]`)
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	items := parsePayload(payload, base, "json")
	if len(items) != 1 {
		t.Fatalf("expected 1 extension, got %d", len(items))
	}
	want := "https://raw.githubusercontent.com/skepsun/kototoro-parsers/repo/apk/plugin.jar"
	if items[0].InstallURL != want {
		t.Fatalf("install url = %q, want %q", items[0].InstallURL, want)
	}
	if items[0].Runtime != "plugin" {
		t.Fatalf("runtime = %q, want plugin", items[0].Runtime)
	}
	if !items[0].Installable {
		t.Fatalf("Kototoro JAR extension must be installable through the embedded runtime")
	}
	if items[0].PluginType != "jar" {
		t.Fatalf("plugin type = %q, want jar", items[0].PluginType)
	}
}

func TestMihonPluginStaysInventoryOnly(t *testing.T) {
	base, err := url.Parse("https://raw.githubusercontent.com/keiyoushi/extensions/repo/index.pb")
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}
	object := map[string]any{
		"name": "Mihon Extension",
		"pkg":  "eu.kanade.tachiyomi.extension.example",
		"apk":  "example.apk",
	}
	item := extensionFromMap(object, base, "mihon", 0)
	if item.Installable {
		t.Fatalf("Mihon plugin must stay inventory-only until its own runtime is integrated")
	}
	if item.PluginType == "jar" {
		t.Fatalf("Mihon APK must not be classified as a Kototoro JAR")
	}
}

func TestPluginRelativePathKeepsExplicitPaths(t *testing.T) {
	cases := map[string]string{
		"plugin.jar":                 "apk/plugin.jar",
		"apk/plugin.jar":             "apk/plugin.jar",
		"https://cdn.example/pl.jar": "https://cdn.example/pl.jar",
		"":                           "",
	}
	for input, want := range cases {
		if got := pluginRelativePath(input); got != want {
			t.Fatalf("pluginRelativePath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestJSONSelectorSourceStaysInstallable(t *testing.T) {
	base, err := url.Parse("https://example.com/repo/index.json")
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}
	object := map[string]any{
		"name":         "示例漫画",
		"homepage":     "https://example.com",
		"baseUrl":      "https://example.com",
		"searchUrl":    "https://example.com/search?q={q}&page={page}",
		"itemSelector": ".comic",
	}
	item := extensionFromMap(object, base, "json", 0)
	if !item.Installable || item.Runtime != "selector" {
		t.Fatalf("selector source should be installable, got installable=%v runtime=%q", item.Installable, item.Runtime)
	}
}
