package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scaffold-engine-go/internal/manifest"
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
// Variable resolution (PRD Section 7.4)
// ---------------------------------------------------------------------------------------------

func TestResolveVariables_Precedence(t *testing.T) {
	m := &manifest.Manifest{Variables: []manifest.Variable{
		{Name: "FromFlag", Flag: "explicit", Default: "ignored"},
		{Name: "FromPositional", FromPositional: "name", Default: "ignored"},
		{Name: "FromDefault", Default: "fallback"},
	}}

	got, err := ResolveVariables([]*manifest.Manifest{m}, VariableSource{
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
		if got := VariableFlagName(manifest.Variable{Name: name}); got != want {
			t.Errorf("%s: expected flag %q, got %q", name, want, got)
		}
	}
	if got := VariableFlagName(manifest.Variable{Name: "PackageName", Flag: "package"}); got != "package" {
		t.Errorf("an explicit flag must win, got %q", got)
	}
}

// A missing required variable names the flag to pass. It must never block on a prompt: the CLI
// has to stay safe to call from a script.
func TestResolveVariables_MissingRequiredNamesTheFlag(t *testing.T) {
	m := &manifest.Manifest{Variables: []manifest.Variable{
		{Name: "SpringBootVersion", Flag: "spring-boot-version", Required: true, Prompt: "Spring Boot release"},
	}}
	_, err := ResolveVariables([]*manifest.Manifest{m}, VariableSource{Flags: map[string]string{}})
	if err == nil {
		t.Fatal("expected a missing required variable to error, got nil")
	}
	if !strings.Contains(err.Error(), "--spring-boot-version") {
		t.Errorf("expected the error to name the flag, got: %v", err)
	}
}

// Later sources win, which is how a version overrides a framework-wide default in one line.
func TestResolveVariables_DeeperSourceOverridesDefault(t *testing.T) {
	framework := &manifest.Manifest{Variables: []manifest.Variable{
		{Name: "SpringBootVersion", Required: true},
	}}
	version := &manifest.Manifest{Variables: []manifest.Variable{
		{Name: "SpringBootVersion", Default: "3.2.5"},
	}}
	got, err := ResolveVariables([]*manifest.Manifest{framework, version}, VariableSource{Flags: map[string]string{}})
	if err != nil {
		t.Fatalf("ResolveVariables: %v", err)
	}
	if got["SpringBootVersion"] != "3.2.5" {
		t.Errorf("expected the deeper level's default to win, got %q", got["SpringBootVersion"])
	}
}

func TestApplyComputed(t *testing.T) {
	ctx := Context{"PackageName": "com.company.app"}
	m := &manifest.Manifest{Computed: []manifest.Computed{
		{Name: "PackagePath", Value: `{{ .PackageName | replace "." "/" }}`},
		{Name: "Derived", Value: `{{ .PackagePath }}/extra`}, // may build on an earlier one
	}}
	if err := ApplyComputed(ctx, []*manifest.Manifest{m}); err != nil {
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
	a := &manifest.Manifest{DependencyKey: key, Dependencies: []manifest.Dependency{
		{"groupId": "g", "artifactId": "starter"},
		{"groupId": "g", "artifactId": "test", "scope": "test"},
	}}
	b := &manifest.Manifest{Dependencies: []manifest.Dependency{
		{"groupId": "g", "artifactId": "starter", "scope": "compile"}, // same identity, extra field
		{"groupId": "g", "artifactId": "web"},
	}}
	got, err := MergeDependencies([]*manifest.Manifest{a, b}, Context{})
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

// Field names are whatever the manifest wrote - the engine privileges none. This is what makes a
// second framework possible without touching engine code (fundamental rule #1).
func TestMergeDependencies_FieldNamesAreNotFixed(t *testing.T) {
	npm := &manifest.Manifest{DependencyKey: []string{"name"}, Dependencies: []manifest.Dependency{
		{"name": "react", "version": "^18.0"},
		{"name": "react", "version": "^19.0"}, // same identity, first wins
	}}
	got, err := MergeDependencies([]*manifest.Manifest{npm}, Context{})
	if err != nil {
		t.Fatalf("MergeDependencies: %v", err)
	}
	if len(got) != 1 || got[0]["name"] != "react" || got[0]["version"] != "^18.0" {
		t.Errorf("expected npm-shaped coordinates to work unchanged, got %+v", got)
	}
}

// Templates run with missingkey=error, so every entry must expose the same key set - otherwise
// `{{ .scope }}` would be a hard error on the entries that happen not to set it.
func TestMergeDependencies_NormalisesKeySet(t *testing.T) {
	m := &manifest.Manifest{DependencyKey: []string{"groupId", "artifactId"},
		Dependencies: []manifest.Dependency{
			{"groupId": "g", "artifactId": "a", "scope": "test"},
			{"groupId": "g", "artifactId": "b"}, // no scope
		}}
	got, err := MergeDependencies([]*manifest.Manifest{m}, Context{})
	if err != nil {
		t.Fatalf("MergeDependencies: %v", err)
	}
	if v, ok := got[1]["scope"]; !ok || v != "" {
		t.Errorf("expected the missing key to be filled with an empty string, got %+v", got[1])
	}
}

// Manifest field values are templates too, so a dependency can reference a variable. Before this,
// a placeholder in `groupId:` survived verbatim into the build file and failed much later, in
// Maven, with a message that named neither the manifest nor the variable.
func TestMergeDependencies_RendersPlaceholders(t *testing.T) {
	m := &manifest.Manifest{Dependencies: []manifest.Dependency{
		{"groupId": "{{ .GroupId }}", "artifactId": "lib-{{ .Feature }}", "version": "{{ .LibVersion }}"},
	}}
	got, err := MergeDependencies([]*manifest.Manifest{m},
		Context{"GroupId": "com.acme", "Feature": "audit", "LibVersion": "1.2.3"})
	if err != nil {
		t.Fatalf("MergeDependencies: %v", err)
	}
	if got[0]["groupId"] != "com.acme" || got[0]["artifactId"] != "lib-audit" || got[0]["version"] != "1.2.3" {
		t.Errorf("expected every field rendered, got %+v", got[0])
	}
}

// `type` and `scope` are passed through untouched: the engine attaches no meaning to them, which
// is what lets a BOM import be an ordinary entry and the template decide where it belongs.
func TestMergeDependencies_PassesThroughTypeAndScope(t *testing.T) {
	m := &manifest.Manifest{Dependencies: []manifest.Dependency{
		{"groupId": "g", "artifactId": "bom", "version": "1.0", "type": "pom", "scope": "import"},
	}}
	got, err := MergeDependencies([]*manifest.Manifest{m}, Context{})
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
// Rendering (PRD Section 7.3)
// ---------------------------------------------------------------------------------------------

func TestRenderSource_RendersContentAndPathsAndSkipsManifest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "manifest.yaml", "name: leaf\n")
	writeFile(t, dir, "src/{{ .PackagePath }}/App.java", "package {{ .PackageName }};\n")

	files, err := RenderSource(Source{Dir: dir, Manifest: &manifest.Manifest{}, Label: "leaf"},
		Context{"PackagePath": "com/acme", "PackageName": "com.acme"})
	if err != nil {
		t.Fatalf("RenderSource: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("manifest.yaml must never be emitted; got %v", pathsOf(files))
	}
	if files[0].Path != "src/com/acme/App.java" {
		t.Errorf("expected the path placeholder to expand, got %q", files[0].Path)
	}
	if !strings.Contains(string(files[0].Content), "package com.acme;") {
		t.Errorf("expected the content to be rendered, got %q", files[0].Content)
	}
}

// A subfolder with its own manifest.yaml belongs to another source - this is what lets a level
// hold shared files while its registered children sit underneath it.
func TestRenderSource_SkipsNestedDiscoveryNodes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "manifest.yaml", "name: parent\n")
	writeFile(t, dir, "shared.txt", "shared\n")
	writeFile(t, dir, "child/manifest.yaml", "name: child\n")
	writeFile(t, dir, "child/owned.txt", "belongs to child\n")

	files, err := RenderSource(Source{Dir: dir, Manifest: &manifest.Manifest{}, Label: "parent"}, Context{})
	if err != nil {
		t.Fatalf("RenderSource: %v", err)
	}
	if got := pathsOf(files); len(got) != 1 || got[0] != "shared.txt" {
		t.Errorf("expected only the parent's own file, got %v", got)
	}
}

func TestRenderSource_FileOverrides(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "manifest.yaml", "name: leaf\n")
	writeFile(t, dir, "verbatim.txt", "{{ this would not parse }}\n")
	writeFile(t, dir, "gitignore.tmpl", "target/\n")
	writeFile(t, dir, "Dockerfile", "FROM eclipse-temurin\n")

	m := &manifest.Manifest{Files: []manifest.FileEntry{
		{Path: "verbatim.txt", Template: boolPtr(false)},
		{Path: "gitignore.tmpl", Target: ".gitignore"},
		{Path: "Dockerfile", Condition: "WithDocker"},
	}}

	files, err := RenderSource(Source{Dir: dir, Manifest: m, Label: "leaf"}, Context{"WithDocker": "false"})
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

	files, err = RenderSource(Source{Dir: dir, Manifest: m, Label: "leaf"}, Context{"WithDocker": "true"})
	if err != nil {
		t.Fatalf("RenderSource: %v", err)
	}
	if len(files) != 3 {
		t.Errorf("expected the conditional file when its variable is truthy, got %v", pathsOf(files))
	}
}

