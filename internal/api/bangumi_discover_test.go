package api

import (
	"fmt"
	"strings"
	"testing"
)

func TestBangumiFeedCatalog(t *testing.T) {
	want := []string{"近期注目", "连载中", "本月新作", "今年新作", "收藏最多", "排行榜", "高分漫画", "高分小说"}
	if len(bangumiFeedDefinitions) != len(want) {
		t.Fatalf("feed count = %d, want %d", len(bangumiFeedDefinitions), len(want))
	}
	for index, name := range want {
		if bangumiFeedDefinitions[index].Name != name {
			t.Fatalf("feed %d = %q, want %q", index, bangumiFeedDefinitions[index].Name, name)
		}
	}
}

func TestBangumiSubjectComic(t *testing.T) {
	subject := bangumiCalendarSubject{
		ID:      42,
		Name:    "Original",
		NameCN:  "示例漫画",
		Summary: "简介",
		Date:    "2026-10-01",
	}
	subject.Images.Large = "http://lain.bgm.tv/large.jpg"
	subject.Images.Common = "https://lain.bgm.tv/common.jpg"
	subject.Rating.Score = 8.6
	subject.Rating.Rank = 12
	subject.Tags = append(subject.Tags, struct {
		Name string `json:"name"`
	}{Name: "科幻"})

	comic, ok := bangumiSubjectComic(subject, bangumiFeedDefinitions[6])
	if !ok {
		t.Fatal("subject was rejected")
	}
	if comic.SourceID != "bangumi-manga" || comic.ID != "42" {
		t.Fatalf("identity = %s/%s", comic.SourceID, comic.ID)
	}
	if comic.Cover != "https://lain.bgm.tv/large.jpg" {
		t.Fatalf("cover = %q", comic.Cover)
	}
	if comic.SourceURL != "https://bgm.tv/subject/42" {
		t.Fatalf("source URL = %q", comic.SourceURL)
	}
	if !strings.Contains(comic.Status, "8.6") || !strings.Contains(comic.Status, "排名 12") {
		t.Fatalf("status = %q", comic.Status)
	}
}

func TestSampleBangumiLoginItems(t *testing.T) {
	items := make([]bangumiLoginItem, 20)
	for index := range items {
		items[index] = bangumiLoginItem{ID: index + 1, Title: fmt.Sprintf("作品 %d", index+1)}
	}
	sample := sampleBangumiLoginItems(items, 10)
	if len(sample) != 10 {
		t.Fatalf("sample count = %d, want 10", len(sample))
	}
	seen := map[int]bool{}
	for _, item := range sample {
		if seen[item.ID] {
			t.Fatalf("duplicate item %d", item.ID)
		}
		seen[item.ID] = true
	}
}

func TestSubscriptionTitleScore(t *testing.T) {
	tests := []struct {
		original  string
		candidate string
		min       float64
	}{
		{"葬送的芙莉莲", "葬送的芙莉莲", 1},
		{"夏日深处 1-86话", "夏日深处", 0.9},
		{"【测试】高分漫画", "高分漫画", 0.9},
		{"完全不同", "另一部作品", 0},
	}
	for _, test := range tests {
		score := subscriptionTitleScore(test.original, test.candidate)
		if score < test.min {
			t.Fatalf("score(%q, %q) = %.3f, want >= %.3f", test.original, test.candidate, score, test.min)
		}
	}
	if !subscriptionAuthorMatches("作者甲 / 作者乙", "作者乙") {
		t.Fatal("author overlap was not detected")
	}
}
