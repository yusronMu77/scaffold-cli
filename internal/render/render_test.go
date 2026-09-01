package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scaffold-engine-go/internal/jig"
)

func boolPtr(b bool) *bool { return &b }

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func pathsOf(files []File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

func contentOf(t *testing.T, files []File, path string) string {
	t.Helper()
	for _, f := range files {
		if f.Path == path {
			return string(f.Content)
		}
	}
	t.Fatalf("no rendered file at %q; got %v", path, pathsOf(files))
	return ""
}

// ---------------------------------------------------------------------------------------------
// Variable resolution
// ---------------------------------------------------------------------------------------------

func TestResolveVariables_Precedence(t *testing.T) {
	m := &jig.Jig{Variables: []jig.Variable{
		{Name: "FromFlag", Flag: "explicit", Default: "ignored"},
		{Name: "FromPositional", FromPositional: "name", Default: "ignored"},
		{Name: "FromDefault", Default: "fallback"},
	}}

	got, err := ResolveVariables([]*jig.Jig{m}, VariableSource{
		Flags:      map[string]string{"explicit": "won"},
		Positional: "my-service",
	})
	if err != nil {
		t.Fatalf("ResolveVariables: %v", err)
	}
	want := map[string]string{
		"FromFlag":       "won",
		"FromPositional": "my-service",
		"FromDefault":    "fallback",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: expected %q, got %q", k, v, got[k])
		}
	}
}

// A variable with no `flag` is filled by the kebab-case of its name.
func TestVariableFlagName(t *testing.T) {
	cases := map[string]string{
		"ProjectName": "project-name",
		"PackageName": "package-name",
		"HTTPPort":    "http-port",
		"Entity":      "entity",
	}
	for name, want := range cases {
		if got := VariableFlagName(jig.Variable{Name: name}); got != want {
			t.Errorf("%s: expected flag %q, got %q", name, want, got)
		}
	}
	if got := VariableFlagName(jig.Variable{Name: "PackageName", Flag: "package"}); got != "package" {
		t.Errorf("an explicit flag must win, got %q", got)
	}
}

// A missing required variable names the flag to pass. It must never block on a prompt: the CLI
// has to stay safe to call from a script.
func TestResolveVariables_MissingRequiredNamesTheFlag(t *testing.T) {
	m := &jig.Jig{Variables: []jig.Variable{
		{Name: "SpringBootVersion", Flag: "spring-boot-version", Required: true, Prompt: "Spring Boot release"},
	}}
	_, err := ResolveVariables([]*jig.Jig{m}, VariableSource{Flags: map[string]string{}})
	if err == nil {
		t.Fatal("expected a missing required variable to error, got nil")
	}
	if !strings.Contains(err.Error(), "--spring-boot-version") {
		t.Errorf("expected the error to name the flag, got: %v", err)
	}
}

// Later sources win, which is how a version overrides a framework-wide default in one line.
func TestResolveVariables_DeeperSourceOverridesDefault(t *testing.T) {
	framework := &jig.Jig{Variables: []jig.Variable{
		{Name: "SpringBootVersion", Required: true},
	}}
	version := &jig.Jig{Variables: []jig.Variable{
		{Name: "SpringBootVersion", Default: "3.2.5"},
	}}
	got, err := ResolveVariables([]*jig.Jig{framework, version}, VariableSource{Flags: map[string]string{}})
	if err != nil {
		t.Fatalf("ResolveVariables: %v", err)
	}
	if got["SpringBootVersion"] != "3.2.5" {
		t.Errorf("expected the deeper level's default to win, got %q", got["SpringBootVersion"])
	}
}

func TestApplyComputed(t *testing.T) {
	ctx := Context{"PackageName": "com.company.app"}
	m := &jig.Jig{Computed: []jig.Computed{
		{Name: "PackagePath", Value: `{{ .PackageName | replace "." "/" }}`},
		{Name: "Derived", Value: `{{ .PackagePath }}/extra`}, // may build on an earlier one
	}}
	if err := ApplyComputed(ctx, []*jig.Jig{m}); err != nil {
		t.Fatalf("ApplyComputed: %v", err)
	}
	if ctx["PackagePath"] != "com/company/app" {
		t.Errorf("expected the dotted package to become a path, got %v", ctx["PackagePath"])
	}
	if ctx["Derived"] != "com/company/app/extra" {
		t.Errorf("expected a computed value to be usable by a later one, got %v", ctx["Derived"])
	}
}

