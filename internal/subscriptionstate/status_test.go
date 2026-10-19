package subscriptionstate

import "testing"

func TestIsCompleted(t *testing.T) {
	cases := map[string]bool{
		"":              false,
		"连载中":           false,
		"未完结":           false,
		"連載中（未完結）":      false,
		"Ongoing":       false,
		"Incomplete":    false,
		"Not Completed": false,
		"Unfinished":    false,
		"已完结":           true,
		"完結":            true,
		"全本":            true,
		"Completed":     true,
		"Finished":      true,
		"Complete":      true,
		"第12话 终章":       false,
	}
	for input, want := range cases {
		if got := IsCompleted(input); got != want {
			t.Fatalf("IsCompleted(%q) = %v, want %v", input, got, want)
		}
	}
}
