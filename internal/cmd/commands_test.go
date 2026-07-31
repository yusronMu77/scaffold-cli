package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The 2026-07-27 design review found that runCreate and runList had no tests at all, while
// internal/discovery sat at 89% - and the broken axis `path` alias lived precisely in this
// untested wiring layer, where `list` resolved the alias and `create` did not (section 5.3).
// These tests drive the commands end to end against a fixture tree.

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

// buildScaffoldingCode creates a scaffolding-code tree that exercises the whole design at once:
// all three name/path/flag identities (the base axis is named "templates" but lives in tmpl/, the
// overlay axis lives in patterns/ but is driven by --style), and the inheritance chain, with the
// shared pom.xml contributed at the framework level and refined further down.
func buildScaffoldingCode(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeFile(t, root, "manifest.yaml", "name: root\nframeworks:\n  - name: fw\n    description: Test framework\n")

	// Framework level: the shared file plus the variables every version inherits.
	fw := filepath.Join(root, "fw")
	writeFile(t, fw, "manifest.yaml", `
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
`)
	writeFile(t, fw, "pom.xml", "<artifactId>{{ .ArtifactId }}</artifactId>\n{{- range .Dependencies }}\n<dep>{{ .artifactId }}</dep>\n{{- end }}\n")

	v := filepath.Join(fw, "1.0")
	writeFile(t, v, "manifest.yaml",
		"name: v\nvalues:\n  - name: templates\n    path: tmpl\n  - name: patterns\n    flag: style\n")

	tmpl := filepath.Join(v, "tmpl")
	writeFile(t, tmpl, "manifest.yaml",
		"name: T\nrequired: true\nvalues:\n  - name: services\n    default: true\n  - name: parent\n")

	// Selector node that also contributes content - the inheritance case.
	svc := filepath.Join(tmpl, "services")
	writeFile(t, svc, "manifest.yaml", `
name: S
selector: function
default: web
values:
  - name: web
dependencies:
  - groupId: g
    artifactId: base-starter
merge_yaml:
  - app.yml
`)
	writeFile(t, svc, "app.yml", "name: {{ .ArtifactId }}\nport: 8080\n")

	web := filepath.Join(tmpl, "services", "web")
	writeFile(t, web, "manifest.yaml", `
name: REST leaf
dependencies:
  - groupId: g
    artifactId: web-starter
merge_yaml:
  - app.yml
`)
	writeFile(t, web, "app.yml", "web: true\n")
	writeFile(t, filepath.Join(web, "src", "{{ .PackagePath }}"), "App.java", "package {{ .PackageName }};\n")

	writeFile(t, filepath.Join(tmpl, "parent"), "manifest.yaml", "name: Parent POM\n")

	writeFile(t, filepath.Join(v, "patterns"), "manifest.yaml", "name: P\nvalues:\n  - name: microservice\n")
	writeFile(t, filepath.Join(v, "patterns", "microservice"), "manifest.yaml",
		"name: MS\ndependencies:\n  - groupId: org.springframework.cloud\n    artifactId: openfeign\n")

	return root
}

// TestMain moves the whole test binary into a throwaway directory before anything runs.
//
// `--output` defaults to ".", and for a Go test "." is the PACKAGE SOURCE DIRECTORY. Early versions
// of these tests omitted the flag and left whole generated Maven projects sitting in internal/cmd/,
// committed-adjacent and easy to miss. Guarding inside the helper is not enough: the tests most
// likely to forget are the ones asserting a failure, where a regression that makes them pass would
// silently start polluting the repo again. Moving the working directory removes the hazard itself
// rather than asking every future test author to remember it.
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

// PRD Section 8 requires `list <framework>` to show versions; it previously showed only axes
// (design review section 5.20). It must also not present the required base axis as a flag, since
// that one is selected positionally as <category> (section 5.98).
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

// The regression that motivated this whole file: `create` used to rebuild the base-axis path from
// its name, so an axis aliased to tmpl/ made create fail while list succeeded.
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

// --style is the axis's declared flag; it must actually select the overlay rather than be
// accepted and dropped (design review section 2.3).
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

// Fundamental rule #7: <name> must not be able to escape <output>.
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

// Fundamental rule #5: all three positionals are required.
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

// NFR #2: an existing target fails by default, and each flag changes that in its own way.
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
// Values files (PRD Section 8.7)
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
framework: fw
category: services
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
framework: fw
category: services
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
framework: fw
category: services
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
framework: fw
category: services
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

// Omitting a positional entirely still fails - the values file relaxes *where* it is written, not
// whether it is required (fundamental rule #5).
func TestCreate_ValuesFileStillRequiresAllThreePositionals(t *testing.T) {
	root := buildScaffoldingCode(t)
	vf := writeValues(t, t.TempDir(), "values.yaml", `
framework: fw
function: web
scaffolding-code: `+root+`
`)

	_, err := run(t, newCreateCommand, "-f", vf)
	if err == nil {
		t.Fatal("expected missing category/name to be rejected, got nil")
	}
	for _, want := range []string{"category", "name"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the error to name the missing %q, got: %v", want, err)
		}
	}
}

// Nested structures have no meaning: every key maps to exactly one flag.
func TestCreate_ValuesFileRejectsNestedStructures(t *testing.T) {
	root := buildScaffoldingCode(t)
	vf := writeValues(t, t.TempDir(), "values.yaml", `
framework: fw
category: services
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
	for _, want := range []string{"fw services", "fw parent", "--style=microservice", "0 failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the lint report, got:\n%s", want, out)
		}
	}
}

// A registered value with no manifest is the exact failure that made `list` advertise things that
// did not work. lint has to catch it, and has to keep going so one break does not mask the rest.
func TestLint_CatchesRegisteredValueWithNoManifest(t *testing.T) {
	root := buildScaffoldingCode(t)
	writeFile(t, filepath.Join(root, "fw", "1.0", "patterns"), "manifest.yaml",
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

// A placeholder naming a variable nobody declares is the most common template bug, and the one
// that used to surface only when someone happened to generate that exact combination.
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
