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
	"time"

	"github.com/hyaeve/manco/internal/model"
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
		{"删除订阅 夏日深处", "删除订阅", "夏日深处"},
		{"删除 夏日深处", "删除", "夏日深处"},
		{"查看榜单 拷贝", "查看榜单", "拷贝"},
	}
	for _, item := range cases {
		command, arg := splitCommand(item.input)
		if command != item.command || arg != item.arg {
			t.Fatalf("splitCommand(%q) = %q, %q; want %q, %q", item.input, command, arg, item.command, item.arg)
		}
	}
}

func TestParseViewText(t *testing.T) {
	cases := map[string]string{
		"查看包子榜单":    "包子",
		"查看包子漫画榜单":  "包子漫画",
		"查看 AA2 榜单": "AA2",
	}
	for input, want := range cases {
		got, ok := parseViewText(input)
		if !ok || got != want {
			t.Fatalf("parseViewText(%q) = %q, %v; want %q, true", input, got, ok, want)
		}
	}
	for _, input := range []string{"查看订阅", "包子榜单", "查看榜单 包子"} {
		if got, ok := parseViewText(input); ok {
			t.Fatalf("parseViewText(%q) unexpectedly matched %q", input, got)
		}
	}
}

func TestSourceKindFilter(t *testing.T) {
	cases := map[string]string{
		"":      "",
		"漫画源":   "comic",
		"书籍源":   "book",
		"视频源":   "other",
		"其他源":   "other",
		"novel": "",
		"小说":    "book",
		"video": "other",
	}
	for input, want := range cases {
		if got := sourceKindFilter(input); got != want {
			t.Fatalf("sourceKindFilter(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTransientMessageExpiryResetsOnActivity(t *testing.T) {
	deleted := make(chan int64, 1)
	service := New(log.New(io.Discard, "", 0))
	service.cfg.Token = "test-token"
	service.transientTTL = 100 * time.Millisecond
	service.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/deleteMessage") {
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err == nil {
				deleted <- int64(payload["message_id"].(float64))
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{}}`)),
		}, nil
	})}

	service.trackTransientMessage(1, "1", 42)
	time.Sleep(60 * time.Millisecond)
	service.refreshTransientChat(1)
	select {
	case id := <-deleted:
		t.Fatalf("message %d expired before the reset window", id)
	case <-time.After(60 * time.Millisecond):
	}
	select {
	case id := <-deleted:
		if id != 42 {
			t.Fatalf("expired message id = %d, want 42", id)
		}
	case <-time.After(80 * time.Millisecond):
		t.Fatal("transient message was not deleted after its inactivity window")
	}
}

func TestPersistentNotificationIsNotTrackedForExpiry(t *testing.T) {
	service := New(log.New(io.Discard, "", 0))
	service.cfg.Token = "test-token"
	service.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":77}}`)),
		}, nil
	})}
	if id := service.sendPersistentMessage(context.Background(), "1", "通知", nil); id != 77 {
		t.Fatalf("persistent message id = %d, want 77", id)
	}
	service.mu.RLock()
	tracked := len(service.transient[1])
	service.mu.RUnlock()
	if tracked != 0 {
		t.Fatalf("persistent notification tracked %d transient messages", tracked)
	}
}

