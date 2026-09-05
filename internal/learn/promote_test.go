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

// The comment most likely to matter is one attached directly to the line being removed - a
// reviewer's own trailing note on `candidate: true` itself, e.g. recording who approved it.
// yaml.v3 attaches a same-line comment to the value node it follows, so naively splicing out the
// candidate key/value pair would silently delete this along with the flag.
func TestPromote_PreservesInlineCommentOnCandidateLine(t *testing.T) {
	dir := t.TempDir()
	raw := "name: widget\n" +
		"candidate: true # reviewed by alice, looks correct\n" +
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
	if !strings.Contains(string(got), "reviewed by alice, looks correct") {
		t.Errorf("expected the inline comment on the candidate line to survive promotion, got:\n%s", got)
	}
	if strings.Contains(string(got), "candidate") {
		t.Errorf("expected the candidate key to be removed, got:\n%s", got)
	}
}

// Same risk, different placement: a standalone comment on the line immediately above
// `candidate: true` - yaml.v3 attaches this as the candidate key's HeadComment, which would also
// be deleted by a naive splice.
func TestPromote_PreservesCommentAboveCandidateLine(t *testing.T) {
	dir := t.TempDir()
	raw := "name: widget\n" +
		"# reviewed - about to promote\n" +
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
	if !strings.Contains(string(got), "reviewed - about to promote") {
		t.Errorf("expected the comment above the candidate line to survive promotion, got:\n%s", got)
	}
	if strings.Contains(string(got), "candidate") {
		t.Errorf("expected the candidate key to be removed, got:\n%s", got)
	}
}

// candidate: true as the FIRST key has no preceding sibling to carry its comment onto - it must
// fall back to the document's own head comment instead.
func TestPromote_PreservesCommentWhenCandidateIsFirstKey(t *testing.T) {
	dir := t.TempDir()
	raw := "candidate: true # reviewed by bob\n" +
		"name: widget\n" +
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
	if !strings.Contains(string(got), "reviewed by bob") {
		t.Errorf("expected the comment to survive even with candidate as the first key, got:\n%s", got)
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
