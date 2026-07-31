package cmd

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"scaffold-engine-go/internal/discovery"
	"scaffold-engine-go/internal/manifest"
	"scaffold-engine-go/internal/render"
)

// engineFlags are the flags the engine itself owns at every invocation, as opposed to the
// selector/axis/variable flags that come from manifest content.
var engineFlags = []string{
	"fw-version", "output", "scaffolding-code",
	"force", "skip-existing", "no-hooks", "dry-run", "explain", "values",
}

func newCreateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "create <framework> <category> <name> [--flag=value ...]",
		Short: "Generate a new artefact (service, lib, parent, ...) from scaffolding-code",
		Long: "scaffold create <framework> <category> <name> [--flag=value ...]\n" +
			"scaffold create -f values.yaml\n\n" +
			"All three positional arguments must be supplied - either on the command line or in a\n" +
			"values file (-f). <framework> and <category> are resolved through the registries under\n" +
			"scaffolding-code/ and are never hardcoded; <name> is the artefact's identifier and must\n" +
			"be a single path segment.\n\n" +
			"A values file is the flag namespace without the dashes: --package=x is `package: x`.\n" +
			"-f may be repeated (later files win), and a flag on the command line beats them all,\n" +
			"so one shared file plus a one-off override is the normal way to use it:\n" +
			"    scaffold create -f base.yaml -f prod.yaml --name=payment-canary\n\n" +
			"Flags use --key=value; a flag with no '=' is a boolean set to true. Which flags are\n" +
			"valid depends on the category's selector chain (e.g. --function/--protocol for\n" +
			"'services'), one flag per optional axis named by that axis's `flag` field in the\n" +
			"registry (e.g. --style), and one flag per variable the resolved templates declare\n" +
			"(e.g. --package). A selector flag left unset falls back to that level's own\n" +
			"`default`, if it declares one. Unknown flags are an error, not silently ignored.\n\n" +
			"Use --dry-run to see what would be generated without writing anything.",
		DisableFlagParsing: true,
		RunE:               runCreate,
	}
}

func runCreate(cmd *cobra.Command, rawArgs []string) error {
	args, err := parseArgs(rawArgs)
	if err != nil {
		return err
	}
	if args.help {
		return cmd.Help()
	}

	// framework/category/name may come from the command line, from a -f values file, or from a
	// mix of the two - but all three must be supplied somewhere (PRD Section 8.7).
	framework, category, name, err := applyValuesFile(args)
	if err != nil {
		return err
	}

	// <name> becomes part of the write path, so it must not be able to escape <output>.
	if err := discovery.ValidateSegment("<name>", name); err != nil {
		return err
	}

	scaffoldingCodeRoot := resolveScaffoldingCodeRoot(args.value("scaffolding-code"))

	// The inheritance chain runs the full depth of the tree, outermost first:
	//
	//   spring-boot/ -> 3.2.x/ -> templates/ -> services/ -> web/ -> rest-http/ -> mvc/
	//
	// Every level may contribute files, dependencies and variables; deeper levels win. Resolving
	// it lives in plan.go because `list` and `lint` must reach exactly the same answer.
	p, err := resolvePlan(args, scaffoldingCodeRoot, framework, category, name)
	if err != nil {
		return err
	}

	args.markConsumed(engineFlags...)
	if err := args.requireAllFlagsConsumed(validFlagsFor(p.Axes, p.Walk, p.Manifests)); err != nil {
		return err
	}

	files, contributions, err := renderPlan(p)
	if err != nil {
		return err
	}

	output := args.value("output")
	if output == "" {
		output = "."
	}
	targetDir := filepath.Join(output, name)
	out := cmd.OutOrStdout()

	if args.value("explain") == "true" {
		printExplain(out, files, contributions, p, targetDir)
		return nil
	}
	if args.value("dry-run") == "true" {
		printPlan(out, files, targetDir, p)
		return nil
	}

	policy := render.FailIfExists
	switch {
	case args.value("force") == "true":
		policy = render.Overwrite
	case args.value("skip-existing") == "true":
		policy = render.SkipExisting
	}

	written, err := render.Write(targetDir, files, policy)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Generated %s\n", targetDir)
	for _, p := range written {
		fmt.Fprintf(out, "  %s\n", p)
	}
	fmt.Fprintf(out, "\n%d file(s) written.\n", len(written))
	return nil
}

