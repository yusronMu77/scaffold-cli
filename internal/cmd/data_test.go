package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// withData appends a `data:` block to the framework level and a template that consumes it, so each
// test below only has to say what makes it different.
func withData(t *testing.T, root, frameworkData, template string) {
	t.Helper()
	fw := filepath.Join(root, "fw")
	existing := readFile(t, filepath.Join(fw, "jig.yaml"))
	writeFile(t, fw, "jig.yaml", existing+frameworkData)
	writeFile(t, filepath.Join(fw, "1.0", "tmpl", "services", "web"), "out.txt", template)
}

// Covers a list declared in a jig being iterated by a template several levels below it.
func TestData_ListReachesTheTemplate(t *testing.T) {
	root := buildScaffoldingCode(t)
	withData(t, root, `
data:
  endpoints:
    - { path: "/orders", method: GET }
    - { path: "/orders/{id}", method: DELETE }
`, `{{ range .Data.endpoints }}{{ .method }} {{ .path }}
{{ end }}`)

	out, _, err := createInto(t, root, "fw", "services", "payment", "--function=web", "--print")
	if err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	for _, want := range []string{"GET /orders", "DELETE /orders/{id}"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the rendered output, got:\n%s", want, out)
		}
	}
}

// Covers a multi-line code block carried through the chain and spliced in at the right
// indentation via `indent`.
func TestData_CodeBlockIsSplicedInIndented(t *testing.T) {
	root := buildScaffoldingCode(t)
	withData(t, root, `
data:
  snippets:
    audit: |
      @PrePersist
      void onCreate() {
          this.owner = "{{ .PackageName }}";
      }
`, `class Order {
{{ .Data.snippets.audit | indent 4 }}
}`)

	out, _, err := createInto(t, root, "fw", "services", "payment", "--function=web", "--print")
	if err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	if !strings.Contains(out, "    @PrePersist") {
		t.Errorf("expected the snippet indented into the class body, got:\n%s", out)
	}
	if !strings.Contains(out, `this.owner = "com.example";`) {
		t.Errorf("expected the variable inside the snippet to be substituted, got:\n%s", out)
	}
}

// Covers a values file merging over jig data rather than replacing it, so setting one nested key
// leaves the rest alone.
func TestData_ValuesFileMergesOverTheManifest(t *testing.T) {
	root := buildScaffoldingCode(t)
	withData(t, root, `
data:
  features:
    audit: false
    swagger: true
`, `audit={{ .Data.features.audit }} swagger={{ .Data.features.swagger }}`)

	values := filepath.Join(t.TempDir(), "values.yaml")
	writeFile(t, filepath.Dir(values), "values.yaml", `
framework: fw
category: services
name: payment
function: web
data:
  features:
    audit: true
`)

	out, _, err := createInto(t, root, "-f", values, "--print")
	if err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	if !strings.Contains(out, "audit=true swagger=true") {
		t.Errorf("expected the values file to override only `audit`, got:\n%s", out)
	}
}

// Covers that nested structure at the top level of a values file is rejected with an error
// pointing at `data:`.
func TestData_TopLevelStructureInValuesFilePointsAtData(t *testing.T) {
	root := buildScaffoldingCode(t)
	values := filepath.Join(t.TempDir(), "values.yaml")
	writeFile(t, filepath.Dir(values), "values.yaml", `
framework: fw
category: services
name: payment
features:
  audit: true
`)

	out, _, err := createInto(t, root, "-f", values)
	if err == nil {
		t.Fatalf("expected a nested top-level key to be rejected, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), "data:") {
		t.Errorf("expected the error to point at `data:`, got: %v", err)
	}
}

// Covers that a misspelled `.Data` key fails the render, since the engine has no schema for
// `data:` content and relies on missingkey=error to catch it.
func TestData_MisspelledKeyFailsLoudly(t *testing.T) {
	root := buildScaffoldingCode(t)
	withData(t, root, "\ndata:\n  timeoutSeconds: 30\n", `timeout={{ .Data.timeoutSecond }}`)

	if _, _, err := createInto(t, root, "fw", "services", "payment", "--function=web", "--print"); err == nil {
		t.Fatal("expected a misspelled .Data key to fail the render, got nil")
	}
}

// --dry-run shows the merged object, because "what did .Data end up as?" should not require
// reading every jig on the chain.
func TestData_DryRunShowsTheMergedObject(t *testing.T) {
	root := buildScaffoldingCode(t)
	withData(t, root, "\ndata:\n  features:\n    audit: true\n", `{{ .Data.features.audit }}`)

	out, _, err := createInto(t, root, "fw", "services", "payment", "--function=web", "--dry-run")
	if err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	if !strings.Contains(out, "Data (.Data") || !strings.Contains(out, "audit: true") {
		t.Errorf("expected the merged data object in the dry run, got:\n%s", out)
	}
}

// Covers that `data` is a reserved flag name, since a variable claiming it would make a values
// file ambiguous.
func TestData_VariableNamedDataIsRejected(t *testing.T) {
	root := buildScaffoldingCode(t)
	web := filepath.Join(root, "fw", "1.0", "tmpl", "services", "web")
	writeFile(t, web, "jig.yaml",
		readFile(t, filepath.Join(web, "jig.yaml"))+
			"variables:\n  - name: Payload\n    flag: data\n    default: x\n")

	_, _, err := createInto(t, root, "fw", "services", "payment", "--function=web", "--dry-run")
	if err == nil {
		t.Fatal("expected a variable flagged --data to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("expected the error to say the name is reserved, got: %v", err)
	}
}