// Maven's identity rule is groupId+artifactId, declared in the data. Two entries that differ only
// in `scope` are the same dependency and must not both survive.
func TestMergeDependencies_UnionsAndDedups(t *testing.T) {
	key := []string{"groupId", "artifactId"}
	a := &jig.Jig{DependencyKey: key, Dependencies: []jig.Dependency{
		{"groupId": "g", "artifactId": "starter"},
		{"groupId": "g", "artifactId": "test", "scope": "test"},
	}}
	b := &jig.Jig{Dependencies: []jig.Dependency{
		{"groupId": "g", "artifactId": "starter", "scope": "compile"}, // same identity, extra field
		{"groupId": "g", "artifactId": "web"},
	}}
	got, err := MergeDependencies([]*jig.Jig{a, b}, Context{})
	if err != nil {
		t.Fatalf("MergeDependencies: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 deduplicated dependencies, got %d: %+v", len(got), got)
	}
	if got[0]["artifactId"] != "starter" || got[2]["artifactId"] != "web" {
		t.Errorf("expected first-seen order to be preserved, got %+v", got)
	}
	if got[1]["scope"] != "test" {
		t.Errorf("expected scope to survive the merge, got %+v", got[1])
	}
}

// Field names are whatever the jig wrote - the engine privileges none. This is what makes a
// second framework possible without touching engine code (fundamental rule #1).
func TestMergeDependencies_FieldNamesAreNotFixed(t *testing.T) {
	npm := &jig.Jig{DependencyKey: []string{"name"}, Dependencies: []jig.Dependency{
		{"name": "react", "version": "^18.0"},
		{"name": "react", "version": "^19.0"}, // same identity, later wins
	}}
	got, err := MergeDependencies([]*jig.Jig{npm}, Context{})
	if err != nil {
		t.Fatalf("MergeDependencies: %v", err)
	}
	if len(got) != 1 || got[0]["name"] != "react" || got[0]["version"] != "^19.0" {
		t.Errorf("expected npm-shaped coordinates to work unchanged, got %+v", got)
	}
}

// A deeper level can override individual fields of a dependency declared higher up, not just add
// new ones.
func TestMergeDependencies_DeeperLevelOverridesFieldsInPlace(t *testing.T) {
	framework := &jig.Jig{
		DependencyKey:    []string{"groupId", "artifactId"},
		DependencyFields: []string{"groupId", "artifactId", "version", "scope"},
		Dependencies: []jig.Dependency{
			{"groupId": "g", "artifactId": "starter", "version": "1.0"},
			{"groupId": "g", "artifactId": "other"},
		},
	}
	// A leaf that wants the same coordinate, provided rather than compile.
	leaf := &jig.Jig{Dependencies: []jig.Dependency{
		{"groupId": "g", "artifactId": "starter", "scope": "provided"},
	}}

	got, err := MergeDependencies([]*jig.Jig{framework, leaf}, Context{})
	if err != nil {
		t.Fatalf("MergeDependencies: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected the coordinate to be merged, not duplicated, got %+v", got)
	}
	if got[0]["scope"] != "provided" {
		t.Errorf("the deeper level must be able to set a field, got %+v", got[0])
	}
	if got[0]["version"] != "1.0" {
		t.Errorf("a field the deeper level did not mention must survive, got %+v", got[0])
	}
	if got[0]["artifactId"] != "starter" {
		t.Errorf("order comes from first sighting, so the list stays stable, got %+v", got)
	}
}

// Every dependency entry must expose the same key set, since templates run with missingkey=error.
func TestMergeDependencies_NormalisesKeySet(t *testing.T) {
	m := &jig.Jig{DependencyKey: []string{"groupId", "artifactId"},
		Dependencies: []jig.Dependency{
			{"groupId": "g", "artifactId": "a", "scope": "test"},
			{"groupId": "g", "artifactId": "b"}, // no scope
		}}
	got, err := MergeDependencies([]*jig.Jig{m}, Context{})
	if err != nil {
		t.Fatalf("MergeDependencies: %v", err)
	}
	if v, ok := got[1]["scope"]; !ok || v != "" {
		t.Errorf("expected the missing key to be filled with an empty string, got %+v", got[1])
	}
}

