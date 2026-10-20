package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hyaeve/manco/internal/store"
)

func TestGlobalSearchHistoryKeepsStoredOrderOnRepeatedReads(t *testing.T) {
	repository, err := store.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	server := &Server{store: repository}
	ctx := context.Background()
	want := []string{"最新搜索", "second", "第三次", "第四次", "第五次"}
	raw, _ := json.Marshal(want)
	if err := repository.SetSetting(ctx, globalSearchHistoryKey, string(raw)); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		got, err := server.globalSearchHistory(ctx)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("history reordered: %v: %v", got, err)
		}
	}
	if _, err := server.recordGlobalSearch(ctx, "second"); err != nil {
		t.Fatal(err)
	}
	got, err := server.globalSearchHistory(ctx)
	expected := []string{"second", "最新搜索", "第三次", "第四次", "第五次"}
	if err != nil || !reflect.DeepEqual(got, expected) {
		t.Fatalf("recorded history = %v: %v", got, err)
	}
}
func TestMergeGlobalSearchHistoryKeepsFiveUniqueQueries(t *testing.T) {
	history := []string{"作品一", "作品二", "作品三", "作品四", "作品五"}
	got := mergeGlobalSearchHistory(history, "作品三")
	want := []string{"作品三", "作品一", "作品二", "作品四", "作品五"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeGlobalSearchHistory() = %#v, want %#v", got, want)
	}
	got = mergeGlobalSearchHistory(got, "作品六")
	want = []string{"作品六", "作品三", "作品一", "作品二", "作品四"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trimmed search history = %#v, want %#v", got, want)
	}
}

func TestNormalizeGlobalSearchQueryCollapsesWhitespace(t *testing.T) {
	if got := normalizeGlobalSearchQuery("  凡人   修仙传  "); got != "凡人 修仙传" {
		t.Fatalf("normalizeGlobalSearchQuery() = %q", got)
	}
}
