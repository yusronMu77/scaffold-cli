package cmd

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"scaffold-engine-go/internal/discovery"
	"scaffold-engine-go/internal/manifest"
)

func newLintCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "lint [<framework>] [--scaffolding-code=<path>]",
		Short: "Render every registered combination to memory and report what breaks",
		Long: "scaffold lint [<framework>]\n\n" +
			"Resolves and renders every combination the registries advertise - each framework,\n" +
			"version, category, selector path, and each optional axis value - without writing\n" +
			"anything. It is the answer to \"is scaffolding-code healthy?\", which otherwise means\n" +
			"running create by hand once per combination.\n\n" +
			"What it catches: a registered value with no manifest, a template that fails to parse,\n" +
			"a placeholder naming a variable nobody declares, a required variable with no default,\n" +
			"a stale exclude pattern, an unmergeable file format, and a combination that renders\n" +
			"nothing at all.\n\n" +
			"Exit code is non-zero if any combination fails, so it works as a CI gate.",
		DisableFlagParsing: true,
		RunE:               runLint,
	}
}

// lintCase is one combination to try.
type lintCase struct {
	framework string
	category  string
	selectors map[string]string
	axisFlag  string
	axisValue string
}

func (c lintCase) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", c.framework, c.category)
	keys := make([]string, 0, len(c.selectors))
	for k := range c.selectors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, " --%s=%s", k, c.selectors[k])
	}
	if c.axisFlag != "" {
		fmt.Fprintf(&b, " --%s=%s", c.axisFlag, c.axisValue)
	}
	return b.String()
}

func runLint(cmd *cobra.Command, rawArgs []string) error {
	args, err := parseArgs(rawArgs)
	if err != nil {
		return err
	}
	if args.help {
		return cmd.Help()
	}
	root := resolveScaffoldingCodeRoot(args.value("scaffolding-code"))
	out := cmd.OutOrStdout()

	frameworks, err := lintFrameworks(root, args.positional)
	if err != nil {
		return err
	}

	var cases []lintCase
	for _, fw := range frameworks {
		fwCases, err := enumerate(root, fw)
		if err != nil {
			return fmt.Errorf("enumerating %s: %w", fw, err)
		}
		cases = append(cases, fwCases...)
	}
	if len(cases) == 0 {
		return fmt.Errorf("nothing to lint: no registered combinations found under %s", root)
	}

	var failed int
	for _, c := range cases {
		if err := lintOne(root, c); err != nil {
			failed++
			fmt.Fprintf(out, "FAIL  %s\n      %s\n", c, indent(err.Error()))
			continue
		}
		fmt.Fprintf(out, "ok    %s\n", c)
	}

	fmt.Fprintf(out, "\n%d combination(s), %d failed.\n", len(cases), failed)
	if failed > 0 {
		return fmt.Errorf("%d of %d combinations failed", failed, len(cases))
	}
	return nil
}

func indent(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n      ")
}

func lintFrameworks(root string, positional []string) ([]string, error) {
	if len(positional) > 1 {
		return nil, fmt.Errorf("usage: scaffold lint [<framework>]")
	}
	if len(positional) == 1 {
		return positional, nil
	}
	rootManifest, err := manifest.LoadRoot(filepath.Join(root, "manifest.yaml"))
	if err != nil {
		return nil, err
	}
	return rootManifest.FrameworkNames(), nil
}

// enumerate produces every combination the registries advertise for one framework.
//
// Selector paths are enumerated exhaustively, because that is where a missing manifest hides. Axis
// values are applied one at a time to the DEFAULT selector path rather than to every path: the
// full cross-product grows multiplicatively and buys little, since an overlay's contribution does
// not depend on which leaf it lands on. `lint` staying fast is what keeps it worth running.
func enumerate(root, framework string) ([]lintCase, error) {
	frameworkPath, err := discovery.ResolveFrameworkPath(root, framework)
	if err != nil {
		return nil, err
	}
	version, err := discovery.ResolveVersion(frameworkPath, "")
	if err != nil {
		return nil, err
	}
	versionPath := filepath.Join(frameworkPath, version)

	axes, err := discovery.DiscoverAxes(versionPath)
	if err != nil {
		return nil, err
	}
	baseAxis, err := discovery.RequiredAxis(axes)
	if err != nil {
		return nil, err
	}
	templatesPath := baseAxis.Path(versionPath)

	var cases []lintCase
	for _, category := range baseAxis.Values {
		dir, err := discovery.ResolveCategoryDir(templatesPath, category)
		if err != nil {
			return nil, err
		}
		tree, err := discovery.DescribeTree(templatesPath, dir)
		if err != nil {
			// A registered category with no manifest is itself a finding, not a reason to stop.
			cases = append(cases, lintCase{framework: framework, category: category})
			continue
		}
		for _, sel := range selectorPaths(tree) {
			cases = append(cases, lintCase{framework: framework, category: category, selectors: sel})
		}
	}

	// One case per optional axis value, on the default category.
	if len(cases) > 0 {
		base := cases[0]
		for _, a := range axes {
			if a.Required {
				continue
			}
			for _, v := range a.Values {
				cases = append(cases, lintCase{
					framework: framework, category: base.category, selectors: base.selectors,
					axisFlag: a.Flag, axisValue: v,
				})
			}
		}
	}
	return cases, nil
}

// selectorPaths flattens a selector tree into one map per reachable leaf.
func selectorPaths(node *discovery.TreeNode) []map[string]string {
	if node == nil || node.IsLeaf || len(node.Children) == 0 {
		return []map[string]string{{}}
	}
	var out []map[string]string
	for i := range node.Children {
		child := &node.Children[i]
		for _, sub := range selectorPaths(child) {
			combined := map[string]string{node.Selector: child.Value}
			for k, v := range sub {
				combined[k] = v
			}
			out = append(out, combined)
		}
	}
	return out
}

// lintOne resolves and renders a single combination in memory.
func lintOne(root string, c lintCase) error {
	flags := map[string]string{}
	for k, v := range c.selectors {
		flags[k] = v
	}
	if c.axisFlag != "" {
		flags[c.axisFlag] = c.axisValue
	}
	args := &parsedArgs{flags: flags, consumed: map[string]bool{}}

	p, err := resolvePlan(args, root, c.framework, c.category, "lint-probe")
	if err != nil {
		return err
	}
	files, _, err := renderPlan(p)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("renders no files at all - the chain contributes nothing")
	}
	return nil
}
