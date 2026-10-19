package telegram

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"testing"

	"github.com/hyaeve/manco/internal/model"
)

func TestGroupDownloadJobsByWork(t *testing.T) {
	groups := groupDownloadJobs([]model.DownloadJob{
		{SourceID: "bao", ComicID: "1", ComicTitle: "作品甲", ChapterTitle: "第1话"},
		{SourceID: "bao", ComicID: "1", ComicTitle: "作品甲", ChapterTitle: "第2话"},
		{SourceID: "goda", ComicID: "2", ComicTitle: "作品乙", ChapterTitle: "第3话"},
	})
	if len(groups) != 2 || len(groups[0]) != 2 || len(groups[1]) != 1 {
		t.Fatalf("resume groups = %#v", groups)
	}
}

func TestRenderDownloadResumedNotice(t *testing.T) {
	text := renderDownloadResumedNotice(DownloadResumedNotice{
		Title:     "作品甲",
		Source:    "包子漫画",
		Author:    "测试作者",
		CoverBlur: true,
		Chapters:  []string{"第1话", "第2话"},
	})
	for _, want := range []string{
		"<b>作品甲（包子）</b>",
		"作者：测试作者",
		"Manco 已恢复任务下载 · 2 个章节已重新加入下载队列：",
		"· 第1话",
		"· 第2话",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("resume notice missing %q:\n%s", want, text)
		}
	}
}

func TestDownloadResumedNoticeUsesSubscriptionCoverBlur(t *testing.T) {
	var payload map[string]any
	service := New(log.New(io.Discard, "", 0))
	service.cfg.Token = "test-token"
	service.cfg.ChatIDs = []string{"1"}
	service.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode telegram payload: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":42}}`)),
		}, nil
	})}
	service.sendDownloadResumedNotice(context.Background(), DownloadResumedNotice{
		Title:     "测试作品",
		Source:    "包子漫画",
		Author:    "测试作者",
		CoverURL:  "https://example.test/cover.jpg",
		CoverBlur: true,
		Chapters:  []string{"第1话"},
	})
	if payload["has_spoiler"] != true {
		t.Fatalf("resumed cover blur not forwarded to Telegram: %+v", payload)
	}
}

func TestNotificationWorkTitleUsesSourceAbbreviation(t *testing.T) {
	if got := notificationWorkTitle("作品甲", "包子漫画"); got != "<b>作品甲（包子）</b>" {
		t.Fatalf("Chinese source title = %q", got)
	}
	if got := notificationWorkTitle("Work", "GoDA"); got != "<b>Work（GoDa）</b>" {
		t.Fatalf("English source title = %q", got)
	}
}

func TestRenderSubscriptionCheckedLayout(t *testing.T) {
	text := renderSubscriptionChecked(model.Subscription{
		Title:    "堕落物语2",
		Author:   "某儿童 / 蜜果实",
		SourceID: "miguoshi",
	}, "蜜果实漫画", 1, []string{"第 74 话"})
	want := strings.Join([]string{
		"<b>堕落物语2（蜜果）</b>",
		"某儿童 / 蜜果实",
		"发现 1 个新章节，已加入下载队列 · 例行检查：",
		"· 第 74 话",
	}, "\n")
	if text != want {
		t.Fatalf("routine check layout:\n%s\nwant:\n%s", text, want)
	}
}

func TestRenderDownloadRetryNotice(t *testing.T) {
	text := renderDownloadRetryNotice(DownloadRetryNotice{
		Title:    "测试作品",
		Source:   "包子漫画",
		Author:   "测试作者",
		Chapters: []string{"第 1 话", "第 2 话"},
	})
	for _, want := range []string{
		"<b>测试作品（包子）</b>",
		"作者：测试作者",
		"失败重试 · 2 个章节已重新加入下载队列：",
		"· 第 1 话",
		"· 第 2 话",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("retry notice missing %q:\n%s", want, text)
		}
	}
}

func TestRenderDownloadRetryNoticeLimitsChaptersToThree(t *testing.T) {
	text := renderDownloadRetryNotice(DownloadRetryNotice{
		Title:    "测试作品",
		Source:   "包子漫画",
		Author:   "测试作者",
		Chapters: []string{"第 1 话", "第 2 话", "第 3 话", "第 4 话", "第 5 话", "第 6 话"},
	})
	if strings.Count(text, "\n· ") != 3 {
		t.Fatalf("retry notice should list exactly three chapters:\n%s", text)
	}
	for _, want := range []string{
		"· 第 3 话",
		"… 其余 3 章（第 4 话 至 第 6 话）",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("limited retry notice missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "· 第 4 话") {
		t.Fatalf("retry notice leaked the fourth chapter as a bullet:\n%s", text)
	}
}
