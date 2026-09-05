package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scaffold-engine-go/internal/jig"
	"scaffold-engine-go/internal/learn"
)

// buildMatchScaffold builds a minimal scaffold "app", one default version "1.0", one required
// "templates" dimension with two leaves:
//   - "hello": two files (Application/Controller-shaped), the intended match target.
//   - "domain": one bare file - deliberately too thin, on its own, to ever be confident (this is
//     the case that would false-positive if the minFiles floor were checked BEFORE subtracting the
//     scaffold's own chassis, since chassis alone would clear a naive "≥2 files" bar).
//
// A chassis file (pom.xml) sits at the scaffold root, inherited by both leaves, so a real
// distinguishing-shape check is actually exercised rather than accidentally passing on chassis
// alone.
func buildMatchScaffold(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeFile(t, root, "jig.yaml", "name: root\nvalues:\n  - name: app\n")

	app := filepath.Join(root, "app")
	writeFile(t, app, "jig.yaml", "name: app\nvalues:\n  - name: \"1.0\"\n    default: true\n")
	writeFile(t, app, "pom.xml", "<project/>\n")

	v := filepath.Join(app, "1.0")
	writeFile(t, v, "jig.yaml", "name: v\nvalues:\n  - name: templates\n")

	tmpl := filepath.Join(v, "templates")
	writeFile(t, tmpl, "jig.yaml",
		"name: T\nrequired: true\nvalues:\n  - name: hello\n    default: true\n  - name: domain\n")

	hello := filepath.Join(tmpl, "hello")
	writeFile(t, hello, "jig.yaml", "name: Hello\nvariables:\n  - name: AppName\n    default: Hello\n")
	writeFile(t, filepath.Join(hello, "java"), "{{ .AppName }}Application.java", "class {{ .AppName }}Application {}\n")
	writeFile(t, filepath.Join(hello, "java"), "{{ .AppName }}Controller.java", "class {{ .AppName }}Controller {}\n")

	domain := filepath.Join(tmpl, "domain")
	writeFile(t, domain, "jig.yaml", "name: Domain\nvariables:\n  - name: EntityName\n    default: Widget\n")
	writeFile(t, filepath.Join(domain, "java"), "{{ .EntityName }}.java", "class {{ .EntityName }} {}\n")

	return root
}

func writeMatchExample(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		writeFile(t, filepath.Dir(filepath.Join(dir, name)), filepath.Base(name), content)
	}
	return dir
}

// A confident match must short-circuit strictly before any provider is resolved - proven here by
// having NO API key set at all: if the match-check didn't fire, the run would fail with "no LLM
// provider configured" instead of succeeding.
func TestLearnMatch_ConfidentMatchSkipsProviderAndDraft(t *testing.T) {
	t.Setenv(learn.EnvAnthropicAPIKey, "")
	t.Setenv(learn.EnvOpenAIAPIKey, "")

	root := buildMatchScaffold(t)
	example := writeMatchExample(t, map[string]string{
		"java/MyApplication.java": "class MyApplication {}\n",
		"java/MyController.java":  "class MyController {}\n",
	})
	outDir := filepath.Join(t.TempDir(), "draft")

	out, err := run(t, newLearnCommand, example, "--output="+outDir, "--scaffolding-code="+root)
	if err != nil {
		t.Fatalf("expected a confident match to succeed with no provider needed, got: %v\n%s", err, out)
	}
	if !strings.Contains(out, "scaffold create app hello") {
		t.Errorf("expected the matched invocation to be printed, got:\n%s", out)
	}
	if _, statErr := os.Stat(outDir); !os.IsNotExist(statErr) {
		t.Error("a matched run must not write a draft")
	}
}

const sampleMatchDraftJSON = `{"name":"x","files":[{"path":"x.txt","content":"x\n"}]}`

// A differently-shaped example must fall through to today's flow - proven via --draft mode (which
// needs no API key either), checking a draft actually gets written, i.e. the match-check said "no
// match" and let the existing code path run exactly as it does today.
func TestLearnMatch_DifferentShapeFallsThroughToDraftPath(t *testing.T) {
	root := buildMatchScaffold(t)
	example := writeMatchExample(t, map[string]string{"main.py": "print('hi')\n"})
	outDir := filepath.Join(t.TempDir(), "draft")
	draftPath := writeDraftFixture(t, sampleMatchDraftJSON)

	out, err := run(t, newLearnCommand, example, "--output="+outDir, "--scaffolding-code="+root,
		"--draft="+draftPath)
	if err != nil {
		t.Fatalf("learn returned error: %v\n%s", err, out)
	}
	if _, err := jig.Load(filepath.Join(outDir, jig.FileName)); err != nil {
		t.Fatalf("expected a draft to be written for a non-matching example: %v", err)
	}
}

