// Package localops implements file operations used by the local library UI.
package localops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hyaeve/manco/internal/downloader"
)

type MergeResult struct {
	ParentDir  string
	MovedFiles int
}

type SimplifyResult struct {
	FolderRenamed bool
	MergedInto    string
	Renamed       int
}

// Merge moves every entry from the copy directories into the first selected
// parent directory. Existing names are never overwritten; a numeric suffix is
// used when a destination name already exists.
func Merge(parent string, copies []string) (MergeResult, error) {
	parent = filepath.Clean(strings.TrimSpace(parent))
	info, err := os.Stat(parent)
	if err != nil {
		return MergeResult{}, fmt.Errorf("读取母本目录失败: %w", err)
	}
	if !info.IsDir() {
		return MergeResult{}, errors.New("母本路径不是目录")
	}
	result := MergeResult{ParentDir: parent}
	for _, copyDir := range copies {
		copyDir = filepath.Clean(strings.TrimSpace(copyDir))
		if copyDir == "" || copyDir == "." {
			continue
		}
		if samePath(parent, copyDir) {
			continue
		}
		if isNested(parent, copyDir) || isNested(copyDir, parent) {
			return result, errors.New("母本目录和副本目录不能相互嵌套")
		}
		info, err := os.Stat(copyDir)
		if err != nil {
			return result, fmt.Errorf("读取副本目录失败: %w", err)
		}
		if !info.IsDir() {
			return result, errors.New("副本路径不是目录")
		}
		moved, _, err := moveEntries(copyDir, parent, false)
		if err != nil {
			return result, err
		}
		result.MovedFiles += moved
		_ = os.Remove(copyDir)
	}
	return result, nil
}

// ConvertToSimplified converts a local item directory and its top-level file
// names to Simplified Chinese. If the simplified directory already exists, the
// entries are merged into it instead of overwriting it.
func ConvertToSimplified(dir string) (SimplifyResult, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	info, err := os.Stat(dir)
	if err != nil {
		return SimplifyResult{}, fmt.Errorf("读取本地作品失败: %w", err)
	}
	if !info.IsDir() {
		return SimplifyResult{}, errors.New("本地作品路径不是目录")
	}
	var result SimplifyResult
	parent := filepath.Dir(dir)
	current := dir
	simplifiedName := downloader.SimplifiedText(filepath.Base(dir))
	if simplifiedName != filepath.Base(dir) {
		target := filepath.Join(parent, simplifiedName)
		if existing, statErr := os.Stat(target); statErr == nil {
			if !existing.IsDir() {
				return result, fmt.Errorf("目标路径已存在且不是目录: %s", target)
			}
			moved, renamed, moveErr := moveEntries(current, target, true)
			if moveErr != nil {
				return result, moveErr
			}
			result.Renamed += renamed
			if moved > 0 {
				result.MergedInto = target
			}
			_ = os.Remove(current)
			current = target
		} else if os.IsNotExist(statErr) {
			if err := os.Rename(current, target); err != nil {
				return result, fmt.Errorf("重命名作品目录失败: %w", err)
			}
			result.FolderRenamed = true
			result.Renamed++
			current = target
		} else {
			return result, statErr
		}
	}
	entries, err := os.ReadDir(current)
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		name := entry.Name()
		simplified := downloader.SimplifiedText(name)
		if simplified == name {
			continue
		}
		target := uniquePath(current, simplified)
		if err := os.Rename(filepath.Join(current, name), target); err != nil {
			return result, fmt.Errorf("重命名文件失败: %w", err)
		}
		result.Renamed++
	}
	return result, nil
}

func moveEntries(source, target string, simplifyNames bool) (int, int, error) {
	entries, err := os.ReadDir(source)
	if err != nil {
		return 0, 0, err
	}
	movedFiles := 0
	renamed := 0
	for _, entry := range entries {
		sourcePath := filepath.Join(source, entry.Name())
		name := entry.Name()
		if simplifyNames {
			nextName := downloader.SimplifiedText(name)
			if nextName != name {
				renamed++
				name = nextName
			}
		}
		targetPath := uniquePath(target, name)
		if err := os.Rename(sourcePath, targetPath); err != nil {
			return movedFiles, renamed, fmt.Errorf("移动文件失败: %w", err)
		}
		count, err := countFiles(targetPath)
		if err != nil {
			return movedFiles, renamed, err
		}
		movedFiles += count
	}
	return movedFiles, renamed, nil
}

func countFiles(path string) (int, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		if info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return 1, nil
		}
		return 0, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		child, err := countFiles(filepath.Join(path, entry.Name()))
		if err != nil {
			return 0, err
		}
		count += child
	}
	return count, nil
}

func uniquePath(dir, name string) string {
	candidate := filepath.Join(dir, name)
	if _, err := os.Lstat(candidate); os.IsNotExist(err) {
		return candidate
	}
	extension := filepath.Ext(name)
	base := strings.TrimSuffix(name, extension)
	for index := 2; ; index++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, index, extension))
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

func samePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && strings.EqualFold(filepath.Clean(leftAbs), filepath.Clean(rightAbs))
}

func isNested(parent, child string) bool {
	parentAbs, parentErr := filepath.Abs(parent)
	childAbs, childErr := filepath.Abs(child)
	if parentErr != nil || childErr != nil {
		return false
	}
	rel, err := filepath.Rel(parentAbs, childAbs)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
