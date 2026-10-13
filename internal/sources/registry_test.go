package sources

import "testing"

func TestBuiltInSourcesAreNotPluginRuntimeSources(t *testing.T) {
	registry := NewRegistry(nil, nil, nil)
	for _, item := range registry.List() {
		switch item.ID {
		case "picacg", "jmcomic", "baozimh", "zlibrary":
			if item.PluginSource {
				t.Fatalf("built-in source %q must not be marked as a plugin runtime source", item.ID)
			}
		}
	}
}