// loadLevel turns one already-resolved level of the tree (framework, version, axis) into a render
// source, or nil when that level has nothing of its own to contribute. Its manifest is a registry,
// so the interesting part is whatever files/dependencies/variables sit alongside the registry.
func loadLevel(dir string) (*render.Source, error) {
	m, err := manifest.LoadOptional(filepath.Join(dir, "manifest.yaml"))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	if m == nil {
		return nil, nil
	}
	return &render.Source{
		Dir:      dir,
		Manifest: m,
		Label:    filepath.Base(dir),
		Priority: m.MergePriority,
	}, nil
}

// resolveOverlays validates and loads every optional axis the user selected, returning the render
// sources sorted by merge_priority plus a flag->value map for the render context.
func resolveOverlays(args *parsedArgs, axes []discovery.Axis, versionPath string) ([]render.Source, map[string]string, error) {
	selected := map[string]string{}
	var sources []render.Source

	for _, axis := range axes {
		if axis.Required {
			continue // the base axis is handled by the category walk
		}
		value, ok := args.get(axis.Flag)
		if !ok {
			continue
		}
		dir, err := axis.ResolveValueDir(versionPath, value)
		if err != nil {
			return nil, nil, err
		}
		m, err := manifest.Load(filepath.Join(dir, "manifest.yaml"))
		if err != nil {
			return nil, nil, fmt.Errorf("loading --%s=%s: %w", axis.Flag, value, err)
		}
		selected[axis.Flag] = value
		sources = append(sources, render.Source{
			Dir:      dir,
			Manifest: m,
			Label:    fmt.Sprintf("--%s=%s", axis.Flag, value),
			Priority: m.MergePriority,
		})
	}

	sort.SliceStable(sources, func(i, j int) bool { return sources[i].Priority < sources[j].Priority })
	return sources, selected, nil
}

// checkReservedFlagNames rejects a manifest that names a selector, an axis flag or a variable
// after one of the three positional arguments.
//
// Such a name is ambiguous the moment both meanings appear in the same values file: `category:
// libs` would have to be the positional <category> AND the value of a selector called `category`.
// On the command line the clash hides, because the positional and the flag are written
// differently - which makes it exactly the kind of latent trap that only surfaces later, in a
// different invocation style, with a baffling error. Rejecting it up front is fundamental rule #8.
func checkReservedFlagNames(walk *discovery.WalkResult, axes []discovery.Axis,
	manifests []*manifest.Manifest) error {

	reserved := map[string]bool{keyFramework: true, keyCategory: true, keyName: true}
	report := func(kind, flag string) error {
		return fmt.Errorf("%s is named %q, which is reserved: %q is one of the three positional "+
			"arguments, so a values file could not tell the two apart. Rename it in the manifest",
			kind, flag, flag)
	}

	if walk != nil {
		for _, step := range walk.Steps {
			if reserved[step.Flag] {
				return report("a selector", step.Flag)
			}
		}
	}
	for _, a := range axes {
		if !a.Required && reserved[a.Flag] {
			return report("an axis flag", a.Flag)
		}
	}
	for _, m := range manifests {
		if m == nil {
			continue
		}
		for _, v := range m.Variables {
			if flag := render.VariableFlagName(v); reserved[flag] {
				return report("a variable flag", flag)
			}
		}
	}
	return nil
}

// checkIncompatibilities enforces `incompatible_with` across what the user actually selected
// (PRD Section 8.5). Only cross-axis constraints need this: compatibility *between selectors* is
// already expressed by the folder structure itself, so there is nothing left for a rule to say.
//
// Absence of a rule means allowed - fail-open for declarations, fail-closed for typos.
func checkIncompatibilities(manifests []*manifest.Manifest, selectors, axes map[string]string) error {
	active := map[string]bool{}
	for flag, value := range selectors {
		active[flag+":"+value] = true
	}
	for flag, value := range axes {
		active[flag+":"+value] = true
	}
	for _, m := range manifests {
		if m == nil {
			continue
		}
		for _, rule := range m.IncompatibleWith {
			if active[rule] {
				return fmt.Errorf("%q declares it is incompatible with %s, which you selected",
					m.Name, rule)
			}
		}
	}
	return nil
}

