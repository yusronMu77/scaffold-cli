package cmd

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"scaffold-engine-go/internal/learn"
)

// multiExampleCapturingInferer records the exact files it was called with, so a test can assert on
// ExampleIndex/Path directly rather than trusting the CLI's own report.
type multiExampleCapturingInferer struct {
	draft    *learn.Draft
	gotFiles []learn.SourceFile
}

func (c *multiExampleCapturingInferer) Infer(ctx context.Context, files []learn.SourceFile) (*learn.Draft, error) {
	c.gotFiles = files
	return c.draft, nil
}

func TestLearnMultiExample_StampsExampleIndexAndKeepsPathsUnprefixed(t *testing.T) {
	example1 := t.TempDir()
	writeFile(t, example1, "Widget.java", "class Widget {}\n")
	example2 := t.TempDir()
	writeFile(t, example2, "Widget.java", "class Gadget {}\n")

	client := &multiExampleCapturingInferer{draft: &learn.Draft{
		Name:  "widget",
		Files: []learn.DraftFile{{Path: "{{ .ClassName }}.java", Content: "class {{ .ClassName }} {}\n"}},
	}}

	outDir := filepath.Join(t.TempDir(), "draft")
	cmd := newLearnCommand()
	var buf strings.Builder
	cmd.SetOut(&buf)
	if err := runLearnWithClientMultiExample(cmd, []string{example1, example2}, outDir, client, false); err != nil {
		t.Fatalf("runLearnWithClientMultiExample returned error: %v", err)
	}

	if len(client.gotFiles) != 2 {
		t.Fatalf("expected 2 files (one per example) to reach the provider, got %d: %+v",
			len(client.gotFiles), client.gotFiles)
	}
	byIndex := map[int]learn.SourceFile{}
	for _, f := range client.gotFiles {
		byIndex[f.ExampleIndex] = f
	}
	for _, idx := range []int{1, 2} {
		f, ok := byIndex[idx]
		if !ok {
			t.Fatalf("expected a file stamped ExampleIndex=%d, got %+v", idx, client.gotFiles)
		}
		if f.Path != "Widget.java" {
			t.Errorf("expected Path to stay unprefixed (\"Widget.java\"), got %q", f.Path)
		}
	}
}

func TestLearnMultiExample_AggregateByteBudgetExceeded(t *testing.T) {
	// Each example individually stays under learn.TotalMaxBytes on its own (Scan would accept it
	// standalone), but the two combined exceed it - the aggregate check this test exists for.
	perExample := learn.TotalMaxBytes*2/3 + 100
	example1 := t.TempDir()
	writeFile(t, example1, "a.txt", strings.Repeat("a", perExample))
	example2 := t.TempDir()
	writeFile(t, example2, "b.txt", strings.Repeat("b", perExample))

	client := &multiExampleCapturingInferer{draft: &learn.Draft{
		Name:  "x",
		Files: []learn.DraftFile{{Path: "x.txt", Content: "x\n"}},
	}}
	outDir := filepath.Join(t.TempDir(), "draft")
	cmd := newLearnCommand()

	err := runLearnWithClientMultiExample(cmd, []string{example1, example2}, outDir, client, false)
	if err == nil {
		t.Fatal("expected the combined examples to exceed the aggregate byte budget, got nil")
	}
	if !strings.Contains(err.Error(), strconv.Itoa(learn.TotalMaxBytes)) {
		t.Errorf("expected the error to name the byte limit, got: %v", err)
	}
	if client.gotFiles != nil {
		t.Error("expected the aggregate budget check to reject before ever calling Infer")
	}
}

// The CLI-arg-parsing layer must accept 2+ positionals as a legitimate invocation (not "learn
// takes at least one positional") - proven the same way TestLearnMatch_* proves a match fires:
// with no API key set, the run must fail at ResolveClient specifically, not at argument parsing.
func TestLearnMultiExample_CLIAcceptsTwoPositionals(t *testing.T) {
	t.Setenv(learn.EnvAnthropicAPIKey, "")
	t.Setenv(learn.EnvOpenAIAPIKey, "")

	example1 := writeExampleFolder(t)
	example2 := writeExampleFolder(t)
	_, err := run(t, newLearnCommand, example1, example2, "--output="+t.TempDir())
	if err == nil {
		t.Fatal("expected an error since no provider is configured")
	}
	if strings.Contains(err.Error(), "at least one positional") {
		t.Errorf("expected two positionals to be accepted as multi-example, not rejected as a bad arg count: %v", err)
	}
	if !strings.Contains(err.Error(), "no LLM provider configured") {
		t.Errorf("expected the failure to come from provider resolution, got: %v", err)
	}
}
