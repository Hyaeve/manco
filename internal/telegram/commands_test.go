package telegram

import "testing"

func TestSplitCommand(t *testing.T) {
	cases := []struct {
		input   string
		command string
		arg     string
	}{
		{"/start", "/start", ""},
		{"/subscriptions@manco_bot", "/subscriptions", ""},
		{"/s 星海镖师", "/s", "星海镖师"},
		{"搜索", "搜索", ""},
	}
	for _, item := range cases {
		command, arg := splitCommand(item.input)
		if command != item.command || arg != item.arg {
			t.Fatalf("splitCommand(%q) = %q, %q; want %q, %q", item.input, command, arg, item.command, item.arg)
		}
	}
}

func TestParseSearchText(t *testing.T) {
	cases := []struct {
		input      string
		query      string
		sourceName string
		ok         bool
	}{
		{input: "搜索漫画 凡人修仙传", query: "凡人修仙传", ok: true},
		{input: "包子搜索 凡人修仙传", query: "凡人修仙传", sourceName: "包子", ok: true},
		{input: "搜索资源 一拳超人", query: "一拳超人", ok: true},
		{input: "检查 凡人修仙传", ok: false},
	}
	for _, item := range cases {
		query, sourceName, ok := parseSearchText(item.input)
		if query != item.query || sourceName != item.sourceName || ok != item.ok {
			t.Fatalf("parseSearchText(%q) = %q, %q, %v; want %q, %q, %v", item.input, query, sourceName, ok, item.query, item.sourceName, item.ok)
		}
	}
}

func TestParseSubscriptionAction(t *testing.T) {
	cases := []struct {
		input  string
		action string
		title  string
		ok     bool
	}{
		{input: "检查 凡人修仙传", action: "check", title: "凡人修仙传", ok: true},
		{input: "补全 凡人修仙传", action: "fill", title: "凡人修仙传", ok: true},
		{input: "全量 凡人修仙传", action: "full", title: "凡人修仙传", ok: true},
		{input: "归档 凡人修仙传", action: "archive", title: "凡人修仙传", ok: true},
		{input: "删除 凡人修仙传", action: "delete", title: "凡人修仙传", ok: true},
		{input: "检查", ok: false},
	}
	for _, item := range cases {
		action, title, ok := parseSubscriptionAction(item.input)
		if action != item.action || title != item.title || ok != item.ok {
			t.Fatalf("parseSubscriptionAction(%q) = %q, %q, %v; want %q, %q, %v", item.input, action, title, ok, item.action, item.title, item.ok)
		}
	}
}

func TestShortSourceName(t *testing.T) {
	cases := map[string]string{
		"包子漫画":      "包子",
		"禁漫天堂":      "禁漫",
		"CopyManga": "CopyManga",
		"JM":        "JM",
	}
	for input, want := range cases {
		if got := shortSourceName(input); got != want {
			t.Fatalf("shortSourceName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTruncatePreservesShortTitles(t *testing.T) {
	if got := truncate("短标题", 10); got != "短标题" {
		t.Fatalf("truncate short = %q", got)
	}
	long := "这是一个特别长的作品名称用于测试省略"
	got := truncate(long, 6)
	if len([]rune(got)) != 7 {
		t.Fatalf("truncate long = %q", got)
	}
}

func TestMatchSource(t *testing.T) {
	sources := []BotSource{
		{ID: "baozimh", Name: "包子漫画"},
		{ID: "jmcomic", Name: "禁漫天堂"},
		{ID: "plugin-copy", Name: "CopyManga"},
	}
	if source, ok := matchSource(sources, "包子"); !ok || source.ID != "baozimh" {
		t.Fatalf("matchSource 包子 = %+v, %v", source, ok)
	}
	if source, ok := matchSource(sources, "copymanga"); !ok || source.ID != "plugin-copy" {
		t.Fatalf("matchSource copymanga = %+v, %v", source, ok)
	}
	if _, ok := matchSource(sources, "不存在"); ok {
		t.Fatal("matchSource 不存在 should fail")
	}
}
