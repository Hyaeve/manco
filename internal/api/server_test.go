package api

import "testing"

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name  string
		left  string
		right string
		want  int
	}{
		{name: "newer patch", left: "v0.1.4", right: "v0.1.3", want: 1},
		{name: "older patch", left: "v0.1.3", right: "v0.1.4", want: -1},
		{name: "equal", left: "v0.1.4", right: "0.1.4", want: 0},
		{name: "hundred base middle", left: "v0.101.0", right: "v0.100.99", want: 1},
		{name: "longer patch", left: "v1.2.0", right: "v1.2", want: 0},
		{name: "prerelease suffix ignored", left: "v0.1.4-rc1", right: "v0.1.4", want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := compareVersions(test.left, test.right); got != test.want {
				t.Fatalf("compareVersions(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
			}
		})
	}
}
