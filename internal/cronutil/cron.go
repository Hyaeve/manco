// Package cronutil centralizes the cron syntax used by subscription checks.
package cronutil

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// Default returns a weekly schedule at the hour when the subscription was
// created. For example, a subscription created on Friday at 21:xx runs every
// Friday at 21:00.
func Default(now time.Time) string {
	return fmt.Sprintf("0 %d * * %d", now.Hour(), int(now.Weekday()))
}

// Validate checks a standard five-field cron expression.
func Validate(expression string) error {
	_, err := parser.Parse(strings.TrimSpace(expression))
	return err
}

// Next returns the next scheduled time after the supplied instant.
func Next(expression string, after time.Time) (time.Time, error) {
	schedule, err := parser.Parse(strings.TrimSpace(expression))
	if err != nil {
		return time.Time{}, err
	}
	return schedule.Next(after), nil
}

// Due reports whether a subscription is due according to its cron expression.
// A blank expression means the user turned the scheduled update check off, so
// the subscription never becomes due on its own.
func Due(expression string, base, now time.Time) (bool, error) {
	if strings.TrimSpace(expression) == "" {
		return false, nil
	}
	if base.IsZero() {
		return false, nil
	}
	next, err := Next(expression, base)
	if err != nil {
		return false, err
	}
	return !next.After(now), nil
}
