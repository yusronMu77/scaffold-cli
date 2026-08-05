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
		Use:   "lint [<scaffold>] [--build] [--scaffolding-code=<path>]",
		Short: "Render every registered combination to memory and report what breaks",
		Long: "scaffold lint [<scaffold>] [--build]\n\n" +
			"Resolves and renders every combination the registries advertise - each scaffold,\n" +
			"version, template, selector path, and each optional overlay value - without writing\n" +
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
			"              <scaffold> when iterating)\n\n" +
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
	scaffold     string
	version      string
	template     string
	selectors    map[string]string
	overlayFlag  string
	overlayValue string
	// err is set when the combination could not even be enumerated, so it is reported as a failure
	// alongside the cases that were.
	err error
}

func (c lintCase) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s", c.scaffold, c.version, c.template)
	keys := make([]string, 0, len(c.selectors))
	for k := range c.selectors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, " --%s=%s", k, c.selectors[k])
	}
	if c.overlayFlag != "" {
		fmt.Fprintf(&b, " --%s=%s", c.overlayFlag, c.overlayValue)
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

	scaffolds, err := lintScaffolds(root, args.positional)
	if err != nil {
		return err
	}

	var cases []lintCase
	for _, sc := range scaffolds {
		scCases, err := enumerate(root, sc)
		if err != nil {
			return fmt.Errorf("enumerating %s: %w", sc, err)
		}
		cases = append(cases, scCases...)
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

func lintScaffolds(root string, positional []string) ([]string, error) {
	if len(positional) > 1 {
		return nil, fmt.Errorf("usage: scaffold lint [<scaffold>]")
	}
	if len(positional) == 1 {
		return positional, nil
	}
	rootJig, err := jig.LoadRoot(filepath.Join(root, jig.FileName))
	if err != nil {
		return nil, err
	}
	return rootJig.ValueNames(), nil
}

// enumerate produces every combination the registries advertise for one scaffold. Selector paths
// are enumerated exhaustively, but overlay values are applied one at a time to the default
// selector path rather than to every path, since the full cross-product buys little and would
// make lint slow.
func enumerate(root, scaffold string) ([]lintCase, error) {
	scaffoldPath, err := discovery.ResolveScaffoldPath(root, scaffold)
	if err != nil {
		return nil, err
	}

	// Every registered version, not just the default - a version that only declares differences
	// from a base is where breakage is most likely to hide.
	versions, err := registeredVersions(scaffoldPath)
	if err != nil {
		return nil, err
	}

	var all []lintCase
	for _, version := range versions {
		cases, err := enumerateVersion(root, scaffoldPath, scaffold, version)
		if err != nil {
			// A version that can't be enumerated is a finding about that version, not a reason
			// to abort the run - report it as one failed case.
			all = append(all, lintCase{scaffold: scaffold, version: version, err: err})
			continue
		}
		all = append(all, cases...)
	}
	return all, nil
}

// registeredVersions lists every version the scaffold registry advertises, or the resolved
// default when there is no registry to ask.
func registeredVersions(scaffoldPath string) ([]string, error) {
	m, err := jig.LoadOptional(filepath.Join(scaffoldPath, jig.FileName))
	if err != nil {
		return nil, err
	}
	if m != nil && len(m.Values) > 0 {
		return m.ValueNames(), nil
	}
	v, err := discovery.ResolveVersion(scaffoldPath, "")
	if err != nil {
		return nil, err
	}
	return []string{v}, nil
}

func enumerateVersion(root, scaffoldPath, scaffold, version string) ([]lintCase, error) {
	chain, err := discovery.ResolveVersionChain(scaffoldPath, version)
	if err != nil {
		return nil, err
	}
	// Structure may live in a base version, so enumerate against the one that actually declares it.
	versionPaths := make([]string, 0, len(chain))
	for _, v := range chain {
		versionPaths = append(versionPaths, filepath.Join(scaffoldPath, v))
	}
	versionPath, _, err := discoverDimensionsInChain(versionPaths)
	if err != nil {
		return nil, err
	}

	dimensions, err := discovery.DiscoverDimensions(versionPath)
	if err != nil {
		return nil, err
	}
	baseDimension, err := discovery.RequiredDimension(dimensions)
	if err != nil {
		return nil, err
	}
	templatesPath := baseDimension.Path(versionPath)

	var cases []lintCase
	for _, template := range baseDimension.Values {
		dir, err := discovery.ResolveTemplateDir(templatesPath, template)
		if err != nil {
			return nil, err
		}
		tree, err := discovery.DescribeTree(templatesPath, dir)
		if err != nil {
			// A registered template with no manifest is itself a finding, not a reason to stop.
			cases = append(cases, lintCase{scaffold: scaffold, version: version, template: template})
			continue
		}
		for _, sel := range selectorPaths(tree) {
			cases = append(cases, lintCase{scaffold: scaffold, version: version, template: template, selectors: sel})
		}
	}

	// One case per optional overlay value, on the default template.
	if len(cases) > 0 {
		base := cases[0]
		for _, d := range dimensions {
			if d.Required {
				continue
			}
			for _, v := range d.Values {
				cases = append(cases, lintCase{
					scaffold: scaffold, version: version, template: base.template,
					selectors: base.selectors, overlayFlag: d.Flag, overlayValue: v,
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
	if c.overlayFlag != "" {
		flags[c.overlayFlag] = c.overlayValue
	}
	if c.version != "" {
		scaffoldPath, err := discovery.ResolveScaffoldPath(root, c.scaffold)
		if err != nil {
			return nil, nil, err
		}
		versionFlag, err := discovery.VersionSelectorFlag(scaffoldPath)
		if err != nil {
			return nil, nil, err
		}
		flags[versionFlag] = c.version
	}
	args := &parsedArgs{flags: flags, consumed: map[string]bool{}}

	p, err := resolvePlan(args, root, c.scaffold, c.template, "lint-probe")
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
