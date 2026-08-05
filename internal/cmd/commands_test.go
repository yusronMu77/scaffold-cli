package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// These tests drive the create and list commands end to end against a fixture scaffolding-code tree.

// writeFile writes content at dir/name, creating dir.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s/%s: %v", dir, name, err)
	}
}

// buildScaffoldingCode creates a scaffolding-code tree exercising name/path/flag identities that
// differ (the templates axis lives in tmpl/, the overlay axis lives in patterns/ and is driven by
// --style) and an inheritance chain, with pom.xml contributed at the framework level and refined
// further down.
func buildScaffoldingCode(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeFile(t, root, "jig.yaml", "name: root\nvalues:\n  - name: fw\n    description: Test scaffold\n")

	// Scaffold level: the shared file plus the variables every version inherits.
	fw := filepath.Join(root, "fw")
	writeFile(t, fw, "jig.yaml", `
name: fw
variables:
  - name: ArtifactId
    from_positional: name
  - name: PackageName
    flag: package
    default: com.example
computed:
  - name: PackagePath
    value: '{{ .PackageName | replace "." "/" }}'
values:
  - name: "1.0"
    default: true
  - name: "2.0"
    inherits: "1.0"
`)
	// 2.0 inherits 1.0 and overrides nothing but a variable - the shape a real "older/newer line"
	// takes when only values differ.
	writeFile(t, filepath.Join(fw, "2.0"), "jig.yaml",
		"name: v2\nvariables:\n  - name: PackageName\n    default: com.v2\n")
	writeFile(t, fw, "pom.xml", "<artifactId>{{ .ArtifactId }}</artifactId>\n{{- range .Dependencies }}\n<dep>{{ .artifactId }}</dep>\n{{- end }}\n")

	v := filepath.Join(fw, "1.0")
	writeFile(t, v, "jig.yaml",
		"name: v\nvalues:\n  - name: templates\n    path: tmpl\n  - name: patterns\n    flag: style\n")

	tmpl := filepath.Join(v, "tmpl")
	writeFile(t, tmpl, "jig.yaml",
		"name: T\nrequired: true\nvalues:\n  - name: services\n    default: true\n  - name: parent\n")

	// Selector node that also contributes content - the inheritance case.
	svc := filepath.Join(tmpl, "services")
	writeFile(t, svc, "jig.yaml", `
name: S
selector: function
default: web
values:
  - name: web
dependencies:
  - groupId: g
    artifactId: base-starter
merge:
  - app.yml
`)
	writeFile(t, svc, "app.yml", "name: {{ .ArtifactId }}\nport: 8080\n")

	web := filepath.Join(tmpl, "services", "web")
	writeFile(t, web, "jig.yaml", `
name: REST leaf
dependencies:
  - groupId: g
    artifactId: web-starter
merge:
  - app.yml
`)
	writeFile(t, web, "app.yml", "web: true\n")
	writeFile(t, filepath.Join(web, "src", "{{ .PackagePath }}"), "App.java", "package {{ .PackageName }};\n")

	writeFile(t, filepath.Join(tmpl, "parent"), "jig.yaml", "name: Parent POM\n")

	writeFile(t, filepath.Join(v, "patterns"), "jig.yaml", "name: P\nvalues:\n  - name: microservice\n")
	writeFile(t, filepath.Join(v, "patterns", "microservice"), "jig.yaml",
		"name: MS\ndependencies:\n  - groupId: org.springframework.cloud\n    artifactId: openfeign\n")

	return root
}

