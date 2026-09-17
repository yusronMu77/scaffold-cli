package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildMergeableConfigScaffold builds a minimal, flat_output leaf whose one file (config.yml) is
// registered under `merge:` - so two `create` runs into the same --output each contribute one
// key, the same shape a real dependency-manifest addition takes across invocations (issue #80).
func buildMergeableConfigScaffold(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeFile(t, root, "jig.yaml", "name: root\nvalues:\n  - name: app\n")
	writeFile(t, filepath.Join(root, "app"), "jig.yaml", "name: app\nvalues:\n  - name: \"1.0\"\n    default: true\n")

	v := filepath.Join(root, "app", "1.0")
	writeFile(t, v, "jig.yaml", "name: v\nvalues:\n  - name: templates\n")

	tmpl := filepath.Join(v, "templates")
	writeFile(t, tmpl, "jig.yaml", "name: T\nrequired: true\nvalues:\n  - name: cfg\n    default: true\n")

	cfg := filepath.Join(tmpl, "cfg")
	writeFile(t, cfg, "jig.yaml", `
name: Cfg
flat_output: true
variables:
  - name: Key
    required: true
  - name: Val
    required: true
merge:
  - config.yml
`)
	writeFile(t, cfg, "config.yml", "{{ .Key }}: {{ .Val }}\n")

	return root
}

// --skip-existing must not throw away a second invocation's contribution to a file registered
// under `merge:` - it should deep-merge into the copy already on disk instead of leaving it
// untouched, the same way a fresh render already merges multiple sources within one invocation.
func TestCreate_SkipExistingStillMergesRegisteredFiles(t *testing.T) {
	root := buildMergeableConfigScaffold(t)
	outDir := t.TempDir()

	if _, err := run(t, newCreateCommand, "app", "cfg", "svc1",
		"--key=a", "--val=1", "--scaffolding-code="+root, "--output="+outDir); err != nil {
		t.Fatalf("first create returned error: %v", err)
	}
	if _, err := run(t, newCreateCommand, "app", "cfg", "svc2",
		"--key=b", "--val=2", "--scaffolding-code="+root, "--output="+outDir, "--skip-existing"); err != nil {
		t.Fatalf("second create (--skip-existing) returned error: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(outDir, "config.yml"))
	if err != nil {
		t.Fatalf("reading merged config.yml: %v", err)
	}
	if !strings.Contains(string(got), "a: 1") || !strings.Contains(string(got), "b: 2") {
		t.Errorf("expected both invocations' keys to survive the merge, got:\n%s", got)
	}
}

// A plain file with no `merge:` entry keeps today's --skip-existing behavior exactly: left
// untouched, the second invocation's content discarded.
func TestCreate_SkipExistingStillSkipsUnregisteredFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "jig.yaml", "name: root\nvalues:\n  - name: app\n")
	writeFile(t, filepath.Join(root, "app"), "jig.yaml", "name: app\nvalues:\n  - name: \"1.0\"\n    default: true\n")
	v := filepath.Join(root, "app", "1.0")
	writeFile(t, v, "jig.yaml", "name: v\nvalues:\n  - name: templates\n")
	tmpl := filepath.Join(v, "templates")
	writeFile(t, tmpl, "jig.yaml", "name: T\nrequired: true\nvalues:\n  - name: cfg\n    default: true\n")
	cfg := filepath.Join(tmpl, "cfg")
	writeFile(t, cfg, "jig.yaml", "name: Cfg\nflat_output: true\nvariables:\n  - name: Val\n    required: true\n")
	writeFile(t, cfg, "config.yml", "val: {{ .Val }}\n")

	outDir := t.TempDir()
	if _, err := run(t, newCreateCommand, "app", "cfg", "svc1",
		"--val=1", "--scaffolding-code="+root, "--output="+outDir); err != nil {
		t.Fatalf("first create returned error: %v", err)
	}
	if _, err := run(t, newCreateCommand, "app", "cfg", "svc2",
		"--val=2", "--scaffolding-code="+root, "--output="+outDir, "--skip-existing"); err != nil {
		t.Fatalf("second create (--skip-existing) returned error: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(outDir, "config.yml"))
	if err != nil {
		t.Fatalf("reading config.yml: %v", err)
	}
	if strings.TrimSpace(string(got)) != "val: 1" {
		t.Errorf("expected the first invocation's file to survive untouched, got:\n%s", got)
	}
}