// An override naming a file that isn't there is a template-authoring typo, not something to
// quietly ignore.
func TestRenderSource_RejectsOverrideForMissingFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "manifest.yaml", "name: leaf\n")
	m := &manifest.Manifest{Files: []manifest.FileEntry{{Path: "not-there.txt"}}}

	_, err := RenderSource(Source{Dir: dir, Manifest: m, Label: "leaf"}, Context{})
	if err == nil || !strings.Contains(err.Error(), "not-there.txt") {
		t.Fatalf("expected an error naming the missing file, got: %v", err)
	}
}

// A typo'd placeholder must fail rather than render "<no value>" into the output.
func TestRenderSource_UnknownVariableIsAnError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "manifest.yaml", "name: leaf\n")
	writeFile(t, dir, "a.txt", "{{ .Typoed }}\n")

	_, err := RenderSource(Source{Dir: dir, Manifest: &manifest.Manifest{}, Label: "leaf"}, Context{})
	if err == nil {
		t.Fatal("expected an unknown placeholder to fail the render, got nil")
	}
}

// Fundamental rule #7 applies to template-declared paths too, not only to <name>.
func TestRenderSource_RejectsEscapingOutputPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "manifest.yaml", "name: leaf\n")
	writeFile(t, dir, "a.txt", "x\n")
	m := &manifest.Manifest{Files: []manifest.FileEntry{{Path: "a.txt", Target: "../../escaped.txt"}}}

	_, err := RenderSource(Source{Dir: dir, Manifest: m, Label: "leaf"}, Context{})
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected an escape to be rejected, got: %v", err)
	}
}

