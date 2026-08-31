package render

import (
	"errors"
	"os/exec"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"scaffold-engine-go/internal/jig"
)

func sourceWith(t *testing.T, label, body string) Source {
	t.Helper()
	var m jig.Jig
	if err := yaml.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("parsing jig fixture: %v", err)
	}
	return Source{Manifest: &m, Label: label}
}

// The DRY case: a framework says "everything I produce must compile" once, at the top, and every
// leaf below inherits it without mentioning it.
func TestVerify_InheritedFromTheTopOfTheChain(t *testing.T) {
	sources := []Source{
		sourceWith(t, "spring-boot", "verify:\n  - name: compiles\n    command: [mvn, -B, test]\n"),
		sourceWith(t, "spring-boot/3.2.x/templates/services", "name: services\n"),
	}

	got, err := CollectVerifications(sources, Context{})
	if err != nil {
		t.Fatalf("CollectVerifications: %v", err)
	}
	if len(got) != 1 || got[0].Name != "compiles" {
		t.Fatalf("expected the framework-level check to reach the leaf, got %+v", got)
	}
	if got[0].Source != "spring-boot" {
		t.Errorf("a failure must name where the check was declared, got %q", got[0].Source)
	}
	if got[0].Timeout != DefaultVerifyTimeout {
		t.Errorf("expected the default timeout, got %v", got[0].Timeout)
	}
}

// Same precedence as layout rules and partials: a deeper level replaces one by name and leaves the
// others alone.
func TestVerify_DeeperLevelOverridesByName(t *testing.T) {
	sources := []Source{
		sourceWith(t, "fw", "verify:\n  - name: compiles\n    command: [mvn, test]\n"+
			"  - name: lints\n    command: [mvn, checkstyle:check]\n"),
		sourceWith(t, "fw/libs", "verify:\n  - name: compiles\n    command: [mvn, verify]\n"+
			"    timeout: 90s\n"),
	}

	got, err := CollectVerifications(sources, Context{})
	if err != nil {
		t.Fatalf("CollectVerifications: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected the override to replace, not append, got %d: %+v", len(got), got)
	}
	if got[0].Name != "compiles" || got[0].String() != "mvn verify" {
		t.Errorf("expected the deeper command to win, got %q", got[0].String())
	}
	if got[0].Timeout != 90*time.Second {
		t.Errorf("expected the declared timeout, got %v", got[0].Timeout)
	}
	if got[1].Name != "lints" {
		t.Errorf("expected the untouched check to survive in order, got %+v", got[1])
	}
}

// Each argv element is rendered, so a check can reference the project's own variables. Safe
// precisely because the result is one argv element, never a fragment of a command line.
func TestVerify_ArgvElementsAreRendered(t *testing.T) {
	sources := []Source{sourceWith(t, "fw",
		"verify:\n  - name: compiles\n    command: [mvn, \"-Djava.version={{ .JavaVersion }}\", test]\n")}

	got, err := CollectVerifications(sources, Context{"JavaVersion": "17"})
	if err != nil {
		t.Fatalf("CollectVerifications: %v", err)
	}
	if got[0].Command[1] != "-Djava.version=17" {
		t.Errorf("expected the variable to be substituted, got %q", got[0].Command[1])
	}
}

// Whatever a check's arguments contain, they stay ONE argument. This is the security property the
// whole design rests on: there is no shell, so there is nothing to inject into.
func TestVerify_ShellMetacharactersStayOneArgument(t *testing.T) {
	sources := []Source{sourceWith(t, "fw",
		"verify:\n  - name: x\n    command: [\"echo\", \"a && rm -rf / ; b | c\"]\n")}

	got, err := CollectVerifications(sources, Context{})
	if err != nil {
		t.Fatalf("CollectVerifications: %v", err)
	}
	if len(got[0].Command) != 2 {
		t.Fatalf("the argv must not be re-split, got %#v", got[0].Command)
	}
	if got[0].Command[1] != "a && rm -rf / ; b | c" {
		t.Errorf("the argument must be carried through verbatim, got %q", got[0].Command[1])
	}
}

// Nothing declared anywhere is not an error - most chains have no checks, and that is fine. It is
// the summary's job to say so out loud.
func TestVerify_NoneDeclaredIsEmptyNotAnError(t *testing.T) {
	got, err := CollectVerifications([]Source{sourceWith(t, "fw", "name: fw\n")}, Context{})
	if err != nil {
		t.Fatalf("CollectVerifications: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no checks, got %+v", got)
	}
}

// A verify command that exits non-zero must be correctly collected and produce an exit error when executed,
// so lint --build can detect and propagate the failure.
func TestVerify_NonZeroExit(t *testing.T) {
	sources := []Source{
		sourceWith(t, "fw", "verify:\n  - name: failing-check\n    command: [go, help, not-a-real-topic]\n"),
	}

	got, err := CollectVerifications(sources, Context{})
	if err != nil {
		t.Fatalf("CollectVerifications: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 check, got %d: %+v", len(got), got)
	}
	v := got[0]
	if v.Name != "failing-check" {
		t.Errorf("expected check name 'failing-check', got %q", v.Name)
	}
	if v.String() != "go help not-a-real-topic" {
		t.Errorf("expected string representation 'go help not-a-real-topic', got %q", v.String())
	}

	cmd := exec.Command(v.Command[0], v.Command[1:]...)
	err = cmd.Run()
	if err == nil {
		t.Fatalf("expected command %v to exit non-zero and return an error", v.Command)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Errorf("expected exit error from non-zero exit code, got %v", err)
	}
}
