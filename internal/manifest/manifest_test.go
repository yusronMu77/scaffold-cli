package manifest

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func writeManifest(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture manifest: %v", err)
	}
	return path
}

func TestLoad_SelectorNode(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, "manifest.yaml", `
name: "Services"
description: "Kategori service - pilih function dulu"
selector: "function"
`)

	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !m.IsSelector() {
		t.Errorf("expected IsSelector() = true, got false")
	}
	if m.IsLeaf() {
		t.Errorf("expected IsLeaf() = false, got true")
	}
	if m.Selector != "function" {
		t.Errorf("expected Selector = %q, got %q", "function", m.Selector)
	}
}

func TestLoad_LeafNode(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, "manifest.yaml", `
name: "REST HTTP Service"
description: "Spring Boot REST service"
type: "service"
variables:
  - name: "ProjectName"
    prompt: "Masukkan nama project"
    default: "my-service"
files:
  - path: "pom.xml"
    template: true
dependencies:
  - groupId: "org.springframework.boot"
    artifactId: "spring-boot-starter-web"
merge_priority: 10
incompatible_with:
  - "protocol:none"
`)

	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !m.IsLeaf() {
		t.Errorf("expected IsLeaf() = true, got false")
	}
	if m.IsSelector() {
		t.Errorf("expected IsSelector() = false, got true")
	}
	if len(m.Variables) != 1 || m.Variables[0].Name != "ProjectName" {
		t.Errorf("expected one Variable named ProjectName, got %+v", m.Variables)
	}
	if len(m.Files) != 1 || m.Files[0].Path != "pom.xml" {
		t.Errorf("expected one FileEntry for pom.xml, got %+v", m.Files)
	}
	if len(m.Dependencies) != 1 || m.Dependencies[0].ArtifactID != "spring-boot-starter-web" {
		t.Errorf("expected one Dependency for spring-boot-starter-web, got %+v", m.Dependencies)
	}
	if m.MergePriority != 10 {
		t.Errorf("expected MergePriority = 10, got %d", m.MergePriority)
	}
	if len(m.IncompatibleWith) != 1 || m.IncompatibleWith[0] != "protocol:none" {
		t.Errorf("expected IncompatibleWith = [protocol:none], got %+v", m.IncompatibleWith)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("expected an error for a missing file, got nil")
	}
}

func TestLoad_ParentLeafWithNoSelector(t *testing.T) {
	dir := t.TempDir()
	// parent/manifest.yaml is a leaf directly, zero selector levels (PRD Section 6/7).
	path := writeManifest(t, dir, "manifest.yaml", `
name: "Parent POM"
description: "Static parent pom.xml"
type: "library"
variables:
  - name: "ProjectName"
    prompt: "Masukkan nama project"
    default: "my-parent"
  - name: "PackageName"
    prompt: "Masukkan base package"
    default: "com.company.my"
files:
  - path: "pom.xml"
    template: true
`)

	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !m.IsLeaf() {
		t.Errorf("expected parent manifest to be a leaf with zero selector levels")
	}
}

func TestLoadRoot(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, "manifest.yaml", `
name: "Scaffolding Code Root"
description: "Registry of supported technologies"
frameworks:
  - name: "spring-boot"
    description: "Spring Boot (Java/Maven)"
`)

	root, err := LoadRoot(path)
	if err != nil {
		t.Fatalf("LoadRoot returned error: %v", err)
	}
	if len(root.Frameworks) != 1 {
		t.Fatalf("expected 1 registered framework, got %d: %+v", len(root.Frameworks), root.Frameworks)
	}
	if root.Frameworks[0].Name != "spring-boot" {
		t.Errorf("expected framework name 'spring-boot', got %q", root.Frameworks[0].Name)
	}
}

func TestLoadRoot_MissingFile(t *testing.T) {
	_, err := LoadRoot(filepath.Join(t.TempDir(), "manifest.yaml"))
	if err == nil {
		t.Fatal("expected an error for a missing root manifest, got nil")
	}
}

func TestManifest_Values_And_DefaultValue(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, "manifest.yaml", `
name: "Templates"
description: "Base axis"
values:
  - name: "services"
    description: "Kategori service"
    default: true
  - name: "libs"
    description: "Kategori lib"
  - name: "svc-alias"
    description: "Aliased entry"
    path: "services"
`)

	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(m.Values) != 3 {
		t.Fatalf("expected 3 values entries, got %d: %+v", len(m.Values), m.Values)
	}

	entry, ok := m.DefaultValue()
	if !ok {
		t.Fatal("expected DefaultValue() to find the 'services' entry marked default:true")
	}
	if entry.Name != "services" {
		t.Errorf("expected default entry name 'services', got %q", entry.Name)
	}

	if m.Values[2].DirName() != "services" {
		t.Errorf("expected the aliased 'svc-alias' entry's DirName() to be 'services', got %q", m.Values[2].DirName())
	}
}