func TestInteractiveSessionsRemainIndependent(t *testing.T) {
	service := New(log.New(io.Discard, "", 0))
	firstID := service.allocateSession(1, &session{
		results:     []resultEntry{{Title: "榜单作品"}},
		resultsText: "榜单结果",
	})
	secondID := service.allocateSession(1, &session{
		results:     []resultEntry{{Title: "搜索作品"}},
		resultsText: "搜索结果",
	})
	if firstID == secondID {
		t.Fatal("interactive sessions received the same id")
	}
	if got := service.sessionResults(1, firstID); len(got) != 1 || got[0].Title != "榜单作品" {
		t.Fatalf("first session results = %#v", got)
	}
	if got := service.sessionResults(1, secondID); len(got) != 1 || got[0].Title != "搜索作品" {
		t.Fatalf("second session results = %#v", got)
	}
	keyboard := resultKeyboard(service.sessionResults(1, firstID), firstID).(map[string]any)
	rows := keyboard["inline_keyboard"].([][]map[string]string)
	if callback := rows[0][0]["callback_data"]; callback != "open:"+firstID+":0" {
		t.Fatalf("first session callback = %q", callback)
	}
	sessionID, index, ok := parseIndexCallback("open:"+secondID+":2", "open:")
	if !ok || sessionID != secondID || index != 2 {
		t.Fatalf("parsed callback = %q, %d, %v", sessionID, index, ok)
	}
	service.deleteSession(1, firstID)
	if service.session(1, firstID) != nil {
		t.Fatal("deleted session still exists")
	}
	if service.session(1, secondID) == nil {
		t.Fatal("new session was removed with the old session")
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
		{Title: "第一个结果", Cover: "https://example.test/first.jpg", CoverBlur: true},
		{Title: "第二个结果", Cover: "https://example.test/second.jpg"},
	}
	id, photo, err := service.sendResultList(context.Background(), "1", "搜索结果", entries, resultKeyboard(entries, "s1"))
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
	if payload["has_spoiler"] != true {
		t.Fatalf("cover blur not forwarded to Telegram: %+v", payload)
	}
}

