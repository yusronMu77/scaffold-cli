package render

import (
	"strings"
	"testing"

	"scaffold-engine-go/internal/jig"
)

// Inheritance stops repetition between files; partials stop it inside them. A fragment that
// belongs in twenty files has nowhere else to live.
func TestPartials_IncludeResolvesAcrossSources(t *testing.T) {
	base := t.TempDir()
	leaf := t.TempDir()
	writeFile(t, base, jig.FileName, "name: base\n")
	writeFile(t, base, "_helpers.tpl", `{{ define "hdr" }}// (c) {{ .Owner }}{{ end }}`)
	writeFile(t, leaf, jig.FileName, "name: leaf\n")
	writeFile(t, leaf, "App.java", "{{ include \"hdr\" . }}\nclass App {}\n")

	sources := []Source{
		{Dir: base, Manifest: &jig.Jig{}, Label: "base"},
		{Dir: leaf, Manifest: &jig.Jig{}, Label: "leaf"},
	}
	partials, err := CollectPartials(sources)
	if err != nil {
		t.Fatalf("CollectPartials: %v", err)
	}

	sources[1].Partials = partials
	files, _, err := RenderSource(sources[1], Context{"Owner": "Acme"})
	if err != nil {
		t.Fatalf("RenderSource: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected only App.java, got %v", pathsOf(files))
	}
	if got := string(files[0].Content); !strings.Contains(got, "// (c) Acme") {
		t.Errorf("expected the partial to be included, got:\n%s", got)
	}
}

// A partial file is definitions, never output.
func TestPartials_AreNeverEmitted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, jig.FileName, "name: leaf\n")
	writeFile(t, dir, "_helpers.tpl", `{{ define "x" }}y{{ end }}`)
	writeFile(t, dir, "a.txt", "content\n")

	src := Source{Dir: dir, Manifest: &jig.Jig{}, Label: "leaf"}
	partials, err := CollectPartials([]Source{src})
	if err != nil {
		t.Fatalf("CollectPartials: %v", err)
	}
	src.Partials = partials

	files, _, err := RenderSource(src, Context{})
	if err != nil {
		t.Fatalf("RenderSource: %v", err)
	}
	if got := pathsOf(files); len(got) != 1 || got[0] != "a.txt" {
		t.Errorf("expected _helpers.tpl to be excluded from output, got %v", got)
	}
}

// A deeper source redefining the same name wins, like every other layered thing.
func TestPartials_DeeperDefinitionWins(t *testing.T) {
	base := t.TempDir()
	leaf := t.TempDir()
	writeFile(t, base, jig.FileName, "name: base\n")
	writeFile(t, base, "_h.tpl", `{{ define "greet" }}from base{{ end }}`)
	writeFile(t, leaf, jig.FileName, "name: leaf\n")
	writeFile(t, leaf, "_h.tpl", `{{ define "greet" }}from leaf{{ end }}`)
	writeFile(t, leaf, "out.txt", `{{ include "greet" . }}`)

	sources := []Source{
		{Dir: base, Manifest: &jig.Jig{}, Label: "base"},
		{Dir: leaf, Manifest: &jig.Jig{}, Label: "leaf"},
	}
	partials, err := CollectPartials(sources)
	if err != nil {
		t.Fatalf("CollectPartials: %v", err)
	}
	sources[1].Partials = partials

	files, _, err := RenderSource(sources[1], Context{})
	if err != nil {
		t.Fatalf("RenderSource: %v", err)
	}
	if got := strings.TrimSpace(string(files[0].Content)); got != "from leaf" {
		t.Errorf("expected the deeper definition to win, got %q", got)
	}
}

// `include` returns a string so it can be piped - the built-in `template` action cannot, which
// makes it unusable for anything that has to be indented.
func TestPartials_IncludeIsPipeable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, jig.FileName, "name: leaf\n")
	writeFile(t, dir, "_h.tpl", `{{ define "block" }}a
b{{ end }}`)
	writeFile(t, dir, "out.yml", `root:
{{ include "block" . | indent 2 }}`)

	src := Source{Dir: dir, Manifest: &jig.Jig{}, Label: "leaf"}
	partials, err := CollectPartials([]Source{src})
	if err != nil {
		t.Fatalf("CollectPartials: %v", err)
	}
	src.Partials = partials

	files, _, err := RenderSource(src, Context{})
	if err != nil {
		t.Fatalf("RenderSource: %v", err)
	}
	if got := string(files[0].Content); !strings.Contains(got, "  a\n  b") {
		t.Errorf("expected the included block to be indented, got:\n%q", got)
	}
}

// A misspelled partial name must fail, not render empty.
func TestPartials_UnknownNameIsAnError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, jig.FileName, "name: leaf\n")
	writeFile(t, dir, "out.txt", `{{ include "nope" . }}`)

	src := Source{Dir: dir, Manifest: &jig.Jig{}, Label: "leaf"}
	partials, err := CollectPartials([]Source{src})
	if err != nil {
		t.Fatalf("CollectPartials: %v", err)
	}
	src.Partials = partials

	if _, _, err := RenderSource(src, Context{}); err == nil {
		t.Fatal("expected an unknown partial name to fail the render, got nil")
	}
}