func TestManifest_DefaultValue_NoneMarked(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, "manifest.yaml", `
name: "Patterns"
values:
  - name: "monolith"
  - name: "microservice"
`)

	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if _, ok := m.DefaultValue(); ok {
		t.Error("expected DefaultValue() to report no default when no entry is marked default:true")
	}
}

// ---------------------------------------------------------------------------------------------
// Shape classification (PRD v1.8 Section 7.1) — added after the 2026-07-27 design review found
// that IsLeaf() = !IsSelector() misclassified v1.7's registry form as a renderable leaf.
// ---------------------------------------------------------------------------------------------

func TestShape_Classification(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want Shape
	}{
		{"selector", "name: S\nselector: function\n", ShapeSelector},
		{"registry", "name: R\nvalues:\n  - name: a\n", ShapeRegistry},
		{"leaf via files", "name: L\nfiles:\n  - path: pom.xml\n", ShapeLeaf},
		{"leaf via dependencies", "name: L\ndependencies:\n  - groupId: g\n    artifactId: a\n", ShapeLeaf},
		{"leaf via variables", "name: L\nvariables:\n  - name: X\n", ShapeLeaf},
		{"metadata only", "name: M\ndescription: d\n", ShapeUnknown},
		{"empty", "", ShapeUnknown},
		{"misspelled selector", "name: S\nselektor: function\n", ShapeUnknown},
		// A selector node may also carry its own values: registry - the list is simply the
		// authoritative set of values for that selector, so it stays a selector.
		{"selector with values", "name: S\nselector: function\nvalues:\n  - name: web\n", ShapeSelector},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m Manifest
			if err := yaml.Unmarshal([]byte(tc.yaml), &m); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := m.Shape(); got != tc.want {
				t.Errorf("expected shape %v, got %v", tc.want, got)
			}
		})
	}
}

// A metadata-only manifest is legitimate at the axis level (name/description/required, values
// from directory listing), so Load must accept it...
func TestLoad_AcceptsMetadataOnlyAxisManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(path, []byte("name: T\nrequired: true\n"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatalf("expected a metadata-only axis manifest to load, got: %v", err)
	}
	if !m.Required {
		t.Error("expected Required to be parsed")
	}
}

// ...but the same manifest is not navigable, so the selector walk rejects it.
func TestRequireNavigable(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{"selector is navigable", "selector: function\n", false},
		{"leaf is navigable", "files:\n  - path: pom.xml\n", false},
		{"registry is not", "values:\n  - name: a\n", true},
		{"metadata only is not", "name: M\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m Manifest
			if err := yaml.Unmarshal([]byte(tc.yaml), &m); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			err := m.RequireNavigable("fixture.yaml")
			if (err != nil) != tc.wantErr {
				t.Errorf("wantErr=%v, got %v", tc.wantErr, err)
			}
		})
	}
}

// selector + leaf content in one file is genuinely ambiguous and rejected at load time.
func TestLoad_RejectsSelectorPlusLeafContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	body := "name: X\nselector: function\nfiles:\n  - path: pom.xml\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected an ambiguous selector+files manifest to be rejected, got nil")
	}
}

// LoadOptional distinguishes "absent" (fine) from "present but broken" (fatal). Conflating the
// two is what let a malformed registry silently fall back to directory listing.
func TestLoadOptional(t *testing.T) {
	dir := t.TempDir()

	m, err := LoadOptional(filepath.Join(dir, "missing.yaml"))
	if err != nil || m != nil {
		t.Errorf("expected (nil, nil) for an absent file, got (%v, %v)", m, err)
	}

	bad := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(bad, []byte("values: [ broken : yaml"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	if _, err := LoadOptional(bad); err == nil {
		t.Fatal("expected a malformed manifest to be an error, not treated as absent")
	}
}

// PRD v1.8 Section 4.1: name / path / flag are three separate identities.
func TestEntry_DirNameAndFlagName(t *testing.T) {
	plain := Entry{Name: "patterns"}
	if plain.DirName() != "patterns" || plain.FlagName() != "patterns" {
		t.Errorf("expected both to default to Name, got dir=%q flag=%q", plain.DirName(), plain.FlagName())
	}

	aliased := Entry{Name: "patterns", Path: "styles", Flag: "style"}
	if aliased.DirName() != "styles" {
		t.Errorf("expected DirName to use Path, got %q", aliased.DirName())
	}
	if aliased.FlagName() != "style" {
		t.Errorf("expected FlagName to use Flag, got %q", aliased.FlagName())
	}
}

func TestLoadRoot_RejectsEmptyRegistry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(path, []byte("name: Root\n"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	if _, err := LoadRoot(path); err == nil {
		t.Fatal("expected a root manifest with no frameworks to be rejected (the registry is mandatory)")
	}
}
