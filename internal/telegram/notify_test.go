package telegram

import (
	"strings"
	"testing"

	"github.com/hyaeve/manco/internal/model"
)

func TestRenderDownloadResumed(t *testing.T) {
	text := renderDownloadResumed([]model.DownloadJob{
		{ComicTitle: "作品甲", ChapterTitle: "第1话"},
		{ComicTitle: "作品乙", ChapterTitle: "第2话"},
	})
	for _, want := range []string{
		"Manco 已恢复任务下载",
		"正在下载：",
		"1. 《作品甲》· 第1话",
		"2. 《作品乙》· 第2话",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("resume notice missing %q:\n%s", want, text)
		}
	}
	if renderDownloadResumed(nil) != "" {
		t.Fatal("empty interrupted jobs should not create a notice")
	}
}
