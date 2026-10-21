package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDailyRetryReplacesSameWorkAndKeepsLatestThree(t *testing.T) {
	service := New(log.New(io.Discard, "", 0))
	service.cfg = Config{Enabled: true, Token: "test", ChatIDs: []string{"1", "2"}}
	var mu sync.Mutex
	var sent []map[string]any
	var deleted []int64
	nextID := int64(100)
	service.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload map[string]any
		_ = json.NewDecoder(request.Body).Decode(&payload)
		mu.Lock()
		defer mu.Unlock()
		id := int64(0)
		if path.Base(request.URL.Path) == "deleteMessage" {
			deleted = append(deleted, int64(payload["message_id"].(float64)))
		} else {
			nextID++
			id = nextID
			sent = append(sent, payload)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"ok":true,"result":{"message_id":%d}}`, id)))}, nil
	})}
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, retryNoticeLocation)
	notice := DownloadRetryNotice{SourceID: "source", ComicID: "work", Title: "作品", Source: "包子漫画",
		CoverURL: "https://example.test/cover.jpg", CoverBlur: true}
	for index := 1; index <= 4; index++ {
		notice.Chapters = []string{fmt.Sprintf("第%d章", index)}
		service.downloadRetryScheduledAt(context.Background(), notice, now.Add(time.Duration(index)*time.Minute))
	}
	if len(sent) != 8 || len(deleted) != 6 {
		t.Fatalf("sent=%d deleted=%v", len(sent), deleted)
	}
	caption := sent[7]["caption"].(string)
	for _, want := range []string{"今日失败重试 · 4 次，累计 4 章次", "第4章", "第3章", "第2章", "省略更早 1 次"} {
		if !strings.Contains(caption, want) {
			t.Fatalf("missing %q: %s", want, caption)
		}
	}
	if strings.Contains(caption, "第1章") || sent[7]["has_spoiler"] != true {
		t.Fatalf("caption/spoiler: %#v", sent[7])
	}
	notice.SourceID = "another-source"
	service.downloadRetryScheduledAt(context.Background(), notice, now)
	if len(deleted) != 6 {
		t.Fatal("same title from a different source replaced the old message")
	}
	service.downloadRetryScheduledAt(context.Background(), notice, now.Add(24*time.Hour))
	if len(deleted) != 6 || len(service.retryNotices) != 2 {
		t.Fatal("new day did not preserve previous day's notifications/reset state")
	}
}

func TestSubscriptionListPaginationAndDirectory(t *testing.T) {
	app := &deleteSubscriptionTestApp{}
	for index := 1; index <= 32; index++ {
		app.items = append(app.items, BotSubscription{ID: int64(index), Title: fmt.Sprintf("作品%02d", index), ParentDir: "国漫"})
	}
	app.items = append(app.items, BotSubscription{ID: 33, Title: "其他目录作品", ParentDir: "日漫"})
	service := New(log.New(io.Discard, "", 0))
	service.cfg.Token = "test"
	service.SetApp(app)
	var sent []map[string]any
	service.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/sendMessage") {
			var payload map[string]any
			_ = json.NewDecoder(request.Body).Decode(&payload)
			sent = append(sent, payload)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":88}}`))}, nil
	})}
	service.replySubscriptions(context.Background(), "1", "国漫")
	defer service.Stop()
	first := sent[0]["text"].(string)
	if !strings.Contains(first, "（32）") || !strings.Contains(first, "第 1/3 页") ||
		!strings.Contains(first, "作品15") || strings.Contains(first, "作品16") {
		t.Fatalf("first page: %s", first)
	}
	var sessionID string
	for id := range service.sessions {
		sessionID = id
	}
	service.showSubscriptionListPage(context.Background(), 1, "1", sessionID, 2, 88)
	second := sent[1]["text"].(string)
	if !strings.Contains(second, "作品16") || !strings.Contains(second, "作品30") || strings.Contains(second, "作品31") {
		t.Fatalf("second page: %s", second)
	}
	service.showSubscriptionListPage(context.Background(), 1, "1", sessionID, 3, 88)
	last := sent[2]["text"].(string)
	if !strings.Contains(last, "作品32") || strings.Contains(last, "其他目录作品") {
		t.Fatalf("last page: %s", last)
	}
	count := len(sent)
	service.showSubscriptionListPage(context.Background(), 1, "1", sessionID, 4, 88)
	service.showSubscriptionListPage(context.Background(), 2, "2", sessionID, 1, 88)
	if len(sent) != count+1 {
		t.Fatal("out-of-range or cross-chat pagination unexpectedly exposed a page")
	}
}

type retryStateTestApp struct {
	deleteSubscriptionTestApp
	state string
}

func (a *retryStateTestApp) BotRetryNoticeState(context.Context) (string, error) {
	return a.state, nil
}

func (a *retryStateTestApp) BotSetRetryNoticeState(_ context.Context, value string) error {
	a.state = value
	return nil
}

func TestDailyRetrySurvivesRestartAndKeepsOldMessageOnSendFailure(t *testing.T) {
	app := &retryStateTestApp{state: `{"bad":null}`}
	deleted := []int64{}
	failSend := false
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload map[string]any
		_ = json.NewDecoder(request.Body).Decode(&payload)
		body := `{"ok":true,"result":{"message_id":123}}`
		if strings.HasSuffix(request.URL.Path, "/deleteMessage") {
			deleted = append(deleted, int64(payload["message_id"].(float64)))
		} else if failSend {
			body = `{"ok":false,"description":"failed"}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	makeService := func(token string) *Service {
		service := New(log.New(io.Discard, "", 0))
		service.cfg = Config{Enabled: true, Token: token, ChatIDs: []string{"1"}}
		service.SetApp(app)
		service.client = client
		return service
	}
	notice := DownloadRetryNotice{SourceID: "source", ComicID: "work", Title: "作品", Chapters: []string{"第1章"}}
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, retryNoticeLocation)
	service := makeService("test")
	service.downloadRetryScheduledAt(context.Background(), notice, now)
	restarted := makeService("test")
	restarted.downloadRetryScheduledAt(context.Background(), notice, now.Add(time.Hour))
	if len(deleted) != 1 || deleted[0] != 123 {
		t.Fatalf("restart lost previous message ID: %v", deleted)
	}
	for _, state := range restarted.retryNotices {
		if state.Rounds != 2 {
			t.Fatalf("restart lost attempts: %+v", state)
		}
	}
	failSend = true
	restarted.downloadRetryScheduledAt(context.Background(), notice, now.Add(2*time.Hour))
	if len(deleted) != 1 {
		t.Fatal("failed send removed the previous notification")
	}
	failSend = false
	otherBot := makeService("different-token")
	otherBot.downloadRetryScheduledAt(context.Background(), notice, now)
	if len(deleted) != 1 {
		t.Fatal("different bot reused another bot's message ID")
	}
}
