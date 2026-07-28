package cmd

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"scaffold-engine-go/internal/discovery"
	"scaffold-engine-go/internal/manifest"
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
	args := parseArgs(rawArgs)
	if args.help {
		return cmd.Help()
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
	if err := args.requireAllFlagsConsumed([]string{"fw-version", "scaffolding-code"}); err != nil {
		return err
	}

	if len(args.positional) == 1 {
		return listFrameworkDetail(out, framework, frameworkPath, version, versionPath)
	}

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
	return nil
}

func listFrameworks(out io.Writer, root string) error {
	rootManifest, err := manifest.LoadRoot(filepath.Join(root, "manifest.yaml"))
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
//
// Two corrections from the design review: the version list was missing entirely even though PRD
// Section 8 requires it (section 5.20), and the required base axis was printed as "--templates"
// as though it were a flag, when it is actually selected positionally as <category> (section
// 5.98). Optional axes are printed under the flag name that really works, which after the `flag`
// field exists is --style rather than --patterns.
func listFrameworkDetail(out io.Writer, framework, frameworkPath, version, versionPath string) error {
	fmt.Fprintf(out, "%s:\n", framework)

	fmt.Fprintln(out, "  versions:")
	if m, err := manifest.LoadOptional(filepath.Join(frameworkPath, "manifest.yaml")); err != nil {
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
