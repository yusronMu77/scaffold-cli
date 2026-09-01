package cmd

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"scaffold-engine-go/internal/discovery"
	"scaffold-engine-go/internal/jig"
	"scaffold-engine-go/internal/render"
)

// engineFlags are the flags the engine itself owns at every invocation, as opposed to the
// selector/overlay/variable flags that come from manifest content.
var engineFlags = []string{
	"output", "scaffolding-code",
	"force", "skip-existing", "dry-run", "explain", "print", "values",
}

func newCreateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "create <scaffold> <template> <name> [--flag=value ...]",
		Short: "Generate a new project (service, lib, parent, ...) from scaffolding-code",
		Long: "scaffold create <scaffold> <template> <name> [--flag=value ...]\n" +
			"scaffold create -f values.yaml\n\n" +
			"All three positional arguments must be supplied - either on the command line or in a\n" +
			"values file (-f). <scaffold> and <template> are resolved through the registries under\n" +
			"scaffolding-code/ and are never hardcoded; <name> is the project's identifier and must\n" +
			"be a single path segment.\n\n" +
			"A values file is the flag namespace without the dashes: --package=x is `package: x`.\n" +
			"-f may be repeated (later files win), and a flag on the command line beats them all,\n" +
			"so one shared file plus a one-off override is the normal way to use it:\n" +
			"    scaffold create -f base.yaml -f prod.yaml --name=payment-canary\n\n" +
			"Flags use --key=value; a flag with no '=' is a boolean set to true. Which flags are\n" +
			"valid depends on the template's selector chain (e.g. --function/--protocol for\n" +
			"'services'), one flag per optional dimension named by that dimension's `flag` field in\n" +
			"the registry (e.g. --style, --scaffold-version), and one flag per variable the resolved\n" +
			"templates declare (e.g. --package). A selector flag left unset falls back to that\n" +
			"level's own `default`, if it declares one. Unknown flags are an error, not silently\n" +
			"ignored.\n\n" +
			"Three ways to look without writing anything:\n" +
			"    --dry-run   which files would be produced\n" +
			"    --print     what is actually in them, to stdout\n" +
			"    --explain   which level contributed each one, and what overrode what",
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

	// scaffold/template/name may come from the command line, from a -f values file, or a mix of
	// both, but all three must be supplied somewhere.
	scaffold, template, name, err := applyValuesFile(args)
	if err != nil {
		return err
	}

	// <name> becomes part of the write path, so it must not be able to escape <output>.
	if err := discovery.ValidateSegment("<name>", name); err != nil {
		return err
	}

	scaffoldingCodeRoot := resolveScaffoldingCodeRoot(args.value("scaffolding-code"))

	// The inheritance chain runs the full depth of the tree, outermost first; every level may
	// contribute files, dependencies and variables, and deeper levels win. Resolving it lives in
	// plan.go so `list` and `lint` reach exactly the same answer as `create`.
	p, err := resolvePlan(args, scaffoldingCodeRoot, scaffold, template, name)
	if err != nil {
		return err
	}

	args.markConsumed(engineFlags...)
	if err := args.requireAllFlagsConsumed(validFlagsFor(p.Dimensions, p.Walk, p.Manifests)); err != nil {
		return err
	}

	files, inserts, contributions, err := renderPlan(p)
	if err != nil {
		return err
	}

	output := args.value("output")
	if output == "" {
		output = "."
	}
	targetDir := filepath.Join(output, name)
	out := cmd.OutOrStdout()

	if args.value("print") == "true" {
		printRendered(out, files)
		printInserts(out, inserts)
		return nil
	}
	if args.value("explain") == "true" {
		printExplain(out, files, contributions, p, targetDir)
		printInserts(out, inserts)
		return nil
	}
	if args.value("dry-run") == "true" {
		printPlan(out, files, targetDir, p)
		printInserts(out, inserts)
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

	if len(inserts) > 0 {
		applied, skipped, err := render.ApplyInserts(targetDir, inserts)
		if err != nil {
			return err
		}
		if len(applied) > 0 {
			fmt.Fprintf(out, "\nSpliced into %d existing file(s):\n", len(applied))
			for _, p := range applied {
				fmt.Fprintf(out, "  %s\n", p)
			}
		}
		if len(skipped) > 0 {
			fmt.Fprintf(out, "\n%d insert(s) already present, skipped:\n", len(skipped))
			for _, p := range skipped {
				fmt.Fprintf(out, "  %s\n", p)
			}
		}
	}
	return nil
}

