package cmd

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"scaffold-engine-go/internal/discovery"
	"scaffold-engine-go/internal/jig"
	"scaffold-engine-go/internal/render"
)

func newLintCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "lint [<framework>] [--build] [--scaffolding-code=<path>]",
		Short: "Render every registered combination to memory and report what breaks",
		Long: "scaffold lint [<framework>] [--build]\n\n" +
			"Resolves and renders every combination the registries advertise - each framework,\n" +
			"version, category, selector path, and each optional axis value - without writing\n" +
			"anything. It is the answer to \"is scaffolding-code healthy?\", which otherwise means\n" +
			"running create by hand once per combination.\n\n" +
			"What it catches: a registered value with no manifest, a template that fails to parse,\n" +
			"a placeholder naming a variable nobody declares, a required variable with no default,\n" +
			"a stale exclude pattern, an unmergeable file format, and a combination that renders\n" +
			"nothing at all.\n\n" +
			"What it does NOT catch on its own is whether the result actually BUILDS. Rendering\n" +
			"proves the templates parse and every placeholder resolves; the build tool is the only\n" +
			"thing that knows the rest. --build closes that gap: each combination is written to a\n" +
			"scratch directory and its `verify:` commands are run there.\n\n" +
			"    --build   also run each combination's `verify:` checks (slow - these are real\n" +
			"              builds, so budget minutes per combination, and narrow the run with\n" +
			"              <framework> when iterating)\n\n" +
			"Those commands come from scaffolding-code, so --build is opt-in and never implied.\n" +
			"`create` never runs them at all. Each is an argv list executed directly, with no shell\n" +
			"involved.\n\n" +
			"Exit code is non-zero if any combination fails, so it works as a CI gate.",
		DisableFlagParsing: true,
		RunE:               runLint,
	}
}

// lintFlags are the only flags lint accepts. An unrecognized flag is rejected rather than
// silently ignored, so a typo like --buidl can't produce a false-positive pass.
var lintFlags = []string{"scaffolding-code", "build"}

// lintCase is one combination to try.
type lintCase struct {
	framework string
	version   string
	category  string
	selectors map[string]string
	axisFlag  string
	axisValue string
	// err is set when the combination could not even be enumerated, so it is reported as a failure
	// alongside the cases that were.
	err error
}

func (c lintCase) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s", c.framework, c.version, c.category)
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
	args.markConsumed(lintFlags...)
	if err := args.requireAllFlagsConsumed(lintFlags); err != nil {
		return err
	}
	root := resolveScaffoldingCodeRoot(args.value("scaffolding-code"))
	out := cmd.OutOrStdout()
	build := args.value("build") == "true"

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

	var failed, built, skipped int
	for _, c := range cases {
		files, checks, err := lintOne(root, c)
		if err != nil {
			failed++
			fmt.Fprintf(out, "FAIL  %s\n      %s\n", c, indent(err.Error()))
			continue
		}
		fmt.Fprintf(out, "ok    %s\n", c)
		if !build {
			continue
		}

		result, err := runVerifications(out, files, checks)
		if err != nil {
			failed++
			fmt.Fprintf(out, "FAIL  %s\n      %s\n", c, indent(err.Error()))
			continue
		}
		built += result.Passed
		skipped += result.Skipped
		if len(result.Failures) > 0 {
			failed++
			for _, f := range result.Failures {
				fmt.Fprintf(out, "FAIL  %s\n      %s\n", c, indent(f))
			}
		}
	}

	fmt.Fprintf(out, "\n%d combination(s), %d failed.\n", len(cases), failed)
	if build {
		// Report explicitly when no verify: was found anywhere - that's different from every
		// check passing, and blurring the two would give false confidence.
		fmt.Fprintf(out, "%d check(s) passed, %d skipped.\n", built, skipped)
		if built == 0 && skipped == 0 {
			fmt.Fprintf(out, "No `verify:` declared anywhere in the chain - --build had nothing "+
				"to run. See PRD Section 8.8.\n")
		}
	}
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
	rootManifest, err := jig.LoadRoot(filepath.Join(root, jig.FileName))
	if err != nil {
		return nil, err
	}
	return rootManifest.FrameworkNames(), nil
}

// enumerate produces every combination the registries advertise for one framework. Selector paths
// are enumerated exhaustively, but axis values are applied one at a time to the default selector
// path rather than to every path, since the full cross-product buys little and would make lint slow.
func enumerate(root, framework string) ([]lintCase, error) {
	frameworkPath, err := discovery.ResolveFrameworkPath(root, framework)
	if err != nil {
		return nil, err
	}

	// Every registered version, not just the default - a version that only declares differences
	// from a base is where breakage is most likely to hide.
	versions, err := registeredVersions(frameworkPath)
	if err != nil {
		return nil, err
	}

	var all []lintCase
	for _, version := range versions {
		cases, err := enumerateVersion(root, frameworkPath, framework, version)
		if err != nil {
			// A version that can't be enumerated is a finding about that version, not a reason
			// to abort the run - report it as one failed case.
			all = append(all, lintCase{framework: framework, version: version, err: err})
			continue
		}
		all = append(all, cases...)
	}
	return all, nil
}

// registeredVersions lists every version the framework registry advertises, or the resolved
// default when there is no registry to ask.
func registeredVersions(frameworkPath string) ([]string, error) {
	m, err := jig.LoadOptional(filepath.Join(frameworkPath, jig.FileName))
	if err != nil {
		return nil, err
	}
	if m != nil && len(m.Values) > 0 {
		return m.ValueNames(), nil
	}
	v, err := discovery.ResolveVersion(frameworkPath, "")
	if err != nil {
		return nil, err
	}
	return []string{v}, nil
}

func enumerateVersion(root, frameworkPath, framework, version string) ([]lintCase, error) {
	chain, err := discovery.ResolveVersionChain(frameworkPath, version)
	if err != nil {
		return nil, err
	}
	// Structure may live in a base version, so enumerate against the one that actually declares it.
	versionPaths := make([]string, 0, len(chain))
	for _, v := range chain {
		versionPaths = append(versionPaths, filepath.Join(frameworkPath, v))
	}
	versionPath, _, err := discoverAxesInChain(versionPaths)
	if err != nil {
		return nil, err
	}

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
			cases = append(cases, lintCase{framework: framework, version: version, category: category})
			continue
		}
		for _, sel := range selectorPaths(tree) {
			cases = append(cases, lintCase{framework: framework, version: version, category: category, selectors: sel})
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
					framework: framework, version: version, category: base.category,
					selectors: base.selectors, axisFlag: a.Flag, axisValue: v,
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

// lintOne resolves and renders a single combination in memory, returning the rendered tree and the
// `verify:` checks its chain declares so `--build` can act on them without resolving twice.
func lintOne(root string, c lintCase) ([]render.File, []render.Verification, error) {
	if c.err != nil {
		return nil, nil, c.err
	}
	flags := map[string]string{}
	for k, v := range c.selectors {
		flags[k] = v
	}
	if c.axisFlag != "" {
		flags[c.axisFlag] = c.axisValue
	}
	if c.version != "" {
		flags["fw-version"] = c.version
	}
	args := &parsedArgs{flags: flags, consumed: map[string]bool{}}

	p, err := resolvePlan(args, root, c.framework, c.category, "lint-probe")
	if err != nil {
		return nil, nil, err
	}
	files, _, err := renderPlan(p)
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("renders no files at all - the chain contributes nothing")
	}
	checks, err := render.CollectVerifications(p.Sources, p.Context)
	if err != nil {
		return nil, nil, err
	}
	return files, checks, nil
}