func TestNewResultEntryInheritsSourceCoverBlur(t *testing.T) {
	entry := newResultEntry(BotSource{
		ID:        "blurred",
		Name:      "测试内容源",
		CoverBlur: true,
	}, model.Comic{
		ID:    "comic-1",
		Title: "测试作品",
		Cover: "https://example.test/cover.jpg",
	})
	if !entry.CoverBlur {
		t.Fatal("result entry did not inherit source cover blur")
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
		{input: "搜索视频 凡人修仙传", query: "凡人修仙传", sourceName: "视频", ok: true},
		{input: "视频搜索 凡人修仙传", query: "凡人修仙传", sourceName: "视频", ok: true},
		{input: "包子搜索 凡人修仙传", query: "凡人修仙传", sourceName: "包子", ok: true},
		{input: "AA2搜索 凡人修仙传", query: "凡人修仙传", sourceName: "AA2", ok: true},
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
		"视频源": "other",
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

func TestSubscriptionDeleteMatchesByPrefixAndMinimumLength(t *testing.T) {
	items := []BotSubscription{
		{ID: 1, Title: "夏日深处", Source: "包子漫画"},
		{ID: 2, Title: "夏日深处 1-86话", Source: "禁漫天堂"},
		{ID: 3, Title: "海贼王", Source: "哔咔漫画"},
		{ID: 4, Title: "One Punch Man", Source: "CopyManga"},
		{ID: 5, Title: "One", Source: "OtherSource"},
	}
	for query, wantIDs := range map[string][]int64{
		"夏日深处":      {1, 2},
		"夏日":        nil,
		"夏日深":       nil,
		"海贼王":       {3},
		"海贼":        nil,
		"one punch": {4},
		"one":       {5},
		"":          {1, 2, 3, 4, 5},
	} {
		matches := subscriptionDeleteMatches(items, query)
		gotIDs := make([]int64, 0, len(matches))
		for _, item := range matches {
			gotIDs = append(gotIDs, item.ID)
		}
		if fmt.Sprint(gotIDs) != fmt.Sprint(wantIDs) {
			t.Fatalf("subscriptionDeleteMatches(%q) ids = %v, want %v", query, gotIDs, wantIDs)
		}
	}
}

func TestRenderSubscriptionDeleteDetailAndKeyboard(t *testing.T) {
	item := BotSubscription{
		Title:       "夏日深处 1-86话",
		Source:      "包子漫画",
		Author:      "测试作者",
		Description: "《夏日深处》全集,作品讲述了海边小镇的夏日故事。",
	}
	text := renderSubscriptionDeleteDetail(item, 0, 2)
	for _, want := range []string{
		"<b>夏日深处 1-86话</b>（包子）",
		"作者：测试作者",
		"匹配结果：第 1/2 个",
		"简介：作品讲述了海边小镇的夏日故事。",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("delete detail missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "《夏日深处》全集") {
		t.Fatalf("delete detail retained source summary prefix:\n%s", text)
	}
	keyboard := subscriptionDeleteCardKeyboard("s1", 2).(map[string]any)
	rows := keyboard["inline_keyboard"].([][]map[string]string)
	if len(rows) != 2 {
		t.Fatalf("delete keyboard rows = %d, want 2", len(rows))
	}
	if rows[0][0]["text"] != "取消" || rows[0][1]["text"] != "删除订阅" {
		t.Fatalf("delete action row = %+v", rows[0])
	}
	if rows[1][0]["text"] != "上一个" || rows[1][1]["text"] != "下一个" {
		t.Fatalf("delete navigation row = %+v", rows[1])
	}
	if rows[1][0]["callback_data"] != "subdel:prev:s1" || rows[1][1]["callback_data"] != "subdel:next:s1" {
		t.Fatalf("delete navigation callbacks = %+v", rows[1])
	}
}

type deleteSubscriptionTestApp struct {
	App
	items   []BotSubscription
	details map[int64]BotSubscription
	deleted []int64
}

func (a *deleteSubscriptionTestApp) BotSubscriptions(context.Context) ([]BotSubscription, error) {
	return append([]BotSubscription(nil), a.items...), nil
}

func (a *deleteSubscriptionTestApp) BotSubscriptionDetail(_ context.Context, id int64) (BotSubscription, error) {
	if item, ok := a.details[id]; ok {
		return item, nil
	}
	for _, item := range a.items {
		if item.ID == id {
			return item, nil
		}
	}
	return BotSubscription{}, errors.New("订阅不存在")
}

func (a *deleteSubscriptionTestApp) BotDelete(_ context.Context, id int64) error {
	a.deleted = append(a.deleted, id)
	return nil
}

func TestReplyDeleteSubscriptionsShowsDetailsAndDeletes(t *testing.T) {
	app := &deleteSubscriptionTestApp{
		items: []BotSubscription{
			{ID: 101, Title: "夏日深处", Source: "包子漫画"},
			{ID: 102, Title: "夏日深处外传", Source: "禁漫天堂"},
		},
		details: map[int64]BotSubscription{
			101: {
				ID:          101,
				Title:       "夏日深处",
				Source:      "包子漫画",
				Author:      "测试作者",
				Description: "作品讲述了夏日小镇的故事。",
			},
		},
	}
	var payload map[string]any
	service := New(log.New(io.Discard, "", 0))
	service.cfg.Token = "test-token"
	service.SetApp(app)
	service.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/sendMessage") {
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatalf("decode telegram payload: %v", err)
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":88}}`)),
		}, nil
	})}

	service.replyDeleteSubscriptions(context.Background(), 1, "夏日深处")
	text, _ := payload["text"].(string)
	for _, want := range []string{"<b>夏日深处</b>（包子）", "作者：测试作者", "简介：作品讲述了夏日小镇的故事。"} {
		if !strings.Contains(text, want) {
			t.Fatalf("delete confirmation missing %q:\n%s", want, text)
		}
	}
	keyboard, ok := payload["reply_markup"].(map[string]any)
	if !ok {
		t.Fatalf("reply_markup = %#v", payload["reply_markup"])
	}
	rows := keyboard["inline_keyboard"].([]any)
	if len(rows) != 2 {
		t.Fatalf("delete keyboard rows = %d, want 2", len(rows))
	}

	var sessionID string
	service.mu.RLock()
	for id, current := range service.sessions {
		if current.ChatID == 1 && current.subscriptionDelete != nil {
			sessionID = id
			break
		}
	}
	service.mu.RUnlock()
	if sessionID == "" {
		t.Fatal("delete subscription session not created")
	}
	service.handleSubscriptionDeleteCallback(
		context.Background(),
		1,
		"1",
		88,
		"subdel:delete:"+sessionID,
	)
	if fmt.Sprint(app.deleted) != "[101]" {
		t.Fatalf("deleted subscriptions = %v, want [101]", app.deleted)
	}
	if service.session(1, sessionID) != nil {
		t.Fatal("delete subscription session was not removed")
	}
}