// buildNestedDimensionScaffold builds a minimal, separate tree whose one template ("widget") has
// a NESTED dimension checkpoint of its own: widget/jig.yaml declares no `selector:`, just a bare
// `values:` with one required child (core/) and one optional overlay (extra-logging/, its own
// flag "logging", itself a dimension folder whose own subfolder "verbose/" is the value) - the
// exact same required-plus-optional-overlay shape as the top-level templates/patterns split, just
// one level deeper. Kept separate from buildScaffoldingCode so this new mechanism can't perturb
// the many existing tests built on that fixture.
func buildNestedDimensionScaffold(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeFile(t, root, "jig.yaml", "name: root\nvalues:\n  - name: w\n")
	writeFile(t, filepath.Join(root, "w"), "jig.yaml", "name: w\nvalues:\n  - name: \"1.0\"\n    default: true\n")

	v := filepath.Join(root, "w", "1.0")
	writeFile(t, v, "jig.yaml", "name: v\nvalues:\n  - name: templates\n")

	tmpl := filepath.Join(v, "templates")
	writeFile(t, tmpl, "jig.yaml", "name: T\nrequired: true\nvalues:\n  - name: widget\n    default: true\n")

	widget := filepath.Join(tmpl, "widget")
	writeFile(t, widget, "jig.yaml", `
name: Widget
values:
  - name: core
  - name: extra-logging
    flag: logging
`)
	writeFile(t, filepath.Join(widget, "core"), "jig.yaml",
		"name: Core\nrequired: true\nfiles:\n  - path: core.txt\n    template: false\n")
	writeFile(t, filepath.Join(widget, "core"), "core.txt", "core file\n")

	writeFile(t, filepath.Join(widget, "extra-logging"), "jig.yaml", "name: Extra Logging\nvalues:\n  - name: verbose\n")
	writeFile(t, filepath.Join(widget, "extra-logging", "verbose"), "jig.yaml",
		"name: Verbose\nmerge_priority: 5\nfiles:\n  - path: logging.txt\n    template: false\n")
	writeFile(t, filepath.Join(widget, "extra-logging", "verbose"), "logging.txt", "logging overlay file\n")

	return root
}

// A nested dimension checkpoint's required child (core/) is always applied - the exact same
// "auto-continue, no flag needed" behaviour as the top-level required dimension.
func TestCreate_NestedDimensionRequiredChildAlwaysApplies(t *testing.T) {
	root := buildNestedDimensionScaffold(t)
	outDir := t.TempDir()

	if _, err := run(t, newCreateCommand, "w", "widget", "plain",
		"--scaffolding-code="+root, "--output="+outDir); err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "plain", "core.txt")); err != nil {
		t.Errorf("expected the nested checkpoint's required child to always be applied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "plain", "logging.txt")); err == nil {
		t.Errorf("expected the nested checkpoint's optional overlay to be ABSENT when its flag is unset")
	}
}

// Setting the nested checkpoint's own flag (--logging=verbose) applies its overlay ON TOP OF the
// required child - proving overlays can be selected at any depth, not just the top level.
func TestCreate_NestedDimensionOptionalOverlayAppliesWhenFlagged(t *testing.T) {
	root := buildNestedDimensionScaffold(t)
	outDir := t.TempDir()

	if _, err := run(t, newCreateCommand, "w", "widget", "logged",
		"--logging=verbose", "--scaffolding-code="+root, "--output="+outDir); err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "logged", "core.txt")); err != nil {
		t.Errorf("expected the required child to still be applied alongside the overlay: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "logged", "logging.txt")); err != nil {
		t.Errorf("expected --logging=verbose to apply the nested overlay: %v", err)
	}
}

