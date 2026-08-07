package cmd

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"scaffold-engine-go/internal/discovery"
	"scaffold-engine-go/internal/jig"
)

func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list [<scaffold>] [<template>] [--flag=value ...]",
		Short: "List available scaffolds, versions, dimensions, and templates",
		Long: "scaffold list                     -> known scaffolds\n" +
			"scaffold list <scaffold>          -> available versions, templates, and optional overlays\n" +
			"scaffold list <scaffold> <template> -> full selector tree for that template",
		DisableFlagParsing: true,
		RunE:               runList,
	}
}

func runList(cmd *cobra.Command, rawArgs []string) error {
	args, err := parseArgs(rawArgs)
	if err != nil {
		return err
	}
	if args.help {
		return cmd.Help()
	}
	// A values file works here too, so the same file that drives `create` can browse what it
	// would select. Keys the file carries that `list` has no use for are marked consumed rather
	// than rejected, since a typo in the file is still caught later by `create`.
	if len(args.valuesFiles) > 0 {
		values, data, err := loadValuesFiles(args.valuesFiles)
		if err != nil {
			return err
		}
		args.data = data
		for k, v := range values {
			if _, fromCLI := args.flags[k]; !fromCLI {
				args.flags[k] = v
			}
			args.markConsumed(k)
		}
		for _, key := range []string{keyScaffold, keyTemplate} {
			if len(args.positional) < 2 && args.flags[key] != "" {
				args.positional = append(args.positional, args.flags[key])
			}
		}
	}
	if len(args.positional) > 2 {
		return fmt.Errorf("usage: scaffold list [<scaffold>] [<template>]\n"+
			"got %d positional arguments, at most 2 are accepted", len(args.positional))
	}

	scaffoldingCodeRoot := resolveScaffoldingCodeRoot(args.value("scaffolding-code"))
	out := cmd.OutOrStdout()

	if len(args.positional) == 0 {
		args.markConsumed(engineFlags...)
		if err := args.requireAllFlagsConsumed([]string{"scaffolding-code"}); err != nil {
			return err
		}
		return listScaffolds(out, scaffoldingCodeRoot)
	}

	scaffold := args.positional[0]
	scaffoldPath, err := discovery.ResolveScaffoldPath(scaffoldingCodeRoot, scaffold)
	if err != nil {
		return err
	}
	versionFlag, err := discovery.VersionSelectorFlag(scaffoldPath)
	if err != nil {
		return err
	}
	version, err := discovery.ResolveVersion(scaffoldPath, args.value(versionFlag))
	if err != nil {
		return fmt.Errorf("resolving version for scaffold %q: %w", scaffold, err)
	}
	versionPath := filepath.Join(scaffoldPath, version)

	args.markConsumed(engineFlags...)
	if len(args.positional) == 1 {
		if err := args.requireAllFlagsConsumed([]string{versionFlag, "scaffolding-code"}); err != nil {
			return err
		}
		return listScaffoldDetail(out, args, scaffold, scaffoldPath, version, versionPath)
	}

	// With a template given, selector flags narrow which leaf's variables to show; which ones are
	// valid is only known after resolving the chain, so the unknown-flag check happens further down.

	structure, err := discovery.ResolveVersionStructure([]string{versionPath})
	if err != nil {
		return fmt.Errorf("discovering structure for %s %s: %w", scaffold, version, err)
	}
	if structure.Leaf != nil {
		return fmt.Errorf("%s %s has no templates dimension - it is itself the template, "+
			"so <template> must be omitted (run `scaffold list %s`)", scaffold, version, scaffold)
	}
	baseDimension, err := discovery.RequiredDimension(structure.Dimensions)
	if err != nil {
		return fmt.Errorf("%s %s: %w", scaffold, version, err)
	}
	templatesPath := baseDimension.Path(versionPath)

	template := args.positional[1]
	templateDir, err := discovery.ResolveTemplateDir(templatesPath, template)
	if err != nil {
		return err
	}
	tree, err := discovery.DescribeTree(templatesPath, templateDir)
	if err != nil {
		return fmt.Errorf("describing template %q: %w", template, err)
	}
	fmt.Fprintf(out, "%s %s %s/%s:\n", scaffold, version, baseDimension.Name, template)
	printTree(out, tree, "  ")

	// Resolve the same plan `create` would build, so the variable list here can never drift from
	// what `create` actually accepts.
	return printVariables(out, args, scaffoldingCodeRoot, scaffold, template)
}

