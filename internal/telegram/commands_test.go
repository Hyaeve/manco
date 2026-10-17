package telegram

import (
	"errors"
	"strings"
	"testing"
)

func TestSplitCommand(t *testing.T) {
	cases := []struct {
		input   string
		command string
		arg     string
	}{
		{"/start", "/start", ""},
		{"/subscriptions@manco_bot", "/subscriptions", ""},
		{"/check@manco_bot", "/check", ""},
		{"/check 星海镖师", "/check", "星海镖师"},
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

func TestCheckSubscriptionTargets(t *testing.T) {
	items := []BotSubscription{
		{Title: "作品一", Source: "包子漫画", Enabled: true, CronExpr: "0 21 * * 5"},
		{Title: "作品二", Source: "哔咔漫画", Enabled: false, CronExpr: "0 21 * * 5"},
		{Title: "凡人修仙传", Source: "禁漫天堂", Enabled: true, CronExpr: "0 21 * * 5"},
		{Title: "凡人修仙传外传", Source: "包子漫画", Enabled: true, CronExpr: ""},
	}
	enabled := checkSubscriptionTargets(items, "")
	if len(enabled) != 2 {
		t.Fatalf("enabled targets = %+v, want 2 scheduled subscriptions", enabled)
	}
	for _, item := range enabled {
		if !item.Enabled || strings.TrimSpace(item.CronExpr) == "" {
			t.Fatalf("batch check included non-tracking item: %+v", item)
		}
	}
	single := checkSubscriptionTargets(items, "作品二")
	if len(single) != 1 || single[0].Title != "作品二" || single[0].Enabled {
		t.Fatalf("named check = %+v, want disabled 作品二", single)
	}
	exact := checkSubscriptionTargets(items, "凡人修仙传")
	if len(exact) != 1 || exact[0].Title != "凡人修仙传" {
		t.Fatalf("exact title preference = %+v", exact)
	}
	explicitNoCron := checkSubscriptionTargets(items, "凡人修仙传外传")
	if len(explicitNoCron) != 1 || explicitNoCron[0].Title != "凡人修仙传外传" {
		t.Fatalf("explicit no-Cron check = %+v", explicitNoCron)
	}
	partial := checkSubscriptionTargets(items, "作品")
	if len(partial) != 2 {
		t.Fatalf("partial targets = %+v, want 作品一 and 作品二", partial)
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
		"CopyManga": "Copy",
		"JM":        "JM",
	}
	for input, want := range cases {
		if got := shortSourceName(input); got != want {
			t.Fatalf("shortSourceName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCleanComicSummaryRemovesSourcePrefix(t *testing.T) {
	input := "《星海镖师》第277话2地图,《星海镖师》全集,作品讲述了D.B星河纪元3000年左右的人类进入第二次大开发时代。"
	got := cleanComicSummary("星海镖师", input)
	want := "作品讲述了D.B星河纪元3000年左右的人类进入第二次大开发时代。"
	if got != want {
		t.Fatalf("cleanComicSummary() = %q, want %q", got, want)
	}
}

func TestRenderSubscriptionListUsesShortSourceAndStatusDot(t *testing.T) {
	text := renderSubscriptionList([]BotSubscription{
		{Title: "欲海交锋/Moby Dick", Source: "哔咔漫画", Enabled: true},
		{Title: "死灵法师！我即是天灾", Source: "包子漫画", Enabled: false},
		{Title: "One Punch Man", Source: "CopyManga", Enabled: true},
	})
	for _, want := range []string{
		"1. 欲海交锋/Moby Dick（哔咔）🟢",
		"2. 死灵法师！我即是天灾（包子）⚪",
		"3. One Punch Man（Copy）🟢",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("subscription list missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "已启用") || strings.Contains(text, "已停用") {
		t.Fatalf("subscription list still uses verbose status text:\n%s", text)
	}
}

func TestRenderResultDetailIncludesChapterCountAndCleanSummary(t *testing.T) {
	entry := resultEntry{
		Title:        "星海镖师",
		SourceName:   "包子",
		Author:       "漫言星空",
		Latest:       "第277话2 地图",
		ChapterCount: 277,
		Summary:      "《星海镖师》第277话2地图,《星海镖师》全集,作品讲述了热血冒险故事。",
	}
	text := renderResultDetail(entry, false)
	for _, want := range []string{
		"作者：漫言星空",
		"最新章节：第277话2 地图",
		"章节数量：共 277 章",
		"作品讲述了热血冒险故事。",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("detail missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "《星海镖师》第277话2地图,《星海镖师》全集") {
		t.Fatalf("detail retained source prefix:\n%s", text)
	}
}

func TestRenderCheckReportAggregatesAllSubscriptions(t *testing.T) {
	outcomes := []subscriptionCheckOutcome{
		{Subscription: BotSubscription{Title: "作品一", Source: "包子漫画", Enabled: true}, Result: BotCheckResult{New: 2, Queued: 2}},
		{Subscription: BotSubscription{Title: "作品二", Source: "哔咔漫画", Enabled: false}, Result: BotCheckResult{Retried: 1}},
		{Subscription: BotSubscription{Title: "作品三", Source: "禁漫天堂", Enabled: true}, Err: errors.New("timeout")},
	}
	text := renderCheckReport(outcomes, nil)
	for _, want := range []string{
		"检查更新完成（3）",
		"成功 2 · 失败 1 · 新章节 2 · 入队 2 · 重试 1",
		"作品一（包子） · 新 2 / 入队 2",
		"作品二（哔咔） · 重试 1",
		"作品三（禁漫） · 失败：timeout",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("check report missing %q:\n%s", want, text)
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