// An unregistered value for a nested overlay's own flag is rejected exactly like a top-level
// axis flag typo would be - the same validation, just triggered from a deeper checkpoint.
func TestCreate_NestedDimensionOverlayRejectsUnknownValue(t *testing.T) {
	root := buildNestedDimensionScaffold(t)

	_, err := run(t, newCreateCommand, "w", "widget", "bad",
		"--logging=verboze", "--scaffolding-code="+root, "--output="+t.TempDir())
	if err == nil {
		t.Fatal("expected an unregistered --logging value to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "verbose") {
		t.Errorf("expected the error to list the valid value 'verbose', got: %v", err)
	}
}

// TestMain moves the whole test binary into a throwaway directory before anything runs, since
// `--output` defaults to ".", which for a Go test is the package source directory, and a forgotten
// flag would otherwise leave generated output committed-adjacent in internal/cmd/.
func TestMain(m *testing.M) {
	sandbox, err := os.MkdirTemp("", "scaffold-cmd-tests-*")
	if err != nil {
		panic(err)
	}
	if err := os.Chdir(sandbox); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(sandbox)
	os.Exit(code)
}

// run executes one command with the given args and returns its combined output and error.
func run(t *testing.T, newCmd func() *cobra.Command, args ...string) (string, error) {
	t.Helper()

	cmd := newCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	err := cmd.Execute()
	return buf.String(), err
}

func TestList_Frameworks(t *testing.T) {
	root := buildScaffoldingCode(t)
	out, err := run(t, newListCommand, "--scaffolding-code="+root)
	if err != nil {
		t.Fatalf("list returned error: %v", err)
	}
	if !strings.Contains(out, "fw") {
		t.Errorf("expected the registered framework in the output, got:\n%s", out)
	}
}

// `list <framework>` must show versions, categories, and flag names, and must not present the
// required base axis as a flag, since that one is selected positionally as <category>.
func TestList_FrameworkShowsVersionsCategoriesAndFlagNames(t *testing.T) {
	root := buildScaffoldingCode(t)
	out, err := run(t, newListCommand, "fw", "--scaffolding-code="+root)
	if err != nil {
		t.Fatalf("list fw returned error: %v", err)
	}

	for _, want := range []string{"versions:", "1.0", "2.0", "(default)", "services", "--style"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the output, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "--templates") {
		t.Errorf("the required base axis is positional, it must not be shown as a flag:\n%s", out)
	}
	if strings.Contains(out, "--patterns") {
		t.Errorf("the axis declares flag: style, so --patterns must not be advertised:\n%s", out)
	}
}

func TestList_CategoryTree(t *testing.T) {
	root := buildScaffoldingCode(t)
	out, err := run(t, newListCommand, "fw", "services", "--scaffolding-code="+root)
	if err != nil {
		t.Fatalf("list fw services returned error: %v", err)
	}
	if !strings.Contains(out, "--function") || !strings.Contains(out, "web") {
		t.Errorf("expected the selector tree, got:\n%s", out)
	}
}

// createInto runs `create` writing into a throwaway directory, and returns the output plus the
// resulting target path. Tests must never write into the process working directory - that is the
// package source tree.
func createInto(t *testing.T, root string, args ...string) (string, string, error) {
	t.Helper()
	outDir := t.TempDir()
	full := append(args, "--scaffolding-code="+root, "--output="+outDir)
	out, err := run(t, newCreateCommand, full...)
	return out, outDir, err
}

func readGenerated(t *testing.T, outDir, name, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(outDir, name, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("reading generated %s: %v", rel, err)
	}
	return string(data)
}

// create must resolve the base axis's aliased path rather than rebuilding it from the axis name.
func TestCreate_ResolvesAxisPathAlias(t *testing.T) {
	root := buildScaffoldingCode(t)
	_, outDir, err := createInto(t, root, "fw", "services", "payment", "--function=web")
	if err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	// App.java only exists under the aliased tmpl/ tree, so finding it proves the alias survived
	// all the way to the renderer.
	if got := readGenerated(t, outDir, "payment", "src/com/example/App.java"); !strings.Contains(got, "package com.example;") {
		t.Errorf("unexpected App.java contents:\n%s", got)
	}
}

// The whole point of the inheritance chain: pom.xml is declared once at the framework level, and
// the dependencies in it are contributed by two different levels below.
func TestCreate_InheritsFilesAndDependenciesAlongTheChain(t *testing.T) {
	root := buildScaffoldingCode(t)
	_, outDir, err := createInto(t, root, "fw", "services", "payment", "--function=web")
	if err != nil {
		t.Fatalf("create returned error: %v", err)
	}

	pom := readGenerated(t, outDir, "payment", "pom.xml")
	if !strings.Contains(pom, "<artifactId>payment</artifactId>") {
		t.Errorf("expected the framework-level pom.xml to be inherited and rendered, got:\n%s", pom)
	}
	for _, want := range []string{"base-starter", "web-starter"} {
		if !strings.Contains(pom, want) {
			t.Errorf("expected %q (contributed further down the chain) in pom.xml, got:\n%s", want, pom)
		}
	}
}

// A computed variable turns a dotted package into a directory path. It has to be computed rather
// than written into the folder name, because a path segment on disk cannot contain a separator.
func TestCreate_ComputedVariableExpandsPathSegments(t *testing.T) {
	root := buildScaffoldingCode(t)
	_, outDir, err := createInto(t, root, "fw", "services", "payment",
		"--function=web", "--package=com.acme.billing")
	if err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "payment", "src", "com", "acme", "billing", "App.java")); err != nil {
		t.Errorf("expected the package to expand into nested directories: %v", err)
	}
}