// Jig field values are templates too, so a dependency can reference a variable.
func TestMergeDependencies_RendersPlaceholders(t *testing.T) {
	m := &jig.Jig{Dependencies: []jig.Dependency{
		{"groupId": "{{ .GroupId }}", "artifactId": "lib-{{ .Feature }}", "version": "{{ .LibVersion }}"},
	}}
	got, err := MergeDependencies([]*jig.Jig{m},
		Context{"GroupId": "com.acme", "Feature": "audit", "LibVersion": "1.2.3"})
	if err != nil {
		t.Fatalf("MergeDependencies: %v", err)
	}
	if got[0]["groupId"] != "com.acme" || got[0]["artifactId"] != "lib-audit" || got[0]["version"] != "1.2.3" {
		t.Errorf("expected every field rendered, got %+v", got[0])
	}
}

// `type` and `scope` pass through untouched; the engine attaches no meaning to them.
func TestMergeDependencies_PassesThroughTypeAndScope(t *testing.T) {
	m := &jig.Jig{Dependencies: []jig.Dependency{
		{"groupId": "g", "artifactId": "bom", "version": "1.0", "type": "pom", "scope": "import"},
	}}
	got, err := MergeDependencies([]*jig.Jig{m}, Context{})
	if err != nil {
		t.Fatalf("MergeDependencies: %v", err)
	}
	if got[0]["type"] != "pom" || got[0]["scope"] != "import" {
		t.Errorf("expected type/scope to survive verbatim, got %+v", got[0])
	}
}

func TestRenderStrings(t *testing.T) {
	got, err := RenderStrings("exclude pattern",
		[]string{"src/test/**/{{ .Entity }}Tests.java", "Dockerfile"},
		Context{"Entity": "Payment"})
	if err != nil {
		t.Fatalf("RenderStrings: %v", err)
	}
	if got[0] != "src/test/**/PaymentTests.java" || got[1] != "Dockerfile" {
		t.Errorf("unexpected result: %v", got)
	}
}

// ---------------------------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------------------------

func TestRenderSource_RendersContentAndPathsAndSkipsJig(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, jig.FileName, "name: leaf\n")
	writeFile(t, dir, "src/{{ .PackagePath }}/App.java", "package {{ .PackageName }};\n")

	files, _, err := RenderSource(Source{Dir: dir, Manifest: &jig.Jig{}, Label: "leaf"},
		Context{"PackagePath": "com/acme", "PackageName": "com.acme"})
	if err != nil {
		t.Fatalf("RenderSource: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("jig.yaml must never be emitted; got %v", pathsOf(files))
	}
	if files[0].Path != "src/com/acme/App.java" {
		t.Errorf("expected the path placeholder to expand, got %q", files[0].Path)
	}
	if !strings.Contains(string(files[0].Content), "package com.acme;") {
		t.Errorf("expected the content to be rendered, got %q", files[0].Content)
	}
}

// A subfolder with its own jig.yaml belongs to another source, not to this one.
func TestRenderSource_SkipsNestedDiscoveryNodes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, jig.FileName, "name: parent\n")
	writeFile(t, dir, "shared.txt", "shared\n")
	writeFile(t, dir, "child/"+jig.FileName, "name: child\n")
	writeFile(t, dir, "child/owned.txt", "belongs to child\n")

	files, _, err := RenderSource(Source{Dir: dir, Manifest: &jig.Jig{}, Label: "parent"}, Context{})
	if err != nil {
		t.Fatalf("RenderSource: %v", err)
	}
	if got := pathsOf(files); len(got) != 1 || got[0] != "shared.txt" {
		t.Errorf("expected only the parent's own file, got %v", got)
	}
}

