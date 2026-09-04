package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"scaffold-engine-go/internal/jig"
	"scaffold-engine-go/internal/learn"
)

// Guard against runLearn ever forgetting to register itself, since DisableFlagParsing commands
// silently accept any flag if RunE is never wired up to the root command tree.
func TestLearn_RegisteredOnRootCommand(t *testing.T) {
	root := &cobra.Command{Use: "scaffold"}
	root.AddCommand(newLearnCommand())
	found, _, err := root.Find([]string{"learn"})
	if err != nil || found.Name() != "learn" {
		t.Fatalf("expected `learn` to be a registered subcommand, got %v, err=%v", found, err)
	}
}

func TestLearn_RequiresExactlyOnePositional(t *testing.T) {
	exampleDir := writeExampleFolder(t)
	if _, err := run(t, newLearnCommand, exampleDir, "extra", "--output="+t.TempDir()); err == nil {
		t.Fatal("expected an error with more than one positional argument")
	}
	if _, err := run(t, newLearnCommand, "--output="+t.TempDir()); err == nil {
		t.Fatal("expected an error with no positional argument")
	}
}

func TestLearn_MissingOutputFlag(t *testing.T) {
	exampleDir := writeExampleFolder(t)
	_, err := run(t, newLearnCommand, exampleDir)
	if err == nil || !strings.Contains(err.Error(), "--output is required") {
		t.Fatalf("expected a clear --output-required error, got %v", err)
	}
}

func TestLearn_UnknownFlag(t *testing.T) {
	exampleDir := writeExampleFolder(t)
	_, err := run(t, newLearnCommand, exampleDir, "--output="+t.TempDir(), "--bogus")
	if err == nil {
		t.Fatal("expected an error for an unknown flag")
	}
}

// No provider env var set and no --provider flag: must fail before any scanning/network happens.
func TestLearn_NoProviderResolvable(t *testing.T) {
	t.Setenv(learn.EnvAnthropicAPIKey, "")
	t.Setenv(learn.EnvOpenAIAPIKey, "")

	exampleDir := writeExampleFolder(t)
	_, err := run(t, newLearnCommand, exampleDir, "--output="+t.TempDir())
	if err == nil {
		t.Fatal("expected an error when no LLM provider can be resolved")
	}
}

// fakeInferer lets command-level tests exercise the scan -> infer -> write pipeline without any
// network call - nothing in this codebase talked to a network service before `learn`, so this
// seam is new.
type fakeInferer struct {
	draft *learn.Draft
}

func (f *fakeInferer) Infer(ctx context.Context, files []learn.SourceFile) (*learn.Draft, error) {
	return f.draft, nil
}

func TestLearn_WritesValidDraftAndReportsIt(t *testing.T) {
	exampleDir := writeExampleFolder(t)
	outDir := filepath.Join(t.TempDir(), "draft")

	client := &fakeInferer{draft: &learn.Draft{
		Name:        "widget-controller",
		Description: "A learned controller",
		Variables: []learn.DraftVariable{
			{Name: "ClassName", Prompt: "Entity class name", Default: "Widget", Required: true},
		},
		Files: []learn.DraftFile{
			{Path: "{{ .ClassName }}Controller.java", Content: "class {{ .ClassName }}Controller {}\n"},
		},
	}}

	cmd := newLearnCommand()
	var buf strings.Builder
	cmd.SetOut(&buf)
	if err := runLearnWithClient(cmd, exampleDir, outDir, client); err != nil {
		t.Fatalf("runLearnWithClient returned error: %v", err)
	}

	if _, err := jig.Load(filepath.Join(outDir, jig.FileName)); err != nil {
		t.Fatalf("jig.Load on the written draft failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "{{ .ClassName }}Controller.java")); err != nil {
		t.Fatalf("expected the templated file on disk: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"widget-controller", "ClassName", outDir} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to mention %q, got:\n%s", want, out)
		}
	}
}

func writeExampleFolder(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "WidgetController.java", "class WidgetController {}\n")
	return dir
}
