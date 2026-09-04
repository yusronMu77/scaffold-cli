package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"scaffold-engine-go/internal/learn"
)

// learnFlags are the flags `learn` itself owns - it has no dimension/variable flags to resolve,
// since it isn't rendering an existing template.
var learnFlags = []string{"output", "provider", "model", "base-url"}

func newLearnCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "learn <path> --output=<dir> [--provider=anthropic|openai] [--model=...] [--base-url=...]",
		Short: "Draft a jig.yaml + templated files from one existing example folder",
		Long: "scaffold learn <path> --output=<dir>\n\n" +
			"Scans the example folder at <path>, calls an LLM once to separate invariant\n" +
			"structure from variable names/paths/fields, and writes the result to --output as a\n" +
			"draft jig.yaml plus templated files - a candidate, not yet a live template.\n" +
			"Regenerating afterward goes through the existing, fully deterministic `create` path:\n" +
			"zero further AI calls.\n\n" +
			"Provider is chosen by --provider=anthropic|openai, or auto-detected from whichever of\n" +
			"ANTHROPIC_API_KEY / OPENAI_API_KEY is set. --base-url points the openai provider at\n" +
			"any compatible endpoint (Groq, OpenRouter, a local server, ...).",
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

	path, outputDir, provider, model, baseURL, err := parseLearnArgs(args)
	if err != nil {
		return err
	}

	client, err := learn.ResolveClient(provider, model, baseURL)
	if err != nil {
		return err
	}

	return runLearnWithClient(cmd, path, outputDir, client)
}

// parseLearnArgs validates learn's positional/flag shape, kept separate from provider resolution
// so tests can exercise argument errors without any provider env var set.
func parseLearnArgs(args *parsedArgs) (path, outputDir, provider, model, baseURL string, err error) {
	if len(args.positional) != 1 {
		return "", "", "", "", "", fmt.Errorf(
			"learn takes exactly one positional argument: the example folder to learn from")
	}
	path = args.positional[0]
	outputDir = args.value("output")
	provider = args.value("provider")
	model = args.value("model")
	baseURL = args.value("base-url")

	if err := args.requireAllFlagsConsumed(learnFlags); err != nil {
		return "", "", "", "", "", err
	}
	if outputDir == "" {
		return "", "", "", "", "", fmt.Errorf(
			"--output is required for learn: a draft must not land somewhere create/list/lint " +
				"would discover it before it has been reviewed")
	}
	return path, outputDir, provider, model, baseURL, nil
}

// runLearnWithClient does the actual scan/infer/write, taking an already-resolved Inferer so
// tests can inject a fake one and never touch the network.
func runLearnWithClient(cmd *cobra.Command, path, outputDir string, client learn.Inferer) error {
	files, err := learn.Scan(path)
	if err != nil {
		return err
	}

	draft, err := client.Infer(context.Background(), files)
	if err != nil {
		return err
	}

	if err := learn.WriteDraft(outputDir, draft); err != nil {
		return err
	}

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
	return nil
}