// The "domain" leaf's own distinguishing shape is a single bare .java file - one file short of
// minMatchFiles even before subtraction, and still one short AFTER subtracting the scaffold's
// shared chassis (pom.xml, irrelevant to either side here). An unrelated single-file example must
// not be reported as a match against it, and must fall through exactly like any other non-match.
func TestLearnMatch_ThinLeafRemainderNeverConfident(t *testing.T) {
	root := buildMatchScaffold(t)
	example := writeMatchExample(t, map[string]string{"java/Unrelated.java": "class Unrelated {}\n"})
	outDir := filepath.Join(t.TempDir(), "draft")
	draftPath := writeDraftFixture(t, sampleMatchDraftJSON)

	out, err := run(t, newLearnCommand, example, "--output="+outDir, "--scaffolding-code="+root,
		"--draft="+draftPath)
	if err != nil {
		t.Fatalf("learn returned error: %v\n%s", err, out)
	}
	if _, err := jig.Load(filepath.Join(outDir, jig.FileName)); err != nil {
		t.Fatalf("expected the thin 'domain' leaf to never match on its own, but no draft was written: %v", err)
	}
}

// --skip-match bypasses the check even when a match would otherwise fire.
func TestLearnMatch_SkipMatchBypassesEvenAConfidentMatch(t *testing.T) {
	root := buildMatchScaffold(t)
	example := writeMatchExample(t, map[string]string{
		"java/MyApplication.java": "class MyApplication {}\n",
		"java/MyController.java":  "class MyController {}\n",
	})
	outDir := filepath.Join(t.TempDir(), "draft")
	draftPath := writeDraftFixture(t, sampleMatchDraftJSON)

	out, err := run(t, newLearnCommand, example, "--output="+outDir, "--scaffolding-code="+root,
		"--skip-match", "--draft="+draftPath)
	if err != nil {
		t.Fatalf("learn --skip-match returned error: %v\n%s", err, out)
	}
	if _, err := jig.Load(filepath.Join(outDir, jig.FileName)); err != nil {
		t.Fatalf("expected a draft to be written when --skip-match bypasses a would-be match: %v", err)
	}
}

func writeDraftFixture(t *testing.T, json string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "draft.json")
	if err := os.WriteFile(path, []byte(json), 0o644); err != nil {
		t.Fatalf("writing draft fixture: %v", err)
	}
	return path
}

// buildLeafVersionScaffold registers a scaffold "spring" with two versions: "current" (the
// registry default, with a normal "templates" dimension whose one leaf has a different, 1-file
// shape) and "legacy" (NOT default, itself a leaf - no "templates" dimension at all, matching
// discovery's ResolveVersionStructure fallback). This is the exact shape real-world "hello-world"
// has in the actual scaffold-templates registry: a version that IS the template.
func buildLeafVersionScaffold(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeFile(t, root, "jig.yaml", "name: root\nvalues:\n  - name: spring\n")

	spring := filepath.Join(root, "spring")
	writeFile(t, spring, "jig.yaml",
		"name: spring\nvalues:\n  - name: current\n    default: true\n  - name: legacy\n")

	current := filepath.Join(spring, "current")
	writeFile(t, current, "jig.yaml", "name: v\nvalues:\n  - name: templates\n")
	writeFile(t, filepath.Join(current, "templates"), "jig.yaml",
		"name: T\nrequired: true\nvalues:\n  - name: web\n    default: true\n")
	writeFile(t, filepath.Join(current, "templates", "web"), "jig.yaml",
		"name: Web\nvariables:\n  - name: AppName\n    default: Hello\n")
	writeFile(t, filepath.Join(current, "templates", "web", "java"), "{{ .AppName }}Only.java",
		"class {{ .AppName }}Only {}\n")

	legacy := filepath.Join(spring, "legacy")
	writeFile(t, legacy, "jig.yaml", "name: Legacy\nvariables:\n  - name: AppName\n    default: Hello\n")
	writeFile(t, filepath.Join(legacy, "java"), "{{ .AppName }}Application.java", "class {{ .AppName }}Application {}\n")
	writeFile(t, filepath.Join(legacy, "java"), "{{ .AppName }}Controller.java", "class {{ .AppName }}Controller {}\n")

	return root
}

// A match against a non-default, leaf-shaped version must print a real, valid invocation: no
// <template> token at all (the version itself is the template), and the version passed as
// --scaffold-version=legacy, never as a bare positional - proving formatCreateInvocation (not
// lintCase.String()) is what actually built the string.
func TestLearnMatch_LeafVersionInvocationOmitsTemplateAndUsesVersionFlag(t *testing.T) {
	root := buildLeafVersionScaffold(t)
	example := writeMatchExample(t, map[string]string{
		"java/MyApplication.java": "class MyApplication {}\n",
		"java/MyController.java":  "class MyController {}\n",
	})

	invocation, found := tryMatchExistingTemplate(root, example)
	if !found {
		t.Fatal("expected the 2-file example to confidently match the 'legacy' leaf version")
	}
	if strings.Contains(invocation, " current ") || strings.Contains(invocation, "  ") {
		t.Errorf("expected no <template> token and no double-space artifact, got %q", invocation)
	}
	if !strings.Contains(invocation, "--scaffold-version=legacy") {
		t.Errorf("expected the version passed as --scaffold-version=legacy, got %q", invocation)
	}
	if strings.Contains(invocation, " legacy ") && !strings.Contains(invocation, "--scaffold-version=legacy") {
		t.Errorf("version must never appear as a bare positional, got %q", invocation)
	}
}