// Files listed in merge_yaml are deep-merged when several levels contribute them, instead of the
// deeper one replacing the shallower one wholesale.
func TestCreate_DeepMergesYAMLAcrossLevels(t *testing.T) {
	root := buildScaffoldingCode(t)
	_, outDir, err := createInto(t, root, "fw", "services", "payment", "--function=web")
	if err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	got := readGenerated(t, outDir, "payment", "app.yml")
	for _, want := range []string{"name: payment", "port: 8080", "web: true"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected merged app.yml to keep %q, got:\n%s", want, got)
		}
	}
}

// --style is the axis's declared flag; it must actually select the overlay, not just be accepted
// and dropped.
func TestCreate_AxisSelectedByDeclaredFlag(t *testing.T) {
	root := buildScaffoldingCode(t)
	_, outDir, err := createInto(t, root, "fw", "services", "payment",
		"--function=web", "--style=microservice")
	if err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	if pom := readGenerated(t, outDir, "payment", "pom.xml"); !strings.Contains(pom, "openfeign") {
		t.Errorf("expected the overlay's dependency to reach pom.xml, got:\n%s", pom)
	}
}

func TestCreate_RejectsUnknownFlag(t *testing.T) {
	root := buildScaffoldingCode(t)
	_, _, err := createInto(t, root, "fw", "services", "payment", "--function=web", "--stlye=microservice")
	if err == nil {
		t.Fatal("expected a mistyped flag to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "--stlye") || !strings.Contains(err.Error(), "--style") {
		t.Errorf("expected the error to name the typo and list valid flags, got: %v", err)
	}
}

func TestCreate_RejectsUnregisteredAxisValue(t *testing.T) {
	root := buildScaffoldingCode(t)
	_, _, err := createInto(t, root, "fw", "services", "payment", "--function=web", "--style=nope")
	if err == nil {
		t.Fatal("expected an unregistered axis value to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "microservice") {
		t.Errorf("expected the error to list valid values, got: %v", err)
	}
}

// <name> must not be able to escape <output>.
func TestCreate_RejectsTraversingName(t *testing.T) {
	root := buildScaffoldingCode(t)
	_, _, err := createInto(t, root, "fw", "services", "../../pwned", "--function=web")
	if err == nil {
		t.Fatal("expected a traversing <name> to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "single path segment") {
		t.Errorf("expected a path-segment error, got: %v", err)
	}
}

// All three positionals are required.
func TestCreate_RequiresThreePositionals(t *testing.T) {
	root := buildScaffoldingCode(t)
	for _, args := range [][]string{
		{"fw", "payment"},
		{"fw"},
		{"fw", "services", "payment", "extra"},
	} {
		if _, _, err := createInto(t, root, args...); err == nil {
			t.Errorf("expected %v to be rejected, got nil", args)
		}
	}
}

// A category with zero selector levels goes through the same command with no extra flags - and
// here it is also a leaf that declares nothing of its own, so everything it produces is inherited.
func TestCreate_ZeroSelectorCategoryInheritsEverything(t *testing.T) {
	root := buildScaffoldingCode(t)
	_, outDir, err := createInto(t, root, "fw", "parent", "payment")
	if err != nil {
		t.Fatalf("create parent returned error: %v", err)
	}
	if pom := readGenerated(t, outDir, "payment", "pom.xml"); !strings.Contains(pom, "<artifactId>payment</artifactId>") {
		t.Errorf("expected the inherited pom.xml, got:\n%s", pom)
	}
}

// An existing target fails by default, and each flag changes that in its own way.
func TestCreate_IdempotencyPolicies(t *testing.T) {
	root := buildScaffoldingCode(t)
	outDir := t.TempDir()
	base := []string{"fw", "services", "payment", "--function=web",
		"--scaffolding-code=" + root, "--output=" + outDir}

	if _, err := run(t, newCreateCommand, base...); err != nil {
		t.Fatalf("first create failed: %v", err)
	}
	_, err := run(t, newCreateCommand, base...)
	if err == nil {
		t.Fatal("expected the second create to refuse an existing target, got nil")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("expected the error to point at --force, got: %v", err)
	}
	if _, err := run(t, newCreateCommand, append(base, "--force")...); err != nil {
		t.Errorf("--force should overwrite: %v", err)
	}
	if _, err := run(t, newCreateCommand, append(base, "--skip-existing")...); err != nil {
		t.Errorf("--skip-existing should succeed: %v", err)
	}
}

// A failed render must leave nothing behind - that is the point of staging before committing.
func TestCreate_FailedRenderWritesNothing(t *testing.T) {
	root := buildScaffoldingCode(t)
	// A template referencing a variable nobody declares fails at render time.
	writeFile(t, filepath.Join(root, "fw", "1.0", "tmpl", "services", "web"), "broken.txt",
		"{{ .ThisVariableDoesNotExist }}\n")

	_, outDir, err := createInto(t, root, "fw", "services", "payment", "--function=web")
	if err == nil {
		t.Fatal("expected the bad template to fail the run, got nil")
	}
	if _, statErr := os.Stat(filepath.Join(outDir, "payment")); !os.IsNotExist(statErr) {
		t.Errorf("a failed render must not leave a partial tree behind, but %s exists",
			filepath.Join(outDir, "payment"))
	}
}

func TestCreate_DryRunWritesNothing(t *testing.T) {
	root := buildScaffoldingCode(t)
	out, outDir, err := createInto(t, root, "fw", "services", "payment", "--function=web", "--dry-run")
	if err != nil {
		t.Fatalf("dry run returned error: %v", err)
	}
	if !strings.Contains(out, "DRY RUN") || !strings.Contains(out, "pom.xml") {
		t.Errorf("expected a plan listing the files, got:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(outDir, "payment")); !os.IsNotExist(statErr) {
		t.Error("--dry-run must not write anything")
	}
}

// --help must work even though both commands disable cobra's flag parsing.
func TestHelpWorksUnderDisableFlagParsing(t *testing.T) {
	for name, newCmd := range map[string]func() *cobra.Command{
		"create": newCreateCommand,
		"list":   newListCommand,
	} {
		out, err := run(t, newCmd, "--help")
		if err != nil {
			t.Errorf("%s --help returned error: %v", name, err)
		}
		if !strings.Contains(out, "scaffold "+name) {
			t.Errorf("%s --help did not print usage, got:\n%s", name, out)
		}
	}
}

// ---------------------------------------------------------------------------------------------
// Values files
// ---------------------------------------------------------------------------------------------

func writeValues(t *testing.T, dir, name, content string) string {
	t.Helper()
	writeFile(t, dir, name, content)
	return filepath.Join(dir, name)
}

// The headline case: everything in one file, nothing else on the command line.
func TestCreate_ValuesFileSuppliesEverything(t *testing.T) {
	root := buildScaffoldingCode(t)
	outDir := t.TempDir()
	vf := writeValues(t, t.TempDir(), "values.yaml", `
scaffold: fw
template: services
name: payment
function: web
package: com.acme.billing
scaffolding-code: `+root+`
output: `+outDir+`
`)

	if _, err := run(t, newCreateCommand, "-f", vf); err != nil {
		t.Fatalf("create -f returned error: %v", err)
	}
	if got := readGenerated(t, outDir, "payment", "pom.xml"); !strings.Contains(got, "<artifactId>payment</artifactId>") {
		t.Errorf("expected the name from the values file to reach the template, got:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(outDir, "payment", "src", "com", "acme", "billing", "App.java")); err != nil {
		t.Errorf("expected the package from the values file to shape the tree: %v", err)
	}
}

// A command-line flag is the deliberate one-off override on top of the file baseline.
func TestCreate_CommandLineOverridesValuesFile(t *testing.T) {
	root := buildScaffoldingCode(t)
	outDir := t.TempDir()
	vf := writeValues(t, t.TempDir(), "values.yaml", `
scaffold: fw
template: services
name: from-file
function: web
package: com.from.file
scaffolding-code: `+root+`
`)

	if _, err := run(t, newCreateCommand, "-f", vf,
		"--package=com.from.cli", "--output="+outDir); err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "from-file", "src", "com", "from", "cli", "App.java")); err != nil {
		t.Errorf("expected --package to beat the file's value: %v", err)
	}
}

// Repeatable, later wins - so a base file can be layered with an environment-specific one.
func TestCreate_LaterValuesFileOverridesEarlier(t *testing.T) {
	root := buildScaffoldingCode(t)
	outDir := t.TempDir()
	dir := t.TempDir()
	base := writeValues(t, dir, "base.yaml", `
scaffold: fw
template: services
name: layered
function: web
package: com.base
scaffolding-code: `+root+`
output: `+outDir+`
`)
	override := writeValues(t, dir, "prod.yaml", "package: com.prod\n")

	if _, err := run(t, newCreateCommand, "-f", base, "-f", override); err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "layered", "src", "com", "prod", "App.java")); err != nil {
		t.Errorf("expected the later file to win: %v", err)
	}
}

// Positionals and a values file can be mixed: the file fills only what the command line omits.
func TestCreate_PositionalsMixWithValuesFile(t *testing.T) {
	root := buildScaffoldingCode(t)
	outDir := t.TempDir()
	vf := writeValues(t, t.TempDir(), "values.yaml", `
name: ignored-because-positional-wins
function: web
package: com.acme
scaffolding-code: `+root+`
output: `+outDir+`
`)

	if _, err := run(t, newCreateCommand, "fw", "services", "from-cli", "-f", vf); err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "from-cli")); err != nil {
		t.Errorf("expected the positional name to win over the file: %v", err)
	}
}

