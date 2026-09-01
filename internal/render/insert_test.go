package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyInserts_SpliceAfterAnchor(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "Controller.java", "class Controller {\n// @scaffold:routes\n}\n")

	applied, skipped, err := ApplyInserts(target, []Insert{
		{Path: "Controller.java", Content: []byte("newRoute();\n"), Anchor: "// @scaffold:routes", After: true, Source: "leaf"},
	})
	if err != nil {
		t.Fatalf("ApplyInserts: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("expected nothing skipped, got %v", skipped)
	}
	if len(applied) != 1 || applied[0] != "Controller.java" {
		t.Errorf("expected Controller.java to be reported applied, got %v", applied)
	}

	got := readFile(t, target, "Controller.java")
	want := "class Controller {\n// @scaffold:routes\nnewRoute();\n}\n"
	if got != want {
		t.Errorf("expected:\n%q\ngot:\n%q", want, got)
	}
}

func TestApplyInserts_SpliceBeforeAnchor(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "Controller.java", "class Controller {\n}\n")

	_, _, err := ApplyInserts(target, []Insert{
		{Path: "Controller.java", Content: []byte("newRoute();\n"), Anchor: "}", After: false, Source: "leaf"},
	})
	if err != nil {
		t.Fatalf("ApplyInserts: %v", err)
	}

	got := readFile(t, target, "Controller.java")
	want := "class Controller {\nnewRoute();\n}\n"
	if got != want {
		t.Errorf("expected:\n%q\ngot:\n%q", want, got)
	}
}

func TestApplyInserts_AnchorNotFoundIsAnError(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "Controller.java", "class Controller {}\n")

	_, _, err := ApplyInserts(target, []Insert{
		{Path: "Controller.java", Content: []byte("x\n"), Anchor: "// nope", After: true, Source: "leaf"},
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected an anchor-not-found error, got: %v", err)
	}
}

func TestApplyInserts_AmbiguousAnchorIsAnError(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "Controller.java", "// mark\nclass Controller {\n// mark\n}\n")

	_, _, err := ApplyInserts(target, []Insert{
		{Path: "Controller.java", Content: []byte("x\n"), Anchor: "// mark", After: true, Source: "leaf"},
	})
	if err == nil || !strings.Contains(err.Error(), "matches 2 lines") {
		t.Fatalf("expected an ambiguous-anchor error, got: %v", err)
	}
}

func TestApplyInserts_MissingTargetFileIsAnError(t *testing.T) {
	target := t.TempDir()

	_, _, err := ApplyInserts(target, []Insert{
		{Path: "Controller.java", Content: []byte("x\n"), Anchor: "// mark", After: true, Source: "leaf"},
	})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected a missing-file error explaining insert requires an existing file, got: %v", err)
	}
}

// Running the same insert twice must not duplicate the spliced block.
func TestApplyInserts_IdempotentOnRepeat(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "Controller.java", "class Controller {\n// @scaffold:routes\n}\n")

	ins := []Insert{
		{Path: "Controller.java", Content: []byte("newRoute();\n"), Anchor: "// @scaffold:routes", After: true, Source: "leaf"},
	}
	if _, _, err := ApplyInserts(target, ins); err != nil {
		t.Fatalf("first ApplyInserts: %v", err)
	}
	applied, skipped, err := ApplyInserts(target, ins)
	if err != nil {
		t.Fatalf("second ApplyInserts: %v", err)
	}
	if len(applied) != 0 || len(skipped) != 1 {
		t.Errorf("expected the second run to skip the already-present insert, applied=%v skipped=%v", applied, skipped)
	}

	got := readFile(t, target, "Controller.java")
	if strings.Count(got, "newRoute();") != 1 {
		t.Errorf("expected exactly one copy of the spliced line, got:\n%s", got)
	}
}

// A target file that uses CRLF (its own convention, restored on write by spliceAtAnchor) must
// still be recognized as "already spliced" against an ins.Content that itself uses plain LF -
// exactly what a template file checked out with git's core.autocrlf produces.
func TestApplyInserts_IdempotentAcrossMixedLineEndings(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "Controller.java", "class Controller {\r\n// @scaffold:routes\r\n}\r\n")

	ins := []Insert{
		{Path: "Controller.java", Content: []byte("newRoute();\n"), Anchor: "// @scaffold:routes", After: true, Source: "leaf"},
	}
	if _, _, err := ApplyInserts(target, ins); err != nil {
		t.Fatalf("first ApplyInserts: %v", err)
	}
	applied, skipped, err := ApplyInserts(target, ins)
	if err != nil {
		t.Fatalf("second ApplyInserts: %v", err)
	}
	if len(applied) != 0 || len(skipped) != 1 {
		t.Errorf("expected the CRLF target to be recognized as already spliced against an LF "+
			"ins.Content, applied=%v skipped=%v", applied, skipped)
	}
	got := readFile(t, target, "Controller.java")
	if strings.Count(got, "newRoute();") != 1 {
		t.Errorf("expected exactly one copy of the spliced line, got:\n%s", got)
	}
}

func TestApplyInserts_RegexAnchor(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "Controller.java", "class Controller {\n// route: /a\n}\n")

	_, _, err := ApplyInserts(target, []Insert{
		{Path: "Controller.java", Content: []byte("// route: /b\n"), Anchor: `// route: /\w+`, Regex: true, After: true, Source: "leaf"},
	})
	if err != nil {
		t.Fatalf("ApplyInserts: %v", err)
	}
	got := readFile(t, target, "Controller.java")
	if !strings.Contains(got, "// route: /a\n// route: /b\n") {
		t.Errorf("expected the regex anchor to match and splice after it, got:\n%s", got)
	}
}

func TestApplyInserts_PreservesCRLF(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "Controller.java", "class Controller {\r\n// @scaffold:routes\r\n}\r\n")

	_, _, err := ApplyInserts(target, []Insert{
		{Path: "Controller.java", Content: []byte("newRoute();\n"), Anchor: "// @scaffold:routes", After: true, Source: "leaf"},
	})
	if err != nil {
		t.Fatalf("ApplyInserts: %v", err)
	}
	got := readFile(t, target, "Controller.java")
	if !strings.Contains(got, "\r\nnewRoute();\r\n") {
		t.Errorf("expected the file's CRLF convention to be preserved, got:\n%q", got)
	}
}

// A second insert into the same path must see what the first one just spliced in, so a later
// insert may anchor off text an earlier one added in this same call.
func TestApplyInserts_ChainedInsertsSeePriorResult(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "Controller.java", "class Controller {\n// @scaffold:routes\n}\n")

	_, _, err := ApplyInserts(target, []Insert{
		{Path: "Controller.java", Content: []byte("// @scaffold:routes:new\n"), Anchor: "// @scaffold:routes", After: true, Source: "base"},
		{Path: "Controller.java", Content: []byte("newRoute();\n"), Anchor: "// @scaffold:routes:new", After: true, Source: "overlay"},
	})
	if err != nil {
		t.Fatalf("ApplyInserts: %v", err)
	}
	got := readFile(t, target, "Controller.java")
	if !strings.Contains(got, "// @scaffold:routes:new\nnewRoute();\n") {
		t.Errorf("expected the second insert to anchor off the first insert's output, got:\n%s", got)
	}
}

func readFile(t *testing.T, dir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(data)
}
