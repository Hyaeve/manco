package store

import (
	"fmt"
	"testing"

	"github.com/hyaeve/manco/internal/model"
)

func TestSubscriptionSearchFiltersBeforePagination(t *testing.T) {
	repository := openTestStore(t)
	ctx := t.Context()
	for index := 0; index < 65; index++ {
		_, err := repository.UpsertSubscription(ctx, model.Subscription{
			SourceID: "comic-source", ComicID: fmt.Sprintf("filler-%d", index),
			Title: fmt.Sprintf("Filler %02d", index), DownloadDir: "/downloads/comics",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	fixtures := []model.Subscription{
		{SourceID: "comic-source", ComicID: "a", Title: "星海 A", SeriesDir: "自定义目录", Author: "Fixture Writer", DownloadDir: "/downloads/comics"},
		{SourceID: "comic-source", ComicID: "b", Title: "星海 B", Author: "Fixture Writer", DownloadDir: "/downloads/comics"},
		{SourceID: "book-source", ComicID: "c", Title: "星海 C", DownloadDir: "/downloads/books"},
		{SourceID: "comic-source", ComicID: "literal", Title: "100%_literal", DownloadDir: "/downloads/comics"},
	}
	for _, fixture := range fixtures {
		if _, err := repository.UpsertSubscription(ctx, fixture); err != nil {
			t.Fatal(err)
		}
	}
	archived, err := repository.UpsertSubscription(ctx, model.Subscription{
		SourceID: "comic-source", ComicID: "archived", Title: "星海 D", Author: "Fixture Writer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ArchiveSubscription(ctx, archived.ID, "fixture"); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		filter SubscriptionFilter
		offset int
		total  int
		comic  string
	}{
		{name: "title past first page", filter: SubscriptionFilter{Query: " 星海 ", Sort: "title"}, offset: 1, total: 3, comic: "b"},
		{name: "directory name", filter: SubscriptionFilter{Query: "自定义目录"}, total: 1, comic: "a"},
		{name: "case insensitive author", filter: SubscriptionFilter{Query: "fixture WRITER", Sort: "title"}, total: 2, comic: "a"},
		{name: "source and parent directory", filter: SubscriptionFilter{Query: "星海", SourceIDs: []string{"book-source"}, DownloadDir: "/downloads/books"}, total: 1, comic: "c"},
		{name: "parent directory excludes match", filter: SubscriptionFilter{Query: "星海", DownloadDir: "/downloads/absent"}},
		{name: "literal wildcard characters", filter: SubscriptionFilter{Query: "100%_"}, total: 1, comic: "literal"},
		{name: "quoted input is not SQL", filter: SubscriptionFilter{Query: "' OR 1=1 --"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			items, total, err := repository.ListSubscriptionsPage(ctx, test.filter, 1, test.offset)
			if err != nil {
				t.Fatal(err)
			}
			if total != test.total {
				t.Fatalf("total=%d, want %d", total, test.total)
			}
			if test.comic == "" {
				if len(items) != 0 {
					t.Fatalf("unexpected matches: %+v", items)
				}
			} else if len(items) != 1 || items[0].ComicID != test.comic {
				t.Fatalf("items=%+v, want comic %s", items, test.comic)
			}
		})
	}
}
