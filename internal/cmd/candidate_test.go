package cmd

import (
	"path/filepath"
	"strings"
	"testing"
)

// buildCandidateScaffold builds a minimal, separate tree whose one leaf ("web") is marked
// `candidate: true` - the state a `scaffold learn` draft would be in if it landed directly inside
// scaffolding-code before being promoted. Kept separate from buildScaffoldingCode for the same
// reason buildInsertScaffold is. Returns the root and the leaf's own jig.yaml path, so a test can
// rewrite it to simulate promotion.
func buildCandidateScaffold(t *testing.T) (root, leafJigPath string) {
	t.Helper()
	root = t.TempDir()

	writeFile(t, root, "jig.yaml", "name: root\nvalues:\n  - name: app\n")
	writeFile(t, filepath.Join(root, "app"), "jig.yaml", "name: app\nvalues:\n  - name: \"1.0\"\n    default: true\n")

	v := filepath.Join(root, "app", "1.0")
	writeFile(t, v, "jig.yaml", "name: v\nvalues:\n  - name: templates\n")

	tmpl := filepath.Join(v, "templates")
	writeFile(t, tmpl, "jig.yaml", "name: T\nrequired: true\nvalues:\n  - name: web\n    default: true\n")

	web := filepath.Join(tmpl, "web")
	writeFile(t, web, "jig.yaml", "name: Web\ncandidate: true\n")
	writeFile(t, web, "App.txt", "hello\n")

	return root, filepath.Join(web, "jig.yaml")
}

func TestCreate_RejectsUnpromotedCandidate(t *testing.T) {
	root, _ := buildCandidateScaffold(t)
	_, _, err := createInto(t, root, "app", "web", "svc")
	if err == nil {
		t.Fatal("expected create to reject an unpromoted candidate leaf, got nil")
	}
	if !strings.Contains(err.Error(), "candidate") || !strings.Contains(err.Error(), "learn-promote") {
		t.Errorf("expected the error to explain the candidate gate, got: %v", err)
	}
}

func TestList_SurfacesCandidateAsSoftMessage(t *testing.T) {
	root, _ := buildCandidateScaffold(t)
	out, err := run(t, newListCommand, "app", "web", "--scaffolding-code="+root)
	if err != nil {
		t.Fatalf("list is a browsing command and must not hard-fail: %v", err)
	}
	if !strings.Contains(out, "candidate") {
		t.Errorf("expected list to mention the candidate gate, got:\n%s", out)
	}
}

func TestLint_FailsOnUnpromotedCandidate(t *testing.T) {
	root, _ := buildCandidateScaffold(t)
	out, err := run(t, newLintCommand, "--scaffolding-code="+root)
	if err == nil {
		t.Fatal("expected lint to fail on an unpromoted candidate leaf, got nil")
	}
	if !strings.Contains(out, "candidate") {
		t.Errorf("expected the lint report to mention the candidate gate, got:\n%s", out)
	}
}

// Once the candidate flag is cleared (what `scaffold learn-promote` does), create/list/lint all
// accept the very same tree.
func TestCandidateGate_ClearsAfterPromotion(t *testing.T) {
	root, leafJigPath := buildCandidateScaffold(t)
	writeFile(t, filepath.Dir(leafJigPath), "jig.yaml", "name: Web\n")

	if _, _, err := createInto(t, root, "app", "web", "svc"); err != nil {
		t.Errorf("expected create to accept the promoted leaf: %v", err)
	}
	if _, err := run(t, newListCommand, "app", "web", "--scaffolding-code="+root); err != nil {
		t.Errorf("expected list to accept the promoted leaf: %v", err)
	}
	if _, err := run(t, newLintCommand, "--scaffolding-code="+root); err != nil {
		t.Errorf("expected lint to accept the promoted leaf: %v", err)
	}
}
