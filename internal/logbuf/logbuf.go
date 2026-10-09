// Package logbuf keeps a bounded in-memory copy of application logs so the
// web UI can show recent entries. Writes are also forwarded to the wrapped
// writer (os.Stdout), which keeps `docker logs` working as before.
package logbuf

import (
	"io"
	"strings"
	"sync"
)

// Buffer is an io.Writer that mirrors every line into a fixed-size ring while
// forwarding the original bytes to the wrapped writer.
type Buffer struct {
	mu    sync.RWMutex
	lines []string
	max   int
	out   io.Writer
}

// New creates a buffer that keeps at most max lines and also writes to out.
func New(max int, out io.Writer) *Buffer {
	if max <= 0 {
		max = 500
	}
	return &Buffer{max: max, out: out}
}

func (b *Buffer) Write(p []byte) (int, error) {
	if b.out != nil {
		_, _ = b.out.Write(p)
	}
	text := strings.TrimRight(string(p), "\r\n")
	if text == "" {
		return len(p), nil
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		b.append(line)
	}
	return len(p), nil
}

func (b *Buffer) append(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, line)
	if len(b.lines) > b.max {
		b.lines = append([]string(nil), b.lines[len(b.lines)-b.max:]...)
	}
}

// Lines returns the most recent limit lines, oldest first. A non-positive or
// oversized limit returns every buffered line.
func (b *Buffer) Lines(limit int) []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if limit <= 0 || limit > len(b.lines) {
		limit = len(b.lines)
	}
	out := make([]string, limit)
	copy(out, b.lines[len(b.lines)-limit:])
	return out
}
