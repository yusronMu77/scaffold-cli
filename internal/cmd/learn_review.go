package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"scaffold-engine-go/internal/learn"
)

func newLearnReviewCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "learn-review <draft-dir> <example-dir>",
		Short: "Check a `scaffold learn` draft against its original example, with zero AI calls",
		Long: "scaffold learn-review <draft-dir> <example-dir>\n\n" +
			"Renders the draft jig.yaml at <draft-dir> using only its own declared `default:`\n" +
			"values - the same way `create` would - then compares the result byte-for-byte\n" +
			"against <example-dir>, the folder `scaffold learn` originally scanned. A correct\n" +
			"draft's own defaults must reproduce the example exactly, so any difference is a\n" +
			"concrete, mechanically-detected sign of over- or under-generalization, with no\n" +
			"second model call. Runnable by a human before hand-editing the draft, or by an AI\n" +
			"agent as a self-review pass before promoting it.\n\n" +
			"Exit code is non-zero if any issue is found.",
		DisableFlagParsing: true,
		RunE:               runLearnReview,
	}
}

func runLearnReview(cmd *cobra.Command, rawArgs []string) error {
	args, err := parseArgs(rawArgs)
	if err != nil {
		return err
	}
	if args.help {
		return cmd.Help()
	}
	if len(args.positional) != 2 {
		return fmt.Errorf("learn-review takes exactly two positional arguments: the draft " +
			"directory and the original example directory")
	}
	if err := args.requireAllFlagsConsumed(nil); err != nil {
		return err
	}

	draftDir, exampleDir := args.positional[0], args.positional[1]
	result, err := learn.Review(draftDir, exampleDir)
	if err != nil {
		return err
	}

	printReviewResult(cmd.OutOrStdout(), draftDir, exampleDir, result)
	if !result.Clean() {
		return fmt.Errorf("review found %d issue(s) - see above", result.IssueCount())
	}
	return nil
}

// printReviewResult reports a ReviewResult in the same plain text either a human or an AI agent
// reads to decide whether to edit the draft further or promote it.
func printReviewResult(out io.Writer, draftDir, exampleDir string, r *learn.ReviewResult) {
	if r.Clean() {
		fmt.Fprintf(out, "OK - rendering %s with its own defaults reproduces %s exactly (nothing "+
			"to flag).\nRun `scaffold learn-promote %s` to approve it.\n", draftDir, exampleDir, draftDir)
		return
	}

	fmt.Fprintf(out, "%d issue(s) found comparing %s's render against %s:\n",
		r.IssueCount(), draftDir, exampleDir)

	if len(r.Missing) > 0 {
		fmt.Fprintln(out, "\nMissing (in the example, not in the draft's render):")
		for _, p := range r.Missing {
			fmt.Fprintf(out, "  %s\n", p)
		}
	}
	if len(r.Extra) > 0 {
		fmt.Fprintln(out, "\nExtra (in the draft's render, not in the example):")
		for _, p := range r.Extra {
			fmt.Fprintf(out, "  %s\n", p)
		}
	}
	if len(r.Mismatched) > 0 {
		fmt.Fprintf(out, "\nContent mismatch (%d file(s)):\n", len(r.Mismatched))
		for _, d := range r.Mismatched {
			fmt.Fprintf(out, "  %s (first differs at line %d)\n", d.Path, d.Line)
			fmt.Fprintln(out, "    example:")
			for _, l := range d.Example {
				fmt.Fprintf(out, "      %s\n", l)
			}
			fmt.Fprintln(out, "    rendered:")
			for _, l := range d.Rendered {
				fmt.Fprintf(out, "      %s\n", l)
			}
		}
	}

	fmt.Fprintf(out, "\nThis suggests the draft over- or under-generalized - fix jig.yaml/files "+
		"under %s, or re-run `scaffold learn` on a cleaner example.\n", draftDir)
}
