package cronutil

import (
	"testing"
	"time"
)

func TestDueTreatsBlankExpressionAsDisabled(t *testing.T) {
	base := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.Local)

	due, err := Due("", base, base.Add(30*24*time.Hour))
	if err != nil {
		t.Fatalf("Due() error = %v", err)
	}
	if due {
		t.Fatal("空 Cron 表示关闭定时检查，不应该到期")
	}
	if due, err := Due("   ", base, base.Add(30*24*time.Hour)); err != nil || due {
		t.Fatalf("只有空白的 Cron 也应视为未设置: due=%v err=%v", due, err)
	}
}

func TestDueFollowsWeeklySchedule(t *testing.T) {
	// 2026-10-09 是周五 12:00，Default 生成「0 12 * * 5」。
	base := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.Local)
	expression := Default(base)
	if expression != "0 12 * * 5" {
		t.Fatalf("Default() = %q", expression)
	}

	due, err := Due(expression, base, base.Add(time.Minute))
	if err != nil {
		t.Fatalf("Due() error = %v", err)
	}
	if due {
		t.Fatal("基线后一分钟不应到期")
	}

	due, err = Due(expression, base, base.AddDate(0, 0, 7))
	if err != nil {
		t.Fatalf("Due() error = %v", err)
	}
	if !due {
		t.Fatal("整周之后应该到期")
	}
}