func TestRenderSource_FileOverrides(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, jig.FileName, "name: leaf\n")
	writeFile(t, dir, "verbatim.txt", "{{ this would not parse }}\n")
	writeFile(t, dir, "gitignore.tpl", "target/\n")
	writeFile(t, dir, "Dockerfile", "FROM eclipse-temurin\n")

	m := &jig.Jig{Files: []jig.FileEntry{
		{Path: "verbatim.txt", Template: boolPtr(false)},
		{Path: "gitignore.tpl", Target: ".gitignore"},
		{Path: "Dockerfile", Condition: "WithDocker"},
	}}

	files, _, err := RenderSource(Source{Dir: dir, Manifest: m, Label: "leaf"}, Context{"WithDocker": "false"})
	if err != nil {
		t.Fatalf("RenderSource: %v", err)
	}
	got := pathsOf(files)
	if len(got) != 2 {
		t.Fatalf("expected the conditional file to be skipped, got %v", got)
	}
	if contentOf(t, files, "verbatim.txt") != "{{ this would not parse }}\n" {
		t.Error("template:false must copy bytes verbatim rather than parsing them")
	}
	if contentOf(t, files, ".gitignore") != "target/\n" {
		t.Error("rename must change the output name")
	}

	files, _, err = RenderSource(Source{Dir: dir, Manifest: m, Label: "leaf"}, Context{"WithDocker": "true"})
	if err != nil {
		t.Fatalf("RenderSource: %v", err)
	}
	if len(files) != 3 {
		t.Errorf("expected the conditional file when its variable is truthy, got %v", pathsOf(files))
	}
}

// An entry declaring insert_after produces an Insert, not a File - it's a snippet to splice into
// an existing file, not output of its own.
func TestRenderSource_InsertEntryProducesInsertNotFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, jig.FileName, "name: leaf\n")
	writeFile(t, dir, "route.snippet", "{{ .RouteName }}();\n")

	m := &jig.Jig{Files: []jig.FileEntry{
		{Path: "route.snippet", Target: "Controller.java", InsertAfter: "// @scaffold:routes"},
	}}

	files, inserts, err := RenderSource(Source{Dir: dir, Manifest: m, Label: "leaf"},
		Context{"RouteName": "newRoute"})
	if err != nil {
		t.Fatalf("RenderSource: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected the insert entry to produce no File, got %v", pathsOf(files))
	}
	if len(inserts) != 1 {
		t.Fatalf("expected exactly one Insert, got %+v", inserts)
	}
	ins := inserts[0]
	if ins.Path != "Controller.java" || ins.Anchor != "// @scaffold:routes" || !ins.After {
		t.Errorf("unexpected insert %+v", ins)
	}
	if string(ins.Content) != "newRoute();\n" {
		t.Errorf("expected the snippet to be rendered against the context, got %q", ins.Content)
	}
}

// An override naming a file that isn't there is a template-authoring typo, not something to
// quietly ignore.
func TestRenderSource_RejectsOverrideForMissingFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, jig.FileName, "name: leaf\n")
	m := &jig.Jig{Files: []jig.FileEntry{{Path: "not-there.txt"}}}

	_, _, err := RenderSource(Source{Dir: dir, Manifest: m, Label: "leaf"}, Context{})
	if err == nil || !strings.Contains(err.Error(), "not-there.txt") {
		t.Fatalf("expected an error naming the missing file, got: %v", err)
	}
}

// A typo'd placeholder must fail rather than render "<no value>" into the output.
func TestRenderSource_UnknownVariableIsAnError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, jig.FileName, "name: leaf\n")
	writeFile(t, dir, "a.txt", "{{ .Typoed }}\n")

	_, _, err := RenderSource(Source{Dir: dir, Manifest: &jig.Jig{}, Label: "leaf"}, Context{})
	if err == nil {
		t.Fatal("expected an unknown placeholder to fail the render, got nil")
	}
}

// Fundamental rule #7 applies to template-declared paths too, not only to <name>.
func TestRenderSource_RejectsEscapingOutputPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, jig.FileName, "name: leaf\n")
	writeFile(t, dir, "a.txt", "x\n")
	m := &jig.Jig{Files: []jig.FileEntry{{Path: "a.txt", Target: "../../escaped.txt"}}}

	_, _, err := RenderSource(Source{Dir: dir, Manifest: m, Label: "leaf"}, Context{})
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected an escape to be rejected, got: %v", err)
	}
}

