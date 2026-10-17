package sources

import "testing"

func TestBuiltInSourcesAreNotPluginRuntimeSources(t *testing.T) {
	registry := NewRegistry(nil, nil, nil)
	seenBookSource := false
	for _, item := range registry.List() {
		switch item.ID {
		case "picacg", "jmcomic", "baozimh", "zlibrary", "biquge":
			if item.PluginSource {
				t.Fatalf("built-in source %q must not be marked as a plugin runtime source", item.ID)
			}
			if item.ID == "biquge" {
				seenBookSource = item.Kind == "book"
			}
		}
	}
	if !seenBookSource {
		t.Fatal("biquge must be registered as a built-in book source")
	}
}
