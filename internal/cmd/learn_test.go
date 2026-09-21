package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"scaffold-engine-go/internal/jig"
	"scaffold-engine-go/internal/learn"
)

// Guard against runLearn ever forgetting to register itself, since DisableFlagParsing commands
// silently accept any flag if RunE is never wired up to the root command tree.
func TestLearn_RegisteredOnRootCommand(t *testing.T) {
	root := &cobra.Command{Use: "scaffold"}
	root.AddCommand(newLearnCommand())
	found, _, err := root.Find([]string{"learn"})
	if err != nil || found.Name() != "learn" {
		t.Fatalf("expected `learn` to be a registered subcommand, got %v, err=%v", found, err)
	}
}

// Zero positionals is still an error; two or more is now the multi-example mode (issue #19), not
// an error - see TestLearnMultiExample_* in learn_multi_example_test.go.
func TestLearn_RequiresAtLeastOnePositional(t *testing.T) {
	if _, err := run(t, newLearnCommand, "--output="+t.TempDir()); err == nil {
		t.Fatal("expected an error with no positional argument")
	}
}

func TestLearn_MissingOutputFlag(t *testing.T) {
	exampleDir := writeExampleFolder(t)
	_, err := run(t, newLearnCommand, exampleDir)
	if err == nil || !strings.Contains(err.Error(), "--output is required") {
		t.Fatalf("expected a clear --output-required error, got %v", err)
	}
}

func TestLearn_UnknownFlag(t *testing.T) {
	exampleDir := writeExampleFolder(t)
	_, err := run(t, newLearnCommand, exampleDir, "--output="+t.TempDir(), "--bogus")
	if err == nil {
		t.Fatal("expected an error for an unknown flag")
	}
}

// No provider env var set and no --provider flag: must fail before any scanning/network happens.
func TestLearn_NoProviderResolvable(t *testing.T) {
	t.Setenv(learn.EnvAnthropicAPIKey, "")
	t.Setenv(learn.EnvOpenAIAPIKey, "")

	exampleDir := writeExampleFolder(t)
	_, err := run(t, newLearnCommand, exampleDir, "--output="+t.TempDir())
	if err == nil {
		t.Fatal("expected an error when no LLM provider can be resolved")
	}
}

// fakeInferer lets command-level tests exercise the scan -> infer -> write pipeline without any
// network call - nothing in this codebase talked to a network service before `learn`, so this
// seam is new.
type fakeInferer struct {
	draft *learn.Draft
}

func (f *fakeInferer) Infer(ctx context.Context, files []learn.SourceFile) (*learn.Draft, error) {
	return f.draft, nil
}

func TestLearn_WritesValidDraftAndReportsIt(t *testing.T) {
	exampleDir := writeExampleFolder(t)
	outDir := filepath.Join(t.TempDir(), "draft")

	client := &fakeInferer{draft: &learn.Draft{
		Name:        "widget-controller",
		Description: "A learned controller",
		Variables: []learn.DraftVariable{
			{Name: "ClassName", Prompt: "Entity class name", Default: "Widget", Required: true},
		},
		Files: []learn.DraftFile{
			{Path: "{{ .ClassName }}Controller.java", Content: "class {{ .ClassName }}Controller {}\n"},
		},
	}}

	cmd := newLearnCommand()
	var buf strings.Builder
	cmd.SetOut(&buf)
	if err := runLearnWithClient(cmd, exampleDir, outDir, client, false); err != nil {
		t.Fatalf("runLearnWithClient returned error: %v", err)
	}

	if _, err := jig.Load(filepath.Join(outDir, jig.FileName)); err != nil {
		t.Fatalf("jig.Load on the written draft failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, learn.DraftVersionName, "{{ .ClassName }}Controller.java")); err != nil {
		t.Fatalf("expected the templated file on disk: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"widget-controller", "ClassName", outDir} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to mention %q, got:\n%s", want, out)
		}
	}
}