func TestReplyDeleteSubscriptionsRejectsShortNamePrefix(t *testing.T) {
	app := &deleteSubscriptionTestApp{
		items: []BotSubscription{
			{ID: 101, Title: "夏日深处", Source: "包子漫画"},
		},
	}
	var text string
	service := New(log.New(io.Discard, "", 0))
	service.cfg.Token = "test-token"
	service.SetApp(app)
	service.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err == nil {
			text, _ = payload["text"].(string)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":89}}`)),
		}, nil
	})}
	service.replyDeleteSubscriptions(context.Background(), 1, "夏日深")
	if text != "没有相关作品的订阅计划。" {
		t.Fatalf("short query response = %q", text)
	}
}

type existingPlanTestApp struct {
	App
	visual        BotSubscriptionVisual
	subscribeCall int
}

func (a *existingPlanTestApp) BotSubscriptionVisual(context.Context, string, string) (BotSubscriptionVisual, error) {
	return a.visual, nil
}

func (a *existingPlanTestApp) BotSubscribe(context.Context, string, string) (model.Subscription, int, error) {
	a.subscribeCall++
	return model.Subscription{}, 0, nil
}

func TestReplySubscribeRejectsExistingPlan(t *testing.T) {
	app := &existingPlanTestApp{
		visual: BotSubscriptionVisual{
			Title:  "夏日深处",
			InPlan: true,
		},
	}
	var text string
	service := New(log.New(io.Discard, "", 0))
	service.cfg.Token = "test-token"
	service.SetApp(app)
	service.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err == nil {
			text, _ = payload["text"].(string)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":90}}`)),
		}, nil
	})}
	sessionID := service.allocateSession(1, &session{
		results: []resultEntry{
			{SourceID: "bao", ComicID: "42", Title: "夏日深处"},
		},
	})
	service.replySubscribe(context.Background(), 1, "1", sessionID, 0, 0)
	if text != "<b>《夏日深处》</b>已在订阅计划中。" {
		t.Fatalf("existing subscription response = %q", text)
	}
	if app.subscribeCall != 0 {
		t.Fatalf("BotSubscribe called %d times for existing subscription", app.subscribeCall)
	}
}

func TestShortSourceName(t *testing.T) {
	cases := map[string]string{
		"包子漫画":      "包子",
		"禁漫天堂":      "禁漫",
		"CopyManga": "Copy",
		"GoDA":      "GoDa",
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
		Source:         "包子漫画",
		Author:         "测试作者",
		Description:    "这是一段作品简介。",
		Succeeded:      25,
		Failed:         2,
		NextCheckLabel: "明天 21:30",
	})
	for _, want := range []string{
		"<b>测试作品（包子）</b>",
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
		{ID: "plugin-goda", Name: "GoDA"},
	}
	if source, ok := matchSource(sources, "包子"); !ok || source.ID != "baozimh" {
		t.Fatalf("matchSource 包子 = %+v, %v", source, ok)
	}
	if source, ok := matchSource(sources, "copymanga"); !ok || source.ID != "plugin-copy" {
		t.Fatalf("matchSource copymanga = %+v, %v", source, ok)
	}
	if source, ok := matchSource(sources, "copy"); !ok || source.ID != "plugin-copy" {
		t.Fatalf("matchSource copy = %+v, %v", source, ok)
	}
	if _, ok := matchSource(sources, "cop"); ok {
		t.Fatal("three-letter English prefix must not match")
	}
	if source, ok := matchSource(sources, "goda"); !ok || source.ID != "plugin-goda" {
		t.Fatalf("case-insensitive GoDA match = %+v, %v", source, ok)
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
	if source, ok := matchViewSource(sources, "copy"); !ok || source.ID != "copy" {
		t.Fatalf("four-letter English prefix = %+v, %v", source, ok)
	}
	if _, ok := matchViewSource(sources, "cop"); ok {
		t.Fatal("three-letter English prefix must not match")
	}
	if source, ok := matchViewSource(sources, "COP YMANGA"); ok || source.ID != "" {
		t.Fatalf("invalid spaced query matched: %+v", source)
	}
}

