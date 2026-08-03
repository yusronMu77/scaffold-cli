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
