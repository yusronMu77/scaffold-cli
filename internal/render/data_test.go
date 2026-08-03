package render

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"scaffold-engine-go/internal/jig"
)

// jigWithData parses a `data:` block the way a real jig.yaml would, so these tests
// exercise the same decoding path the engine uses rather than hand-built Go maps.
func jigWithData(t *testing.T, body string) *jig.Jig {
	t.Helper()
	var m jig.Jig
	if err := yaml.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("parsing jig fixture: %v", err)
	}
	return &m
}

func mergeData(t *testing.T, ctx Context, bodies ...string) map[string]any {
	t.Helper()
	sources := make([]*jig.Jig, 0, len(bodies))
	for _, b := range bodies {
		sources = append(sources, jigWithData(t, b))
	}
	out, err := MergeData(sources, nil, ctx)
	if err != nil {
		t.Fatalf("MergeData: %v", err)
	}
	return out
}

// The point of `data:` living on the chain: a deeper level adds one nested key and inherits its
// siblings, instead of restating the whole object.
func TestData_DeeperLevelAddsWithoutRestating(t *testing.T) {
	got := mergeData(t, Context{},
		`data:
  observability:
    tracing:
      enabled: true
      sampler: 0.1
    metrics: prometheus
`,
		`data:
  observability:
    tracing:
      sampler: 1.0
`)

	obs := got["observability"].(map[string]any)
	tracing := obs["tracing"].(map[string]any)
	if tracing["sampler"] != 1.0 {
		t.Errorf("the deeper level should win on the key it set, got %v", tracing["sampler"])
	}
	if tracing["enabled"] != true {
		t.Error("a sibling key it did not mention must survive")
	}
	if obs["metrics"] != "prometheus" {
		t.Error("a sibling branch it did not mention must survive")
	}
}

// Lists replace rather than append - the same rule `merge:` applies to files. Appending would make
// every re-application of an overlay silently grow the list.
func TestData_ListsReplaceWholesale(t *testing.T) {
	got := mergeData(t, Context{},
		"data:\n  endpoints: [a, b, c]\n",
		"data:\n  endpoints: [z]\n")

	list := got["endpoints"].([]any)
	if len(list) != 1 || list[0] != "z" {
		t.Errorf("expected the list to be replaced, got %v", list)
	}
}

// An explicit null deletes, which is how a level opts out of something inherited - the `exclude:`
// of the data world.
func TestData_ExplicitNullDeletes(t *testing.T) {
	got := mergeData(t, Context{},
		"data:\n  features:\n    audit: true\n    swagger: true\n",
		"data:\n  features:\n    swagger: null\n")

	features := got["features"].(map[string]any)
	if _, still := features["swagger"]; still {
		t.Error("an explicit null must delete the inherited key")
	}
	if features["audit"] != true {
		t.Error("deleting one key must not disturb the others")
	}
}

// The case this whole field was added for: a block of code, carried verbatim, with the project's
// own variables substituted into it.
func TestData_CodeBlockKeepsItsShapeAndIsRendered(t *testing.T) {
	got := mergeData(t, Context{"EntityName": "Order"},
		`data:
  snippets:
    audit: |
      @PrePersist
      void onCreate() {
          this.createdBy = "{{ .EntityName }}";
      }
`)

	snippet := got["snippets"].(map[string]any)["audit"].(string)
	if !strings.Contains(snippet, `this.createdBy = "Order";`) {
		t.Errorf("expected the variable to be substituted, got:\n%s", snippet)
	}
	if lines := strings.Count(strings.TrimRight(snippet, "\n"), "\n") + 1; lines != 4 {
		t.Errorf("expected the four lines to survive intact, got %d:\n%s", lines, snippet)
	}
	if !strings.Contains(snippet, "    this.createdBy") {
		t.Error("indentation inside the block must be preserved verbatim")
	}
}

// A snippet is code, and code is full of braces. Only Go template's `{{` is an action; a lone brace
// must survive untouched, or nothing with a function body could be stored here.
func TestData_SingleBracesAreNotTemplateSyntax(t *testing.T) {
	got := mergeData(t, Context{},
		"data:\n  snippet: \"if (x) { return {\\\"a\\\": 1}; }\"\n")

	if got["snippet"] != `if (x) { return {"a": 1}; }` {
		t.Errorf("braces must pass through untouched, got %q", got["snippet"])
	}
}

// Non-string scalars keep their own type, so `{{ if .Data.tracing.enabled }}` is a boolean test and
// a port stays a number in a merged application.yml.
func TestData_ScalarsKeepTheirType(t *testing.T) {
	got := mergeData(t, Context{}, "data:\n  enabled: true\n  port: 8080\n")

	if got["enabled"] != true {
		t.Errorf("expected a bool, got %T(%v)", got["enabled"], got["enabled"])
	}
	if got["port"] != 8080 {
		t.Errorf("expected an int, got %T(%v)", got["port"], got["port"])
	}
}

// An error in a forty-line snippet has to say WHERE in the object it was, or the author is left
// searching every level of the chain for it.
func TestData_ErrorNamesThePathInsideTheObject(t *testing.T) {
	sources := []*jig.Jig{jigWithData(t,
		"data:\n  observability:\n    exporters:\n      - endpoint: \"{{ .Nope }}\"\n")}

	_, err := MergeData(sources, nil, Context{})
	if err == nil {
		t.Fatal("expected an unknown variable inside data to fail, got nil")
	}
	if !strings.Contains(err.Error(), "data.observability.exporters[0].endpoint") {
		t.Errorf("expected the error to name the path inside data, got: %v", err)
	}
}

// A values file's `data:` is applied after every jig, so a user can adjust generated content
// without editing scaffolding-code.
func TestData_ValuesFileOverridesTheChain(t *testing.T) {
	sources := []*jig.Jig{jigWithData(t,
		"data:\n  features:\n    audit: false\n    swagger: true\n")}

	got, err := MergeData(sources, map[string]any{
		"features": map[string]any{"audit": true},
	}, Context{})
	if err != nil {
		t.Fatalf("MergeData: %v", err)
	}
	features := got["features"].(map[string]any)
	if features["audit"] != true {
		t.Error("the values file must win over the jig")
	}
	if features["swagger"] != true {
		t.Error("it must merge, not replace the whole object")
	}
}

// Data strings see variables and computed values, not the object they belong to. That ordering is
// what keeps the resolution acyclic; this pins it so it cannot drift into a self-reference.
func TestData_StringsSeeTheVariableContext(t *testing.T) {
	got := mergeData(t, Context{"PackageName": "com.acme.orders"},
		`data:
  imports:
    - "{{ .PackageName }}.model.Order"
`)

	list := got["imports"].([]any)
	if list[0] != "com.acme.orders.model.Order" {
		t.Errorf("expected the variable context to be visible, got %v", list[0])
	}
}
