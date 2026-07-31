package discovery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixtureManifest writes a manifest.yaml at dir/manifest.yaml, creating dir if needed.
func writeFixtureManifest(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating fixture dir %s: %v", dir, err)
	}
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture manifest %s: %v", path, err)
	}
}

// buildFixtureTree mimics scaffolding-code/<framework>/<version>/ with three contrasting
// category depths (parent=0, libs=1, services=2), plus a patterns axis, matching PRD Section 4.
func buildFixtureTree(t *testing.T) (versionPath string) {
	t.Helper()
	root := t.TempDir()
	versionPath = filepath.Join(root, "spring-boot", "3.2.x")
	templatesPath := filepath.Join(versionPath, "templates")

	// parent: leaf directly, 0 selector levels.
	writeFixtureManifest(t, filepath.Join(templatesPath, "parent"), `
name: "Parent POM"
description: "Static parent pom.xml"
type: "library"
variables:
  - name: "ProjectName"
    prompt: "Nama project"
    default: "my-parent"
files:
  - path: "pom.xml"
    template: true
`)

	// libs: selector "category", 1 level.
	writeFixtureManifest(t, filepath.Join(templatesPath, "libs"), `
name: "Libs"
description: "Kategori lib - pilih category dulu"
selector: "category"
`)
	writeFixtureManifest(t, filepath.Join(templatesPath, "libs", "starter"), `
name: "Starter Lib"
description: "Starter library leaf"
type: "library"
files:
  - path: "pom.xml"
    template: true
`)

	// templates root: explicit values: registry (not directory listing), with "services"
	// marked as the default category if <category> itself is omitted.
	writeFixtureManifest(t, templatesPath, `
name: "Templates"
description: "Base axis"
values:
  - name: "services"
    description: "Kategori service"
    default: true
  - name: "libs"
    description: "Kategori lib"
  - name: "parent"
    description: "Parent POM"
`)

	// services: selector "function" then "protocol", 2 levels, each with a default.
	writeFixtureManifest(t, filepath.Join(templatesPath, "services"), `
name: "Services"
description: "Kategori service - pilih function dulu"
selector: "function"
default: "web"
`)
	writeFixtureManifest(t, filepath.Join(templatesPath, "services", "web"), `
name: "Web"
description: "Function web - pilih protocol dulu"
selector: "protocol"
default: "rest-http"
`)
	writeFixtureManifest(t, filepath.Join(templatesPath, "services", "web", "rest-http"), `
name: "REST HTTP Service"
description: "Spring Boot REST service"
type: "service"
files:
  - path: "pom.xml"
    template: true
dependencies:
  - groupId: "org.springframework.boot"
    artifactId: "spring-boot-starter-web"
`)

	// patterns: optional top-level axis, sibling to templates.
	writeFixtureManifest(t, filepath.Join(versionPath, "patterns", "microservice"), `
name: "Microservice"
description: "Microservice architecture overlay"
type: "pattern"
dependencies:
  - groupId: "org.springframework.cloud"
    artifactId: "spring-cloud-starter-openfeign"
`)

	return versionPath
}

func writeRootManifest(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "manifest.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("writing root manifest fixture: %v", err)
	}
}

