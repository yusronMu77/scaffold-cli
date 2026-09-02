package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"scaffold-engine-go/internal/jig"
)

func runInitCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newInitCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if args == nil {
		args = []string{}
	}
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestInit_DefaultPath(t *testing.T) {
	dir := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd)

	if _, err := runInitCmd(t); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	jigPath := filepath.Join(dir, jig.FileName)
	m, err := jig.Load(jigPath)
	if err != nil {
		t.Fatalf("written %s does not parse: %v", jig.FileName, err)
	}
	if m.Name == "" {
		t.Errorf("expected a non-empty name in the starter jig")
	}

	// Deliberately still fails LoadRoot's empty-values check - "start empty" per the issue, not
	// a fake non-empty registry.
	if _, err := jig.LoadRoot(jigPath); err == nil {
		t.Errorf("expected LoadRoot to reject the starter jig's empty values: list, got no error")
	}
}

func TestInit_CreatesNestedPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "a", "b", "scaffolding-code")

	if _, err := runInitCmd(t, target); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(target, jig.FileName)); err != nil {
		t.Fatalf("expected %s to exist: %v", jig.FileName, err)
	}
}

func TestInit_RefusesToOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, jig.FileName, "name: existing\nvalues:\n  - name: keep-me\n")

	if _, err := runInitCmd(t, dir); err == nil {
		t.Fatal("expected init to refuse to overwrite an existing jig.yaml without --force")
	}

	content, err := os.ReadFile(filepath.Join(dir, jig.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "name: existing\nvalues:\n  - name: keep-me\n" {
		t.Errorf("existing jig.yaml was modified despite the refusal: %s", content)
	}
}

func TestInit_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, jig.FileName, "name: existing\nvalues:\n  - name: keep-me\n")

	if _, err := runInitCmd(t, dir, "--force"); err != nil {
		t.Fatalf("init --force failed: %v", err)
	}

	m, err := jig.Load(filepath.Join(dir, jig.FileName))
	if err != nil {
		t.Fatalf("overwritten %s does not parse: %v", jig.FileName, err)
	}
	if m.Name == "existing" {
		t.Errorf("expected --force to overwrite the existing jig.yaml")
	}
}

func TestInit_RejectsExtraPositionals(t *testing.T) {
	dir := t.TempDir()
	if _, err := runInitCmd(t, dir, "extra"); err == nil {
		t.Fatal("expected init to reject more than one positional argument")
	}
}

func TestInit_RejectsUnknownFlag(t *testing.T) {
	dir := t.TempDir()
	if _, err := runInitCmd(t, dir, "--bogus"); err == nil {
		t.Fatal("expected init to reject an unknown flag")
	}
}

// Guard against runInit ever forgetting to register itself, since DisableFlagParsing commands
// silently accept any flag if RunE is never wired up to the root command tree.
func TestInit_RegisteredOnRootCommand(t *testing.T) {
	root := &cobra.Command{Use: "scaffold"}
	root.AddCommand(newInitCommand())
	found, _, err := root.Find([]string{"init"})
	if err != nil || found.Name() != "init" {
		t.Fatalf("expected `init` to be a registered subcommand, got %v, err=%v", found, err)
	}
}
