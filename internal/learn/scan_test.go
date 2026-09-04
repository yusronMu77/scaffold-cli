package learn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScan_SkipsHiddenDirsAndBinaryFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "src/Widget.java", "class Widget {}")
	write(t, dir, ".git/HEAD", "ref: refs/heads/main")
	write(t, dir, "assets/logo.bin", "\x00\x01\x02binary")

	files, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}

	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	if len(paths) != 1 || paths[0] != "src/Widget.java" {
		t.Fatalf("expected only src/Widget.java, got %v", paths)
	}
}

func TestScan_RejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", perFileMaxBytes+1)
	write(t, dir, "Big.txt", big)

	if _, err := Scan(dir); err == nil {
		t.Fatal("expected an error for a file over the per-file size limit")
	}
}

func TestScan_ErrorsOnEmptyFolder(t *testing.T) {
	dir := t.TempDir()
	if _, err := Scan(dir); err == nil {
		t.Fatal("expected an error scanning an empty folder")
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", full, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}
