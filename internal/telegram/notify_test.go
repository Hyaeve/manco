package telegram

import (
	"strings"
	"testing"

	"github.com/hyaeve/manco/internal/model"
)

func TestRenderDownloadResumed(t *testing.T) {
	text := renderDownloadResumed([]model.DownloadJob{
		{SourceID: "bao", ComicTitle: "作品甲", ChapterTitle: "第1话"},
		{SourceID: "goda", ComicTitle: "作品乙", ChapterTitle: "第2话"},
	}, map[string]string{"bao": "包子漫画", "goda": "GoDA"})
	for _, want := range []string{
		"Manco 已恢复任务下载",
		"正在下载：",
		"1. 《作品甲》（包子）· 第1话",
		"2. 《作品乙》（GoDa）· 第2话",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("resume notice missing %q:\n%s", want, text)
		}
	}
	if renderDownloadResumed(nil, nil) != "" {
		t.Fatal("empty interrupted jobs should not create a notice")
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
