package cmd

import (
	"path/filepath"
	"strings"
	"testing"
)

// addVerify appends a `verify:` block to the framework level, where every combination inherits it.
// The commands below use `go`, which is by definition on PATH while `go test` is running.
func addVerify(t *testing.T, root, block string) {
	t.Helper()
	fw := filepath.Join(root, "fw")
	writeFile(t, fw, "jig.yaml", readFile(t, filepath.Join(fw, "jig.yaml"))+block)
}

// TestBuild_RunsTheChecksAndReportsThem verifies --build actually builds the generated project and
// reports each check's result.
func TestBuild_RunsTheChecksAndReportsThem(t *testing.T) {
	root := buildScaffoldingCode(t)
	addVerify(t, root, "\nverify:\n  - name: compiles\n    command: [go, version]\n")

	out, err := run(t, newLintCommand, "--scaffolding-code="+root, "--build")
	if err != nil {
		t.Fatalf("lint --build returned error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "build compiles") || !strings.Contains(out, "ok    go version") {
		t.Errorf("expected each check to be reported, got:\n%s", out)
	}
	if !strings.Contains(out, "check(s) passed, 0 skipped") {
		t.Errorf("expected a check summary, got:\n%s", out)
	}
}

// TestBuild_FailingCheckFailsTheRun verifies a failing check fails the lint run.
func TestBuild_FailingCheckFailsTheRun(t *testing.T) {
	root := buildScaffoldingCode(t)
	addVerify(t, root, "\nverify:\n  - name: compiles\n    command: [go, help, not-a-real-topic]\n")

	out, err := run(t, newLintCommand, "--scaffolding-code="+root, "--build")
	if err == nil {
		t.Fatalf("expected a failing check to fail the run, got:\n%s", out)
	}
	if !strings.Contains(out, "build compiles") || !strings.Contains(out, "FAIL") {
		t.Errorf("expected the failure to be reported, got:\n%s", out)
	}
	// Command output must be included so the failure is diagnosable, not just an exit code.
	if !strings.Contains(out, "not-a-real-topic") {
		t.Errorf("expected the command output to be included, got:\n%s", out)
	}
}

// TestBuild_MissingToolIsReportedAsSkipped verifies a missing build tool is reported as skipped,
// not silently counted as a pass.
func TestBuild_MissingToolIsReportedAsSkipped(t *testing.T) {
	root := buildScaffoldingCode(t)
	addVerify(t, root, "\nverify:\n  - name: compiles\n    command: [scaffold-no-such-tool-xyz]\n")

	out, err := run(t, newLintCommand, "--scaffolding-code="+root, "--build")
	if err != nil {
		t.Fatalf("a missing tool must not fail the run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "SKIP") || !strings.Contains(out, "not on PATH") {
		t.Errorf("expected a loud skip naming the tool, got:\n%s", out)
	}
	if !strings.Contains(out, "0 check(s) passed") {
		t.Errorf("a skipped check must not be counted as passed, got:\n%s", out)
	}
}

// TestBuild_PlainLintRunsNoChecks verifies plain `lint` runs no checks; --build is opt-in.
func TestBuild_PlainLintRunsNoChecks(t *testing.T) {
	root := buildScaffoldingCode(t)
	addVerify(t, root, "\nverify:\n  - name: compiles\n    command: [go, help, not-a-real-topic]\n")

	out, err := run(t, newLintCommand, "--scaffolding-code="+root)
	if err != nil {
		t.Fatalf("plain lint must not be affected by verify: %v\n%s", err, out)
	}
	if strings.Contains(out, "build compiles") {
		t.Errorf("plain lint must not run checks, got:\n%s", out)
	}
}

// TestBuild_CreateNeverRunsChecks verifies `create` never runs verify checks, with or without a
// flag, keeping project generation free of arbitrary code execution.
func TestBuild_CreateNeverRunsChecks(t *testing.T) {
	root := buildScaffoldingCode(t)
	addVerify(t, root, "\nverify:\n  - name: compiles\n    command: [go, help, not-a-real-topic]\n")

	out, _, err := createInto(t, root, "fw", "services", "payment", "--function=web")
	if err != nil {
		t.Fatalf("create must ignore verify entirely: %v\n%s", err, out)
	}
	if strings.Contains(out, "compiles") {
		t.Errorf("create must not mention checks at all, got:\n%s", out)
	}
}

// TestBuild_SaysSoWhenThereIsNothingToRun verifies --build reports having nothing to run rather
// than a reassuring "0 failed" when a chain declares no checks.
func TestBuild_SaysSoWhenThereIsNothingToRun(t *testing.T) {
	root := buildScaffoldingCode(t)

	out, err := run(t, newLintCommand, "--scaffolding-code="+root, "--build")
	if err != nil {
		t.Fatalf("lint --build returned error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No `verify:` declared") {
		t.Errorf("expected --build to say it had nothing to run, got:\n%s", out)
	}
}

// TestBuild_MisspelledFlagIsRejected verifies a misspelled flag like --buidl is rejected rather
// than silently ignored.
func TestBuild_MisspelledFlagIsRejected(t *testing.T) {
	root := buildScaffoldingCode(t)

	out, err := run(t, newLintCommand, "--scaffolding-code="+root, "--buidl")
	if err == nil {
		t.Fatalf("expected a misspelled flag to be rejected, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), "buidl") {
		t.Errorf("expected the error to name the flag, got: %v", err)
	}
}

// TestBuild_WritesNothingIntoTheWorkingDirectory verifies each combination builds in its own
// scratch directory, leaving nothing behind in the working directory.
func TestBuild_WritesNothingIntoTheWorkingDirectory(t *testing.T) {
	root := buildScaffoldingCode(t)
	addVerify(t, root, "\nverify:\n  - name: compiles\n    command: [go, version]\n")

	if _, err := run(t, newLintCommand, "--scaffolding-code="+root, "--build"); err != nil {
		t.Fatalf("lint --build returned error: %v", err)
	}
	// TestMain has already moved the process into an empty sandbox, so anything here is ours.
	entries, err := filepath.Glob("*")
	if err != nil {
		t.Fatalf("globbing the working directory: %v", err)
	}
	if len(entries) > 0 {
		t.Errorf("--build leaked files into the working directory: %v", entries)
	}
}
