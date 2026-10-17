package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestSplitCommand(t *testing.T) {
	cases := []struct {
		input   string
		command string
		arg     string
	}{
		{"/start", "/start", ""},
		{"/subscriptions@manco_bot", "/subscriptions", ""},
		{"/subscriptions 国漫", "/subscriptions", "国漫"},
		{"/check@manco_bot", "/check", ""},
		{"/check 星海镖师", "/check", "星海镖师"},
		{"/sh 星海镖师", "/sh", "星海镖师"},
		{"/downloading", "/downloading", ""},
		{"/view 包子", "/view", "包子"},
		{"搜索", "搜索", ""},
		{"查看订阅 国漫", "查看订阅", "国漫"},
		{"查看榜单 拷贝", "查看榜单", "拷贝"},
	}
	for _, item := range cases {
		command, arg := splitCommand(item.input)
		if command != item.command || arg != item.arg {
			t.Fatalf("splitCommand(%q) = %q, %q; want %q, %q", item.input, command, arg, item.command, item.arg)
		}
	}
}

func TestSendResultListUsesFirstCoverPhoto(t *testing.T) {
	var method string
	var payload map[string]any
	service := New(log.New(io.Discard, "", 0))
	service.cfg.Token = "test-token"
	service.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		method = request.URL.Path
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode telegram payload: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":42}}`)),
		}, nil
	})}
	entries := []resultEntry{
		{Title: "第一个结果", Cover: "https://example.test/first.jpg"},
		{Title: "第二个结果", Cover: "https://example.test/second.jpg"},
	}
	id, photo, err := service.sendResultList(context.Background(), "1", "搜索结果", entries, resultKeyboard(entries))
	if err != nil {
		t.Fatalf("sendResultList: %v", err)
	}
	if id != 42 || !photo {
		t.Fatalf("message id=%d photo=%v", id, photo)
	}
	if !strings.HasSuffix(method, "/sendPhoto") {
		t.Fatalf("method = %q, want sendPhoto", method)
	}
	if payload["photo"] != "https://example.test/first.jpg" || payload["caption"] != "搜索结果" {
		t.Fatalf("payload = %+v", payload)
	}
	if _, ok := payload["reply_markup"]; !ok {
		t.Fatal("reply_markup missing from photo message")
	}
}

func TestTelegramPhotoURLRejectsRelativePath(t *testing.T) {
	if got := telegramPhotoURL("/api/discover/cover?sourceId=demo"); got != "" {
		t.Fatalf("relative cover = %q, want empty", got)
	}
	if got := telegramPhotoURL("//example.test/cover.jpg"); got != "https://example.test/cover.jpg" {
		t.Fatalf("protocol-relative cover = %q", got)
	}
}