// ---------------------------------------------------------------------------------------------
// Merge
// ---------------------------------------------------------------------------------------------

func TestMerge_LaterSourceWinsOnCollision(t *testing.T) {
	base := []File{{Path: "a.txt", Content: []byte("base"), Source: "base"}}
	overlay := []File{{Path: "a.txt", Content: []byte("overlay"), Source: "overlay"}}

	got, err := Merge([][]File{base, overlay})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(got) != 1 || string(got[0].Content) != "overlay" {
		t.Errorf("expected the later source to win, got %+v", got)
	}
}

func TestMerge_DeepMergesYAML(t *testing.T) {
	base := []File{{Path: "app.yml", Merge: true, Source: "base",
		Content: []byte("server:\n  port: 8080\nlist:\n  - a\nkeep: yes\ndrop: value\n")}}
	overlay := []File{{Path: "app.yml", Merge: true, Source: "overlay",
		Content: []byte("server:\n  ssl: true\nlist:\n  - b\ndrop: null\n")}}

	got, err := Merge([][]File{base, overlay})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	out := string(got[0].Content)

	// Maps merge recursively; "yes" stays a string since yaml.v3 follows YAML 1.2.
	for _, want := range []string{"port: 8080", "ssl: true", `keep: "yes"`} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the merged YAML, got:\n%s", want, out)
		}
	}
	// Arrays are replaced wholesale, not appended.
	if strings.Contains(out, "- a") {
		t.Errorf("arrays must be replaced, not appended:\n%s", out)
	}
	// An explicit null deletes the key.
	if strings.Contains(out, "drop:") {
		t.Errorf("an explicit null must delete the key:\n%s", out)
	}
}

// ---------------------------------------------------------------------------------------------
// Inherited layout rules
// ---------------------------------------------------------------------------------------------

// A template can drop files into a plain `java/` folder and declare nothing; a layout rule
// declared higher up puts them in the right place.
func TestLayout_RewritesPrefixWithNoJigEntries(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, jig.FileName, "name: leaf\n")
	writeFile(t, dir, "java/Application.java", "package {{ .PackageName }};\n")
	writeFile(t, dir, "java/controller/{{ .EntityName }}Controller.java", "// controller\n")
	writeFile(t, dir, "resources/application.yml", "name: x\n")

	rules, err := CollectLayout([]*jig.Jig{{Layout: []jig.LayoutRule{
		{From: "java", To: "src/main/java/{{ .PackagePath }}"},
		{From: "resources", To: "src/main/resources"},
	}}}, Context{"PackagePath": "com/acme"})
	if err != nil {
		t.Fatalf("CollectLayout: %v", err)
	}

	files, _, err := RenderSource(
		Source{Dir: dir, Manifest: &jig.Jig{}, Label: "leaf", Layout: rules},
		Context{"PackagePath": "com/acme", "PackageName": "com.acme", "EntityName": "Payment"})
	if err != nil {
		t.Fatalf("RenderSource: %v", err)
	}

	want := map[string]bool{
		"src/main/java/com/acme/Application.java":                  true,
		"src/main/java/com/acme/controller/PaymentController.java": true,
		"src/main/resources/application.yml":                       true,
	}
	for _, p := range pathsOf(files) {
		if !want[p] {
			t.Errorf("unexpected output path %q", p)
		}
		delete(want, p)
	}
	for p := range want {
		t.Errorf("missing expected output path %q", p)
	}
}

// A rule declared deeper in the chain replaces one with the same `from`, so a template with an
// unusual layout can opt out without affecting its siblings.
func TestCollectLayout_DeeperRuleOverridesSameFrom(t *testing.T) {
	shallow := &jig.Jig{Layout: []jig.LayoutRule{{From: "java", To: "src/main/java"}}}
	deep := &jig.Jig{Layout: []jig.LayoutRule{{From: "java", To: "app/src"}}}

	rules, err := CollectLayout([]*jig.Jig{shallow, deep}, Context{})
	if err != nil {
		t.Fatalf("CollectLayout: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected the rules to be deduplicated by `from`, got %+v", rules)
	}
	if rules[0].To != "app/src" {
		t.Errorf("expected the deeper rule to win, got %q", rules[0].To)
	}
}

