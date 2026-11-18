package downloader

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRenameFileReplacingKeepsOldOutputOnFailure(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "chapter.cbz")
	if err := os.WriteFile(output, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := renameFileReplacing(filepath.Join(dir, "missing.part"), output); err == nil {
		t.Fatal("missing staged file did not fail")
	}
	assertPublishedFile(t, output, "old")
	if err := writeFileAtomic(output, []byte("new")); err != nil {
		t.Fatal(err)
	}
	assertPublishedFile(t, output, "new")
}

func TestReplacementOutputPathChecksLocationAndFormat(t *testing.T) {
	series := t.TempDir()
	other := t.TempDir()
	old := filepath.Join(series, "original.cbz")
	if err := os.WriteFile(old, []byte("archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(series, "renamed.cbz")
	if got := replacementOutputPath(old, output, false); got != old {
		t.Fatalf("matching output path = %q, want %q", got, old)
	}
	newFormat := filepath.Join(series, "renamed.pdf")
	if got := replacementOutputPath(old, newFormat, false); got != newFormat {
		t.Fatalf("changed format path = %q, want %q", got, newFormat)
	}
	newSeries := filepath.Join(other, "renamed.cbz")
	if got := replacementOutputPath(old, newSeries, false); got != newSeries {
		t.Fatalf("changed series path = %q, want %q", got, newSeries)
	}
	oldDir := filepath.Join(series, "old-episode")
	if err := os.Mkdir(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	newDir := filepath.Join(series, "new-episode")
	if got := replacementOutputPath(oldDir, newDir, true); got != oldDir {
		t.Fatalf("matching directory path = %q, want %q", got, oldDir)
	}
	if got := replacementOutputPath(old, newDir, true); got != newDir {
		t.Fatalf("changed output type path = %q, want %q", got, newDir)
	}
	t.Run("symlink", func(t *testing.T) {
		link := filepath.Join(series, "link.cbz")
		if err := os.Symlink(old, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if got := replacementOutputPath(link, output, false); got != output {
			t.Fatalf("symlink selected as prior artifact: %s", got)
		}
	})
}

func TestWriteChapterDirectoryStagesAllFilesBeforeReplacement(t *testing.T) {
	dir := t.TempDir()
	sources := t.TempDir()
	output := filepath.Join(dir, "episode")
	if err := os.Mkdir(output, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(output, "old.mp4")
	if err := os.WriteFile(old, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(sources, "video.mp4")
	second := filepath.Join(sources, "subtitle.srt")
	if err := os.WriteFile(first, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := []pageEntry{{name: "001.mp4", path: first}, {name: "002.srt", path: second}}
	engine := &Engine{}
	if err := engine.writeChapterDirectory(context.Background(), output, entries); err == nil {
		t.Fatal("incomplete staged chapter did not fail")
	}
	assertPublishedFile(t, old, "old")
	if _, err := os.Stat(filepath.Join(output, "001.mp4")); !os.IsNotExist(err) {
		t.Fatalf("partial chapter was published: %v", err)
	}

	if err := os.WriteFile(second, []byte("subtitles"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := engine.writeChapterDirectory(context.Background(), output, entries); err != nil {
		t.Fatal(err)
	}
	assertPublishedFile(t, filepath.Join(output, "001.mp4"), "video")
	assertPublishedFile(t, filepath.Join(output, "002.srt"), "subtitles")
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("previous chapter file remained after successful replacement: %v", err)
	}
	children, err := os.ReadDir(dir)
	if err != nil || len(children) != 1 || children[0].Name() != "episode" {
		t.Fatalf("temporary publication directories not cleaned: %v, %v", children, err)
	}
}

func TestPublishChapterDirectoryRestoresPreviousDirectoryOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "episode")
	if err := os.Mkdir(output, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(output, "old.mp4")
	if err := os.WriteFile(old, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	keepPrevious, err := publishChapterDirectory(filepath.Join(workspace, "missing"), output)
	if err == nil || keepPrevious {
		t.Fatalf("publish should fail and restore previous directory: retain workspace = %v, err = %v", keepPrevious, err)
	}
	assertPublishedFile(t, old, "old")
}

func assertPublishedFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("published file %q = %q, %v, want %q", path, got, err, want)
	}
}
