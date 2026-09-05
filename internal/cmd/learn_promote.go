package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"scaffold-engine-go/internal/learn"
)

func newLearnPromoteCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "learn-promote <draft-dir>",
		Short: "Approve a `scaffold learn` draft so create/list/lint can use it",
		Long: "scaffold learn-promote <draft-dir>\n\n" +
			"Clears the `candidate:` flag `scaffold learn` set on the draft jig.yaml at\n" +
			"<draft-dir>, so create/list/lint stop refusing to use it. Run `scaffold learn-review`\n" +
			"first (or review it by hand in an editor) - promote is the approval step itself and\n" +
			"performs no checks of its own beyond confirming the draft is still a candidate.\n\n" +
			"Edits jig.yaml in place, preserving any comments or formatting already there, and\n" +
			"clearing only the one key.",
		DisableFlagParsing: true,
		RunE:               runLearnPromote,
	}
}

func runLearnPromote(cmd *cobra.Command, rawArgs []string) error {
	args, err := parseArgs(rawArgs)
	if err != nil {
		return err
	}
	if args.help {
		return cmd.Help()
	}
	if len(args.positional) != 1 {
		return fmt.Errorf("learn-promote takes exactly one positional argument: the draft directory")
	}
	if err := args.requireAllFlagsConsumed(nil); err != nil {
		return err
	}

	draftDir := args.positional[0]
	if err := learn.Promote(draftDir); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Promoted %s - now usable by create/list/lint.\n", draftDir)
	return nil
}
