package source

import "testing"

func TestNormalizeTimestamp(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "unix seconds", raw: "1792209600", want: "2026-10-17T04:00:00Z"},
		{name: "unix milliseconds", raw: "1792209600000", want: "2026-10-17T04:00:00Z"},
		{name: "rfc3339", raw: "2026-10-17T12:34:56+08:00", want: "2026-10-17T12:34:56+08:00"},
		{name: "local date time", raw: "2026-10-17 12:34:56", want: "2026-10-17T12:34:56+08:00"},
		{name: "chinese date", raw: "2026年10月17日", want: "2026-10-17T00:00:00+08:00"},
		{name: "invalid", raw: "最近更新", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := NormalizeTimestamp(test.raw); got != test.want {
				t.Fatalf("NormalizeTimestamp(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}
