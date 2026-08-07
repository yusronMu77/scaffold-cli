package cmd

import (
	"path/filepath"
	"strings"
	"testing"
)

// A jig default is a template like any other jig string, so it is rendered rather than emitted
// verbatim.
func TestRules_VariableDefaultIsRendered(t *testing.T) {
	root := buildScaffoldingCode(t)
	web := filepath.Join(root, "fw", "1.0", "tmpl", "services", "web")
	writeFile(t, web, "jig.yaml", readFile(t, filepath.Join(web, "jig.yaml"))+
		"variables:\n  - name: Coordinates\n    default: \"{{ .PackageName }}:{{ .ArtifactId }}\"\n")
	writeFile(t, web, "out.txt", "{{ .Coordinates }}")

	out, _, err := createInto(t, root, "fw", "services", "payment", "--function=web", "--print")
	if err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	if !strings.Contains(out, "com.example:payment") {
		t.Errorf("expected the default to be rendered, got:\n%s", out)
	}
}

// Engine facts are available too, so a default can name what was selected.
func TestRules_DefaultSeesEngineFacts(t *testing.T) {
	root := buildScaffoldingCode(t)
	web := filepath.Join(root, "fw", "1.0", "tmpl", "services", "web")
	writeFile(t, web, "jig.yaml", readFile(t, filepath.Join(web, "jig.yaml"))+
		"variables:\n  - name: Tag\n    default: \"{{ .Scaffold }}-{{ .Version }}\"\n")
	writeFile(t, web, "out.txt", "{{ .Tag }}")

	out, _, err := createInto(t, root, "fw", "services", "payment", "--function=web", "--print")
	if err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	if !strings.Contains(out, "fw-1.0") {
		t.Errorf("expected the engine facts to be visible, got:\n%s", out)
	}
}

// A value the user supplied is data, not a template - braces they typed are emitted verbatim,
// never rendered.
func TestRules_FlagValueIsNotRendered(t *testing.T) {
	root := buildScaffoldingCode(t)
	web := filepath.Join(root, "fw", "1.0", "tmpl", "services", "web")
	writeFile(t, web, "out.txt", "{{ .PackageName }}")

	out, _, err := createInto(t, root, "fw", "services", "payment", "--function=web",
		"--package={{ .ArtifactId }}", "--print")
	if err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	if !strings.Contains(out, "{{ .ArtifactId }}") {
		t.Errorf("a flag value must be carried through verbatim, got:\n%s", out)
	}
}

// An overlay may not default a variable it does not own: it applies last in the chain, so its
// default would silently win over the owning level's.
func TestRules_OverlayMayNotDefaultAnotherLevelsVariable(t *testing.T) {
	root := buildScaffoldingCode(t)
	overlay := filepath.Join(root, "fw", "1.0", "patterns", "microservice")
	writeFile(t, overlay, "jig.yaml",
		"name: MS\nvariables:\n  - name: PackageName\n    default: com.overlay\n")

	_, _, err := createInto(t, root, "fw", "services", "payment", "--function=web", "--style=microservice")
	if err == nil {
		t.Fatal("expected an overlay default for an inherited variable to be rejected, got nil")
	}
	for _, want := range []string{"PackageName", "--style=microservice", "applied last"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the error to mention %q, got: %v", want, err)
		}
	}
}

// An overlay declaring a variable of its OWN, with a default, is exactly what overlays are for.
func TestRules_OverlayMayDefaultItsOwnVariable(t *testing.T) {
	root := buildScaffoldingCode(t)
	overlay := filepath.Join(root, "fw", "1.0", "patterns", "microservice")
	writeFile(t, overlay, "jig.yaml",
		"name: MS\nvariables:\n  - name: FeignTimeout\n    default: \"2000\"\n")
	writeFile(t, overlay, "timeout.txt", "{{ .FeignTimeout }}")

	out, _, err := createInto(t, root, "fw", "services", "payment", "--function=web",
		"--style=microservice", "--print")
	if err != nil {
		t.Fatalf("an overlay's own variable must be allowed: %v", err)
	}
	if !strings.Contains(out, "2000") {
		t.Errorf("expected the overlay's own default to apply, got:\n%s", out)
	}
}

// Removed fields must fail by name, via strict decoding, rather than being silently ignored.
func TestRules_RemovedFieldsAreRejectedByName(t *testing.T) {
	for field, line := range map[string]string{
		"merge_yaml": "merge_yaml:\n  - app.yml\n",
		"post_hooks": "post_hooks:\n  - command: echo hi\n    condition: X\n",
		"type":       "type: \"service\"\n",
	} {
		root := buildScaffoldingCode(t)
		web := filepath.Join(root, "fw", "1.0", "tmpl", "services", "web")
		writeFile(t, web, "jig.yaml", readFile(t, filepath.Join(web, "jig.yaml"))+line)

		_, _, err := createInto(t, root, "fw", "services", "payment", "--function=web", "--dry-run")
		if err == nil {
			t.Errorf("%s: expected the removed field to be rejected, got nil", field)
			continue
		}
		if !strings.Contains(err.Error(), field) {
			t.Errorf("%s: expected the error to name the field, got: %v", field, err)
		}
	}
}

// `--no-hooks` went with post_hooks. A flag that does nothing is worse than no flag.
func TestRules_NoHooksFlagIsGone(t *testing.T) {
	root := buildScaffoldingCode(t)
	_, _, err := createInto(t, root, "fw", "services", "payment", "--function=web", "--no-hooks")
	if err == nil {
		t.Fatal("expected --no-hooks to be rejected as unknown, got nil")
	}
	if !strings.Contains(err.Error(), "no-hooks") {
		t.Errorf("expected the error to name the flag, got: %v", err)
	}
}