// loadLevel turns one already-resolved level of the tree (scaffold, version, dimension, or a node
// on the template chain) into a render source, or nil when that level has nothing of its own to
// contribute. root is the scaffolding-code root, used only to build a readable label for
// `--explain`.
func loadLevel(root, dir string) (*render.Source, error) {
	m, err := jig.LoadOptional(filepath.Join(dir, jig.FileName))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	if m == nil {
		return nil, nil
	}
	label := dir
	if rel, err := filepath.Rel(root, dir); err == nil {
		label = filepath.ToSlash(rel)
	}
	return &render.Source{
		Dir:      dir,
		Manifest: m,
		Label:    label,
		Priority: m.MergePriority,
	}, nil
}

// resolveOverlays validates and loads every optional dimension the user selected, returning the
// render sources sorted by merge_priority plus a flag->value map for the render context.
func resolveOverlays(args *parsedArgs, dimensions []discovery.Dimension, versionPath string) ([]render.Source, map[string]string, error) {
	selected := map[string]string{}
	var sources []render.Source

	for _, dim := range dimensions {
		if dim.Required {
			continue // the base dimension is handled by the template walk
		}
		value, ok := args.get(dim.Flag)
		if !ok {
			continue
		}
		dir, err := dim.ResolveValueDir(versionPath, value)
		if err != nil {
			return nil, nil, err
		}
		m, err := jig.Load(filepath.Join(dir, jig.FileName))
		if err != nil {
			return nil, nil, fmt.Errorf("loading --%s=%s: %w", dim.Flag, value, err)
		}
		selected[dim.Flag] = value
		sources = append(sources, render.Source{
			Dir:      dir,
			Manifest: m,
			Label:    fmt.Sprintf("--%s=%s", dim.Flag, value),
			Priority: m.MergePriority,
			Overlay:  true,
		})
	}

	sort.SliceStable(sources, func(i, j int) bool { return sources[i].Priority < sources[j].Priority })
	return sources, selected, nil
}

// checkReservedFlagNames rejects a manifest that names a selector, an overlay flag or a variable
// after a key a values file already gives a meaning to (see reservedValueKeys). Such a name would
// be ambiguous the moment both meanings appeared in the same values file, so it is rejected up
// front instead of surfacing later as a baffling error.
func checkReservedFlagNames(walk *discovery.WalkResult, dimensions []discovery.Dimension,
	manifests []*jig.Jig) error {

	report := func(kind, flag string) error {
		return fmt.Errorf("%s is named %q, which is reserved: in a values file %q is %s, so the "+
			"two could not be told apart. Rename it in the manifest",
			kind, flag, flag, jig.ReservedValueKeys[flag])
	}

	if walk != nil {
		for _, step := range walk.Steps {
			if _, bad := jig.ReservedValueKeys[step.Flag]; bad {
				return report("a selector", step.Flag)
			}
		}
		for _, cp := range walk.Checkpoints {
			for _, d := range cp.Overlays {
				if _, bad := jig.ReservedValueKeys[d.Flag]; bad {
					return report("an overlay flag", d.Flag)
				}
			}
		}
	}
	for _, d := range dimensions {
		if _, bad := jig.ReservedValueKeys[d.Flag]; bad && !d.Required {
			return report("an overlay flag", d.Flag)
		}
	}
	for _, m := range manifests {
		if m == nil {
			continue
		}
		for _, v := range m.Variables {
			flag := render.VariableFlagName(v)
			if _, bad := jig.ReservedValueKeys[flag]; bad {
				return report("a variable flag", flag)
			}
		}
	}
	return nil
}

// checkOverlayDefaults rejects an overlay that supplies a `default:` for a variable some other
// level already declares. Overlays are applied last, so under the single precedence rule an
// overlay's default would silently beat one declared earlier - but an overlay cannot know a
// correct default for a variable it does not own. Declaring a brand-new variable with a default is
// still fine, since that one it does own.
func checkOverlayDefaults(sources []render.Source) error {
	owner := map[string]string{}
	for _, s := range sources {
		if s.Overlay || s.Manifest == nil {
			continue
		}
		for _, v := range s.Manifest.Variables {
			if _, seen := owner[v.Name]; !seen {
				owner[v.Name] = s.Label
			}
		}
	}
	for _, s := range sources {
		if !s.Overlay || s.Manifest == nil {
			continue
		}
		for _, v := range s.Manifest.Variables {
			if v.Default == "" {
				continue
			}
			if declaredBy, clash := owner[v.Name]; clash {
				return fmt.Errorf("%s declares variable %q with a default, but %s already "+
					"declares it.\n"+
					"An overlay does not know which version or template it is applied to, so it "+
					"cannot know a correct default - and overlays are applied last, so this one "+
					"would silently win.\n"+
					"Drop the `default:` here, or move the variable out of the overlay entirely.",
					s.Label, v.Name, declaredBy)
			}
		}
	}
	return nil
}

