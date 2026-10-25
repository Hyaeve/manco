package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hyaeve/manco/internal/sources"
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
		if bangumiFeedDefinitions[index].Type != 1 {
			t.Fatalf("feed %d type = %d, want book type 1", index, bangumiFeedDefinitions[index].Type)
		}
		if bangumiFeedDefinitions[index].Calendar {
			t.Fatalf("feed %d must not use the anime calendar", index)
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

func TestBangumiSubjectInfersNovelMatchKindFromTags(t *testing.T) {
	subject := bangumiCalendarSubject{ID: 43, NameCN: "示例小说", Type: 1}
	subject.Images.Large = "https://lain.bgm.tv/novel.jpg"
	subject.Tags = append(subject.Tags, struct {
		Name string `json:"name"`
	}{Name: "轻小说"})

	comic, ok := bangumiSubjectComic(subject, bangumiFeedDefinitions[0])
	if !ok {
		t.Fatal("subject was rejected")
	}
	if comic.SourceID != "bangumi-novel" {
		t.Fatalf("source ID = %q, want bangumi-novel", comic.SourceID)
	}
}

func TestSearchBangumiSubjectsUsesBookFilter(t *testing.T) {
	var requestPayload map[string]any
	client := &http.Client{Transport: bangumiRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(request.Body).Decode(&requestPayload); err != nil {
			t.Fatal(err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"data":[{"id":7,"type":1,"name":"链锯人","images":{"large":"https://lain.bgm.tv/7.jpg"}}],
				"total":1
			}`)),
		}, nil
	})}
	server := &Server{registry: sources.NewRegistry(client, nil, nil)}
	items, total, err := server.searchBangumiSubjects(
		context.Background(),
		"链锯人",
		"match",
		map[string]any{"type": []int{1}, "nsfw": false},
		1,
		12,
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].ID != 7 {
		t.Fatalf("search result = %+v, total %d", items, total)
	}
	filter, ok := requestPayload["filter"].(map[string]any)
	if !ok {
		t.Fatalf("missing filter: %#v", requestPayload)
	}
	types, ok := filter["type"].([]any)
	if !ok || len(types) != 1 || types[0] != float64(1) {
		t.Fatalf("type filter = %#v", filter["type"])
	}
	if requestPayload["keyword"] != "链锯人" || requestPayload["sort"] != "match" {
		t.Fatalf("request payload = %#v", requestPayload)
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

type bangumiRoundTripFunc func(*http.Request) (*http.Response, error)

func (f bangumiRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
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
