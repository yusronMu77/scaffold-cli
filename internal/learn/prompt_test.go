package learn

import (
	"strings"
	"testing"
)

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
	if got := promptForFiles(single); got != systemPrompt {
		t.Error("expected the single-example case to send exactly v1's systemPrompt, unmodified")
	}

	multi := []SourceFile{{Path: "a.txt", Content: "x", ExampleIndex: 1}}
	if got := promptForFiles(multi); got != multiExampleSystemPrompt {
		t.Error("expected a multi-example set to select multiExampleSystemPrompt")
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