// A misspelled key must fail exactly like a misspelled flag - that is the whole reason the file
// format is the flag namespace rather than a separate schema.
func TestCreate_ValuesFileTypoIsRejected(t *testing.T) {
	root := buildScaffoldingCode(t)
	vf := writeValues(t, t.TempDir(), "values.yaml", `
scaffold: fw
template: services
name: payment
function: web
packge: com.acme
scaffolding-code: `+root+`
`)

	_, err := run(t, newCreateCommand, "-f", vf, "--output="+t.TempDir())
	if err == nil {
		t.Fatal("expected a misspelled key to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "packge") || !strings.Contains(err.Error(), "--package") {
		t.Errorf("expected the error to name the typo and list valid keys, got: %v", err)
	}
}

// Omitting a positional entirely still fails - the values file relaxes where it is written, not
// whether it is required.
func TestCreate_ValuesFileStillRequiresAllThreePositionals(t *testing.T) {
	root := buildScaffoldingCode(t)
	vf := writeValues(t, t.TempDir(), "values.yaml", `
scaffold: fw
function: web
scaffolding-code: `+root+`
`)

	_, err := run(t, newCreateCommand, "-f", vf)
	if err == nil {
		t.Fatal("expected missing template/name to be rejected, got nil")
	}
	for _, want := range []string{"template", "name"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the error to name the missing %q, got: %v", want, err)
		}
	}
}