func TestResolveFrameworkPath_DefaultsToName(t *testing.T) {
	root := t.TempDir()
	writeRootManifest(t, root, `
name: "Scaffolding Code Root"
frameworks:
  - name: "spring-boot"
    description: "Spring Boot"
`)
	if err := os.MkdirAll(filepath.Join(root, "spring-boot"), 0o755); err != nil {
		t.Fatalf("creating framework dir: %v", err)
	}

	got, err := ResolveFrameworkPath(root, "spring-boot")
	if err != nil {
		t.Fatalf("ResolveFrameworkPath returned error: %v", err)
	}
	want := filepath.Join(root, "spring-boot")
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestResolveFrameworkPath_AliasedPath(t *testing.T) {
	root := t.TempDir()
	writeRootManifest(t, root, `
name: "Scaffolding Code Root"
frameworks:
  - name: "spring"
    description: "Spring Boot"
    path: "spring-boot"
`)
	if err := os.MkdirAll(filepath.Join(root, "spring-boot"), 0o755); err != nil {
		t.Fatalf("creating framework dir: %v", err)
	}

	got, err := ResolveFrameworkPath(root, "spring")
	if err != nil {
		t.Fatalf("ResolveFrameworkPath returned error: %v", err)
	}
	want := filepath.Join(root, "spring-boot")
	if got != want {
		t.Errorf("expected the aliased 'spring' name to resolve to folder 'spring-boot', got %q", got)
	}
}

func TestResolveFrameworkPath_UnknownFramework(t *testing.T) {
	root := t.TempDir()
	writeRootManifest(t, root, `
name: "Scaffolding Code Root"
frameworks:
  - name: "spring-boot"
    description: "Spring Boot"
`)

	_, err := ResolveFrameworkPath(root, "does-not-exist")
	if err == nil {
		t.Fatal("expected an error for an unregistered framework name, got nil")
	}
}

func TestResolveVersion_Explicit(t *testing.T) {
	got, err := ResolveVersion(t.TempDir(), "3.2.x")
	if err != nil {
		t.Fatalf("ResolveVersion returned error: %v", err)
	}
	if got != "3.2.x" {
		t.Errorf("expected explicit version to pass through unchanged, got %q", got)
	}
}

func TestResolveVersion_PicksHighest(t *testing.T) {
	frameworkPath := t.TempDir()
	for _, v := range []string{"2.7.x", "3.2.x", "3.10.x", "3.9.x"} {
		if err := os.MkdirAll(filepath.Join(frameworkPath, v), 0o755); err != nil {
			t.Fatalf("creating version dir: %v", err)
		}
	}

	got, err := ResolveVersion(frameworkPath, "")
	if err != nil {
		t.Fatalf("ResolveVersion returned error: %v", err)
	}
	if got != "3.10.x" {
		t.Errorf("expected 3.10.x to sort highest (numeric, not lexical), got %q", got)
	}
}

func TestResolveVersion_NoVersionsFound(t *testing.T) {
	_, err := ResolveVersion(t.TempDir(), "")
	if err == nil {
		t.Fatal("expected an error when no version folders exist, got nil")
	}
}

func TestResolveVersion_RegistryDefaultWinsOverHighest(t *testing.T) {
	frameworkPath := t.TempDir()
	for _, v := range []string{"2.7.x", "3.2.x", "3.10.x"} {
		if err := os.MkdirAll(filepath.Join(frameworkPath, v), 0o755); err != nil {
			t.Fatalf("creating version dir: %v", err)
		}
	}
	// "3.2.x" is marked default even though "3.10.x" would numerically sort higher - the
	// explicit registry default must win, same as everywhere else this pattern is used.
	writeFixtureManifest(t, frameworkPath, `
name: "Spring Boot"
values:
  - name: "2.7.x"
    description: "Older line"
  - name: "3.2.x"
    description: "Current recommended line"
    default: true
  - name: "3.10.x"
    description: "Newest line"
`)

	got, err := ResolveVersion(frameworkPath, "")
	if err != nil {
		t.Fatalf("ResolveVersion returned error: %v", err)
	}
	if got != "3.2.x" {
		t.Errorf("expected registry default '3.2.x' to win over the numerically-higher '3.10.x', got %q", got)
	}
}

func TestResolveVersion_ExplicitOverridesRegistryDefault(t *testing.T) {
	frameworkPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(frameworkPath, "3.10.x"), 0o755); err != nil {
		t.Fatalf("creating version dir: %v", err)
	}
	writeFixtureManifest(t, frameworkPath, `
name: "Spring Boot"
values:
  - name: "3.2.x"
    default: true
  - name: "3.10.x"
`)

	got, err := ResolveVersion(frameworkPath, "3.10.x")
	if err != nil {
		t.Fatalf("ResolveVersion returned error: %v", err)
	}
	if got != "3.10.x" {
		t.Errorf("expected explicit --fw-version to override the registry default, got %q", got)
	}
}

// PRD Section 4.1: the registry is authoritative for explicitly-typed values too, not just
// for defaults. Previously an explicit --fw-version was returned untouched, so a typo surfaced
// much later as "cannot find the file" naming a path the user never typed (design review section
// 2.12).
func TestResolveVersion_ExplicitRejectedWhenUnregistered(t *testing.T) {
	frameworkPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(frameworkPath, "9.9.9"), 0o755); err != nil {
		t.Fatalf("creating version dir: %v", err)
	}
	writeFixtureManifest(t, frameworkPath, `
name: "Spring Boot"
values:
  - name: "3.2.x"
    default: true
`)

	_, err := ResolveVersion(frameworkPath, "9.9.9")
	if err == nil {
		t.Fatal("expected an unregistered explicit version to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "3.2.x") {
		t.Errorf("expected the error to list the known versions, got: %v", err)
	}
}

// An explicit version also goes through the `path` alias, like every other level.
func TestResolveVersion_ExplicitAppliesPathAlias(t *testing.T) {
	frameworkPath := t.TempDir()
	writeFixtureManifest(t, frameworkPath, `
name: "Spring Boot"
values:
  - name: "3.2"
    path: "3.2.x"
`)

	got, err := ResolveVersion(frameworkPath, "3.2")
	if err != nil {
		t.Fatalf("ResolveVersion returned error: %v", err)
	}
	if got != "3.2.x" {
		t.Errorf("expected the alias to resolve '3.2' to folder '3.2.x', got %q", got)
	}
}

func TestDiscoverAxes(t *testing.T) {
	versionPath := buildFixtureTree(t)

	axes, err := DiscoverAxes(versionPath)
	if err != nil {
		t.Fatalf("DiscoverAxes returned error: %v", err)
	}

	byName := map[string]Axis{}
	for _, a := range axes {
		byName[a.Name] = a
	}

	templatesAxis, ok := byName["templates"]
	if !ok {
		t.Fatal("expected a 'templates' axis")
	}
	if !templatesAxis.Required {
		t.Error("expected 'templates' axis to be Required")
	}
	wantCategories := map[string]bool{"parent": true, "libs": true, "services": true}
	if len(templatesAxis.Values) != len(wantCategories) {
		t.Errorf("expected 3 categories under templates, got %v", templatesAxis.Values)
	}
	for _, v := range templatesAxis.Values {
		if !wantCategories[v] {
			t.Errorf("unexpected category %q under templates", v)
		}
	}

	patternsAxis, ok := byName["patterns"]
	if !ok {
		t.Fatal("expected a 'patterns' axis")
	}
	if patternsAxis.Required {
		t.Error("expected 'patterns' axis to be optional (Required=false)")
	}
	if len(patternsAxis.Values) != 1 || patternsAxis.Values[0] != "microservice" {
		t.Errorf("expected patterns axis to have one value 'microservice', got %v", patternsAxis.Values)
	}
}

