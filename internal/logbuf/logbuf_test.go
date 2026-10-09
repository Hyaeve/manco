package logbuf

import (
	"bytes"
	"strings"
	"testing"
)

func TestBufferKeepsRecentLinesAndForwardsOutput(t *testing.T) {
	var out bytes.Buffer
	buffer := New(2, &out)
	if _, err := buffer.Write([]byte("one\ntwo\nthree\n")); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	lines := buffer.Lines(0)
	if len(lines) != 2 || lines[0] != "two" || lines[1] != "three" {
		t.Fatalf("lines = %#v, want [two three]", lines)
	}
	if !strings.Contains(out.String(), "one") || !strings.Contains(out.String(), "three") {
		t.Fatalf("forwarded output = %q, want original payload", out.String())
	}
}

func TestBufferLimit(t *testing.T) {
	buffer := New(10, nil)
	_, _ = buffer.Write([]byte("a\nb\nc\n"))
	lines := buffer.Lines(2)
	if len(lines) != 2 || lines[0] != "b" || lines[1] != "c" {
		t.Fatalf("lines = %#v, want last two entries", lines)
	}
}