func TestDuplicateSourceOrdinalSelection(t *testing.T) {
	sources := []BotSource{
		{ID: "aa-first", Name: "AA"},
		{ID: "aa-second", Name: "AA"},
		{ID: "other", Name: "OtherSource"},
	}
	for name, matcher := range map[string]func([]BotSource, string) (BotSource, bool){
		"search": matchSource,
		"view":   matchViewSource,
	} {
		if source, ok := matcher(sources, "AA"); !ok || source.ID != "aa-first" {
			t.Fatalf("%s duplicate exact name = %+v, %v; want first source", name, source, ok)
		}
		if source, ok := matcher(sources, "AA2"); !ok || source.ID != "aa-second" {
			t.Fatalf("%s duplicate ordinal = %+v, %v; want second source", name, source, ok)
		}
		if _, ok := matcher(sources, "AA3"); ok {
			t.Fatalf("%s duplicate ordinal beyond range unexpectedly matched", name)
		}
	}
}

func TestBrowseKeyboardAndPageRules(t *testing.T) {
	entries := []resultEntry{{Title: "作品一"}, {Title: "作品二"}}
	first := browseKeyboard(entries, 1, true, "s1").(map[string]any)
	rows := first["inline_keyboard"].([][]map[string]string)
	if len(rows) != 2 {
		t.Fatalf("first page keyboard rows = %d, want one number row + next row", len(rows))
	}
	if rows[0][0]["text"] != "1" || rows[0][1]["text"] != "2" {
		t.Fatalf("first page result buttons = %+v", rows[0])
	}
	if rows[1][0]["callback_data"] != "browse:s1:next" {
		t.Fatalf("first page navigation = %+v", rows[1])
	}
	second := browseKeyboard(entries, 2, false, "s1").(map[string]any)
	rows = second["inline_keyboard"].([][]map[string]string)
	if rows[0][0]["text"] != "11" || rows[0][0]["callback_data"] != "open:s1:0" {
		t.Fatalf("second page result numbering = %+v", rows[0])
	}
	if rows[1][0]["callback_data"] != "browse:s1:prev" {
		t.Fatalf("second page navigation = %+v", rows[1])
	}
}

func TestBrowseKeyboardUsesFiveNumberButtonsPerRow(t *testing.T) {
	entries := make([]resultEntry, browsePageSize)
	keyboard := browseKeyboard(entries, 1, true, "s1").(map[string]any)
	rows := keyboard["inline_keyboard"].([][]map[string]string)
	if len(rows) != 3 {
		t.Fatalf("keyboard rows = %d, want two result rows + navigation row", len(rows))
	}
	for index, row := range rows[:2] {
		if len(row) != 5 {
			t.Fatalf("result row %d has %d buttons, want 5", index+1, len(row))
		}
	}
	if rows[0][0]["text"] != "1" || rows[1][4]["text"] != "10" {
		t.Fatalf("result button numbering = %+v / %+v", rows[0], rows[1])
	}
	if len(rows[2]) != 1 || rows[2][0]["callback_data"] != "browse:s1:next" {
		t.Fatalf("navigation row = %+v, want separate next button", rows[2])
	}
}

func TestGlobalSearchKeyboardHasTypePageNavigation(t *testing.T) {
	entries := []resultEntry{{Title: "作品一"}, {Title: "作品二"}}
	first := globalSearchKeyboard(entries, 1, 3, "s1").(map[string]any)
	rows := first["inline_keyboard"].([][]map[string]string)
	if len(rows) != 2 || rows[1][0]["callback_data"] != "browse:s1:next" {
		t.Fatalf("first page keyboard = %+v", rows)
	}
	second := globalSearchKeyboard(entries, 2, 3, "s1").(map[string]any)
	rows = second["inline_keyboard"].([][]map[string]string)
	if len(rows) != 2 || rows[1][0]["callback_data"] != "browse:s1:prev" || rows[1][1]["callback_data"] != "browse:s1:next" {
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
