package learn

import (
	"os"
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

	if err := WriteDraft(dir, d, false); err != nil {
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
	err := WriteDraft(dir, d, false)
	if err == nil || !strings.Contains(err.Error(), "cannot appear in a filename") {
		t.Fatalf("expected a clear piped-path-filter error, got %v", err)
	}
}

// A draft's Files come from a provider's tool call or an agent-supplied --draft JSON, neither of
// which is trusted input, so WriteDraft must reject a path that would escape outputDir instead of
// silently writing outside it via filepath.Join's ".." handling.
func TestWriteDraft_RejectsPathEscapingOutputDir(t *testing.T) {
	dir := t.TempDir()
	d := &Draft{
		Name:      "broken",
		Variables: []DraftVariable{{Name: "ClassName", Default: "Widget"}},
		Files: []DraftFile{
			{Path: "../../escaped.txt", Content: "x"},
		},
	}
	if err := WriteDraft(dir, d, false); err == nil {
		t.Fatal("expected WriteDraft to reject a file path escaping the output directory")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(dir)), "escaped.txt")); err == nil {
		t.Fatal("escaped.txt was written outside the output directory")
	}
}

// An absolute path bypasses outputDir entirely rather than merely escaping it via "..".
func TestWriteDraft_RejectsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(t.TempDir(), "evil.txt")
	d := &Draft{
		Name:      "broken",
		Variables: []DraftVariable{{Name: "ClassName", Default: "Widget"}},
		Files: []DraftFile{
			{Path: abs, Content: "x"},
		},
	}
	if err := WriteDraft(dir, d, false); err == nil {
		t.Fatal("expected WriteDraft to reject an absolute file path")
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
	if err := WriteDraft(dir, d, false); err == nil {
		t.Fatal("expected WriteDraft to surface the self-validation failure")
	}
}

// A variable named `Name` maps to the flag --name, which jig reserves. jig.Load accepts it, so
// without this check `learn` reports success and every later `scaffold create` fails instead.
func TestWriteDraft_RejectsReservedVariableFlag(t *testing.T) {
	for _, name := range []string{"Name", "Scaffold", "Template", "Data"} {
		dir := t.TempDir()
		d := &Draft{
			Name:      "reserved",
			Variables: []DraftVariable{{Name: name, Default: "x"}},
			Files:     []DraftFile{{Path: "a.txt", Content: "x"}},
		}
		err := WriteDraft(dir, d, false)
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("expected variable %q to be rejected as reserved, got %v", name, err)
		}
	}
}

// ApplyComputed would silently overwrite the variable of the same name at render time, and
// jig.Validate has no duplicate check of its own.
func TestWriteDraft_RejectsComputedShadowingVariable(t *testing.T) {
	dir := t.TempDir()
	d := &Draft{
		Name:      "shadowed",
		Variables: []DraftVariable{{Name: "EntityName", Default: "Widget"}},
		Computed:  []DraftComputed{{Name: "EntityName", Value: "{{ .EntityName | kebabcase }}"}},
		Files:     []DraftFile{{Path: "a.txt", Content: "x"}},
	}
	if err := WriteDraft(dir, d, false); err == nil {
		t.Fatal("expected a computed entry shadowing a variable to be rejected")
	}
}

// jig.yaml at the root would clobber the manifest WriteDraft just generated; a `_*.tpl` file is
// only ever read as shared template definitions, never emitted as output.
func TestWriteDraft_RejectsReservedFileNames(t *testing.T) {
	for _, p := range []string{"jig.yaml", "nested/jig.yaml", "_helpers.tpl", ""} {
		dir := t.TempDir()
		d := &Draft{
			Name:  "reserved-path",
			Files: []DraftFile{{Path: p, Content: "x"}},
		}
		if err := WriteDraft(dir, d, false); err == nil {
			t.Fatalf("expected file path %q to be rejected", p)
		}
	}
}

// Windows matches filenames case-insensitively, so "Jig.yaml" is the same file as the manifest
// WriteDraft wrote moments earlier. When the draft file's content happens to parse as a jig, the
// self-validation load succeeds too - so without a case-insensitive check `learn` reports success
// over a manifest it destroyed.
func TestWriteDraft_RejectsReservedFileNamesCaseInsensitively(t *testing.T) {
	for _, p := range []string{"Jig.yaml", "JIG.YAML", "nested/Jig.Yaml", "_helpers.TPL"} {
		dir := t.TempDir()
		d := &Draft{
			Name:      "reserved-path",
			Variables: []DraftVariable{{Name: "EntityName", Default: "Widget"}},
			Files:     []DraftFile{{Path: p, Content: "name: harmless\n"}},
		}
		if err := WriteDraft(dir, d, false); err == nil {
			t.Fatalf("expected file path %q to be rejected as reserved", p)
		}
	}
}

