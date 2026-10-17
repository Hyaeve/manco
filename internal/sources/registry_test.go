package sources

import "testing"

func TestBuiltInSourcesAreNotPluginRuntimeSources(t *testing.T) {
	registry := NewRegistry(nil, nil, nil)
	seenBookSource := false
	var bookOrder []string
	for _, item := range registry.List() {
		if item.Kind == "book" {
			bookOrder = append(bookOrder, item.ID)
		}
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
	if len(bookOrder) == 0 || bookOrder[0] != "biquge" {
		t.Fatalf("biquge should be the first book source, got %v", bookOrder)
	}
}
