package learn

import (
	"os"
	"path/filepath"
	"testing"
)

// writeExample writes one file into a fresh example directory and returns its path.
func writeExample(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("writing example file %s: %v", name, err)
		}
	}
	return dir
}

// A draft whose own defaults reproduce the example exactly must review clean - this is the
// contract prompt.go's systemPrompt asks the model to uphold, and Review is what checks it
// mechanically.
func TestReview_CleanDraftFindsNothing(t *testing.T) {
	draftDir := t.TempDir()
	d := &Draft{
		Name: "widget-controller",
		Variables: []DraftVariable{
			{Name: "ClassName", Default: "Widget", Required: true},
		},
		Files: []DraftFile{
			{Path: "{{ .ClassName }}Controller.java", Content: "class {{ .ClassName }}Controller {}\n"},
		},
	}
	if err := WriteDraft(draftDir, d, false); err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}

	exampleDir := writeExample(t, map[string]string{
		"WidgetController.java": "class WidgetController {}\n",
	})

	result, err := Review(draftDir, exampleDir)
	if err != nil {
		t.Fatalf("Review returned error: %v", err)
	}
	if !result.Clean() {
		t.Fatalf("expected a clean review, got %+v", result)
	}
}

// A draft whose default no longer matches the example's actual content - the concrete signature
// of over/under-generalization - must be flagged as a content mismatch, with no AI call involved.
func TestReview_ContentMismatchIsFlagged(t *testing.T) {
	draftDir := t.TempDir()
	d := &Draft{
		Name: "widget-controller",
		Variables: []DraftVariable{
			{Name: "ClassName", Default: "Widget", Required: true},
		},
		Files: []DraftFile{
			// Wrong: the model over-generalized "Controller" into the variable's own default text,
			// so rendering it no longer reproduces the example.
			{Path: "{{ .ClassName }}Controller.java", Content: "class {{ .ClassName }}Service {}\n"},
		},
	}
	if err := WriteDraft(draftDir, d, false); err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}

	exampleDir := writeExample(t, map[string]string{
		"WidgetController.java": "class WidgetController {}\n",
	})

	result, err := Review(draftDir, exampleDir)
	if err != nil {
		t.Fatalf("Review returned error: %v", err)
	}
	if len(result.Mismatched) != 1 {
		t.Fatalf("expected exactly one content mismatch, got %+v", result)
	}
	if result.Mismatched[0].Path != "WidgetController.java" {
		t.Errorf("expected the mismatch to name WidgetController.java, got %+v", result.Mismatched[0])
	}
	if result.Mismatched[0].LineEndingOnly {
		t.Errorf("expected a genuine content mismatch not to be flagged as line-ending-only, got %+v",
			result.Mismatched[0])
	}
}

// #55: a mismatch caused purely by CRLF-vs-LF line endings must be flagged as such - the raw diff
// otherwise shows two visually-identical blocks (a trailing \r doesn't render in a terminal) with
// nothing pointing at the real difference.
func TestReview_LineEndingOnlyMismatchIsFlagged(t *testing.T) {
	draftDir := t.TempDir()
	d := &Draft{
		Name:  "widget",
		Files: []DraftFile{{Path: "Widget.java", Content: "class Widget {}\n"}},
	}
	if err := WriteDraft(draftDir, d, false); err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}

	exampleDir := writeExample(t, map[string]string{
		"Widget.java": "class Widget {}\r\n",
	})

	result, err := Review(draftDir, exampleDir)
	if err != nil {
		t.Fatalf("Review returned error: %v", err)
	}
	if len(result.Mismatched) != 1 {
		t.Fatalf("expected exactly one content mismatch, got %+v", result)
	}
	if !result.Mismatched[0].LineEndingOnly {
		t.Errorf("expected the CRLF-vs-LF mismatch to be flagged LineEndingOnly, got %+v", result.Mismatched[0])
	}
}

// A genuine content mismatch that also happens to differ in line endings must not be
// misclassified as line-ending-only - stripping \r has to still leave a real difference.
func TestReview_GenuineMismatchWithDifferentLineEndingsIsNotMisflagged(t *testing.T) {
	draftDir := t.TempDir()
	d := &Draft{
		Name:  "widget",
		Files: []DraftFile{{Path: "Widget.java", Content: "class Widget {}\n"}},
	}
	if err := WriteDraft(draftDir, d, false); err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}

	exampleDir := writeExample(t, map[string]string{
		"Widget.java": "class Something {}\r\n",
	})

	result, err := Review(draftDir, exampleDir)
	if err != nil {
		t.Fatalf("Review returned error: %v", err)
	}
	if len(result.Mismatched) != 1 {
		t.Fatalf("expected exactly one content mismatch, got %+v", result)
	}
	if result.Mismatched[0].LineEndingOnly {
		t.Errorf("expected a genuine content mismatch not to be misflagged as LineEndingOnly, got %+v",
			result.Mismatched[0])
	}
}

