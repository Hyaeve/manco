package subscriptionstate

import "strings"

// IsCompleted reports whether a source-provided serialization status means the
// work is finished. Chapter titles are intentionally not considered.
func IsCompleted(status string) bool {
	value := strings.ToLower(strings.TrimSpace(status))
	if value == "" {
		return false
	}
	for _, marker := range []string{
		"未完结", "未完結", "连载", "連載", "更新中",
		"ongoing", "serializing", "serialising", "serialized", "serialised",
		"publishing", "updating", "incomplete", "unfinished",
		"not completed", "not complete", "not finished",
	} {
		if strings.Contains(value, marker) {
			return false
		}
	}
	for _, marker := range []string{
		"完结", "完結", "全本", "完本", "已完",
		"completed", "finished", "complete", "ended",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
