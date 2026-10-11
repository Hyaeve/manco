package localops

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMergeMovesCopyFilesIntoFirstDirectory(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "母本")
	copyDir := filepath.Join(root, "副本")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(copyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "001.cbz"), []byte("parent"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copyDir, "002.cbz"), []byte("copy"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Merge(parent, []string{copyDir})
	if err != nil {
		t.Fatal(err)
	}
	if result.MovedFiles != 1 {
		t.Fatalf("moved files = %d, want 1", result.MovedFiles)
	}
	for _, name := range []string{"001.cbz", "002.cbz"} {
		if _, err := os.Stat(filepath.Join(parent, name)); err != nil {
			t.Fatalf("missing merged file %s: %v", name, err)
		}
	}
	if _, err := os.Stat(copyDir); !os.IsNotExist(err) {
		t.Fatalf("copy directory still exists: %v", err)
	}
}

func TestMergeDoesNotOverwriteDestinationNames(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "母本")
	copyDir := filepath.Join(root, "副本")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(copyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "001.cbz"), []byte("parent"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copyDir, "001.cbz"), []byte("copy"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Merge(parent, []string{copyDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(parent, "001 (2).cbz")); err != nil {
		t.Fatalf("conflicting file was not preserved: %v", err)
	}
}

func TestConvertToSimplifiedRenamesDirectoryAndFiles(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "龍珠")
	if err := os.MkdirAll(original, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(original, "第一話.cbz"), []byte("chapter"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := ConvertToSimplified(original)
	if err != nil {
		t.Fatal(err)
	}
	if result.Renamed < 2 {
		t.Fatalf("renamed = %d, want at least directory and file", result.Renamed)
	}
	if _, err := os.Stat(filepath.Join(root, "龙珠", "第一话.cbz")); err != nil {
		t.Fatalf("simplified paths missing: %v", err)
	}
}
