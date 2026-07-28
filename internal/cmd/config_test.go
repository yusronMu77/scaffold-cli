package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// withTempCwd runs fn with the process cwd changed to dir, restoring it afterward. Needed
// because resolveScaffoldingCodeRoot reads a fixed relative filename (configFileName) from
// the current directory.
func withTempCwd(t *testing.T, dir string, fn func()) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer os.Chdir(orig)
	fn()
}

func TestResolveScaffoldingCodeRoot_FlagWins(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, configFileName), []byte("scaffolding_code: from-config\n"), 0o644); err != nil {
		t.Fatalf("writing config fixture: %v", err)
	}

	withTempCwd(t, dir, func() {
		got := resolveScaffoldingCodeRoot("from-flag")
		if got != "from-flag" {
			t.Errorf("expected flag value to win over config, got %q", got)
		}
	})
}

func TestResolveScaffoldingCodeRoot_ConfigFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, configFileName), []byte("scaffolding_code: ../../scaffolding-code\n"), 0o644); err != nil {
		t.Fatalf("writing config fixture: %v", err)
	}

	withTempCwd(t, dir, func() {
		got := resolveScaffoldingCodeRoot("")
		if got != "../../scaffolding-code" {
			t.Errorf("expected config file value, got %q", got)
		}
	})
}

func TestResolveScaffoldingCodeRoot_DefaultWhenNoConfig(t *testing.T) {
	dir := t.TempDir()

	withTempCwd(t, dir, func() {
		got := resolveScaffoldingCodeRoot("")
		if got != defaultScaffoldingCodeRoot {
			t.Errorf("expected default %q, got %q", defaultScaffoldingCodeRoot, got)
		}
	})
}
