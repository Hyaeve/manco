package downloader

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// replacementOutputPath keeps a renamed chapter's existing path when its
// series directory and output format still match the new download.
func replacementOutputPath(previous, output string, directory bool) string {
	if strings.TrimSpace(previous) == "" {
		return output
	}
	previous, err := filepath.Abs(previous)
	if err != nil {
		return output
	}
	seriesDir, err := filepath.Abs(filepath.Dir(output))
	if err != nil {
		return output
	}
	relative, err := filepath.Rel(seriesDir, previous)
	if err != nil || relative == "." || relative == ".." || filepath.Dir(relative) != "." {
		return output
	}
	info, err := os.Lstat(previous)
	if err != nil {
		return output
	}
	if directory && info.IsDir() || !directory && info.Mode().IsRegular() &&
		strings.EqualFold(filepath.Ext(previous), filepath.Ext(output)) {
		return previous
	}
	return output
}

func (e *Engine) writeChapterDirectory(ctx context.Context, output string, entries []pageEntry) error {
	workspace, err := os.MkdirTemp(filepath.Dir(output), ".manco-chapter-")
	if err != nil {
		return err
	}
	keepPrevious := false
	defer func() {
		if !keepPrevious {
			_ = os.RemoveAll(workspace)
		}
	}()

	staged := filepath.Join(workspace, "incoming")
	if err := os.Mkdir(staged, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := copyFileAtomic(entry.path, filepath.Join(staged, entry.name)); err != nil {
			return err
		}
	}

	e.publishMu.Lock()
	defer e.publishMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	keepPrevious, err = publishChapterDirectory(staged, output)
	return err
}

// publishChapterDirectory swaps a fully staged chapter into place. Both paths
// must be on the same filesystem. The old directory stays recoverable in the
// staging workspace if publishing and restoring both fail or the process exits
// between the two renames.
func publishChapterDirectory(staged, output string) (keepPrevious bool, err error) {
	info, err := os.Lstat(output)
	if errors.Is(err, os.ErrNotExist) {
		return false, os.Rename(staged, output)
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("章节路径不是普通目录: %s", output)
	}

	backupDir := filepath.Join(filepath.Dir(staged), "previous")
	if err := os.Mkdir(backupDir, 0o700); err != nil {
		return false, err
	}
	backup := filepath.Join(backupDir, filepath.Base(output))
	if err := os.Rename(output, backup); err != nil {
		return false, err
	}
	if err := os.Rename(staged, output); err != nil {
		if restoreErr := os.Rename(backup, output); restoreErr != nil {
			return true, errors.Join(err, fmt.Errorf("恢复章节目录失败，原文件保留在 %s: %w", backup, restoreErr))
		}
		return false, err
	}
	return false, nil
}
