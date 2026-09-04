package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"scaffold-engine-go/internal/learn"
)

// learnFlags are the flags `learn` itself owns - it has no dimension/variable flags to resolve,
// since it isn't rendering an existing template.
var learnFlags = []string{"output", "provider", "model", "base-url", "draft"}

func newLearnCommand() *cobra.Command {
	return &cobra.Command{
		Use: "learn <path> --output=<dir> " +
			"[--provider=anthropic|openai] [--model=...] [--base-url=...] [--draft=<path|->]",
		Short: "Draft a jig.yaml + templated files from one existing example folder",
		Long: "scaffold learn <path> --output=<dir>\n\n" +
			"Scans the example folder at <path>, calls an LLM once to separate invariant\n" +
			"structure from variable names/paths/fields, and writes the result to --output as a\n" +
			"draft jig.yaml plus templated files - a candidate, not yet a live template.\n" +
			"Regenerating afterward goes through the existing, fully deterministic `create` path:\n" +
			"zero further AI calls.\n\n" +
			"Provider is chosen by --provider=anthropic|openai, or auto-detected from whichever of\n" +
			"ANTHROPIC_API_KEY / OPENAI_API_KEY is set. --base-url points the openai provider at\n" +
			"any compatible endpoint (Groq, OpenRouter, a local server, ...).\n\n" +
			"An AI agent invoking this command is already an LLM - rather than pay for a second,\n" +
			"separately-billed model call, it can do the invariant/variable separation itself and\n" +
			"pass the result straight through with --draft=<path|->, skipping any provider call and\n" +
			"any API key entirely.",
		DisableFlagParsing: true,
		RunE:               runLearn,
	}
}

func runLearn(cmd *cobra.Command, rawArgs []string) error {
	args, err := parseArgs(rawArgs)
	if err != nil {
		return err
	}
	if args.help {
		return cmd.Help()
	}

	learnArgs, err := parseLearnArgs(args)
	if err != nil {
		return err
	}

	if learnArgs.draftPath != "" {
		raw, err := readDraftInput(cmd, learnArgs.draftPath)
		if err != nil {
			return err
		}
		return runLearnWithDraftJSON(cmd, learnArgs.outputDir, raw)
	}

	client, err := learn.ResolveClient(learnArgs.provider, learnArgs.model, learnArgs.baseURL)
	if err != nil {
		return err
	}
	return runLearnWithClient(cmd, learnArgs.path, learnArgs.outputDir, client)
}

type learnArgs struct {
	path, outputDir, provider, model, baseURL, draftPath string
}

// parseLearnArgs validates learn's positional/flag shape, kept separate from provider resolution
// so tests can exercise argument errors without any provider env var set.
func parseLearnArgs(args *parsedArgs) (learnArgs, error) {
	if len(args.positional) != 1 {
		return learnArgs{}, fmt.Errorf(
			"learn takes exactly one positional argument: the example folder to learn from")
	}
	la := learnArgs{
		path:      args.positional[0],
		outputDir: args.value("output"),
		provider:  args.value("provider"),
		model:     args.value("model"),
		baseURL:   args.value("base-url"),
		draftPath: args.value("draft"),
	}

	if err := args.requireAllFlagsConsumed(learnFlags); err != nil {
		return learnArgs{}, err
	}
	if la.outputDir == "" {
		return learnArgs{}, fmt.Errorf(
			"--output is required for learn: a draft must not land somewhere create/list/lint " +
				"would discover it before it has been reviewed")
	}
	if la.draftPath != "" && (la.provider != "" || la.model != "" || la.baseURL != "") {
		return learnArgs{}, fmt.Errorf(
			"--draft supplies an already-reasoned draft directly, so --provider/--model/--base-url " +
				"(which pick a provider to call) don't apply together with it")
	}
	return la, nil
}

// readDraftInput reads a draft's JSON from a file, or from stdin when path is "-".
func readDraftInput(cmd *cobra.Command, path string) ([]byte, error) {
	if path == "-" {
		raw, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return nil, fmt.Errorf("reading draft JSON from stdin: %w", err)
		}
		return raw, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading draft JSON from %s: %w", path, err)
	}
	return raw, nil
}

// runLearnWithDraftJSON writes an already-reasoned draft (see --draft) with no provider call at
// all - the caller (typically an AI agent) already did the invariant/variable separation itself.
func runLearnWithDraftJSON(cmd *cobra.Command, outputDir string, raw []byte) error {
	draft, err := learn.ParseDraft(raw)
	if err != nil {
		return err
	}
	if err := learn.WriteDraft(outputDir, draft); err != nil {
		return err
	}
	reportDraft(cmd, outputDir, draft)
	return nil
}

// runLearnWithClient does the actual scan/infer/write, taking an already-resolved Inferer so
// tests can inject a fake one and never touch the network.
func runLearnWithClient(cmd *cobra.Command, path, outputDir string, client learn.Inferer) error {
	files, skipped, err := learn.Scan(path)
	if err != nil {
		return err
	}
	// Said before the call, not after: the point is the user knows what did and didn't leave the
	// machine, and the call is what sends it.
	if len(skipped) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(),
			"Skipped %d credential file(s) - not sent to the provider:\n", len(skipped))
		for _, s := range skipped {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", s)
		}
	}

	draft, err := client.Infer(context.Background(), files)
	if err != nil {
		return err
	}

	if err := learn.WriteDraft(outputDir, draft); err != nil {
		return err
	}
	reportDraft(cmd, outputDir, draft)
	return nil
}

// reportDraft prints the same summary regardless of how the draft was produced (a provider call
// or an agent-supplied --draft).
func reportDraft(cmd *cobra.Command, outputDir string, draft *learn.Draft) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Learned %q into %s (draft - review before use)\n", draft.Name, outputDir)
	if len(draft.Variables) > 0 {
		fmt.Fprintln(out, "\nVariables:")
		for _, v := range draft.Variables {
			fmt.Fprintf(out, "  %-20s default=%q\n", v.Name, v.Default)
		}
	}
	fmt.Fprintf(out, "\n%d file(s) written:\n", len(draft.Files))
	for _, f := range draft.Files {
		fmt.Fprintf(out, "  %s\n", f.Path)
	}
}
