package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"scaffold-engine-go/internal/render"
)

// buildResult summarises what running one combination's `verify:` checks produced.
type buildResult struct {
	Passed  int
	Skipped int
	// Failures are the checks that ran and did not succeed.
	Failures []string
}

// maxCapturedOutput bounds a failing check's captured output to its tail, since the useful part of
// a long build log is usually near the end.
const maxCapturedOutput = 4000

// runVerifications writes one rendered project to a scratch directory and runs its checks there,
// closing the gap that `lint` alone leaves: rendering proves the templates resolve, not that the
// result builds. Each combination gets its own scratch directory, removed afterward, since the
// checks are real builds that write files like target/ or node_modules/ and would otherwise
// contaminate each other.
func runVerifications(out io.Writer, files []render.File, checks []render.Verification) (buildResult, error) {
	var result buildResult
	if len(checks) == 0 {
		return result, nil
	}

	dir, err := os.MkdirTemp("", "scaffold-build-*")
	if err != nil {
		return result, fmt.Errorf("creating a scratch directory to build in: %w", err)
	}
	defer os.RemoveAll(dir)

	if _, err := render.Write(dir, files, render.Overwrite); err != nil {
		return result, fmt.Errorf("writing the project to build: %w", err)
	}

	for _, check := range checks {
		started := time.Now()
		status, output, err := runOne(dir, check)
		elapsed := time.Since(started).Round(100 * time.Millisecond)

		switch status {
		case verifyPassed:
			result.Passed++
			fmt.Fprintf(out, "      build %-14s ok    %s (%s)\n", check.Name, check, elapsed)
		case verifySkipped:
			// Reported loudly rather than silently: a skipped check must not count as a pass, but a
			// missing tool should not fail the whole run either.
			result.Skipped++
			fmt.Fprintf(out, "      build %-14s SKIP  %v\n", check.Name, err)
		default:
			detail := check.Name
			if check.Description != "" {
				detail += " (" + check.Description + ")"
			}
			result.Failures = append(result.Failures,
				fmt.Sprintf("%s: %v\n  declared by %s\n  command: %s\n%s",
					detail, err, check.Source, check, indentBlock(output)))
			fmt.Fprintf(out, "      build %-14s FAIL  %s (%s)\n", check.Name, check, elapsed)
		}
	}
	return result, nil
}

type verifyStatus int

const (
	verifyFailed verifyStatus = iota
	verifyPassed
	verifySkipped
)

// runOne executes a single check in dir. The command is passed to the OS as an argv list, never
// through a shell, so nothing in scaffolding-code can inject a pipe, redirect, or `&&`. See
// jig.Verify.
func runOne(dir string, check render.Verification) (verifyStatus, string, error) {
	// Resolved up front so a missing tool is reported as a skip, not an indistinguishable exec
	// failure.
	if _, err := exec.LookPath(check.Command[0]); err != nil {
		return verifySkipped, "", fmt.Errorf("%s is not on PATH, so this check did not run",
			check.Command[0])
	}

	ctx, cancel := context.WithTimeout(context.Background(), check.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, check.Command[0], check.Command[1:]...)
	cmd.Dir = dir
	// No stdin, so a build tool that tries to prompt gets EOF and gives up instead of hanging.
	cmd.Stdin = nil
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	output := tail(buf.String(), maxCapturedOutput)

	switch {
	case err == nil:
		return verifyPassed, output, nil
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return verifyFailed, output, fmt.Errorf("timed out after %s", check.Timeout)
	default:
		return verifyFailed, output, err
	}
}

// tail keeps the last n bytes, on a line boundary, and says so when it truncates.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[len(s)-n:]
	if i := strings.IndexByte(cut, '\n'); i >= 0 {
		cut = cut[i+1:]
	}
	return "... (earlier output omitted)\n" + cut
}

func indentBlock(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	return "  | " + strings.ReplaceAll(s, "\n", "\n  | ")
}