func TestDiscoverAxes_DescriptionOptional(t *testing.T) {
	versionPath := t.TempDir()

	// "templates" has its own descriptive manifest.yaml.
	writeFixtureManifest(t, filepath.Join(versionPath, "templates"), `
name: "Templates"
description: "Base axis describing what to generate"
`)
	if err := os.MkdirAll(filepath.Join(versionPath, "templates", "parent"), 0o755); err != nil {
		t.Fatalf("creating category dir: %v", err)
	}

	// "patterns" has no manifest.yaml at all - must not error, just leave Description empty.
	if err := os.MkdirAll(filepath.Join(versionPath, "patterns"), 0o755); err != nil {
		t.Fatalf("creating patterns dir: %v", err)
	}

	axes, err := DiscoverAxes(versionPath)
	if err != nil {
		t.Fatalf("DiscoverAxes returned error: %v", err)
	}

	byName := map[string]Axis{}
	for _, a := range axes {
		byName[a.Name] = a
	}

	if byName["templates"].Description != "Base axis describing what to generate" {
		t.Errorf("expected templates axis description to be read from its manifest.yaml, got %q", byName["templates"].Description)
	}
	if byName["patterns"].Description != "" {
		t.Errorf("expected patterns axis (no manifest.yaml) to have empty description, got %q", byName["patterns"].Description)
	}
}