// Nested structures have no meaning: every key maps to exactly one flag.
func TestCreate_ValuesFileRejectsNestedStructures(t *testing.T) {
	root := buildScaffoldingCode(t)
	vf := writeValues(t, t.TempDir(), "values.yaml", `
scaffold: fw
template: services
name: payment
nested:
  a: 1
scaffolding-code: `+root+`
`)

	_, err := run(t, newCreateCommand, "-f", vf)
	if err == nil || !strings.Contains(err.Error(), "nested") {
		t.Fatalf("expected a nested map to be rejected by name, got: %v", err)
	}
}

func TestCreate_MissingValuesFileIsReported(t *testing.T) {
	_, err := run(t, newCreateCommand, "-f", filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil || !strings.Contains(err.Error(), "nope.yaml") {
		t.Fatalf("expected the missing file to be named, got: %v", err)
	}
}

// ---------------------------------------------------------------------------------------------
// lint, --explain, and variable discovery
// ---------------------------------------------------------------------------------------------

// lint is the answer to "is scaffolding-code healthy?" - it must enumerate the combinations the
// registries advertise, not just the one someone happens to type.
func TestLint_PassesOnAHealthyTree(t *testing.T) {
	root := buildScaffoldingCode(t)
	out, err := run(t, newLintCommand, "--scaffolding-code="+root)
	if err != nil {
		t.Fatalf("lint on a healthy tree returned error: %v\n%s", err, out)
	}
	for _, want := range []string{"fw 1.0 services", "fw 1.0 parent", "--style=microservice", "0 failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the lint report, got:\n%s", want, out)
		}
	}
}

