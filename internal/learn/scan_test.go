package learn

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestScan_SkipsHiddenDirsAndBinaryFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "src/Widget.java", "class Widget {}")
	write(t, dir, ".git/HEAD", "ref: refs/heads/main")
	write(t, dir, "assets/logo.bin", "\x00\x01\x02binary")

	files, _, err := Scan(dir)
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

// .gitignore and friends are part of the pattern being learned, so a dot-*file* is kept even
// though a dot-*directory* is skipped. `.env` is the exception - it's a credential store.
func TestScan_KeepsDotFilesButSkipsDotEnv(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "src/Widget.java", "class Widget {}")
	write(t, dir, ".gitignore", "target/\n")
	write(t, dir, ".dockerignore", "*.md\n")
	write(t, dir, ".env", "API_KEY=sekrit\n")
	write(t, dir, ".env.local", "API_KEY=also-sekrit\n")

	files, skipped, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}

	got := map[string]bool{}
	for _, f := range files {
		got[f.Path] = true
		if strings.Contains(f.Content, "sekrit") {
			t.Fatalf("%s carries a credential into the prompt", f.Path)
		}
	}
	for _, want := range []string{".gitignore", ".dockerignore", "src/Widget.java"} {
		if !got[want] {
			t.Errorf("expected %s to be kept, got %v", want, files)
		}
	}
	if len(skipped) != 2 {
		t.Errorf("expected both .env files reported as skipped, got %v", skipped)
	}
}

// WalkDir doesn't follow symlinks but os.ReadFile does: without an explicit skip, a link named
// like ordinary config ships its target - possibly a credential outside the folder entirely.
func TestScan_SkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "src/Widget.java", "class Widget {}")

	secret := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(secret, []byte("aws_secret_access_key = sekrit\n"), 0o600); err != nil {
		t.Fatalf("writing link target: %v", err)
	}
	link := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks not creatable in this environment: %v", err)
	}

	files, skipped, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	for _, f := range files {
		if strings.Contains(f.Content, "sekrit") {
			t.Fatalf("%s followed a symlink and carried its target into the prompt", f.Path)
		}
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "config.yaml") {
		t.Errorf("expected the symlink reported as skipped, got %v", skipped)
	}
}

func TestScan_RejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", perFileMaxBytes+1)
	write(t, dir, "Big.txt", big)

	if _, _, err := Scan(dir); err == nil {
		t.Fatal("expected an error for a file over the per-file size limit")
	}
}

func TestScan_ErrorsOnEmptyFolder(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := Scan(dir); err == nil {
		t.Fatal("expected an error scanning an empty folder")
	}
}

func TestScan_SkipsCredentialFilesButKeepsOrdinaryConfig(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "src/Widget.java", "class Widget {}")
	write(t, dir, "src/main/resources/application.properties", "server.port=8080")
	write(t, dir, "certs/server.pem", "-----BEGIN PRIVATE KEY-----")
	write(t, dir, "config/credentials.json", `{"private_key":"x"}`)
	write(t, dir, "keys/id_rsa", "-----BEGIN OPENSSH PRIVATE KEY-----")

	files, skipped, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}

	var sent []string
	for _, f := range files {
		sent = append(sent, f.Path)
	}
	for _, p := range sent {
		if p == "certs/server.pem" || p == "config/credentials.json" || p == "keys/id_rsa" {
			t.Fatalf("credential file %s was included in what gets sent to the provider", p)
		}
	}
	// An ordinary config file is part of the pattern being learned, so it must still be included.
	if !slices.Contains(sent, "src/main/resources/application.properties") {
		t.Errorf("expected application.properties to be kept, got %v", sent)
	}
	if len(skipped) != 3 {
		t.Errorf("expected 3 skipped credential files, got %v", skipped)
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
