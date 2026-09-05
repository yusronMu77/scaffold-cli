package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"scaffold-engine-go/internal/learn"
)

func TestLearnReview_RegisteredOnRootCommand(t *testing.T) {
	root := &cobra.Command{Use: "scaffold"}
	root.AddCommand(newLearnReviewCommand())
	found, _, err := root.Find([]string{"learn-review"})
	if err != nil || found.Name() != "learn-review" {
		t.Fatalf("expected `learn-review` to be a registered subcommand, got %v, err=%v", found, err)
	}
}

func TestLearnReview_RequiresExactlyTwoPositionals(t *testing.T) {
	if _, err := run(t, newLearnReviewCommand, "onlyone"); err == nil {
		t.Fatal("expected an error with only one positional argument")
	}
	if _, err := run(t, newLearnReviewCommand, "one", "two", "three"); err == nil {
		t.Fatal("expected an error with three positional arguments")
	}
}

func TestLearnReview_UnknownFlag(t *testing.T) {
	_, err := run(t, newLearnReviewCommand, "a", "b", "--bogus")
	if err == nil {
		t.Fatal("expected an error for an unknown flag")
	}
}

// learnDraftInto writes a draft directory whose own defaults reproduce exampleDir's
// WidgetController.java exactly, via the real scan -> infer -> write pipeline (a fakeInferer
// stands in for the network call, same seam learn_test.go already uses).
func learnDraftInto(t *testing.T, exampleDir, outDir string) {
	t.Helper()
	client := &fakeInferer{draft: &learn.Draft{
		Name: "widget-controller",
		Variables: []learn.DraftVariable{
			{Name: "ClassName", Default: "Widget", Required: true},
		},
		Files: []learn.DraftFile{
			{Path: "{{ .ClassName }}Controller.java", Content: "class {{ .ClassName }}Controller {}\n"},
		},
	}}
	cmd := newLearnCommand()
	if err := runLearnWithClient(cmd, exampleDir, outDir, client, false); err != nil {
		t.Fatalf("runLearnWithClient returned error: %v", err)
	}
}

func TestLearnReview_CleanDraftReportsOK(t *testing.T) {
	exampleDir := writeExampleFolder(t)
	draftDir := filepath.Join(t.TempDir(), "draft")
	learnDraftInto(t, exampleDir, draftDir)

	out, err := run(t, newLearnReviewCommand, draftDir, exampleDir)
	if err != nil {
		t.Fatalf("learn-review on a clean draft returned error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "OK") {
		t.Errorf("expected a clean report, got:\n%s", out)
	}
}

func TestLearnReview_MismatchExitsNonZero(t *testing.T) {
	exampleDir := writeExampleFolder(t)
	draftDir := filepath.Join(t.TempDir(), "draft")
	client := &fakeInferer{draft: &learn.Draft{
		Name: "widget-controller",
		Variables: []learn.DraftVariable{
			{Name: "ClassName", Default: "Widget", Required: true},
		},
		Files: []learn.DraftFile{
			// Deliberately wrong: doesn't reproduce "class WidgetController {}\n".
			{Path: "{{ .ClassName }}Controller.java", Content: "class {{ .ClassName }}Service {}\n"},
		},
	}}
	cmd := newLearnCommand()
	if err := runLearnWithClient(cmd, exampleDir, draftDir, client, false); err != nil {
		t.Fatalf("runLearnWithClient returned error: %v", err)
	}

	out, err := run(t, newLearnReviewCommand, draftDir, exampleDir)
	if err == nil {
		t.Fatal("expected a mismatched draft to exit non-zero")
	}
	if !strings.Contains(out, "issue") {
		t.Errorf("expected the report to mention issues found, got:\n%s", out)
	}
}