// printVariables resolves the chain the way `create` would and lists the variables it declares.
// Failure is silent on purpose - `list` is a browsing command, so an unresolved chain still shows
// the tree above instead of an error.
func printVariables(out io.Writer, args *parsedArgs, root, scaffold, template string) error {
	probe := &parsedArgs{flags: args.flags, consumed: map[string]bool{}}
	p, err := resolvePlan(probe, root, scaffold, template, "<name>")
	if err != nil {
		// Usually just means a selector with no default is unset; say which flag would resolve
		// it rather than printing nothing.
		fmt.Fprintf(out, "\nPick a leaf to see the variables it declares:\n  %s\n", err)
		return nil
	}

	// Now that the plan is resolved, the valid flag set is known - so a typo can finally be caught
	// here too, instead of `list` accepting anything.
	probe.markConsumed(engineFlags...)
	if err := probe.requireAllFlagsConsumed(validFlagsFor(p.Dimensions, p.Walk, p.Manifests)); err != nil {
		return err
	}

	vars := declaredVariables(p)
	if len(vars) == 0 {
		return nil
	}

	if template != "" {
		fmt.Fprintf(out, "\nVariables for %s", template)
	} else {
		fmt.Fprint(out, "\nVariables")
	}
	if len(p.Walk.Steps) > 0 {
		for _, s := range p.Walk.Steps {
			fmt.Fprintf(out, " --%s=%s", s.Flag, s.Value)
			if s.Defaulted {
				fmt.Fprint(out, "(default)")
			}
		}
	}
	fmt.Fprintln(out, ":")

	for _, v := range vars {
		required := ""
		if v.Required && v.Default == "" {
			required = "  (required)"
		}
		def := v.Default
		if def == "" && !v.Required {
			def = "-"
		}
		fmt.Fprintf(out, "  --%-22s %-22s from %s%s\n", v.Flag, def, v.From, required)
		if v.Prompt != "" {
			fmt.Fprintf(out, "  %-24s %s\n", "", v.Prompt)
		}
	}
	return nil
}

func listScaffolds(out io.Writer, root string) error {
	rootJig, err := jig.LoadRoot(filepath.Join(root, jig.FileName))
	if err != nil {
		return fmt.Errorf("loading scaffold registry: %w", err)
	}
	fmt.Fprintln(out, "Available scaffolds:")
	for _, s := range rootJig.Values {
		if s.Description != "" {
			fmt.Fprintf(out, "  %s - %s\n", s.Name, s.Description)
		} else {
			fmt.Fprintf(out, "  %s\n", s.Name)
		}
	}
	return nil
}

// listScaffoldDetail prints versions, then the templates of the required base dimension, then the
// optional overlays.
func listScaffoldDetail(out io.Writer, args *parsedArgs, scaffold, scaffoldPath, version, versionPath string) error {
	fmt.Fprintf(out, "%s:\n", scaffold)

	fmt.Fprintln(out, "  versions:")
	if m, err := jig.LoadOptional(filepath.Join(scaffoldPath, jig.FileName)); err != nil {
		return fmt.Errorf("reading version registry: %w", err)
	} else if m != nil && len(m.Values) > 0 {
		for _, v := range m.Values {
			marker := ""
			if v.Default {
				marker = " (default)"
			}
			if v.Description != "" {
				fmt.Fprintf(out, "    %s%s - %s\n", v.Name, marker, v.Description)
			} else {
				fmt.Fprintf(out, "    %s%s\n", v.Name, marker)
			}
		}
	} else {
		fmt.Fprintf(out, "    %s (no version registry; resolved from folders)\n", version)
	}
	fmt.Fprintf(out, "  resolved version: %s\n", version)

	structure, err := discovery.ResolveVersionStructure([]string{versionPath})
	if err != nil {
		return err
	}
	if structure.Leaf != nil {
		fmt.Fprintln(out, "  no templates dimension - this version is itself the template; "+
			"omit <template> (variables shown below)")
		return printVariables(out, &parsedArgs{flags: args.flags, consumed: map[string]bool{}},
			filepath.Dir(scaffoldPath), scaffold, "")
	}

	dimensions := structure.Dimensions
	baseDimension, err := discovery.RequiredDimension(dimensions)
	if err != nil {
		return fmt.Errorf("%s %s: %w", scaffold, version, err)
	}

	fmt.Fprintf(out, "  templates (positional <template>, from the %q dimension): %s\n",
		baseDimension.Name, strings.Join(baseDimension.Values, ", "))

	fmt.Fprintln(out, "  optional overlays:")
	any := false
	for _, d := range dimensions {
		if d.Required {
			continue
		}
		any = true
		desc := ""
		if d.Description != "" {
			desc = " - " + d.Description
		}
		fmt.Fprintf(out, "    --%s%s: %s\n", d.Flag, desc, strings.Join(d.Values, ", "))
	}
	if !any {
		fmt.Fprintln(out, "    (none)")
	}
	return nil
}

func printTree(out io.Writer, node *discovery.TreeNode, indent string) {
	if node.IsLeaf {
		fmt.Fprintf(out, "%s%s (leaf)\n", indent, node.Value)
		return
	}
	fmt.Fprintf(out, "%s%s --%s:\n", indent, node.Value, node.Selector)
	for i := range node.Children {
		printTree(out, &node.Children[i], indent+"  ")
	}
}
