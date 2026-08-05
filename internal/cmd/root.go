// Package cmd implements the scaffold CLI's command tree: a positional, kubectl-style grammar -
// `scaffold create <scaffold> <template> <name> [--flag=value ...]` and
// `scaffold list [<scaffold>] [<template>]` - with everything inside the flags fully dynamic.
package cmd

import (
	"github.com/spf13/cobra"
)

const defaultScaffoldingCodeRoot = "./scaffolding-code"

// Version is the engine version, reported by `scaffold --version`, so a binary can be matched
// against the scaffolding-code it reads since the two are distributed separately.
const Version = "0.2.0-dev"

// Execute builds and runs the cobra command tree.
func Execute() error {
	root := &cobra.Command{
		Use:     "scaffold",
		Short:   "Universal scaffolding engine (PRD v2.0)",
		Version: Version,
		Long: "A universal, dynamically-extensible scaffolding engine. Scaffolds, versions, " +
			"dimensions, templates, selector values, and even the CLI flag names themselves are " +
			"all declared by jigs under scaffolding-code/ at runtime - none are hardcoded " +
			"(PRD Section 13.1).",
		SilenceErrors: true, // main() prints the error; without this cobra prints it too
		SilenceUsage:  true, // a runtime failure is not a usage problem
	}
	root.AddCommand(newListCommand(), newCreateCommand(), newLintCommand())
	return root.Execute()
}
