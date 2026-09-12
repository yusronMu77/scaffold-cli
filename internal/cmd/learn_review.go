package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"scaffold-engine-go/internal/learn"
)

func newLearnReviewCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "learn-review <draft-dir> <example-dir> [<example-dir2> ...]",
		Short: "Check a `scaffold learn` draft against its original example(s), with zero AI calls",
		Long: "scaffold learn-review <draft-dir> <example-dir> [<example-dir2> ...]\n\n" +
			"Renders the draft jig.yaml at <draft-dir> using only its own declared `default:`\n" +
			"values - the same way `create` would - then compares the result byte-for-byte\n" +
			"against <example-dir>, the first folder `scaffold learn` originally scanned. A\n" +
			"correct draft's own defaults must reproduce that first example exactly, so any\n" +
			"difference is a concrete, mechanically-detected sign of over- or under-\n" +
			"generalization, with no second model call.\n\n" +
			"Given a multi-example `learn` run, pass every example directory in the same order:\n" +
			"only <example-dir> (the first) is checked byte-for-byte - a later example may\n" +
			"legitimately have different values, since a variable's default is always drawn\n" +
			"from the first example specifically - but each later <example-dirN> is still\n" +
			"checked structurally: it must have the same set of files as the draft's render.\n\n" +
			"Runnable by a human before hand-editing the draft, or by an AI agent as a\n" +
			"self-review pass before promoting it.\n\n" +
			"`.Name` (the <name> positional `create` supplies on the command line) has no\n" +
			"`default:` of its own to draw from here, so it renders as <example-dir>'s own\n" +
			"basename instead - declare an explicit variable for anything that needs a real\n" +
			"default during review.\n\n" +
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
	if len(args.positional) < 2 {
		return fmt.Errorf("learn-review takes at least two positional arguments: the draft " +
			"directory and the original example directory (plus any further example directories " +
			"from a multi-example learn run)")
	}
	if err := args.requireAllFlagsConsumed(nil); err != nil {
		return err
	}

	draftDir, exampleDir := args.positional[0], args.positional[1]
	result, err := learn.Review(draftDir, exampleDir, args.positional[2:]...)
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
	for _, e := range r.ExtraExamples {
		fmt.Fprintf(out, "\nStructural mismatch against %s (content not compared - only "+
			"%s is checked byte-for-byte):\n", e.Dir, exampleDir)
		for _, p := range e.Missing {
			fmt.Fprintf(out, "  missing: %s\n", p)
		}
		for _, p := range e.Extra {
			fmt.Fprintf(out, "  extra:   %s\n", p)
		}
	}

	fmt.Fprintf(out, "\nThis suggests the draft over- or under-generalized - fix jig.yaml/files "+
		"under %s, or re-run `scaffold learn` on a cleaner example.\n", draftDir)
}