// A file the example has, but the draft never emits, is a Missing finding - the draft omitted
// part of the pattern.
func TestReview_MissingFileIsFlagged(t *testing.T) {
	draftDir := t.TempDir()
	d := &Draft{
		Name: "widget",
		Files: []DraftFile{
			{Path: "Widget.java", Content: "class Widget {}\n"},
		},
	}
	if err := WriteDraft(draftDir, d, false); err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}

	exampleDir := writeExample(t, map[string]string{
		"Widget.java":     "class Widget {}\n",
		"WidgetTest.java": "class WidgetTest {}\n",
	})

	result, err := Review(draftDir, exampleDir)
	if err != nil {
		t.Fatalf("Review returned error: %v", err)
	}
	if len(result.Missing) != 1 || result.Missing[0] != "WidgetTest.java" {
		t.Fatalf("expected WidgetTest.java to be reported missing, got %+v", result)
	}
}

// A file the draft invents that the example never had is an Extra finding.
func TestReview_ExtraFileIsFlagged(t *testing.T) {
	draftDir := t.TempDir()
	d := &Draft{
		Name: "widget",
		Files: []DraftFile{
			{Path: "Widget.java", Content: "class Widget {}\n"},
			{Path: "Invented.java", Content: "class Invented {}\n"},
		},
	}
	if err := WriteDraft(draftDir, d, false); err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}

	exampleDir := writeExample(t, map[string]string{
		"Widget.java": "class Widget {}\n",
	})

	result, err := Review(draftDir, exampleDir)
	if err != nil {
		t.Fatalf("Review returned error: %v", err)
	}
	if len(result.Extra) != 1 || result.Extra[0] != "Invented.java" {
		t.Fatalf("expected Invented.java to be reported extra, got %+v", result)
	}
}

// A `redacted: true` variable has no default by design, so Review must not hard-fail resolving it
// (the v1 behavior, before this variable shape existed) - and once resolved via its probe value, a
// draft that otherwise reproduces the example exactly must still review clean.
func TestReview_RedactedVariableReviewsCleanWhenShapeMatches(t *testing.T) {
	draftDir := t.TempDir()
	d := &Draft{
		Name: "widget-config",
		Variables: []DraftVariable{
			{Name: "ClassName", Default: "Widget", Required: true},
			{Name: "DbPassword", Required: true, Redacted: true},
		},
		Files: []DraftFile{
			{Path: "{{ .ClassName }}Config.java", Content: "class {{ .ClassName }}Config {\n" +
				"  String password = \"{{ .DbPassword }}\";\n}\n"},
		},
	}
	if err := WriteDraft(draftDir, d, false); err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}

	exampleDir := writeExample(t, map[string]string{
		"WidgetConfig.java": "class WidgetConfig {\n" +
			"  String password = \"realSecretValue123456\";\n}\n",
	})

	result, err := Review(draftDir, exampleDir)
	if err != nil {
		t.Fatalf("Review returned error: %v", err)
	}
	if !result.Clean() {
		t.Fatalf("expected a clean review once the redacted position is normalized, got %+v", result)
	}
}

// #49: `.Name` is the create-time <name> positional and has no `default:` of its own to draw
// from during review - it must resolve to exampleDir's own basename rather than the empty string,
// or a draft path built from `.Name` mismatches the example on every single file.
func TestReview_NameInPathDefaultsToExampleDirBasename(t *testing.T) {
	exampleDir := t.TempDir()
	name := filepath.Base(exampleDir)
	if err := os.MkdirAll(filepath.Join(exampleDir, name), 0o755); err != nil {
		t.Fatalf("seeding example dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(exampleDir, name, "main.tf"), []byte("cluster\n"), 0o644); err != nil {
		t.Fatalf("writing example file: %v", err)
	}

	draftDir := t.TempDir()
	d := &Draft{
		Name:  "cluster",
		Files: []DraftFile{{Path: "{{ .Name }}/main.tf", Content: "cluster\n"}},
	}
	if err := WriteDraft(draftDir, d, false); err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}

	result, err := Review(draftDir, exampleDir)
	if err != nil {
		t.Fatalf("Review returned error: %v", err)
	}
	if !result.Clean() {
		t.Fatalf("expected .Name to resolve to %q (exampleDir's own basename) and review clean, got %+v",
			name, result)
	}
}