// validFlagsFor builds the list shown when an unknown flag is rejected: the engine's own flags,
// one per optional axis, every selector consumed on the way to the leaf, and every variable the
// resolved templates declare.
func validFlagsFor(axes []discovery.Axis, walk *discovery.WalkResult, manifests []*manifest.Manifest) []string {
	valid := append([]string{}, engineFlags...)
	for _, axis := range axes {
		if !axis.Required {
			valid = append(valid, axis.Flag)
		}
	}
	if walk != nil {
		for _, step := range walk.Steps {
			valid = append(valid, step.Flag)
		}
	}
	for _, m := range manifests {
		if m == nil {
			continue
		}
		for _, v := range m.Variables {
			valid = append(valid, render.VariableFlagName(v))
		}
	}
	return valid
}

func printSelection(out io.Writer, p *plan) {
	if len(p.Walk.Steps) == 0 && len(p.SelectedAxes) == 0 {
		return
	}
	fmt.Fprint(out, "Selected:")
	for _, step := range p.Walk.Steps {
		fmt.Fprintf(out, " --%s=%s", step.Flag, step.Value)
		if step.Defaulted {
			fmt.Fprint(out, "(default)")
		}
	}
	for flag, value := range p.SelectedAxes {
		fmt.Fprintf(out, " --%s=%s", flag, value)
	}
	fmt.Fprintln(out)
}

func printPlan(out io.Writer, files []render.File, targetDir string, p *plan) {
	fmt.Fprintf(out, "DRY RUN - nothing written.\n\nTarget: %s\n", targetDir)
	printSelection(out, p)

	fmt.Fprintln(out, "\nInheritance chain (outermost first, deeper wins):")
	for _, s := range p.Sources {
		fmt.Fprintf(out, "  %s\n", s.Dir)
	}

	if len(p.Variables) > 0 {
		fmt.Fprintln(out, "\nVariables:")
		keys := make([]string, 0, len(p.Variables))
		for k := range p.Variables {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(out, "  %-18s = %s\n", k, p.Variables[k])
		}
	}

	fmt.Fprintf(out, "\nWould write %d file(s):\n", len(files))
	for _, f := range files {
		fmt.Fprintf(out, "  %s\n", f.Path)
	}
}

// printExplain answers "where did this file come from, and what else touched it?".
//
// In a seven-level chain the merged result looks the same whether one source produced a file or
// five fought over it, so without this the only way to find out is to read every manifest in the
// chain and simulate the merge by hand.
func printExplain(out io.Writer, files []render.File,
	contributions map[string][]render.Contribution, p *plan, targetDir string) {

	fmt.Fprintf(out, "EXPLAIN - nothing written.\n\nTarget: %s\n", targetDir)
	printSelection(out, p)

	fmt.Fprintln(out, "\nInheritance chain (outermost first, deeper wins):")
	for i, s := range p.Sources {
		fmt.Fprintf(out, "  %d. %s\n", i+1, s.Dir)
	}

	fmt.Fprintf(out, "\n%d file(s), and who contributed each:\n", len(files))
	for _, f := range files {
		marks := contributions[f.Path]
		fmt.Fprintf(out, "\n  %s\n", f.Path)
		for _, c := range marks {
			note := ""
			switch c.Action {
			case "overrode":
				note = "  <- replaced what came before"
			case "merged":
				note = "  <- deep-merged with what came before"
			}
			fmt.Fprintf(out, "      %-9s %s%s\n", c.Action, c.Source, note)
		}
	}

	// Anything dropped by `exclude:` is absent from files but present in contributions, and that
	// difference is exactly what someone hunting a missing file needs to see.
	var dropped []string
	present := map[string]bool{}
	for _, f := range files {
		present[f.Path] = true
	}
	for path := range contributions {
		if !present[path] {
			dropped = append(dropped, path)
		}
	}
	if len(dropped) > 0 {
		sort.Strings(dropped)
		fmt.Fprintln(out, "\nRemoved by `exclude:` after merging:")
		for _, path := range dropped {
			fmt.Fprintf(out, "  %s (contributed by", path)
			for _, c := range contributions[path] {
				fmt.Fprintf(out, " %s", c.Source)
			}
			fmt.Fprintln(out, ")")
		}
	}
}
