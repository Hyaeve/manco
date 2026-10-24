package api

import (
	"testing"
	"time"
)

func TestParseBangumiCalendarPrefersTodayAndFiltersIncompleteSubjects(t *testing.T) {
	raw := []byte(`[
	  {"weekday":{"id":1},"items":[
	    {"id":1,"type":2,"name":"Monday","summary":"Monday summary","images":{"common":"https://lain.bgm.tv/1.jpg"},"collection":{"doing":99}}
	  ]},
	  {"weekday":{"id":2},"items":[
	    {"id":2,"type":2,"name":"Today low","summary":"Today summary","images":{"common":"https://lain.bgm.tv/2.jpg"},"collection":{"doing":1}},
	    {"id":3,"type":2,"url":"javascript:alert(1)","name":"Today high","summary":"Today summary","images":{"large":"https://lain.bgm.tv/3-large.jpg","common":"https://lain.bgm.tv/3-common.jpg"},"collection":{"doing":20}},
	    {"id":4,"type":1,"name":"Book","summary":"Book summary","images":{"common":"https://lain.bgm.tv/4.jpg"}},
	    {"id":5,"type":2,"name":"No image","summary":"No image summary"}
	  ]}
	]`)
	// 2026-10-27 is a Tuesday.
	items, err := parseBangumiCalendar(raw, time.Date(2026, 10, 27, 12, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %+v", items)
	}
	if items[0].ID != 3 || items[1].ID != 2 || items[2].ID != 1 {
		t.Fatalf("calendar ordering = %+v", items)
	}
	if items[0].Cover != "https://lain.bgm.tv/3-large.jpg" {
		t.Fatalf("preferred cover = %q", items[0].Cover)
	}
	if items[2].URL != "https://bgm.tv/subject/1" {
		t.Fatalf("fallback subject URL = %q", items[2].URL)
	}
}

func TestBangumiImageURLRestrictsHosts(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{"https://lain.bgm.tv/pic/cover/c/1.jpg", true},
		{"https://bgm.tv/cover.jpg", true},
		{"http://lain.bgm.tv/cover.jpg", true},
		{"https://lain.bgm.tv.evil.example/cover.jpg", false},
	} {
		if got := isBangumiImageURL(test.value); got != test.want {
			t.Fatalf("isBangumiImageURL(%q) = %v, want %v", test.value, got, test.want)
		}
	}
	if got, ok := normalizeBangumiImageURL("http://lain.bgm.tv/cover.jpg"); !ok || got != "https://lain.bgm.tv/cover.jpg" {
		t.Fatalf("normalized Bangumi cover = %q, %v", got, ok)
	}
}