// checkIncompatibilities enforces `incompatible_with` across what the user actually selected.
// Only cross-dimension constraints need this, since compatibility between selectors is already
// expressed by the folder structure itself. Absence of a rule means allowed - fail-open for
// declarations, fail-closed for typos.
func checkIncompatibilities(manifests []*jig.Jig, selectors, overlays map[string]string) error {
	active := map[string]bool{}
	for flag, value := range selectors {
		active[flag+":"+value] = true
	}
	for flag, value := range overlays {
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
// one per optional dimension, every selector consumed on the way to the leaf, and every variable
// the resolved templates declare.
func validFlagsFor(dimensions []discovery.Dimension, walk *discovery.WalkResult, manifests []*jig.Jig) []string {
	valid := append([]string{}, engineFlags...)
	for _, dim := range dimensions {
		if !dim.Required {
			valid = append(valid, dim.Flag)
		}
	}
	if walk != nil {
		for _, step := range walk.Steps {
			valid = append(valid, step.Flag)
		}
		for _, cp := range walk.Checkpoints {
			for _, d := range cp.Overlays {
				valid = append(valid, d.Flag)
			}
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

// printRendered writes every rendered file to stdout instead of to disk, so a template can be
// edited and re-run without a scratch directory - it answers "what is actually in them", as
// opposed to `--dry-run` ("which files") and `--explain` ("who contributed them"). The
// `==> path <==` marker follows tail(1)'s convention so it cannot be mistaken for file content.
func printRendered(out io.Writer, files []render.File) {
	for _, f := range files {
		fmt.Fprintf(out, "==> %s <==\n", f.Path)
		out.Write(f.Content)
		if len(f.Content) > 0 && f.Content[len(f.Content)-1] != '\n' {
			fmt.Fprintln(out)
		}
		fmt.Fprintln(out)
	}
}

// printInserts reports every pending anchor-based splice for the inspection modes (--print,
// --dry-run, --explain) - none of which touch disk, so this only ever describes what create
// would do, matching printRendered/printPlan/printExplain's "nothing written" contract.
func printInserts(out io.Writer, inserts []render.Insert) {
	if len(inserts) == 0 {
		return
	}
	fmt.Fprintf(out, "\nWould splice %d insert(s) into existing files:\n", len(inserts))
	for _, ins := range inserts {
		direction := "after"
		if !ins.After {
			direction = "before"
		}
		fmt.Fprintf(out, "  %s  insert_%s %q  (from %s)\n", ins.Path, direction, ins.Anchor, ins.Source)
	}
}

func printSelection(out io.Writer, p *plan) {
	if len(p.Walk.Steps) == 0 && len(p.SelectedOverlays) == 0 {
		return
	}
	fmt.Fprint(out, "Selected:")
	for _, step := range p.Walk.Steps {
		fmt.Fprintf(out, " --%s=%s", step.Flag, step.Value)
		if step.Defaulted {
			fmt.Fprint(out, "(default)")
		}
	}
	for flag, value := range p.SelectedOverlays {
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

	// The merged `.Data` object, shown as YAML since it is a document rather than a table.
	if len(p.Data) > 0 {
		fmt.Fprintln(out, "\nData (.Data, merged across the chain):")
		if encoded, err := yaml.Marshal(p.Data); err == nil {
			for _, line := range strings.Split(strings.TrimRight(string(encoded), "\n"), "\n") {
				fmt.Fprintf(out, "  %s\n", line)
			}
		}
	}

	fmt.Fprintf(out, "\nWould write %d file(s):\n", len(files))
	for _, f := range files {
		fmt.Fprintf(out, "  %s\n", f.Path)
	}
}

// printExplain answers "where did this file come from, and what else touched it?" - the merged
// result looks the same whether one source produced it or five fought over it, so without this the
// only way to tell is to simulate the merge by hand.
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

	// Anything dropped by `exclude:` is absent from files but present in contributions - exactly
	// what someone hunting a missing file needs to see.
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
