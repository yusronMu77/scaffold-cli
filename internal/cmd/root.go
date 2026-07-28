// Package cmd implements the scaffold CLI's command tree (PRD v1.5/v1.6/v1.8): a positional,
// kubectl-style grammar - `scaffold create <framework> <category> <name> [--flag=value ...]`
// and `scaffold list [<framework>] [<category>]` - fixed per fundamental rule #5 (Section
// 13.1), with everything inside the flags fully dynamic and never hardcoded (rule #2).
package cmd

import (
	"github.com/spf13/cobra"
)

const defaultScaffoldingCodeRoot = "./scaffolding-code"

// Version is the engine version, reported by `scaffold --version`. It exists so a binary can be
// matched against the scaffolding-code it reads (implementation-plan Open Question #3), which
// matters as soon as the two are distributed separately.
const Version = "0.1.0-dev (Phase 3a)"

// Execute builds and runs the cobra command tree.
func Execute() error {
	root := &cobra.Command{
		Use:     "scaffold",
		Short:   "Universal scaffolding engine (PRD v1.8)",
		Version: Version,
		Long: "A universal, dynamically-extensible scaffolding engine. Frameworks, versions, " +
			"axes, categories, selector values, and even the CLI flag names themselves are all " +
			"declared by manifests under scaffolding-code/ at runtime - none are hardcoded " +
			"(PRD Section 13.1).",
		SilenceErrors: true, // main() prints the error; without this cobra prints it too
		SilenceUsage:  true, // a runtime failure is not a usage problem
	}
	root.AddCommand(newListCommand(), newCreateCommand())
	return root.Execute()
}