// lint must catch a registered value with no manifest, and keep going so one break does not mask
// the rest.
func TestLint_CatchesRegisteredValueWithNoManifest(t *testing.T) {
	root := buildScaffoldingCode(t)
	writeFile(t, filepath.Join(root, "fw", "1.0", "patterns"), "jig.yaml",
		"name: P\nvalues:\n  - name: microservice\n  - name: not-built-yet\n")

	out, err := run(t, newLintCommand, "--scaffolding-code="+root)
	if err == nil {
		t.Fatal("expected lint to fail, got nil")
	}
	if !strings.Contains(out, "not-built-yet") {
		t.Errorf("expected the unbuilt value to be named, got:\n%s", out)
	}
	if !strings.Contains(out, "ok ") {
		t.Errorf("expected healthy combinations to still be reported, got:\n%s", out)
	}
}

// lint must catch a placeholder naming a variable nobody declares, the most common template bug.
func TestLint_CatchesUndeclaredVariableInTemplate(t *testing.T) {
	root := buildScaffoldingCode(t)
	writeFile(t, filepath.Join(root, "fw", "1.0", "tmpl", "services", "web"), "broken.txt",
		"{{ .NobodyDeclaresThis }}\n")

	out, err := run(t, newLintCommand, "--scaffolding-code="+root)
	if err == nil {
		t.Fatal("expected lint to fail, got nil")
	}
	if !strings.Contains(out, "NobodyDeclaresThis") {
		t.Errorf("expected the offending variable to be named, got:\n%s", out)
	}
}

func TestLint_ExitsNonZeroSoItCanGateCI(t *testing.T) {
	root := buildScaffoldingCode(t)
	writeFile(t, filepath.Join(root, "fw", "1.0", "tmpl", "services", "web"), "broken.txt", "{{ .Nope }}\n")

	if _, err := run(t, newLintCommand, "--scaffolding-code="+root); err == nil {
		t.Fatal("expected a non-nil error so the process exits non-zero")
	}
}

// --explain answers "who produced this file, and what else touched it?" - unanswerable from the
// merged result alone once the chain is several levels deep.
func TestCreate_ExplainShowsContributors(t *testing.T) {
	root := buildScaffoldingCode(t)
	out, _, err := createInto(t, root, "fw", "services", "payment", "--function=web", "--explain")
	if err != nil {
		t.Fatalf("--explain returned error: %v", err)
	}
	if !strings.Contains(out, "EXPLAIN") {
		t.Errorf("expected an explain report, got:\n%s", out)
	}
	// app.yml is contributed by services/ and then deep-merged by web/.
	if !strings.Contains(out, "merged") {
		t.Errorf("expected a deep-merge to be reported, got:\n%s", out)
	}
	if !strings.Contains(out, "added") {
		t.Errorf("expected first contributions to be reported, got:\n%s", out)
	}
}

func TestCreate_ExplainWritesNothing(t *testing.T) {
	root := buildScaffoldingCode(t)
	_, outDir, err := createInto(t, root, "fw", "services", "payment", "--function=web", "--explain")
	if err != nil {
		t.Fatalf("--explain returned error: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(outDir, "payment")); !os.IsNotExist(statErr) {
		t.Error("--explain must not write anything")
	}
}

// Without this, the only way to discover that --package exists was to open the manifests.
func TestList_ShowsDeclaredVariables(t *testing.T) {
	root := buildScaffoldingCode(t)
	out, err := run(t, newListCommand, "fw", "services", "--scaffolding-code="+root)
	if err != nil {
		t.Fatalf("list returned error: %v", err)
	}
	if !strings.Contains(out, "--package") {
		t.Errorf("expected the declared variable and its flag, got:\n%s", out)
	}
	if !strings.Contains(out, "com.example") {
		t.Errorf("expected the variable's default, got:\n%s", out)
	}
	if !strings.Contains(out, "from fw") {
		t.Errorf("expected the level that declared it, got:\n%s", out)
	}
}

// A selector flag narrows which leaf's variables to show; a typo is still rejected.
func TestList_AcceptsSelectorFlagsButRejectsTypos(t *testing.T) {
	root := buildScaffoldingCode(t)
	if _, err := run(t, newListCommand, "fw", "services", "--function=web",
		"--scaffolding-code="+root); err != nil {
		t.Errorf("a valid selector flag must be accepted: %v", err)
	}
	_, err := run(t, newListCommand, "fw", "services", "--funtcion=web", "--scaffolding-code="+root)
	if err == nil {
		t.Fatal("expected a mistyped selector flag to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "--funtcion") {
		t.Errorf("expected the typo to be named, got: %v", err)
	}
}
