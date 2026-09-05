package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"scaffold-engine-go/internal/jig"
	"scaffold-engine-go/internal/learn"
)

func TestLearnPromote_RegisteredOnRootCommand(t *testing.T) {
	root := &cobra.Command{Use: "scaffold"}
	root.AddCommand(newLearnPromoteCommand())
	found, _, err := root.Find([]string{"learn-promote"})
	if err != nil || found.Name() != "learn-promote" {
		t.Fatalf("expected `learn-promote` to be a registered subcommand, got %v, err=%v", found, err)
	}
}

func TestLearnPromote_RequiresExactlyOnePositional(t *testing.T) {
	if _, err := run(t, newLearnPromoteCommand); err == nil {
		t.Fatal("expected an error with no positional argument")
	}
	if _, err := run(t, newLearnPromoteCommand, "one", "two"); err == nil {
		t.Fatal("expected an error with two positional arguments")
	}
}

func TestLearnPromote_HappyPath(t *testing.T) {
	draftDir := t.TempDir()
	if err := learn.WriteDraft(draftDir, &learn.Draft{
		Name:  "widget",
		Files: []learn.DraftFile{{Path: "Widget.java", Content: "class Widget {}\n"}},
	}, false); err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}

	out, err := run(t, newLearnPromoteCommand, draftDir)
	if err != nil {
		t.Fatalf("learn-promote returned error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Promoted") {
		t.Errorf("expected a confirmation message, got:\n%s", out)
	}

	m, err := jig.Load(filepath.Join(draftDir, jig.FileName))
	if err != nil {
		t.Fatalf("jig.Load after promote failed: %v", err)
	}
	if m.Candidate {
		t.Error("expected Candidate to be cleared after learn-promote")
	}
}

func TestLearnPromote_RejectsNonCandidate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, jig.FileName),
		[]byte("name: not-a-draft\nfiles:\n  - path: x.txt\n"), 0o644); err != nil {
		t.Fatalf("writing jig.yaml: %v", err)
	}
	if _, err := run(t, newLearnPromoteCommand, dir); err == nil {
		t.Fatal("expected learn-promote to reject a non-candidate jig, got nil")
	}
}
