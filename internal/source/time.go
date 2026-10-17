package source

import (
	"strconv"
	"strings"
	"time"
)

var localTimestampLayouts = []string{
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006/01/02 15:04:05",
	"2006/01/02 15:04",
	"2006年1月2日 15:04:05",
	"2006年1月2日 15:04",
	"2006-01-02",
	"2006/01/02",
	"2006年1月2日",
}

// NormalizeTimestamp turns source timestamps into RFC3339. Plugin chapters
// commonly use Unix milliseconds, while built-in web sources use local dates.
func NormalizeTimestamp(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if numeric, err := strconv.ParseFloat(value, 64); err == nil && numeric > 0 {
		seconds := int64(numeric)
		switch {
		case numeric >= 1e15:
			seconds = int64(numeric / 1e6)
		case numeric >= 1e12:
			seconds = int64(numeric / 1e3)
		}
		if seconds > 0 {
			return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
		}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.Format(time.RFC3339)
	}
	china := time.FixedZone("CST", 8*60*60)
	for _, layout := range localTimestampLayouts {
		if parsed, err := time.ParseInLocation(layout, value, china); err == nil {
			return parsed.Format(time.RFC3339)
		}
	}
	return ""
}