// The most specific rule must win, otherwise "java" would swallow paths meant for "java/gen".
func TestCollectLayout_LongestPrefixWins(t *testing.T) {
	m := &jig.Jig{Layout: []jig.LayoutRule{
		{From: "java", To: "src/main/java"},
		{From: "java/gen", To: "target/generated-sources"},
	}}
	rules, err := CollectLayout([]*jig.Jig{m}, Context{})
	if err != nil {
		t.Fatalf("CollectLayout: %v", err)
	}
	if got := applyLayout("java/gen/Stub.java", rules); got != "target/generated-sources/Stub.java" {
		t.Errorf("expected the longer prefix to match, got %q", got)
	}
	if got := applyLayout("java/App.java", rules); got != "src/main/java/App.java" {
		t.Errorf("expected the shorter prefix to still apply, got %q", got)
	}
}

// A path matching no rule is left alone - layout rules are opt-in per prefix.
func TestApplyLayout_LeavesUnmatchedPathsAlone(t *testing.T) {
	rules := []ResolvedLayout{{From: "java", To: "src/main/java"}}
	if got := applyLayout("pom.xml", rules); got != "pom.xml" {
		t.Errorf("expected an unmatched path to pass through, got %q", got)
	}
}

// An explicit per-file `target` beats the inherited layout rule.
func TestRenderSource_FileTargetBeatsLayoutRule(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, jig.FileName, "name: leaf\n")
	writeFile(t, dir, "java/special.txt", "x\n")

	m := &jig.Jig{Files: []jig.FileEntry{
		{Path: "java/special.txt", Target: "docs/special.txt"},
	}}
	files, _, err := RenderSource(
		Source{Dir: dir, Manifest: m, Label: "leaf",
			Layout: []ResolvedLayout{{From: "java", To: "src/main/java"}}},
		Context{})
	if err != nil {
		t.Fatalf("RenderSource: %v", err)
	}
	if files[0].Path != "docs/special.txt" {
		t.Errorf("expected the explicit target to win over the layout rule, got %q", files[0].Path)
	}
}

// `target` may name a directory, mapping a whole subtree in one entry.
func TestRenderSource_DirectoryTargetMapsSubtree(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, jig.FileName, "name: leaf\n")
	writeFile(t, dir, "java/a/b/Deep.java", "x\n")

	files, _, err := RenderSource(
		Source{Dir: dir, Manifest: &jig.Jig{}, Label: "leaf",
			Layout: []ResolvedLayout{{From: "java", To: "src/main/java/com/acme"}}},
		Context{})
	if err != nil {
		t.Fatalf("RenderSource: %v", err)
	}
	if files[0].Path != "src/main/java/com/acme/a/b/Deep.java" {
		t.Errorf("expected the subtree structure to be preserved under the new prefix, got %q", files[0].Path)
	}
}

// ---------------------------------------------------------------------------------------------
// Exclude
// ---------------------------------------------------------------------------------------------

func TestExclude_DropsMatchingFiles(t *testing.T) {
	files := []File{
		{Path: "pom.xml"},
		{Path: "Dockerfile"},
		{Path: "src/test/java/com/acme/ApplicationTests.java"},
	}
	got, err := Exclude(files, []string{"Dockerfile"})
	if err != nil {
		t.Fatalf("Exclude: %v", err)
	}
	for _, f := range got {
		if f.Path == "Dockerfile" {
			t.Error("expected Dockerfile to be dropped")
		}
	}
	if len(got) != 2 {
		t.Errorf("expected 2 files to survive, got %v", pathsOf(got))
	}
}

// `**` must span several segments, since the package depth isn't known when the pattern is written.
func TestExclude_DoubleStarSpansSegments(t *testing.T) {
	files := []File{
		{Path: "src/test/java/com/company/deep/pkg/ApplicationTests.java"},
		{Path: "src/main/java/com/company/deep/pkg/Application.java"},
	}
	got, err := Exclude(files, []string{"src/test/java/**/ApplicationTests.java"})
	if err != nil {
		t.Fatalf("Exclude: %v", err)
	}
	if len(got) != 1 || got[0].Path != "src/main/java/com/company/deep/pkg/Application.java" {
		t.Errorf("expected only the main source to survive, got %v", pathsOf(got))
	}
}

