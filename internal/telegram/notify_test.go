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

func TestRenderSubscriptionArchiveWarning(t *testing.T) {
	text := renderSubscriptionArchiveWarning(SubscriptionArchiveNotice{
		Title:          "夏日深处",
		Source:         "包子漫画",
		Author:         "测试作者",
		ChapterCount:   42,
		DaysWithoutNew: 12,
		DaysRemaining:  3,
		Completed:      true,
	})
	for _, want := range []string{
		"订阅归档预警",
		"<b>夏日深处（包子）</b>",
		"内容源：包子漫画",
		"作者：测试作者",
		"章节总数：42 章",
		"作品状态：已完结",
		"已连续 12 天未检测到新章节",
		"预计 3 天后自动归档",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("archive warning missing %q:\n%s", want, text)
		}
	}
}

func TestRenderSubscriptionArchived(t *testing.T) {
	text := renderSubscriptionArchived(SubscriptionArchiveNotice{
		Title:         "长期连载",
		Source:        "哔咔漫画",
		Author:        "测试作者",
		ChapterCount:  80,
		ThresholdDays: 30,
	})
	for _, want := range []string{
		"订阅已归档",
		"<b>长期连载（哔咔）</b>",
		"内容源：哔咔漫画",
		"作者：测试作者",
		"章节总数：80 章",
		"未检测到完结状态，连续 30 天未检测到新章节",
		"已自动移入归档集",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("archive notice missing %q:\n%s", want, text)
		}
	}
}

func TestSubscriptionArchiveNoticeUsesCoverBlur(t *testing.T) {
	var payload map[string]any
	service := New(log.New(io.Discard, "", 0))
	service.cfg.Enabled = true
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
	service.SubscriptionArchived(context.Background(), SubscriptionArchiveNotice{
		Title:         "测试作品",
		Source:        "包子漫画",
		Author:        "测试作者",
		CoverURL:      "https://example.test/cover.jpg",
		CoverBlur:     true,
		ChapterCount:  18,
		ThresholdDays: 30,
	})
	if payload["has_spoiler"] != true {
		t.Fatalf("archive cover blur not forwarded to Telegram: %+v", payload)
	}
	caption, _ := payload["caption"].(string)
	if !strings.Contains(caption, "章节总数：18 章") {
		t.Fatalf("archive caption missing chapter count: %s", caption)
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

func TestRenderBangumiMatchedNotice(t *testing.T) {
	text := renderBangumiMatchedNotice(BangumiMatchedNotice{
		Title:         "葬送的芙莉莲",
		OriginalTitle: "葬送のフリーレン",
		Source:        "包子漫画",
		Author:        "山田钟人",
		Description:   "作品讲述：魔王讨伐结束后，精灵魔法使芙莉莲踏上新的旅程。",
		Status:        "连载中",
	})
	for _, want := range []string{
		"<b>Bangumi 匹配成功</b>",
		"<b>葬送的芙莉莲（包子）</b>",
		"内容源：包子漫画",
		"作者：山田钟人",
		"Bangumi 名称：葬送のフリーレン",
		"作品状态：连载中",
		"简介：魔王讨伐结束后，精灵魔法使芙莉莲踏上新的旅程。",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("matched notice missing %q:\n%s", want, text)
		}
	}
}

func TestBangumiMatchedUsesCoverBlur(t *testing.T) {
	var payload map[string]any
	service := New(log.New(io.Discard, "", 0))
	service.cfg.Enabled = true
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
	service.BangumiMatched(context.Background(), BangumiMatchedNotice{
		Title:     "测试作品",
		Source:    "包子漫画",
		CoverURL:  "https://example.test/cover.jpg",
		CoverBlur: true,
	})
	if payload["has_spoiler"] != true {
		t.Fatalf("matched cover blur not forwarded to Telegram: %+v", payload)
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