const sampleDraftJSON = `{
	"name": "widget-controller",
	"description": "A learned controller",
	"variables": [
		{"name": "ClassName", "prompt": "Entity class name", "default": "Widget", "required": true}
	],
	"files": [
		{"path": "{{ .ClassName }}Controller.java", "content": "class {{ .ClassName }}Controller {}\n"}
	]
}`

// --draft skips provider resolution entirely, so it must work with neither API key env var set.
func TestLearn_DraftFlagFromFile(t *testing.T) {
	t.Setenv(learn.EnvAnthropicAPIKey, "")
	t.Setenv(learn.EnvOpenAIAPIKey, "")

	exampleDir := writeExampleFolder(t)
	outDir := filepath.Join(t.TempDir(), "draft")
	draftPath := filepath.Join(t.TempDir(), "draft.json")
	if err := os.WriteFile(draftPath, []byte(sampleDraftJSON), 0o644); err != nil {
		t.Fatalf("writing draft fixture: %v", err)
	}

	out, err := run(t, newLearnCommand, exampleDir, "--output="+outDir, "--draft="+draftPath)
	if err != nil {
		t.Fatalf("learn --draft returned error: %v", err)
	}
	if _, err := jig.Load(filepath.Join(outDir, jig.FileName)); err != nil {
		t.Fatalf("jig.Load on the written draft failed: %v", err)
	}
	if !strings.Contains(out, "widget-controller") {
		t.Errorf("expected output to mention the draft name, got:\n%s", out)
	}
}