// ---------------------------------------------------------------------------------------------
// Merge (PRD Section 6 step 6)
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

	// Maps merge recursively (note "yes" stays a string - yaml.v3 follows YAML 1.2, where only
	// true/false are booleans)...
	for _, want := range []string{"port: 8080", "ssl: true", `keep: "yes"`} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the merged YAML, got:\n%s", want, out)
		}
	}
	// ...arrays are replaced wholesale, not appended...
	if strings.Contains(out, "- a") {
		t.Errorf("arrays must be replaced, not appended:\n%s", out)
	}
	// ...and an explicit null deletes the key.
	if strings.Contains(out, "drop:") {
		t.Errorf("an explicit null must delete the key:\n%s", out)
	}
}

// ---------------------------------------------------------------------------------------------
// Inherited layout rules (PRD Section 7.3)
// ---------------------------------------------------------------------------------------------

// The point of layout rules: a template drops files into a plain `java/` folder and declares
// nothing, and the rule declared far above it puts them in the right place. This is what keeps
// writing a new template cheap.
func TestLayout_RewritesPrefixWithNoManifestEntries(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "manifest.yaml", "name: leaf\n")
	writeFile(t, dir, "java/Application.java", "package {{ .PackageName }};\n")
	writeFile(t, dir, "java/controller/{{ .EntityName }}Controller.java", "// controller\n")
	writeFile(t, dir, "resources/application.yml", "name: x\n")

	rules, err := CollectLayout([]*manifest.Manifest{{Layout: []manifest.LayoutRule{
		{From: "java", To: "src/main/java/{{ .PackagePath }}"},
		{From: "resources", To: "src/main/resources"},
	}}}, Context{"PackagePath": "com/acme"})
	if err != nil {
		t.Fatalf("CollectLayout: %v", err)
	}

	files, err := RenderSource(
		Source{Dir: dir, Manifest: &manifest.Manifest{}, Label: "leaf", Layout: rules},
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
	shallow := &manifest.Manifest{Layout: []manifest.LayoutRule{{From: "java", To: "src/main/java"}}}
	deep := &manifest.Manifest{Layout: []manifest.LayoutRule{{From: "java", To: "app/src"}}}

	rules, err := CollectLayout([]*manifest.Manifest{shallow, deep}, Context{})
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
	m := &manifest.Manifest{Layout: []manifest.LayoutRule{
		{From: "java", To: "src/main/java"},
		{From: "java/gen", To: "target/generated-sources"},
	}}
	rules, err := CollectLayout([]*manifest.Manifest{m}, Context{})
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

// An explicit per-file `target` beats the inherited rule, which is what makes the escape hatch an
// escape hatch.
func TestRenderSource_FileTargetBeatsLayoutRule(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "manifest.yaml", "name: leaf\n")
	writeFile(t, dir, "java/special.txt", "x\n")

	m := &manifest.Manifest{Files: []manifest.FileEntry{
		{Path: "java/special.txt", Target: "docs/special.txt"},
	}}
	files, err := RenderSource(
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
	writeFile(t, dir, "manifest.yaml", "name: leaf\n")
	writeFile(t, dir, "java/a/b/Deep.java", "x\n")

	files, err := RenderSource(
		Source{Dir: dir, Manifest: &manifest.Manifest{}, Label: "leaf",
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
// Exclude - the third inheritance operation: remove (PRD Section 7.3)
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

// `**` has to span several segments, because the package layout - and therefore the depth - is not
// known when the pattern is written.
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

// A pattern matching nothing is almost always a leftover from a rename. Silently ignoring it would
// leave the author believing a file is still being dropped when it is not (fundamental rule #8).
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

// The merge rules are format-independent; only the codec differs. Proving it with JSON matters
// because a Node framework needs exactly this for package.json, and a field named `merge_yaml`
// would have announced that it could not.
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

// A merge asked for on a format the engine cannot parse must fail loudly. Falling back to
// whole-file replacement would quietly do something other than what the manifest requested.
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
