package learn

import (
	"path/filepath"
	"strings"
	"testing"

	"scaffold-engine-go/internal/jig"
	"scaffold-engine-go/internal/render"
)

func TestWriteDraft_ValidDraftRoundTripsThroughJigLoad(t *testing.T) {
	dir := t.TempDir()
	d := &Draft{
		Name:        "widget-controller",
		Description: "A learned controller",
		Variables: []DraftVariable{
			{Name: "ClassName", Prompt: "Entity class name", Default: "Widget", Required: true},
		},
		Computed: []DraftComputed{
			{Name: "ClassNameKebab", Value: "{{ .ClassName | kebabcase }}"},
		},
		Files: []DraftFile{
			{Path: "{{ .ClassName }}Controller.java", Content: "class {{ .ClassName }}Controller {}\n"},
			{Path: "routes/{{ .ClassNameKebab }}.txt", Content: "route: {{ .ClassName | kebabcase }}\n"},
		},
	}

	if err := WriteDraft(dir, d); err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}

	m, err := jig.Load(filepath.Join(dir, jig.FileName))
	if err != nil {
		t.Fatalf("jig.Load on the written draft failed: %v", err)
	}
	if m.Name != "widget-controller" || len(m.Variables) != 1 || m.Variables[0].Name != "ClassName" {
		t.Fatalf("unexpected decoded jig: %+v", m)
	}
	if len(m.Computed) != 1 || m.Computed[0].Name != "ClassNameKebab" {
		t.Fatalf("expected the computed entry to round-trip, got: %+v", m.Computed)
	}
}

// A piped filter cannot appear in a physical path - Windows forbids "|" in filenames - so
// WriteDraft must reject it clearly instead of failing with a cryptic OS error partway through.
func TestWriteDraft_RejectsPipedPathFilter(t *testing.T) {
	dir := t.TempDir()
	d := &Draft{
		Name:      "broken",
		Variables: []DraftVariable{{Name: "ClassName", Default: "Widget"}},
		Files: []DraftFile{
			{Path: "routes/{{ .ClassName | kebabcase }}.txt", Content: "x"},
		},
	}
	err := WriteDraft(dir, d)
	if err == nil || !strings.Contains(err.Error(), "cannot appear in a filename") {
		t.Fatalf("expected a clear piped-path-filter error, got %v", err)
	}
}

// jig.Validate rejects an unnamed variable - WriteDraft must surface that rather than reporting
// success, since the draft is otherwise written to disk before the check runs.
func TestWriteDraft_SelfValidationCatchesBadDraft(t *testing.T) {
	dir := t.TempDir()
	d := &Draft{
		Name:      "broken",
		Variables: []DraftVariable{{Name: ""}},
		Files:     []DraftFile{{Path: "a.txt", Content: "x"}},
	}
	if err := WriteDraft(dir, d); err == nil {
		t.Fatal("expected WriteDraft to surface the self-validation failure")
	}
}

// End-to-end proof that a draft is actually consumable, not just schema-valid: feed it through
// the same render.RenderSource flow `create` uses, with a fabricated variable value standing in
// for a real invocation.
func TestWriteDraft_ConsumableByRender(t *testing.T) {
	dir := t.TempDir()
	d := &Draft{
		Name: "widget",
		Variables: []DraftVariable{
			{Name: "ClassName", Default: "Widget", Required: true},
		},
		Files: []DraftFile{
			{Path: "{{ .ClassName }}Controller.java", Content: "class {{ .ClassName }}Controller {}\n"},
		},
	}
	if err := WriteDraft(dir, d); err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}

	m, err := jig.Load(filepath.Join(dir, jig.FileName))
	if err != nil {
		t.Fatalf("jig.Load failed: %v", err)
	}

	ctx := render.BuildContext(map[string]string{"ClassName": "Order"}, nil, "order-svc", "", "", "", nil, nil)
	files, _, err := render.RenderSource(render.Source{Dir: dir, Manifest: m}, ctx)
	if err != nil {
		t.Fatalf("RenderSource failed: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 rendered file, got %d", len(files))
	}
	if files[0].Path != "OrderController.java" {
		t.Errorf("expected substituted path OrderController.java, got %s", files[0].Path)
	}
	if string(files[0].Content) != "class OrderController {}\n" {
		t.Errorf("expected substituted content, got %q", files[0].Content)
	}
}