func TestDiscoverAxes_ValuesRegistryOverridesDirectoryListing(t *testing.T) {
	versionPath := t.TempDir()

	// templates/ has an explicit values: registry naming a CLI alias "svc" -> folder
	// "services", plus a stray extra directory that must NOT show up (registry is
	// authoritative once present, per fundamental rule #2).
	writeFixtureManifest(t, filepath.Join(versionPath, "templates"), `
name: "Templates"
values:
  - name: "svc"
    description: "Aliased services category"
    path: "services"
`)
	if err := os.MkdirAll(filepath.Join(versionPath, "templates", "services"), 0o755); err != nil {
		t.Fatalf("creating services dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(versionPath, "templates", "unregistered-stray-folder"), 0o755); err != nil {
		t.Fatalf("creating stray dir: %v", err)
	}

	axes, err := DiscoverAxes(versionPath)
	if err != nil {
		t.Fatalf("DiscoverAxes returned error: %v", err)
	}

	var templatesAxis Axis
	for _, a := range axes {
		if a.Name == "templates" {
			templatesAxis = a
		}
	}
	if len(templatesAxis.Values) != 1 || templatesAxis.Values[0] != "svc" {
		t.Errorf("expected registry to report exactly one value 'svc' (the CLI-facing name, not the folder name, and not the stray folder), got %v", templatesAxis.Values)
	}
}

func TestDiscoverAxes_RequiredFromManifestNotName(t *testing.T) {
	versionPath := t.TempDir()

	// Folder named "base" (not "templates") explicitly declares required: true.
	writeFixtureManifest(t, filepath.Join(versionPath, "base"), `
name: "Base"
required: true
`)
	if err := os.MkdirAll(filepath.Join(versionPath, "base", "services"), 0o755); err != nil {
		t.Fatalf("creating category dir: %v", err)
	}
	// A sibling axis that must NOT be required, even though it has a manifest.
	writeFixtureManifest(t, filepath.Join(versionPath, "extras"), `
name: "Extras"
`)

	axes, err := DiscoverAxes(versionPath)
	if err != nil {
		t.Fatalf("DiscoverAxes returned error: %v", err)
	}

	byName := map[string]Axis{}
	for _, a := range axes {
		byName[a.Name] = a
	}
	if !byName["base"].Required {
		t.Error("expected the 'base' folder (required: true in its manifest) to be Required, regardless of its name")
	}
	if byName["extras"].Required {
		t.Error("expected 'extras' (no required field, not named templates) to NOT be required")
	}
}

func TestDiscoverAxes_RequiredFallsBackToTemplatesName(t *testing.T) {
	versionPath := t.TempDir()

	// No manifest.yaml at all under "templates" - must still fall back to the name-based
	// default, for backward compatibility with setups that don't declare `required` at all.
	if err := os.MkdirAll(filepath.Join(versionPath, "templates", "services"), 0o755); err != nil {
		t.Fatalf("creating category dir: %v", err)
	}

	axes, err := DiscoverAxes(versionPath)
	if err != nil {
		t.Fatalf("DiscoverAxes returned error: %v", err)
	}
	if len(axes) != 1 || axes[0].Name != "templates" || !axes[0].Required {
		t.Errorf("expected a Required 'templates' axis via the name-based fallback, got %+v", axes)
	}
}

func TestDiscoverAxes_VersionRegistryOverridesDirectoryListing(t *testing.T) {
	versionPath := t.TempDir()

	// The version itself now registers which axes exist, same registry pattern as every
	// other level - a stray unregistered folder must not show up once this file is present.
	writeFixtureManifest(t, versionPath, `
name: "Spring Boot 3.2.x"
values:
  - name: "templates"
    description: "Required base axis"
  - name: "patterns"
    description: "Optional overlay axis"
`)
	writeFixtureManifest(t, filepath.Join(versionPath, "templates"), `
name: "Templates"
required: true
`)
	if err := os.MkdirAll(filepath.Join(versionPath, "templates", "services"), 0o755); err != nil {
		t.Fatalf("creating category dir: %v", err)
	}
	writeFixtureManifest(t, filepath.Join(versionPath, "patterns"), `
name: "Patterns"
`)
	if err := os.MkdirAll(filepath.Join(versionPath, "stray"), 0o755); err != nil {
		t.Fatalf("creating stray dir: %v", err)
	}

	axes, err := DiscoverAxes(versionPath)
	if err != nil {
		t.Fatalf("DiscoverAxes returned error: %v", err)
	}
	if len(axes) != 2 {
		t.Fatalf("expected exactly the 2 registered axes (not the stray folder), got %d: %+v", len(axes), axes)
	}
	byName := map[string]Axis{}
	for _, a := range axes {
		byName[a.Name] = a
	}
	if _, ok := byName["stray"]; ok {
		t.Error("expected the unregistered 'stray' folder to be excluded once a version-level manifest.yaml exists")
	}
	if !byName["templates"].Required {
		t.Error("expected 'templates' axis to still read Required from its own manifest.yaml")
	}
}

func TestDiscoverAxes_VersionRegistryPathAlias(t *testing.T) {
	versionPath := t.TempDir()

	// "base" is the CLI-facing axis name, aliased to the actual "templates" folder - same
	// path-aliasing mechanism as frameworks/versions/categories.
	writeFixtureManifest(t, versionPath, `
name: "Spring Boot 3.2.x"
values:
  - name: "base"
    path: "templates"
    description: "Required base axis, exposed under a different CLI-facing name"
`)
	writeFixtureManifest(t, filepath.Join(versionPath, "templates"), `
name: "Templates"
required: true
`)
	if err := os.MkdirAll(filepath.Join(versionPath, "templates", "services"), 0o755); err != nil {
		t.Fatalf("creating category dir: %v", err)
	}

	axes, err := DiscoverAxes(versionPath)
	if err != nil {
		t.Fatalf("DiscoverAxes returned error: %v", err)
	}
	if len(axes) != 1 {
		t.Fatalf("expected exactly 1 axis, got %d: %+v", len(axes), axes)
	}
	if axes[0].Name != "base" {
		t.Errorf("expected the CLI-facing axis name 'base' (from the registry entry), got %q", axes[0].Name)
	}
	if !axes[0].Required {
		t.Error("expected the aliased axis to resolve into the 'templates' folder and read its required:true")
	}
	if len(axes[0].Values) != 1 || axes[0].Values[0] != "services" {
		t.Errorf("expected the aliased axis's Values to come from the actual 'templates' folder, got %v", axes[0].Values)
	}
}

func TestRequiredAxis(t *testing.T) {
	axes := []Axis{
		{Name: "patterns", Required: false},
		{Name: "base", Required: true},
		{Name: "extras", Required: false},
	}

	got, err := RequiredAxis(axes)
	if err != nil {
		t.Fatalf("RequiredAxis returned error: %v", err)
	}
	if got.Name != "base" {
		t.Errorf("expected the axis marked Required ('base'), got %q", got.Name)
	}
}

func TestRequiredAxis_NoneMarkedErrors(t *testing.T) {
	axes := []Axis{
		{Name: "patterns", Required: false},
		{Name: "extras", Required: false},
	}

	_, err := RequiredAxis(axes)
	if err == nil {
		t.Fatal("expected an error when no axis is marked Required, got nil")
	}
}

func TestWalkCategory_Parent_ZeroLevels(t *testing.T) {
	versionPath := buildFixtureTree(t)
	templatesPath := filepath.Join(versionPath, "templates")

	result, err := WalkCategory(templatesPath, "parent", map[string]string{})
	if err != nil {
		t.Fatalf("WalkCategory returned error: %v", err)
	}
	if len(result.Steps) != 0 {
		t.Errorf("expected 0 selector steps for parent, got %d: %+v", len(result.Steps), result.Steps)
	}
	if result.Leaf.Name != "Parent POM" {
		t.Errorf("expected leaf name 'Parent POM', got %q", result.Leaf.Name)
	}
}

func TestWalkCategory_Libs_OneLevel(t *testing.T) {
	versionPath := buildFixtureTree(t)
	templatesPath := filepath.Join(versionPath, "templates")

	result, err := WalkCategory(templatesPath, "libs", map[string]string{"category": "starter"})
	if err != nil {
		t.Fatalf("WalkCategory returned error: %v", err)
	}
	if len(result.Steps) != 1 || result.Steps[0].Flag != "category" || result.Steps[0].Value != "starter" {
		t.Errorf("expected one step {category:starter}, got %+v", result.Steps)
	}
	if result.Leaf.Name != "Starter Lib" {
		t.Errorf("expected leaf name 'Starter Lib', got %q", result.Leaf.Name)
	}
}

func TestWalkCategory_Services_TwoLevels(t *testing.T) {
	versionPath := buildFixtureTree(t)
	templatesPath := filepath.Join(versionPath, "templates")

	result, err := WalkCategory(templatesPath, "services", map[string]string{
		"function": "web",
		"protocol": "rest-http",
	})
	if err != nil {
		t.Fatalf("WalkCategory returned error: %v", err)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("expected 2 selector steps, got %d: %+v", len(result.Steps), result.Steps)
	}
	if result.Steps[0].Flag != "function" || result.Steps[0].Value != "web" {
		t.Errorf("expected first step {function:web}, got %+v", result.Steps[0])
	}
	if result.Steps[1].Flag != "protocol" || result.Steps[1].Value != "rest-http" {
		t.Errorf("expected second step {protocol:rest-http}, got %+v", result.Steps[1])
	}
	if result.Leaf.Name != "REST HTTP Service" {
		t.Errorf("expected leaf name 'REST HTTP Service', got %q", result.Leaf.Name)
	}
	if len(result.Leaf.Dependencies) != 1 || result.Leaf.Dependencies[0]["artifactId"] != "spring-boot-starter-web" {
		t.Errorf("expected one dependency spring-boot-starter-web, got %+v", result.Leaf.Dependencies)
	}
}

func TestWalkCategory_MissingSelectionUsesDefault(t *testing.T) {
	versionPath := buildFixtureTree(t)
	templatesPath := filepath.Join(versionPath, "templates")

	// --protocol omitted - services/web/manifest.yaml declares default: "rest-http".
	result, err := WalkCategory(templatesPath, "services", map[string]string{"function": "web"})
	if err != nil {
		t.Fatalf("expected default to kick in for --protocol, got error: %v", err)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d: %+v", len(result.Steps), result.Steps)
	}
	protocolStep := result.Steps[1]
	if protocolStep.Value != "rest-http" || !protocolStep.Defaulted {
		t.Errorf("expected protocol step to be {rest-http, Defaulted:true}, got %+v", protocolStep)
	}
}

func TestWalkCategory_MissingSelectionNoDefaultErrors(t *testing.T) {
	versionPath := buildFixtureTree(t)
	templatesPath := filepath.Join(versionPath, "templates")

	// libs/manifest.yaml has selector "category" but no default set - must still error.
	_, err := WalkCategory(templatesPath, "libs", map[string]string{})
	if err == nil {
		t.Fatal("expected an error when --category is missing and no default is set, got nil")
	}
}

func TestDefaultCategory(t *testing.T) {
	versionPath := buildFixtureTree(t)
	templatesPath := filepath.Join(versionPath, "templates")

	got, err := DefaultCategory(templatesPath)
	if err != nil {
		t.Fatalf("DefaultCategory returned error: %v", err)
	}
	if got != "services" {
		t.Errorf("expected default category 'services' (from templates/manifest.yaml), got %q", got)
	}
}

func TestDefaultCategory_NoDefaultSet(t *testing.T) {
	templatesPath := t.TempDir()
	writeFixtureManifest(t, templatesPath, `
name: "Templates"
description: "No default set"
`)

	_, err := DefaultCategory(templatesPath)
	if err == nil {
		t.Fatal("expected an error when templates/manifest.yaml has no default field, got nil")
	}
}

func TestDefaultCategory_FallsBackToLegacyStringDefault(t *testing.T) {
	// A templates/manifest.yaml with no `values:` list at all, just the older bare
	// `default: "<name>"` string - DefaultCategory must still honor it.
	templatesPath := t.TempDir()
	writeFixtureManifest(t, templatesPath, `
name: "Templates"
description: "Legacy string default, no values: list"
default: "services"
`)

	got, err := DefaultCategory(templatesPath)
	if err != nil {
		t.Fatalf("DefaultCategory returned error: %v", err)
	}
	if got != "services" {
		t.Errorf("expected legacy string default 'services', got %q", got)
	}
}

func TestDefaultCategory_And_ResolveCategoryDir_PathOverride(t *testing.T) {
	// A values: entry can alias its CLI-facing name to a different on-disk folder, exactly
	// like the framework registry's path field. DefaultCategory returns the CLI-facing name
	// (matching what a user would type); ResolveCategoryDir is the separate step that turns
	// that name into the actual folder to walk into - same two-step shape as
	// ResolveFrameworkPath's registry lookup.
	templatesPath := t.TempDir()
	writeFixtureManifest(t, templatesPath, `
name: "Templates"
values:
  - name: "svc"
    description: "Short alias for the services folder"
    path: "services"
    default: true
`)

	name, err := DefaultCategory(templatesPath)
	if err != nil {
		t.Fatalf("DefaultCategory returned error: %v", err)
	}
	if name != "svc" {
		t.Fatalf("expected DefaultCategory to return the CLI-facing name 'svc', got %q", name)
	}

	got, err := ResolveCategoryDir(templatesPath, name)
	if err != nil {
		t.Fatalf("ResolveCategoryDir returned error: %v", err)
	}
	if got != "services" {
		t.Errorf("expected ResolveCategoryDir to resolve 'svc' to folder 'services', got %q", got)
	}
}

func TestResolveCategoryDir_NoRegistryFallsBackToNameAsIs(t *testing.T) {
	// No templates/manifest.yaml at all - name is used directly, preserving
	// plain-directory-listing behavior for setups with no explicit registry.
	templatesPath := t.TempDir()

	got, err := ResolveCategoryDir(templatesPath, "services")
	if err != nil {
		t.Fatalf("ResolveCategoryDir returned error: %v", err)
	}
	if got != "services" {
		t.Errorf("expected fallback to the name as-is when there's no registry, got %q", got)
	}
}

// With a registry present it is authoritative: an unregistered category is rejected rather than
// passed through to the filesystem (fundamental rule #2).
func TestResolveCategoryDir_RegistryRejectsUnregisteredCategory(t *testing.T) {
	templatesPath := t.TempDir()
	writeFixtureManifest(t, templatesPath, `
name: "Templates"
values:
  - name: "services"
`)

	_, err := ResolveCategoryDir(templatesPath, "stray")
	if err == nil {
		t.Fatal("expected an unregistered category to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "services") {
		t.Errorf("expected the error to list known categories, got: %v", err)
	}
}

func TestWalkCategory_InvalidSelectionValue(t *testing.T) {
	versionPath := buildFixtureTree(t)
	templatesPath := filepath.Join(versionPath, "templates")

	_, err := WalkCategory(templatesPath, "services", map[string]string{
		"function": "web",
		"protocol": "does-not-exist",
	})
	if err == nil {
		t.Fatal("expected an error for an invalid --protocol value, got nil")
	}
}

func TestDescribeCategory(t *testing.T) {
	versionPath := buildFixtureTree(t)
	templatesPath := filepath.Join(versionPath, "templates")

	m, err := DescribeCategory(templatesPath, "services")
	if err != nil {
		t.Fatalf("DescribeCategory returned error: %v", err)
	}
	if !m.IsSelector() || m.Selector != "function" {
		t.Errorf("expected services category root to be a selector on 'function', got %+v", m)
	}

	m, err = DescribeCategory(templatesPath, "parent")
	if err != nil {
		t.Fatalf("DescribeCategory returned error: %v", err)
	}
	if !m.IsLeaf() {
		t.Errorf("expected parent category root to be a leaf directly")
	}
}

func TestDescribeTree_Parent_ZeroLevels(t *testing.T) {
	versionPath := buildFixtureTree(t)
	templatesPath := filepath.Join(versionPath, "templates")

	tree, err := DescribeTree(templatesPath, "parent")
	if err != nil {
		t.Fatalf("DescribeTree returned error: %v", err)
	}
	if !tree.IsLeaf || len(tree.Children) != 0 {
		t.Errorf("expected parent tree to be a single leaf node, got %+v", tree)
	}
}

func TestDescribeTree_Libs_OneLevel(t *testing.T) {
	versionPath := buildFixtureTree(t)
	templatesPath := filepath.Join(versionPath, "templates")

	tree, err := DescribeTree(templatesPath, "libs")
	if err != nil {
		t.Fatalf("DescribeTree returned error: %v", err)
	}
	if tree.IsLeaf || tree.Selector != "category" {
		t.Fatalf("expected libs root to be a selector on 'category', got %+v", tree)
	}
	if len(tree.Children) != 1 || tree.Children[0].Value != "starter" || !tree.Children[0].IsLeaf {
		t.Errorf("expected one leaf child 'starter', got %+v", tree.Children)
	}
}

func TestDescribeTree_Services_TwoLevels(t *testing.T) {
	versionPath := buildFixtureTree(t)
	templatesPath := filepath.Join(versionPath, "templates")

	tree, err := DescribeTree(templatesPath, "services")
	if err != nil {
		t.Fatalf("DescribeTree returned error: %v", err)
	}
	if tree.IsLeaf || tree.Selector != "function" {
		t.Fatalf("expected services root to be a selector on 'function', got %+v", tree)
	}
	if len(tree.Children) != 1 || tree.Children[0].Value != "web" {
		t.Fatalf("expected one child 'web', got %+v", tree.Children)
	}
	webNode := tree.Children[0]
	if webNode.IsLeaf || webNode.Selector != "protocol" {
		t.Fatalf("expected 'web' node to be a selector on 'protocol', got %+v", webNode)
	}
	if len(webNode.Children) != 1 || webNode.Children[0].Value != "rest-http" || !webNode.Children[0].IsLeaf {
		t.Errorf("expected one leaf child 'rest-http' under web, got %+v", webNode.Children)
	}
}

// ---------------------------------------------------------------------------------------------
// Regression tests for the 2026-07-27 design review (see notes/2026-07-27-design-review-*.md).
// Each of these reproduced a deviation that previously succeeded silently with exit code 0.
// ---------------------------------------------------------------------------------------------

// Section 2.2: DiscoverAxes resolved an axis's `path` alias internally and then discarded it, so
// callers rebuilt the path from Name and read a folder the registry had aliased away. `list`
// worked (it used the resolved path) while `create` failed. Axis.Dir/Axis.Path now carry it.
func TestDiscoverAxes_AxisCarriesResolvedDirAndFlag(t *testing.T) {
	versionPath := t.TempDir()
	writeFixtureManifest(t, versionPath, `
name: "v"
values:
  - name: "templates"
    path: "tmpl"
  - name: "patterns"
    flag: "style"
`)
	writeFixtureManifest(t, filepath.Join(versionPath, "tmpl"), "name: T\nrequired: true\n")
	writeFixtureManifest(t, filepath.Join(versionPath, "patterns"), "name: P\n")

	axes, err := DiscoverAxes(versionPath)
	if err != nil {
		t.Fatalf("DiscoverAxes returned error: %v", err)
	}

	base, err := RequiredAxis(axes)
	if err != nil {
		t.Fatalf("RequiredAxis returned error: %v", err)
	}
	if base.Dir != "tmpl" {
		t.Errorf("expected the required axis to carry its resolved folder 'tmpl', got %q", base.Dir)
	}
	want := filepath.Join(versionPath, "tmpl")
	if got := base.Path(versionPath); got != want {
		t.Errorf("expected Path() to apply the alias (%s), got %s", want, got)
	}

	style, ok := FindAxisByFlag(axes, "style")
	if !ok {
		t.Fatal("expected the patterns axis to be findable by its declared flag --style")
	}
	if style.Name != "patterns" || style.Dir != "patterns" {
		t.Errorf("expected flag 'style' to map to axis name/dir 'patterns', got %q/%q", style.Name, style.Dir)
	}
}

// An axis with no `flag` field keeps its name as the flag, the common case.
func TestDiscoverAxes_FlagDefaultsToName(t *testing.T) {
	versionPath := t.TempDir()
	writeFixtureManifest(t, versionPath, "name: v\nvalues:\n  - name: \"templates\"\n  - name: \"test\"\n")
	writeFixtureManifest(t, filepath.Join(versionPath, "templates"), "name: T\nrequired: true\n")
	writeFixtureManifest(t, filepath.Join(versionPath, "test"), "name: Test\n")

	axes, err := DiscoverAxes(versionPath)
	if err != nil {
		t.Fatalf("DiscoverAxes returned error: %v", err)
	}
	for _, a := range axes {
		if a.Flag != a.Name {
			t.Errorf("axis %q: expected Flag to default to Name, got %q", a.Name, a.Flag)
		}
	}
}

// Section 2.8: the "templates" name fallback was evaluated per axis rather than once, so a
// folder named templates/ alongside an axis declaring required:true produced TWO required axes,
// and RequiredAxis silently returned whichever came first.
func TestDiscoverAxes_NameFallbackSuppressedByExplicitRequired(t *testing.T) {
	versionPath := t.TempDir()
	writeFixtureManifest(t, versionPath, "name: v\nvalues:\n  - name: \"templates\"\n  - name: \"base\"\n")
	writeFixtureManifest(t, filepath.Join(versionPath, "templates"), "name: T\n")
	writeFixtureManifest(t, filepath.Join(versionPath, "base"), "name: B\nrequired: true\n")

	axes, err := DiscoverAxes(versionPath)
	if err != nil {
		t.Fatalf("DiscoverAxes returned error: %v", err)
	}

	required := 0
	for _, a := range axes {
		if a.Required {
			required++
		}
	}
	if required != 1 {
		t.Fatalf("expected exactly one required axis, got %d", required)
	}
	base, err := RequiredAxis(axes)
	if err != nil {
		t.Fatalf("RequiredAxis returned error: %v", err)
	}
	if base.Name != "base" {
		t.Errorf("expected the axis that explicitly declared required:true to win, got %q", base.Name)
	}
}

func TestRequiredAxis_MultipleRequiredIsAnError(t *testing.T) {
	axes := []Axis{
		{Name: "templates", Required: true},
		{Name: "extra", Required: true},
	}
	_, err := RequiredAxis(axes)
	if err == nil {
		t.Fatal("expected two required axes to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "extra") {
		t.Errorf("expected the error to name the conflicting axes, got: %v", err)
	}
}

// Section 2.6: WalkCategory validated a selector value with os.Stat alone, so a folder that the
// node's own `values:` registry never listed was still selectable.
func TestWalkCategory_RegistryRejectsUnregisteredSelectorValue(t *testing.T) {
	templatesPath := t.TempDir()
	writeFixtureManifest(t, filepath.Join(templatesPath, "services"), `
name: "Services"
selector: "function"
values:
  - name: "web"
`)
	writeFixtureManifest(t, filepath.Join(templatesPath, "services", "web"), "name: leaf\nfiles:\n  - path: pom.xml\n")
	writeFixtureManifest(t, filepath.Join(templatesPath, "services", "stray"), "name: stray\nfiles:\n  - path: evil.txt\n")

	if _, err := WalkCategory(templatesPath, "services", map[string]string{"function": "web"}); err != nil {
		t.Fatalf("registered value should walk cleanly: %v", err)
	}

	_, err := WalkCategory(templatesPath, "services", map[string]string{"function": "stray"})
	if err == nil {
		t.Fatal("expected an unregistered selector value to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "web") {
		t.Errorf("expected the error to list valid values, got: %v", err)
	}
}

// Section 2.5: a selector value went straight into filepath.Join, so "../../patterns/x" walked
// clean out of the base axis into a sibling one.
func TestWalkCategory_SelectorValueCannotEscapeTheCategory(t *testing.T) {
	templatesPath := t.TempDir()
	writeFixtureManifest(t, filepath.Join(templatesPath, "services"), "name: S\nselector: function\n")

	_, err := WalkCategory(templatesPath, "services", map[string]string{"function": "../../patterns/microservice"})
	if err == nil {
		t.Fatal("expected a traversing selector value to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "single path segment") {
		t.Errorf("expected a path-segment error, got: %v", err)
	}
}

// Section 2.9: a registry manifest has no `selector`, so the old IsLeaf() = !IsSelector() call
// classified it as a renderable leaf and generated an empty project without complaining.
func TestWalkCategory_RegistryManifestIsNotARenderableLeaf(t *testing.T) {
	templatesPath := t.TempDir()
	writeFixtureManifest(t, filepath.Join(templatesPath, "services"), "name: S\nvalues:\n  - name: web\n")

	_, err := WalkCategory(templatesPath, "services", nil)
	if err == nil {
		t.Fatal("expected a registry manifest on the selector chain to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "registry") {
		t.Errorf("expected the error to explain it is a registry, got: %v", err)
	}
}

// A manifest that declares nothing of its own ends the walk as a leaf, because under the
// inheritance model its content legitimately comes entirely from the levels above it.
//
// This is also what a misspelled `selector` key looks like, and the two are indistinguishable
// from the manifest alone. Erroring here would make pure-inheritance leaves impossible, so the
// typo is caught elsewhere and just as loudly: the flags intended for the missing selector are
// rejected as unknown, and a chain that renders no files fails at write time.
func TestWalkCategory_ManifestWithNoContentIsAnInheritingLeaf(t *testing.T) {
	templatesPath := t.TempDir()
	writeFixtureManifest(t, filepath.Join(templatesPath, "services"), "name: S\n")

	result, err := WalkCategory(templatesPath, "services", nil)
	if err != nil {
		t.Fatalf("expected the walk to end at an inheriting leaf, got: %v", err)
	}
	if len(result.Steps) != 0 {
		t.Errorf("expected no selector steps, got %+v", result.Steps)
	}
	if len(result.Chain) != 1 {
		t.Errorf("expected a single-node chain, got %d nodes", len(result.Chain))
	}
}

// Section 2.7: four call sites treated every manifest.Load error as "file absent", so a
// malformed registry silently downgraded to raw directory listing and changed what got generated.
func TestDiscoverAxes_MalformedRegistryIsAnErrorNotAFallback(t *testing.T) {
	versionPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(versionPath, "templates"), 0o755); err != nil {
		t.Fatalf("creating dir: %v", err)
	}
	bad := filepath.Join(versionPath, "manifest.yaml")
	if err := os.WriteFile(bad, []byte("values: [ this is : not : valid yaml"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	if _, err := DiscoverAxes(versionPath); err == nil {
		t.Fatal("expected a malformed version registry to be an error, not a silent fallback to directory listing")
	}
}

// The walk now records every node it passes through, so Phase 3b can merge shared content from
// intermediate selector nodes instead of forcing each leaf to duplicate the whole skeleton.
func TestWalkCategory_ReturnsWholeChain(t *testing.T) {
	versionPath := buildFixtureTree(t)
	templatesPath := filepath.Join(versionPath, "templates")

	result, err := WalkCategory(templatesPath, "services", map[string]string{
		"function": "web",
		"protocol": "rest-http",
	})
	if err != nil {
		t.Fatalf("WalkCategory returned error: %v", err)
	}
	if len(result.Chain) != 3 {
		t.Fatalf("expected the chain to hold services -> web -> rest-http (3 nodes), got %d", len(result.Chain))
	}
	if result.Chain[len(result.Chain)-1].Dir != result.LeafDir {
		t.Errorf("expected the last chain node to be the leaf, got %q vs %q",
			result.Chain[len(result.Chain)-1].Dir, result.LeafDir)
	}
}

func TestValidateSegment(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "../../pwned", "a/b", `a\b`, "/abs"} {
		if err := ValidateSegment("<name>", bad); err == nil {
			t.Errorf("expected %q to be rejected as a path segment", bad)
		}
	}
	for _, ok := range []string{"payment-service", "lib_common", "3.2.x"} {
		if err := ValidateSegment("<name>", ok); err != nil {
			t.Errorf("expected %q to be accepted, got: %v", ok, err)
		}
	}
}

// Section 5.19: DescribeTree hard-failed on any subfolder without a manifest.yaml, so
// `scaffold list` would break as soon as 3b/3c added shared asset folders.
func TestDescribeTree_SkipsFoldersWithoutAManifest(t *testing.T) {
	templatesPath := t.TempDir()
	writeFixtureManifest(t, filepath.Join(templatesPath, "services"), "name: S\nselector: function\n")
	writeFixtureManifest(t, filepath.Join(templatesPath, "services", "web"), "name: leaf\nfiles:\n  - path: pom.xml\n")
	if err := os.MkdirAll(filepath.Join(templatesPath, "services", "_shared"), 0o755); err != nil {
		t.Fatalf("creating asset dir: %v", err)
	}

	tree, err := DescribeTree(templatesPath, "services")
	if err != nil {
		t.Fatalf("DescribeTree returned error: %v", err)
	}
	if len(tree.Children) != 1 || tree.Children[0].Value != "web" {
		t.Errorf("expected only 'web' as a selector value, got %+v", tree.Children)
	}
}
