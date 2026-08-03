package discovery

import (
	"path/filepath"
	"strings"
	"testing"
)

// Version inheritance. Versions are siblings, so the top-down chain cannot relate them; without
// `inherits`, supporting an older release line means copying the entire template tree.

func TestResolveVersionChain_BaseFirst(t *testing.T) {
	frameworkPath := t.TempDir()
	writeFixtureManifest(t, frameworkPath, `
name: fw
values:
  - name: "3.2.x"
    default: true
  - name: "2.7.x"
    inherits: "3.2.x"
`)
	got, err := ResolveVersionChain(frameworkPath, "2.7.x")
	if err != nil {
		t.Fatalf("ResolveVersionChain: %v", err)
	}
	if len(got) != 2 || got[0] != "3.2.x" || got[1] != "2.7.x" {
		t.Errorf("expected [3.2.x 2.7.x] with the base first, got %v", got)
	}
}

func TestResolveVersionChain_NoInheritance(t *testing.T) {
	frameworkPath := t.TempDir()
	writeFixtureManifest(t, frameworkPath, "name: fw\nvalues:\n  - name: \"3.2.x\"\n    default: true\n")
	got, err := ResolveVersionChain(frameworkPath, "")
	if err != nil {
		t.Fatalf("ResolveVersionChain: %v", err)
	}
	if len(got) != 1 || got[0] != "3.2.x" {
		t.Errorf("expected a single-element chain, got %v", got)
	}
}

// A typo in `inherits` must name the problem rather than resolve to something arbitrary.
func TestResolveVersionChain_RejectsUnknownParent(t *testing.T) {
	frameworkPath := t.TempDir()
	writeFixtureManifest(t, frameworkPath, `
name: fw
values:
  - name: "2.7.x"
    default: true
    inherits: "3.2.y"
`)
	_, err := ResolveVersionChain(frameworkPath, "")
	if err == nil || !strings.Contains(err.Error(), "3.2.y") {
		t.Fatalf("expected the unknown parent to be named, got: %v", err)
	}
}

// A cycle has to terminate with a message, not spin.
func TestResolveVersionChain_RejectsCycle(t *testing.T) {
	frameworkPath := t.TempDir()
	writeFixtureManifest(t, frameworkPath, `
name: fw
values:
  - name: "a"
    default: true
    inherits: "b"
  - name: "b"
    inherits: "a"
`)
	_, err := ResolveVersionChain(frameworkPath, "")
	if err == nil || !strings.Contains(err.Error(), "loops") {
		t.Fatalf("expected a cycle to be reported, got: %v", err)
	}
}

// Each node is resolved in the most derived version that HAS it, and every version that does is
// recorded - so a derived version can override one leaf without owning the path above it.
func TestWalkCategoryChain_DerivedOverridesOneLeaf(t *testing.T) {
	base := t.TempDir()
	derived := t.TempDir()

	writeFixtureManifest(t, filepath.Join(base, "services"),
		"name: S\nselector: function\ndefault: web\nvalues:\n  - name: web\n")
	writeFixtureManifest(t, filepath.Join(base, "services", "web"), "name: leaf\nfiles:\n  - path: a.txt\n")
	// The derived version has ONLY the leaf - no jig at services/ above it.
	writeFixtureManifest(t, filepath.Join(derived, "services", "web"), "name: leaf override\n")

	result, err := WalkCategoryChain([]string{base, derived}, "services", nil)
	if err != nil {
		t.Fatalf("WalkCategoryChain: %v", err)
	}
	if len(result.Chain) != 2 {
		t.Fatalf("expected services then web, got %d nodes", len(result.Chain))
	}
	if got := result.Chain[0].Dirs; len(got) != 1 {
		t.Errorf("expected services/ from the base version only, got %v", got)
	}
	if got := result.Chain[1].Dirs; len(got) != 2 || got[1] != filepath.Join(derived, "services", "web") {
		t.Errorf("expected web/ from both versions with the derived one last, got %v", got)
	}
}

// A derived jig must not erase navigational fields it did not restate, or an intermediate node
// silently becomes a leaf and drops every level below it.
func TestWalkCategoryChain_DerivedNodeKeepsInheritedSelector(t *testing.T) {
	base := t.TempDir()
	derived := t.TempDir()

	writeFixtureManifest(t, filepath.Join(base, "services"),
		"name: S\nselector: function\ndefault: web\nvalues:\n  - name: web\n")
	writeFixtureManifest(t, filepath.Join(base, "services", "web"), "name: leaf\nfiles:\n  - path: a.txt\n")
	// Declares nothing but a name: it exists only to contribute a file.
	writeFixtureManifest(t, filepath.Join(derived, "services"), "name: S override\n")

	result, err := WalkCategoryChain([]string{base, derived}, "services", nil)
	if err != nil {
		t.Fatalf("WalkCategoryChain: %v", err)
	}
	if len(result.Steps) != 1 || result.Steps[0].Flag != "function" {
		t.Fatalf("expected the inherited selector to survive, got steps %+v", result.Steps)
	}
	if filepath.Base(result.LeafDir) != "web" {
		t.Errorf("expected the walk to reach web/, got %q", result.LeafDir)
	}
}