// `a/**/b` must also match `a/b`, with nothing in between.
func TestExclude_DoubleStarMatchesZeroSegments(t *testing.T) {
	got, err := Exclude([]File{{Path: "src/test/App.java"}}, []string{"src/**/App.java"})
	if err != nil {
		t.Fatalf("Exclude: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected the file to be dropped, got %v", pathsOf(got))
	}
}

// A pattern matching nothing is almost always a stale leftover from a rename, so it must error
// rather than be silently ignored (fundamental rule #8).
func TestExclude_StalePatternIsAnError(t *testing.T) {
	_, err := Exclude([]File{{Path: "pom.xml"}}, []string{"src/main/java/**/Gone.java"})
	if err == nil {
		t.Fatal("expected a pattern that matches nothing to error, got nil")
	}
	if !strings.Contains(err.Error(), "Gone.java") {
		t.Errorf("expected the error to name the stale pattern, got: %v", err)
	}
}

func TestExclude_NoPatternsIsANoop(t *testing.T) {
	files := []File{{Path: "pom.xml"}}
	got, err := Exclude(files, nil)
	if err != nil {
		t.Fatalf("Exclude: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected the tree untouched, got %v", pathsOf(got))
	}
}

// The merge rules are format-independent; only the codec differs between YAML and JSON.
func TestMerge_DeepMergesJSON(t *testing.T) {
	base := []File{{Path: "package.json", Merge: true, Source: "base",
		Content: []byte(`{"name":"app","scripts":{"build":"tsc"},"keywords":["a"],"drop":"x"}`)}}
	overlay := []File{{Path: "package.json", Merge: true, Source: "overlay",
		Content: []byte(`{"scripts":{"test":"vitest"},"keywords":["b"],"drop":null}`)}}

	got, err := Merge([][]File{base, overlay})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	out := string(got[0].Content)

	for _, want := range []string{`"name": "app"`, `"build": "tsc"`, `"test": "vitest"`} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the merged JSON, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"a"`) {
		t.Errorf("arrays must be replaced, not appended:\n%s", out)
	}
	if strings.Contains(out, `"drop"`) {
		t.Errorf("an explicit null must delete the key:\n%s", out)
	}
}

// A merge requested on a format the engine cannot parse must fail loudly, not silently fall back
// to whole-file replacement.
func TestMerge_UnknownExtensionIsAnError(t *testing.T) {
	base := []File{{Path: "config.toml", Merge: true, Source: "base", Content: []byte("a = 1")}}
	overlay := []File{{Path: "config.toml", Merge: true, Source: "overlay", Content: []byte("b = 2")}}

	_, err := Merge([][]File{base, overlay})
	if err == nil {
		t.Fatal("expected an unsupported merge format to error, got nil")
	}
	if !strings.Contains(err.Error(), ".toml") {
		t.Errorf("expected the error to name the extension, got: %v", err)
	}
}

// Go's json package HTML-escapes <, >, and & by default, which would mangle values like npm
// version ranges; the merge must disable that.
func TestMerge_JSONDoesNotHTMLEscape(t *testing.T) {
	base := []File{{Path: "package.json", Merge: true, Source: "base",
		Content: []byte(`{"engines":{"node":">=20.11.0"}}`)}}
	overlay := []File{{Path: "package.json", Merge: true, Source: "overlay",
		Content: []byte(`{"name":"a & b"}`)}}

	got, err := Merge([][]File{base, overlay})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	out := string(got[0].Content)
	if strings.Contains(out, `\u003e`) || strings.Contains(out, `\u0026`) {
		t.Errorf("expected no HTML escaping, got:\n%s", out)
	}
	if !strings.Contains(out, `">=20.11.0"`) || !strings.Contains(out, `"a & b"`) {
		t.Errorf("expected the literal characters preserved, got:\n%s", out)
	}
}
