package cmd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"scaffold-engine-go/internal/learn"
)

// capturingInferer records the exact files it was called with, so a test can assert on what
// actually reached the "provider" - proving redaction ran before Infer, not just that the CLI
// printed a report claiming it did.
type capturingInferer struct {
	draft    *learn.Draft
	gotFiles []learn.SourceFile
}

func (c *capturingInferer) Infer(ctx context.Context, files []learn.SourceFile) (*learn.Draft, error) {
	c.gotFiles = files
	return c.draft, nil
}

func TestLearn_RedactsSecretValueBeforeItReachesTheProvider(t *testing.T) {
	exampleDir := t.TempDir()
	const secret = "AKIAIOSFODNN7EXAMPLE"
	writeFile(t, exampleDir, "Config.java",
		"class Config {\n  String awsKey = \""+secret+"\";\n}\n")

	client := &capturingInferer{draft: &learn.Draft{
		Name:  "config",
		Files: []learn.DraftFile{{Path: "x.txt", Content: "x\n"}},
	}}

	outDir := filepath.Join(t.TempDir(), "draft")
	cmd := newLearnCommand()
	var buf strings.Builder
	cmd.SetOut(&buf)
	if err := runLearnWithClient(cmd, exampleDir, outDir, client, false); err != nil {
		t.Fatalf("runLearnWithClient returned error: %v", err)
	}

	if len(client.gotFiles) != 1 {
		t.Fatalf("expected exactly one file to reach the provider, got %d", len(client.gotFiles))
	}
	if strings.Contains(client.gotFiles[0].Content, secret) {
		t.Fatalf("expected the secret to be redacted before reaching the provider, got:\n%s",
			client.gotFiles[0].Content)
	}

	out := buf.String()
	if !strings.Contains(out, "Redacted") || !strings.Contains(out, "Config.java") {
		t.Errorf("expected a redaction report naming the file, got:\n%s", out)
	}
	if strings.Contains(out, secret) {
		t.Errorf("expected the report to never print the actual secret value, got:\n%s", out)
	}
}
