package learn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scaffold-engine-go/internal/jig"
)

func TestPromote_ClearsCandidateAndStaysValid(t *testing.T) {
	dir := t.TempDir()
	d := &Draft{
		Name: "widget",
		Files: []DraftFile{
			{Path: "Widget.java", Content: "class Widget {}\n"},
		},
	}
	if err := WriteDraft(dir, d, false); err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}

	before, err := jig.Load(filepath.Join(dir, jig.FileName))
	if err != nil {
		t.Fatalf("jig.Load before promote failed: %v", err)
	}
	if !before.Candidate {
		t.Fatal("expected WriteDraft's output to start as a candidate")
	}

	if err := Promote(dir); err != nil {
		t.Fatalf("Promote returned error: %v", err)
	}

	after, err := jig.Load(filepath.Join(dir, jig.FileName))
	if err != nil {
		t.Fatalf("jig.Load after promote failed: %v", err)
	}
	if after.Candidate {
		t.Error("expected Candidate to be cleared after Promote")
	}
	if after.Name != "widget" {
		t.Errorf("expected the rest of the jig to survive promotion, got %+v", after)
	}
}

func TestPromote_AlreadyPromotedIsRejected(t *testing.T) {
	dir := t.TempDir()
	d := &Draft{Name: "widget", Files: []DraftFile{{Path: "Widget.java", Content: "x\n"}}}
	if err := WriteDraft(dir, d, false); err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}
	if err := Promote(dir); err != nil {
		t.Fatalf("first Promote returned error: %v", err)
	}
	if err := Promote(dir); err == nil {
		t.Fatal("expected a second Promote to be rejected, got nil")
	}
}

func TestPromote_NonCandidateJigIsRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, jig.FileName),
		[]byte("name: not-a-draft\nfiles:\n  - path: x.txt\n"), 0o644); err != nil {
		t.Fatalf("writing jig.yaml: %v", err)
	}
	if err := Promote(dir); err == nil {
		t.Fatal("expected Promote to reject a jig with no candidate flag, got nil")
	}
}

// The whole point of editing jig.yaml as a yaml.Node instead of re-marshalling a struct: a
// developer's own comment, added while hand-reviewing the draft, must survive promotion.
func TestPromote_PreservesHandWrittenComment(t *testing.T) {
	dir := t.TempDir()
	raw := "# reviewed by hand, looks correct\n" +
		"name: widget\n" +
		"candidate: true\n" +
		"files:\n" +
		"  - path: Widget.java\n"
	if err := os.WriteFile(filepath.Join(dir, jig.FileName), []byte(raw), 0o644); err != nil {
		t.Fatalf("writing jig.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Widget.java"), []byte("class Widget {}\n"), 0o644); err != nil {
		t.Fatalf("writing Widget.java: %v", err)
	}

	if err := Promote(dir); err != nil {
		t.Fatalf("Promote returned error: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, jig.FileName))
	if err != nil {
		t.Fatalf("reading promoted jig.yaml: %v", err)
	}
	if !strings.Contains(string(got), "reviewed by hand, looks correct") {
		t.Errorf("expected the hand-written comment to survive promotion, got:\n%s", got)
	}
	if strings.Contains(string(got), "candidate") {
		t.Errorf("expected the candidate key to be removed, got:\n%s", got)
	}
}
