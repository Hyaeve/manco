// Package logbuf keeps a bounded in-memory copy of application logs so the
// web UI can show recent entries. Writes are also forwarded to the wrapped
// writer (os.Stdout), which keeps `docker logs` working as before.
package logbuf

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Buffer is an io.Writer that mirrors every line into a fixed-size ring while
// forwarding the original bytes to the wrapped writer.
type Buffer struct {
	mu    sync.RWMutex
	lines []string
	max   int
	out   io.Writer
	// 按天落盘：fileDir 为空或 retentionDays<=0 时不写文件。
	fileDir       string
	retentionDays int
	fileDay       string
}

// New creates a buffer that keeps at most max lines and also writes to out.
func New(max int, out io.Writer) *Buffer {
	if max <= 0 {
		max = 10000
	}
	return &Buffer{max: max, out: out}
}

// SetMax 调整环形缓冲容量；缩容时丢弃最旧的条目。
func (b *Buffer) SetMax(max int) {
	if max <= 0 {
		max = 10000
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.max = max
	if len(b.lines) > max {
		b.lines = append([]string(nil), b.lines[len(b.lines)-max:]...)
	}
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
	b.writeFileLocked(line)
}

// SetFileSink 把日志按天追加到 dir/manco-YYYY-MM-DD.log，并清理超过
// retentionDays 天的旧文件（0 表示不落盘）。
func (b *Buffer) SetFileSink(dir string, retentionDays int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fileDir = strings.TrimSpace(dir)
	b.retentionDays = retentionDays
	b.fileDay = ""
}

func (b *Buffer) writeFileLocked(line string) {
	if b.fileDir == "" || b.retentionDays <= 0 {
		return
	}
	day := time.Now().Format("2006-01-02")
	if day != b.fileDay {
		b.fileDay = day
		b.pruneLocked()
	}
	if err := os.MkdirAll(b.fileDir, 0o755); err != nil {
		return
	}
	file, err := os.OpenFile(filepath.Join(b.fileDir, "manco-"+day+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = file.WriteString(line + "\n")
	_ = file.Close()
}

func (b *Buffer) pruneLocked() {
	entries, err := os.ReadDir(b.fileDir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -b.retentionDays)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "manco-") || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(b.fileDir, entry.Name()))
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