// #38: a second example directory whose files exactly match the draft's render (content
// legitimately differing, since only example-1 is checked byte-for-byte) must not be flagged at
// all - ExtraExamples stays empty and the review is clean.
func TestReview_SecondExampleWithMatchingStructureIsClean(t *testing.T) {
	draftDir := t.TempDir()
	d := &Draft{
		Name: "widget",
		Variables: []DraftVariable{
			{Name: "ClassName", Default: "Widget", Required: true},
		},
		Files: []DraftFile{
			{Path: "{{ .ClassName }}.java", Content: "class {{ .ClassName }} {}\n"},
		},
	}
	if err := WriteDraft(draftDir, d, false); err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}

	example1 := writeExample(t, map[string]string{"Widget.java": "class Widget {}\n"})
	// Same file set as the draft's render, different content - legitimately allowed for a
	// non-primary example.
	example2 := writeExample(t, map[string]string{"Widget.java": "class SomethingElse {}\n"})

	result, err := Review(draftDir, example1, example2)
	if err != nil {
		t.Fatalf("Review returned error: %v", err)
	}
	if !result.Clean() {
		t.Fatalf("expected a clean review (structure matches, content divergence in example-2 is allowed), got %+v", result)
	}
	if len(result.ExtraExamples) != 0 {
		t.Fatalf("expected no ExtraExamples entries when structure matches, got %+v", result.ExtraExamples)
	}
}

// A second example missing a file the draft's render produces must be flagged under
// ExtraExamples, distinct from the primary Missing/Extra which are about example-1 only.
func TestReview_SecondExampleMissingFileIsFlaggedStructurally(t *testing.T) {
	draftDir := t.TempDir()
	d := &Draft{
		Name: "widget",
		Files: []DraftFile{
			{Path: "Widget.java", Content: "class Widget {}\n"},
			{Path: "WidgetTest.java", Content: "class WidgetTest {}\n"},
		},
	}
	if err := WriteDraft(draftDir, d, false); err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}

	example1 := writeExample(t, map[string]string{
		"Widget.java":     "class Widget {}\n",
		"WidgetTest.java": "class WidgetTest {}\n",
	})
	// example-2 is missing WidgetTest.java entirely - a real structural difference.
	example2 := writeExample(t, map[string]string{"Widget.java": "class Widget2 {}\n"})

	result, err := Review(draftDir, example1, example2)
	if err != nil {
		t.Fatalf("Review returned error: %v", err)
	}
	if result.Clean() {
		t.Fatal("expected the second example's missing file to be flagged, got a clean review")
	}
	if len(result.ExtraExamples) != 1 {
		t.Fatalf("expected exactly one ExtraExamples entry, got %+v", result.ExtraExamples)
	}
	e := result.ExtraExamples[0]
	if e.Dir != example2 {
		t.Errorf("expected the ExtraExamples entry to name example2's dir, got %q", e.Dir)
	}
	if len(e.Extra) != 1 || e.Extra[0] != "WidgetTest.java" {
		t.Errorf("expected WidgetTest.java reported extra (present in the draft's render, missing from example-2), got %+v", e)
	}
	if len(e.Missing) != 0 {
		t.Errorf("expected no Missing entries, got %+v", e.Missing)
	}
}

// The normalization for a redacted position must not blind Review to a genuine mismatch elsewhere
// in the very same file.
func TestReview_RedactedVariableStillCatchesMismatchElsewhereInFile(t *testing.T) {
	draftDir := t.TempDir()
	d := &Draft{
		Name: "widget-config",
		Variables: []DraftVariable{
			{Name: "ClassName", Default: "Widget", Required: true},
			{Name: "DbPassword", Required: true, Redacted: true},
		},
		Files: []DraftFile{
			{Path: "{{ .ClassName }}Config.java", Content: "class {{ .ClassName }}Config {\n" +
				"  String password = \"{{ .DbPassword }}\";\n}\n"},
		},
	}
	if err := WriteDraft(draftDir, d, false); err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}

	// The example has an extra field the draft never captured - a genuine under-generalization,
	// unrelated to the redacted password position.
	exampleDir := writeExample(t, map[string]string{
		"WidgetConfig.java": "class WidgetConfig {\n" +
			"  int extra = 42;\n" +
			"  String password = \"realSecretValue123456\";\n}\n",
	})

	result, err := Review(draftDir, exampleDir)
	if err != nil {
		t.Fatalf("Review returned error: %v", err)
	}
	if result.Clean() {
		t.Fatal("expected the extra field to still be caught as a mismatch, got a clean review")
	}
}
