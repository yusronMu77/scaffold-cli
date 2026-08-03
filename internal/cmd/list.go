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
		Use:   "list [<framework>] [<category>] [--flag=value ...]",
		Short: "List available frameworks, versions, axes, and categories",
		Long: "scaffold list                       -> known frameworks\n" +
			"scaffold list <framework>           -> available versions, categories, and optional axes\n" +
			"scaffold list <framework> <category> -> full selector tree for that category",
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
		for _, key := range []string{keyFramework, keyCategory} {
			if len(args.positional) < 2 && args.flags[key] != "" {
				args.positional = append(args.positional, args.flags[key])
			}
		}
	}
	if len(args.positional) > 2 {
		return fmt.Errorf("usage: scaffold list [<framework>] [<category>]\n"+
			"got %d positional arguments, at most 2 are accepted", len(args.positional))
	}

	scaffoldingCodeRoot := resolveScaffoldingCodeRoot(args.value("scaffolding-code"))
	out := cmd.OutOrStdout()

	if len(args.positional) == 0 {
		args.markConsumed(engineFlags...)
		if err := args.requireAllFlagsConsumed([]string{"scaffolding-code"}); err != nil {
			return err
		}
		return listFrameworks(out, scaffoldingCodeRoot)
	}

	framework := args.positional[0]
	frameworkPath, err := discovery.ResolveFrameworkPath(scaffoldingCodeRoot, framework)
	if err != nil {
		return err
	}
	version, err := discovery.ResolveVersion(frameworkPath, args.value("fw-version"))
	if err != nil {
		return fmt.Errorf("resolving version for framework %q: %w", framework, err)
	}
	versionPath := filepath.Join(frameworkPath, version)

	args.markConsumed(engineFlags...)
	if len(args.positional) == 1 {
		if err := args.requireAllFlagsConsumed([]string{"fw-version", "scaffolding-code"}); err != nil {
			return err
		}
		return listFrameworkDetail(out, framework, frameworkPath, version, versionPath)
	}

	// With a category given, selector flags narrow which leaf's variables to show; which ones are
	// valid is only known after resolving the chain, so the unknown-flag check happens further down.

	axes, err := discovery.DiscoverAxes(versionPath)
	if err != nil {
		return fmt.Errorf("discovering axes for %s %s: %w", framework, version, err)
	}
	baseAxis, err := discovery.RequiredAxis(axes)
	if err != nil {
		return fmt.Errorf("%s %s: %w", framework, version, err)
	}
	templatesPath := baseAxis.Path(versionPath)

	category := args.positional[1]
	categoryDir, err := discovery.ResolveCategoryDir(templatesPath, category)
	if err != nil {
		return err
	}
	tree, err := discovery.DescribeTree(templatesPath, categoryDir)
	if err != nil {
		return fmt.Errorf("describing category %q: %w", category, err)
	}
	fmt.Fprintf(out, "%s %s %s/%s:\n", framework, version, baseAxis.Name, category)
	printTree(out, tree, "  ")

	// Resolve the same plan `create` would build, so the variable list here can never drift from
	// what `create` actually accepts.
	return printVariables(out, args, scaffoldingCodeRoot, framework, category)
}

// printVariables resolves the chain the way `create` would and lists the variables it declares.
// Failure is silent on purpose - `list` is a browsing command, so an unresolved chain still shows
// the tree above instead of an error.
func printVariables(out io.Writer, args *parsedArgs, root, framework, category string) error {
	probe := &parsedArgs{flags: args.flags, consumed: map[string]bool{}}
	p, err := resolvePlan(probe, root, framework, category, "<name>")
	if err != nil {
		// Usually just means a selector with no default is unset; say which flag would resolve
		// it rather than printing nothing.
		fmt.Fprintf(out, "\nPick a leaf to see the variables it declares:\n  %s\n", err)
		return nil
	}

	// Now that the plan is resolved, the valid flag set is known - so a typo can finally be caught
	// here too, instead of `list` accepting anything.
	probe.markConsumed(engineFlags...)
	if err := probe.requireAllFlagsConsumed(validFlagsFor(p.Axes, p.Walk, p.Manifests)); err != nil {
		return err
	}

	vars := declaredVariables(p)
	if len(vars) == 0 {
		return nil
	}

	fmt.Fprintf(out, "\nVariables for %s", category)
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

func listFrameworks(out io.Writer, root string) error {
	rootManifest, err := jig.LoadRoot(filepath.Join(root, jig.FileName))
	if err != nil {
		return fmt.Errorf("loading framework registry: %w", err)
	}
	fmt.Fprintln(out, "Available frameworks:")
	for _, f := range rootManifest.Frameworks {
		if f.Description != "" {
			fmt.Fprintf(out, "  %s - %s\n", f.Name, f.Description)
		} else {
			fmt.Fprintf(out, "  %s\n", f.Name)
		}
	}
	return nil
}

// listFrameworkDetail prints versions, then the categories of the required base axis, then the
// optional axes.
func listFrameworkDetail(out io.Writer, framework, frameworkPath, version, versionPath string) error {
	fmt.Fprintf(out, "%s:\n", framework)

	fmt.Fprintln(out, "  versions:")
	if m, err := jig.LoadOptional(filepath.Join(frameworkPath, jig.FileName)); err != nil {
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

	axes, err := discovery.DiscoverAxes(versionPath)
	if err != nil {
		return err
	}
	baseAxis, err := discovery.RequiredAxis(axes)
	if err != nil {
		return fmt.Errorf("%s %s: %w", framework, version, err)
	}

	fmt.Fprintf(out, "  categories (positional <category>, from the %q axis): %s\n",
		baseAxis.Name, strings.Join(baseAxis.Values, ", "))

	fmt.Fprintln(out, "  optional axes:")
	any := false
	for _, a := range axes {
		if a.Required {
			continue
		}
		any = true
		desc := ""
		if a.Description != "" {
			desc = " - " + a.Description
		}
		fmt.Fprintf(out, "    --%s%s: %s\n", a.Flag, desc, strings.Join(a.Values, ", "))
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
