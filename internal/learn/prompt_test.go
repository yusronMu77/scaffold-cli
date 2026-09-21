package learn

import (
	"strings"
	"testing"
)

// #45: ParseDraft must carry a file's "raw" flag through to DraftFile.Raw, and default it to
// false when omitted (the common case, and every draft written before this field existed).
func TestParseDraft_RawFieldRoundTrips(t *testing.T) {
	raw := `{
		"name": "ansible-role",
		"variables": [],
		"files": [
			{"path": "tasks/main.yml", "content": "{{ ansible_user }}\n", "raw": true},
			{"path": "README.md", "content": "docs\n"}
		]
	}`
	d, err := ParseDraft([]byte(raw))
	if err != nil {
		t.Fatalf("ParseDraft returned error: %v", err)
	}
	if len(d.Files) != 2 {
		t.Fatalf("expected 2 files, got %+v", d.Files)
	}
	if !d.Files[0].Raw {
		t.Errorf("expected tasks/main.yml to parse with Raw=true, got %+v", d.Files[0])
	}
	if d.Files[1].Raw {
		t.Errorf("expected README.md (raw omitted) to default to Raw=false, got %+v", d.Files[1])
	}
}

func TestIsMultiExample_TrueOnlyWhenAnyExampleIndexSet(t *testing.T) {
	single := []SourceFile{{Path: "a.txt", Content: "x"}, {Path: "b.txt", Content: "y"}}
	if isMultiExample(single) {
		t.Error("expected an ordinary scan (ExampleIndex always 0) to not be multi-example")
	}

	multi := []SourceFile{
		{Path: "a.txt", Content: "x", ExampleIndex: 1},
		{Path: "a.txt", Content: "z", ExampleIndex: 2},
	}
	if !isMultiExample(multi) {
		t.Error("expected any ExampleIndex > 0 to mark the set multi-example")
	}
}

func TestPromptForFiles_SelectsAddendumOnlyForMultiExample(t *testing.T) {
	single := []SourceFile{{Path: "a.txt", Content: "x"}}
	if got := promptForFiles(single, ""); got != systemPrompt {
		t.Error("expected the single-example case to send exactly v1's systemPrompt, unmodified")
	}

	multi := []SourceFile{{Path: "a.txt", Content: "x", ExampleIndex: 1}}
	if got := promptForFiles(multi, ""); got != multiExampleSystemPrompt {
		t.Error("expected a multi-example set to select multiExampleSystemPrompt")
	}
}

// An empty userAddendum must reproduce today's exact output - no trailing separator, nothing
// appended - so a caller that never sets --prompt-addendum sees zero behavior change.
func TestPromptForFiles_EmptyAddendumUnchanged(t *testing.T) {
	single := []SourceFile{{Path: "a.txt", Content: "x"}}
	if got := promptForFiles(single, ""); got != systemPrompt {
		t.Errorf("expected exactly systemPrompt with no addendum, got a different string (len %d vs %d)",
			len(got), len(systemPrompt))
	}
}

// A non-empty userAddendum must be appended after the base prompt, never spliced earlier or
// allowed to replace any of it - the whole point is that a project's guidance can only add, never
// override, the engine's compiled invariants (reserved names, schema, casing filters).
func TestPromptForFiles_AddendumAppendedAfterBase(t *testing.T) {
	addendum := "Project-specific: also treat \"Widget\" as a reserved word."

	single := []SourceFile{{Path: "a.txt", Content: "x"}}
	got := promptForFiles(single, addendum)
	want := systemPrompt + "\n\n" + addendum
	if got != want {
		t.Errorf("expected base prompt + addendum, got:\n%s", got)
	}
	if !strings.HasPrefix(got, systemPrompt) {
		t.Error("expected the addendum to come after the full base prompt, not replace any of it")
	}

	multi := []SourceFile{{Path: "a.txt", Content: "x", ExampleIndex: 1}}
	gotMulti := promptForFiles(multi, addendum)
	wantMulti := multiExampleSystemPrompt + "\n\n" + addendum
	if gotMulti != wantMulti {
		t.Errorf("expected multi-example prompt + addendum, got:\n%s", gotMulti)
	}
}

func TestBuildUserContent_LabelsFilesOnlyWhenMultiExample(t *testing.T) {
	single := buildUserContent([]SourceFile{{Path: "java/Foo.java", Content: "class Foo {}"}})
	if !strings.Contains(single, "=== FILE: java/Foo.java ===") {
		t.Errorf("expected an unlabeled path for a single example, got:\n%s", single)
	}
	if strings.Contains(single, "example-") {
		t.Errorf("expected no example label at all for a single example, got:\n%s", single)
	}

	multi := buildUserContent([]SourceFile{
		{Path: "java/Foo.java", Content: "class Foo {}", ExampleIndex: 1},
		{Path: "java/Foo.java", Content: "class Bar {}", ExampleIndex: 2},
	})
	if !strings.Contains(multi, "=== FILE: example-1/java/Foo.java ===") ||
		!strings.Contains(multi, "=== FILE: example-2/java/Foo.java ===") {
		t.Errorf("expected each file labeled by its own example index, got:\n%s", multi)
	}
}