// `target` is the path a file takes in a generated project and comes from the same untrusted draft
// as `path`, but validDraftPath never saw it - so every reserved-name and escape rule was one
// field away from being bypassed.
func TestWriteDraft_RejectsBadTarget(t *testing.T) {
	for _, target := range []string{jig.FileName, "Jig.yaml", "_helpers.tpl", "../escaped.txt",
		"/etc/passwd", ".", "  "} {
		dir := t.TempDir()
		d := &Draft{
			Name:  "bad-target",
			Files: []DraftFile{{Path: "config.txt", Content: "x", Target: target}},
		}
		if err := WriteDraft(dir, d, false); err == nil {
			t.Fatalf("expected target %q to be rejected", target)
		}
	}
}

// Two entries writing the same path, or an entry whose path another entry needs as a directory,
// fail partway through writing and leave --output half-populated - which the non-empty check then
// refuses to let the user retry into.
func TestWriteDraft_RejectsConflictingPaths(t *testing.T) {
	cases := map[string][]DraftFile{
		"duplicate path": {
			{Path: "a.txt", Content: "first"},
			{Path: "./a.txt", Content: "second"},
		},
		"file used as a directory": {
			{Path: "a", Content: "i am a file"},
			{Path: "a/b.txt", Content: "i need a to be a directory"},
		},
	}
	for name, files := range cases {
		dir := t.TempDir()
		d := &Draft{Name: "conflicting", Files: files}
		if err := WriteDraft(dir, d, false); err == nil {
			t.Fatalf("%s: expected WriteDraft to reject the file set", name)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("%s: --output was left populated after a rejected draft: %d entries", name, len(entries))
		}
	}
}

// --output pointed at an existing template must not silently overwrite it, which is the whole
// reason --output is mandatory in the first place.
func TestWriteDraft_RefusesNonEmptyOutputWithoutForce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("keep me"), 0o644); err != nil {
		t.Fatalf("seeding output dir: %v", err)
	}
	d := &Draft{
		Name:  "widget",
		Files: []DraftFile{{Path: "a.txt", Content: "x"}},
	}

	err := WriteDraft(dir, d, false)
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("expected a non-empty --output to be refused, got %v", err)
	}
	if err := WriteDraft(dir, d, true); err != nil {
		t.Fatalf("--force should write anyway, got %v", err)
	}
}

// A file stored under a safe name (gitignore.tpl) must land as its real name (.gitignore) via a
// `files:` entry, so git doesn't apply it to the templates repo itself.
func TestWriteDraft_TargetBecomesFilesEntry(t *testing.T) {
	dir := t.TempDir()
	d := &Draft{
		Name:  "widget",
		Files: []DraftFile{{Path: "gitignore.tpl", Content: "target/\n", Target: ".gitignore"}},
	}
	if err := WriteDraft(dir, d, false); err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}

	m, err := jig.Load(filepath.Join(dir, jig.FileName))
	if err != nil {
		t.Fatalf("jig.Load failed: %v", err)
	}
	if len(m.Files) != 1 || m.Files[0].Path != "gitignore.tpl" || m.Files[0].Target != ".gitignore" {
		t.Fatalf("expected a files: entry mapping gitignore.tpl -> .gitignore, got %+v", m.Files)
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
	if err := WriteDraft(dir, d, false); err != nil {
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

// A model that fails to templatize a detected secret - leaving the raw placeholder token in a
// file's content instead of a {{ .Var }} reference - must be caught, not silently written.
func TestWriteDraft_RejectsSurvivingRedactionPlaceholder(t *testing.T) {
	dir := t.TempDir()
	d := &Draft{
		Name: "widget",
		Variables: []DraftVariable{
			{Name: "DbPassword", Required: true, Redacted: true},
		},
		Files: []DraftFile{
			// Wrong: should reference {{ .DbPassword }}, not leave the raw token behind.
			{Path: "app.properties", Content: "password=__SCAFFOLD_REDACTED_SECRET_1__\n"},
		},
	}
	err := WriteDraft(dir, d, false)
	if err == nil {
		t.Fatal("expected a surviving raw redaction placeholder to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "redaction placeholder") {
		t.Errorf("expected the error to name the redaction placeholder, got: %v", err)
	}
}

// A `redacted: true` variable declaring a default anyway is rejected - jig.Validate's own rule,
// exercised here through the same jig.Load self-validation WriteDraft already relies on.
func TestWriteDraft_RejectsRedactedVariableWithDefault(t *testing.T) {
	dir := t.TempDir()
	d := &Draft{
		Name: "widget",
		Variables: []DraftVariable{
			{Name: "DbPassword", Required: true, Redacted: true, Default: "changeit"},
		},
		Files: []DraftFile{
			{Path: "app.properties", Content: "password={{ .DbPassword }}\n"},
		},
	}
	err := WriteDraft(dir, d, false)
	if err == nil {
		t.Fatal("expected a redacted variable with a default to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "redacted") || !strings.Contains(err.Error(), "default") {
		t.Errorf("expected the error to explain the redacted/default conflict, got: %v", err)
	}
}
