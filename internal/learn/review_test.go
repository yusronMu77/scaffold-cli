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