func TestCheckSubscriptionTargets(t *testing.T) {
	items := []BotSubscription{
		{Title: "作品一", Source: "包子漫画", Enabled: true, CronExpr: "0 21 * * 5", ParentDir: "国漫", DownloadDir: "/downloads/国漫"},
		{Title: "作品二", Source: "哔咔漫画", Enabled: false, CronExpr: "0 21 * * 5", ParentDir: "国漫", DownloadDir: "/downloads/国漫"},
		{Title: "凡人修仙传", Source: "禁漫天堂", Enabled: true, CronExpr: "0 21 * * 5", ParentDir: "日漫", DownloadDir: "/downloads/日漫"},
		{Title: "凡人修仙传外传", Source: "包子漫画", Enabled: true, CronExpr: "", ParentDir: "日漫", DownloadDir: "/downloads/日漫"},
	}
	all := checkSubscriptionTargets(items, "")
	if len(all) != 4 {
		t.Fatalf("default targets = %+v, want all 4 subscriptions", all)
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
	directory := checkSubscriptionTargets(items, "国漫")
	if len(directory) != 2 || directory[0].Title != "作品一" || directory[1].Title != "作品二" {
		t.Fatalf("directory targets = %+v, want both 国漫 subscriptions", directory)
	}
	if got := checkTargetDirectory(items, "国漫"); got != "国漫" {
		t.Fatalf("checkTargetDirectory 国漫 = %q", got)
	}
	if got := checkTargetDirectory(items, "作品一"); got != "" {
		t.Fatalf("exact title should win over directory scope, got %q", got)
	}
}

func TestSubscriptionsInDownloadDirectory(t *testing.T) {
	items := []BotSubscription{
		{Title: "作品一", ParentDir: "国漫", DownloadDir: "/downloads/国漫"},
		{Title: "作品二", ParentDir: "日漫", DownloadDir: "/downloads/日漫"},
	}
	got := subscriptionsInDownloadDirectory(items, "国漫")
	if len(got) != 1 || got[0].Title != "作品一" {
		t.Fatalf("subscriptionsInDownloadDirectory = %+v", got)
	}
}

func TestParseSearchText(t *testing.T) {
	cases := []struct {
		input      string
		query      string
		sourceName string
		ok         bool
	}{
		{input: "搜索 凡人修仙传", query: "凡人修仙传", ok: true},
		{input: "搜索漫画 凡人修仙传", query: "凡人修仙传", sourceName: "漫画", ok: true},
		{input: "漫画搜索 凡人修仙传", query: "凡人修仙传", sourceName: "漫画", ok: true},
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

func TestSearchKindScope(t *testing.T) {
	cases := map[string]string{
		"漫画":  "comic",
		"漫画源": "comic",
		"书籍":  "book",
		"小说源": "book",
		"其他源": "other",
		"包子":  "",
	}
	for input, want := range cases {
		if got := searchKindScope(input); got != want {
			t.Fatalf("searchKindScope(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRankSearchEntriesPrioritizesExactTitle(t *testing.T) {
	groups := []sourceSearchResult{
		{Source: BotSource{ID: "source-1"}, Entries: []resultEntry{
			{SourceID: "source-1", ComicID: "1", Title: "凡人修仙传外传"},
		}},
		{Source: BotSource{ID: "source-2"}, Entries: []resultEntry{
			{SourceID: "source-2", ComicID: "2", Title: "凡人修仙传"},
		}},
	}
	entries := rankSearchEntries("凡人修仙传", groups)
	if len(entries) != 2 || entries[0].ComicID != "2" {
		t.Fatalf("ranked entries = %+v", entries)
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

func TestRenderSubscriptionListShowsDirectoryScope(t *testing.T) {
	text := renderSubscriptionList([]BotSubscription{
		{Title: "作品一", Source: "包子漫画", Enabled: true, ParentDir: "国漫"},
	}, "国漫")
	if !strings.Contains(text, "订阅清单 · 国漫（1）") {
		t.Fatalf("directory scope missing:\n%s", text)
	}
}

func TestRenderDownloadingRunningOnlyAndPauseNext(t *testing.T) {
	running := renderDownloading(BotDownloadingSnapshot{
		Running: []BotDownloadItem{
			{ComicTitle: "作品甲", ChapterTitle: "第1话"},
			{ComicTitle: "作品乙", ChapterTitle: "第2话"},
		},
	})
	for _, want := range []string{"正在下载（2）", "《作品甲》· 第1话", "《作品乙》· 第2话"} {
		if !strings.Contains(running, want) {
			t.Fatalf("running status missing %q:\n%s", want, running)
		}
	}
	queuedOnly := renderDownloading(BotDownloadingSnapshot{
		NextQueued: &BotDownloadItem{ComicTitle: "排队作品", ChapterTitle: "第9话"},
	})
	if queuedOnly != "当前没有正在下载的任务。" || strings.Contains(queuedOnly, "排队作品") {
		t.Fatalf("queued job leaked into running status: %s", queuedOnly)
	}
	paused := renderDownloading(BotDownloadingSnapshot{
		CooldownMinutes: 7,
		NextQueued:      &BotDownloadItem{ComicTitle: "待下载作品", ChapterTitle: "第3话"},
	})
	for _, want := range []string{"处于暂停期", "还有 7 分钟后开始下载《待下载作品》· 第3话"} {
		if !strings.Contains(paused, want) {
			t.Fatalf("pause status missing %q:\n%s", want, paused)
		}
	}
}

func TestRenderSubscriptionDownloadCompletedNotice(t *testing.T) {
	text := renderSubscriptionDownloadCompleted(SubscriptionDownloadCompletedNotice{
		Title:          "测试作品",
		Author:         "测试作者",
		Description:    "这是一段作品简介。",
		Succeeded:      25,
		Failed:         2,
		NextCheckLabel: "明天 21:30",
	})
	for _, want := range []string{
		"<b>测试作品</b>",
		"作者：测试作者",
		"简介：这是一段作品简介。",
		"历史章节下载完成：成功 25 章",
		"下载失败：2 章",
		"下次检查更新：明天 21:30",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("completion notice missing %q:\n%s", want, text)
		}
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

func TestMatchViewSourceRules(t *testing.T) {
	sources := []BotSource{
		{ID: "baozimh", Name: "包子漫画"},
		{ID: "copy", Name: "CopyManga"},
		{ID: "jm", Name: "JMComic"},
	}
	for _, value := range []string{"包子", "包子漫画"} {
		if source, ok := matchViewSource(sources, value); !ok || source.ID != "baozimh" {
			t.Fatalf("matchViewSource(%q) = %+v, %v", value, source, ok)
		}
	}
	if _, ok := matchViewSource(sources, "包"); ok {
		t.Fatal("single Chinese character must not match")
	}
	if source, ok := matchViewSource(sources, "copyma"); !ok || source.ID != "copy" {
		t.Fatalf("six-letter English prefix = %+v, %v", source, ok)
	}
	if _, ok := matchViewSource(sources, "copy"); ok {
		t.Fatal("five-letter English prefix must not match")
	}
	if source, ok := matchViewSource(sources, "COP YMANGA"); ok || source.ID != "" {
		t.Fatalf("invalid spaced query matched: %+v", source)
	}
}

func TestBrowseKeyboardAndPageRules(t *testing.T) {
	entries := []resultEntry{{Title: "作品一"}, {Title: "作品二"}}
	first := browseKeyboard(entries, 1, true).(map[string]any)
	rows := first["inline_keyboard"].([][]map[string]string)
	if len(rows) != 3 {
		t.Fatalf("first page keyboard rows = %d, want 2 results + next row", len(rows))
	}
	if rows[2][0]["callback_data"] != "browse:next" {
		t.Fatalf("first page navigation = %+v", rows[2])
	}
	second := browseKeyboard(entries, 2, false).(map[string]any)
	rows = second["inline_keyboard"].([][]map[string]string)
	if rows[0][0]["text"] != "11. 作品一" {
		t.Fatalf("second page result numbering = %+v", rows[0])
	}
	if rows[2][0]["callback_data"] != "browse:prev" {
		t.Fatalf("second page navigation = %+v", rows[2])
	}
}

func TestGlobalSearchKeyboardHasTypePageNavigation(t *testing.T) {
	entries := []resultEntry{{Title: "作品一"}, {Title: "作品二"}}
	first := globalSearchKeyboard(entries, 1, 3).(map[string]any)
	rows := first["inline_keyboard"].([][]map[string]string)
	if len(rows) != 3 || rows[2][0]["callback_data"] != "browse:next" {
		t.Fatalf("first page keyboard = %+v", rows)
	}
	second := globalSearchKeyboard(entries, 2, 3).(map[string]any)
	rows = second["inline_keyboard"].([][]map[string]string)
	if len(rows) != 3 || rows[2][0]["callback_data"] != "browse:prev" || rows[2][1]["callback_data"] != "browse:next" {
		t.Fatalf("middle page keyboard = %+v", rows)
	}
}

func TestRenderBrowseResultsUsesTenItemPages(t *testing.T) {
	entries := make([]resultEntry, 2)
	for index := range entries {
		entries[index].Title = fmt.Sprintf("作品%d", index+1)
	}
	text := renderBrowseResults("包子漫画", 2, 10, entries)
	for _, want := range []string{"包子漫画 · 榜单", "第 2 页", "11. 作品1", "12. 作品2"} {
		if !strings.Contains(text, want) {
			t.Fatalf("browse text missing %q:\n%s", want, text)
		}
	}
}
