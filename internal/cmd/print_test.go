package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --print answers the question you have while WRITING a template: not which files appear
// (--dry-run) nor who contributed them (--explain), but what is actually in them.
func TestCreate_PrintShowsRenderedContent(t *testing.T) {
	root := buildScaffoldingCode(t)
	out, _, err := createInto(t, root, "fw", "services", "payment", "--function=web", "--print")
	if err != nil {
		t.Fatalf("--print returned error: %v", err)
	}
	if !strings.Contains(out, "==> pom.xml <==") {
		t.Errorf("expected a header per file, got:\n%s", out)
	}
	if !strings.Contains(out, "<artifactId>payment</artifactId>") {
		t.Errorf("expected the rendered content itself, got:\n%s", out)
	}
}

func TestCreate_PrintWritesNothing(t *testing.T) {
	root := buildScaffoldingCode(t)
	_, outDir, err := createInto(t, root, "fw", "services", "payment", "--function=web", "--print")
	if err != nil {
		t.Fatalf("--print returned error: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(outDir, "payment")); !os.IsNotExist(statErr) {
		t.Error("--print must not write anything")
	}
}

// --print-written is the one inspection-like flag that does write: a caller wanting both the
// write and a verifiable transcript of what landed gets both in one call (issue #91).
func TestCreate_PrintWrittenWritesAndShowsContent(t *testing.T) {
	root := buildScaffoldingCode(t)
	out, outDir, err := createInto(t, root, "fw", "services", "payment", "--function=web", "--print-written")
	if err != nil {
		t.Fatalf("--print-written returned error: %v", err)
	}
	if !strings.Contains(out, "==> pom.xml <==") || !strings.Contains(out, "<artifactId>payment</artifactId>") {
		t.Errorf("expected the written content to be echoed, got:\n%s", out)
	}
	got := readGenerated(t, outDir, "payment", "pom.xml")
	if !strings.Contains(got, "<artifactId>payment</artifactId>") {
		t.Errorf("expected the file to actually be written to disk, got:\n%s", got)
	}
}

// The printed content must be the true final bytes, not the raw pre-merge render - a
// --skip-existing rerun against a `merge:`-registered file deep-merges before writing.
func TestCreate_PrintWrittenReflectsSkipExistingMerge(t *testing.T) {
	root := buildMergeableConfigScaffold(t)
	outDir := t.TempDir()

	if _, err := run(t, newCreateCommand, "app", "cfg", "svc1",
		"--key=a", "--val=1", "--scaffolding-code="+root, "--output="+outDir); err != nil {
		t.Fatalf("first create returned error: %v", err)
	}
	out, err := run(t, newCreateCommand, "app", "cfg", "svc2",
		"--key=b", "--val=2", "--scaffolding-code="+root, "--output="+outDir,
		"--skip-existing", "--print-written")
	if err != nil {
		t.Fatalf("second create (--skip-existing --print-written) returned error: %v", err)
	}
	if !strings.Contains(out, "a: 1") || !strings.Contains(out, "b: 2") {
		t.Errorf("expected the printed content to be the merged result, not the raw render, got:\n%s", out)
	}
}

// A spliced file's printed content must be the full post-splice file, since ApplyInserts runs
// after Write commits - printing the pre-splice render would show stale content for it.
func TestCreate_PrintWrittenReflectsSplice(t *testing.T) {
	root := buildInsertScaffold(t)
	out, outDir, err := createInto(t, root, "app", "web", "svc", "--print-written")
	if err != nil {
		t.Fatalf("create --print-written returned error: %v", err)
	}
	want := "class Controller {\n// @scaffold:routes\nnewRoute();\n}\n"
	if !strings.Contains(out, "==> Controller.java <==") || !strings.Contains(out, want) {
		t.Errorf("expected the printed content to be the full post-splice file, got:\n%s", out)
	}
	got := readGenerated(t, outDir, "svc", "Controller.java")
	if got != want {
		t.Errorf("expected the file on disk to match, got:\n%s", got)
	}
}

func TestCreate_PrintWrittenRejectsCombinationWithPrint(t *testing.T) {
	root := buildScaffoldingCode(t)
	_, outDir, err := createInto(t, root, "fw", "services", "payment", "--function=web",
		"--print-written", "--print")
	if err == nil {
		t.Fatal("expected an error combining --print-written with --print")
	}
	if _, statErr := os.Stat(filepath.Join(outDir, "payment")); !os.IsNotExist(statErr) {
		t.Error("expected nothing to be written when the combination is rejected")
	}
}

func TestCreate_PrintWrittenRejectsCombinationWithDryRun(t *testing.T) {
	root := buildScaffoldingCode(t)
	_, outDir, err := createInto(t, root, "fw", "services", "payment", "--function=web",
		"--print-written", "--dry-run")
	if err == nil {
		t.Fatal("expected an error combining --print-written with --dry-run")
	}
	if _, statErr := os.Stat(filepath.Join(outDir, "payment")); !os.IsNotExist(statErr) {
		t.Error("expected nothing to be written when the combination is rejected")
	}
}

// Partials reach the CLI, not just the render package: a fragment declared at the framework level
// is usable by a leaf template several levels down, with nothing in between mentioning it.
func TestCreate_PartialsAreAvailableAcrossTheChain(t *testing.T) {
	root := buildScaffoldingCode(t)
	// Declared at the framework level...
	writeFile(t, filepath.Join(root, "fw"), "_helpers.tpl",
		`{{ define "banner" }}// generated for {{ .ArtifactId }}{{ end }}`)
	// ...used by the leaf, four levels down.
	writeFile(t, filepath.Join(root, "fw", "1.0", "tmpl", "services", "web"), "banner.txt",
		`{{ include "banner" . }}`)

	out, _, err := createInto(t, root, "fw", "services", "payment", "--function=web", "--print")
	if err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	if !strings.Contains(out, "// generated for payment") {
		t.Errorf("expected the framework-level partial to be included, got:\n%s", out)
	}
	if strings.Contains(out, "_helpers.tpl") {
		t.Errorf("a partial file must never be emitted as output, got:\n%s", out)
	}
}

// lint renders every combination, so a broken partial reference is caught there too rather than
// waiting for someone to generate that exact combination.
func TestLint_CatchesUnknownPartial(t *testing.T) {
	root := buildScaffoldingCode(t)
	writeFile(t, filepath.Join(root, "fw", "1.0", "tmpl", "services", "web"), "bad.txt",
		`{{ include "does-not-exist" . }}`)

	out, err := run(t, newLintCommand, "--scaffolding-code="+root)
	if err == nil {
		t.Fatal("expected lint to fail on an unknown partial, got nil")
	}
	if !strings.Contains(out, "does-not-exist") {
		t.Errorf("expected the missing partial to be named, got:\n%s", out)
	}
}