func TestLearn_DraftFlagFromStdin(t *testing.T) {
	t.Setenv(learn.EnvAnthropicAPIKey, "")
	t.Setenv(learn.EnvOpenAIAPIKey, "")

	exampleDir := writeExampleFolder(t)
	outDir := filepath.Join(t.TempDir(), "draft")

	cmd := newLearnCommand()
	var buf strings.Builder
	cmd.SetOut(&buf)
	cmd.SetIn(strings.NewReader(sampleDraftJSON))
	cmd.SetArgs([]string{exampleDir, "--output=" + outDir, "--draft=-"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("learn --draft=- returned error: %v", err)
	}
	if _, err := jig.Load(filepath.Join(outDir, jig.FileName)); err != nil {
		t.Fatalf("jig.Load on the written draft failed: %v", err)
	}
}

func TestLearn_DraftRejectsProviderFlags(t *testing.T) {
	exampleDir := writeExampleFolder(t)
	_, err := run(t, newLearnCommand, exampleDir, "--output="+t.TempDir(),
		"--draft="+filepath.Join(t.TempDir(), "draft.json"), "--provider=anthropic")
	if err == nil || !strings.Contains(err.Error(), "don't apply together") {
		t.Fatalf("expected --draft combined with --provider to be rejected, got %v", err)
	}
}

func TestLearn_DraftRejectsResponseFormatFlag(t *testing.T) {
	exampleDir := writeExampleFolder(t)
	_, err := run(t, newLearnCommand, exampleDir, "--output="+t.TempDir(),
		"--draft="+filepath.Join(t.TempDir(), "draft.json"), "--response-format=json_schema")
	if err == nil || !strings.Contains(err.Error(), "don't apply together") {
		t.Fatalf("expected --draft combined with --response-format to be rejected, got %v", err)
	}
}

// --draft never calls a provider, so a prompt addendum (which only affects the provider call)
// combined with it must be rejected the same way --provider/--response-format already are.
func TestLearn_DraftRejectsPromptAddendumFlag(t *testing.T) {
	exampleDir := writeExampleFolder(t)
	addendum := filepath.Join(t.TempDir(), "addendum.md")
	if err := os.WriteFile(addendum, []byte("extra rule"), 0o644); err != nil {
		t.Fatalf("writing addendum fixture: %v", err)
	}
	_, err := run(t, newLearnCommand, exampleDir, "--output="+t.TempDir(),
		"--draft="+filepath.Join(t.TempDir(), "draft.json"), "--prompt-addendum="+addendum)
	if err == nil || !strings.Contains(err.Error(), "don't apply together") {
		t.Fatalf("expected --draft combined with --prompt-addendum to be rejected, got %v", err)
	}
}

// A --prompt-addendum naming a file that doesn't exist is a real misconfiguration and must fail
// loudly, not silently fall back to the built-in prompt as if nothing were set.
func TestLearn_PromptAddendumMissingFileIsReported(t *testing.T) {
	exampleDir := writeExampleFolder(t)
	t.Setenv(learn.EnvOpenAIAPIKey, "sk-oai-test")
	t.Setenv(learn.EnvAnthropicAPIKey, "")

	_, err := run(t, newLearnCommand, exampleDir, "--output="+t.TempDir(),
		"--prompt-addendum="+filepath.Join(t.TempDir(), "does-not-exist.md"))
	if err == nil || !strings.Contains(err.Error(), "prompt-addendum") {
		t.Fatalf("expected a missing --prompt-addendum file to be reported, got %v", err)
	}
}

// parseLearnArgs must read the addendum file's actual content (not just resolve a path) into
// learnArgs.promptAddendum, ready to hand straight to learn.ResolveClient.
func TestParseLearnArgs_ReadsPromptAddendumFileContent(t *testing.T) {
	addendum := filepath.Join(t.TempDir(), "addendum.md")
	if err := os.WriteFile(addendum, []byte("PROJECT RULE"), 0o644); err != nil {
		t.Fatalf("writing addendum fixture: %v", err)
	}

	args := mustParseArgs(t, []string{"some-example", "--output=out", "--prompt-addendum=" + addendum})
	la, err := parseLearnArgs(args)
	if err != nil {
		t.Fatalf("parseLearnArgs returned error: %v", err)
	}
	if la.promptAddendum != "PROJECT RULE" {
		t.Errorf("expected the addendum file's content, got %q", la.promptAddendum)
	}
}

// No --prompt-addendum and no config value must resolve to "", not an error.
func TestParseLearnArgs_PromptAddendumEmptyWhenNotConfigured(t *testing.T) {
	args := mustParseArgs(t, []string{"some-example", "--output=out"})
	la, err := parseLearnArgs(args)
	if err != nil {
		t.Fatalf("parseLearnArgs returned error: %v", err)
	}
	if la.promptAddendum != "" {
		t.Errorf("expected no addendum, got %q", la.promptAddendum)
	}
}

// A bare --prompt-addendum (no "=") is the same footgun #93 fixed for --output/--scaffolding-code
// - it must be rejected, not silently resolved to the literal path "true".
func TestParseLearnArgs_RejectsBarePromptAddendum(t *testing.T) {
	args := mustParseArgs(t, []string{"some-example", "--output=out", "--prompt-addendum", "some-file.md"})
	if _, err := parseLearnArgs(args); err == nil {
		t.Fatal("expected a bare --prompt-addendum to be rejected")
	}
}

// No provider env var set: must fail resolving the provider, not fail on flag validation, so an
// unrecognized --response-format value is still reachable and reported clearly.
func TestLearn_UnknownResponseFormatRejected(t *testing.T) {
	t.Setenv(learn.EnvOpenAIAPIKey, "sk-oai-test")
	t.Setenv(learn.EnvAnthropicAPIKey, "")

	exampleDir := writeExampleFolder(t)
	_, err := run(t, newLearnCommand, exampleDir, "--output="+t.TempDir(), "--response-format=bogus")
	if err == nil || !strings.Contains(err.Error(), "unknown --response-format") {
		t.Fatalf("expected an unknown --response-format value to be rejected, got %v", err)
	}
}

func TestLearn_DraftMalformedJSONSurfacesError(t *testing.T) {
	exampleDir := writeExampleFolder(t)
	outDir := filepath.Join(t.TempDir(), "draft")
	draftPath := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(draftPath, []byte("not json"), 0o644); err != nil {
		t.Fatalf("writing bad draft fixture: %v", err)
	}

	_, err := run(t, newLearnCommand, exampleDir, "--output="+outDir, "--draft="+draftPath)
	if err == nil {
		t.Fatal("expected a malformed --draft JSON to surface an error")
	}
}

func writeExampleFolder(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "WidgetController.java", "class WidgetController {}\n")
	return dir
}
