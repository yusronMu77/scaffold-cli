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

// buildScaffoldingCode creates a scaffolding-code tree that exercises all three v1.8 identities
// at once: the base axis is named "templates" but lives in tmpl/ (path alias), and the overlay
// axis lives in patterns/ but is driven by --style (flag alias).
func buildScaffoldingCode(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeFile(t, root, "manifest.yaml", "name: root\nframeworks:\n  - name: fw\n    description: Test framework\n")
	writeFile(t, filepath.Join(root, "fw"), "manifest.yaml",
		"name: fw\nvalues:\n  - name: \"1.0\"\n    default: true\n  - name: \"2.0\"\n")

	v := filepath.Join(root, "fw", "1.0")
	writeFile(t, v, "manifest.yaml",
		"name: v\nvalues:\n  - name: templates\n    path: tmpl\n  - name: patterns\n    flag: style\n")

	tmpl := filepath.Join(v, "tmpl")
	writeFile(t, tmpl, "manifest.yaml",
		"name: T\nrequired: true\nvalues:\n  - name: services\n    default: true\n  - name: parent\n")
	writeFile(t, filepath.Join(tmpl, "services"), "manifest.yaml",
		"name: S\nselector: function\nvalues:\n  - name: web\n")
	writeFile(t, filepath.Join(tmpl, "services", "web"), "manifest.yaml",
		"name: REST leaf\nfiles:\n  - path: pom.xml\n")
	writeFile(t, filepath.Join(tmpl, "parent"), "manifest.yaml",
		"name: Parent POM\nfiles:\n  - path: pom.xml\n")

	writeFile(t, filepath.Join(v, "patterns"), "manifest.yaml", "name: P\nvalues:\n  - name: microservice\n")
	writeFile(t, filepath.Join(v, "patterns", "microservice"), "manifest.yaml",
		"name: MS\ndependencies:\n  - groupId: org.springframework.cloud\n    artifactId: spring-cloud-starter-openfeign\n")

	return root
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

// The regression that motivated this whole file: `create` used to rebuild the base-axis path from
// its name, so an axis aliased to tmpl/ made create fail while list succeeded.
func TestCreate_ResolvesAxisPathAlias(t *testing.T) {
	root := buildScaffoldingCode(t)
	out, err := run(t, newCreateCommand, "fw", "services", "payment", "--function=web", "--scaffolding-code="+root)
	if err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	if !strings.Contains(out, "tmpl") {
		t.Errorf("expected the leaf to resolve under the aliased tmpl/ folder, got:\n%s", out)
	}
	if !strings.Contains(out, "name=payment") {
		t.Errorf("expected the artefact name in the output, got:\n%s", out)
	}
}

// --style is the axis's declared flag; it must actually select the overlay rather than be
// accepted and dropped (design review section 2.3).
func TestCreate_AxisSelectedByDeclaredFlag(t *testing.T) {
	root := buildScaffoldingCode(t)
	out, err := run(t, newCreateCommand,
		"fw", "services", "payment", "--function=web", "--style=microservice", "--scaffolding-code="+root)
	if err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	if !strings.Contains(out, "microservice") {
		t.Errorf("expected --style=microservice to select the patterns axis, got:\n%s", out)
	}
}

func TestCreate_RejectsUnknownFlag(t *testing.T) {
	root := buildScaffoldingCode(t)
	_, err := run(t, newCreateCommand,
		"fw", "services", "payment", "--function=web", "--stlye=microservice", "--scaffolding-code="+root)
	if err == nil {
		t.Fatal("expected a mistyped flag to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "--stlye") || !strings.Contains(err.Error(), "--style") {
		t.Errorf("expected the error to name the typo and list valid flags, got: %v", err)
	}
}

func TestCreate_RejectsUnregisteredAxisValue(t *testing.T) {
	root := buildScaffoldingCode(t)
	_, err := run(t, newCreateCommand,
		"fw", "services", "payment", "--function=web", "--style=nope", "--scaffolding-code="+root)
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
	_, err := run(t, newCreateCommand, "fw", "services", "../../pwned", "--function=web", "--scaffolding-code="+root)
	if err == nil {
		t.Fatal("expected a traversing <name> to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "single path segment") {
		t.Errorf("expected a path-segment error, got: %v", err)
	}
}

// Fundamental rule #5: all three positionals are required. The 2-positional form that Phase 3a
// briefly supported turned a forgotten <name> into a silently wrong artefact.
func TestCreate_RequiresThreePositionals(t *testing.T) {
	root := buildScaffoldingCode(t)
	for _, args := range [][]string{
		{"fw", "payment", "--scaffolding-code=" + root},
		{"fw", "--scaffolding-code=" + root},
		{"fw", "services", "payment", "extra", "--scaffolding-code=" + root},
	} {
		if _, err := run(t, newCreateCommand, args...); err == nil {
			t.Errorf("expected %v to be rejected, got nil", args)
		}
	}
}

// A category with zero selector levels goes through the same command with no extra flags.
func TestCreate_ZeroSelectorCategory(t *testing.T) {
	root := buildScaffoldingCode(t)
	out, err := run(t, newCreateCommand, "fw", "parent", "payment", "--scaffolding-code="+root)
	if err != nil {
		t.Fatalf("create parent returned error: %v", err)
	}
	if !strings.Contains(out, "Parent POM") {
		t.Errorf("expected the parent leaf to resolve, got:\n%s", out)
	}
}

func TestCreate_OutputTargetIsOutputSlashName(t *testing.T) {
	root := buildScaffoldingCode(t)
	out, err := run(t, newCreateCommand,
		"fw", "services", "payment", "--function=web", "--output=./project", "--scaffolding-code="+root)
	if err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	want := filepath.Join("project", "payment")
	if !strings.Contains(out, want) {
		t.Errorf("expected the write target %q, got:\n%s", want, out)
	}
}

// --help must work even though both commands disable cobra's flag parsing (design review 2.11).
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
