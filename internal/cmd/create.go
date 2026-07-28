package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"scaffold-engine-go/internal/discovery"
)

// engineFlags are the flags the engine itself owns at every invocation, as opposed to the
// selector/axis/variable flags that come from manifest content.
var engineFlags = []string{"fw-version", "output", "scaffolding-code", "force", "skip-existing", "no-hooks"}

func newCreateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "create <framework> <category> <name> [--flag=value ...]",
		Short: "Generate a new artefact (service, lib, parent, ...) from scaffolding-code",
		Long: "scaffold create <framework> <category> <name> [--flag=value ...]\n\n" +
			"All three positional arguments are required (PRD Section 13.1 rule #5). <framework>\n" +
			"and <category> are resolved through the registries under scaffolding-code/ and are\n" +
			"never hardcoded (rule #2); <name> is the artefact's identifier and must be a single\n" +
			"path segment.\n\n" +
			"Flags use --key=value; a flag with no '=' is a boolean set to true. Which flags are\n" +
			"valid depends on the category's selector chain (e.g. --function/--protocol for\n" +
			"'services', --category for 'libs', none for 'parent') plus one flag per optional\n" +
			"axis, named by that axis's `flag` field in the registry (e.g. --style for the\n" +
			"patterns axis). A selector flag left unset falls back to that level's own `default`,\n" +
			"if it declares one. Unknown flags are an error, not silently ignored.",
		DisableFlagParsing: true,
		RunE:               runCreate,
	}
}

func runCreate(cmd *cobra.Command, rawArgs []string) error {
	args := parseArgs(rawArgs)
	if args.help {
		return cmd.Help()
	}
	if len(args.positional) != 3 {
		return fmt.Errorf("usage: scaffold create <framework> <category> <name> [--flag=value ...]\n"+
			"got %d positional argument(s); all three are required", len(args.positional))
	}
	framework, category, name := args.positional[0], args.positional[1], args.positional[2]

	// <name> becomes part of the write path, so it must not be able to escape <output>.
	if err := discovery.ValidateSegment("<name>", name); err != nil {
		return err
	}

	scaffoldingCodeRoot := resolveScaffoldingCodeRoot(args.value("scaffolding-code"))
	frameworkPath, err := discovery.ResolveFrameworkPath(scaffoldingCodeRoot, framework)
	if err != nil {
		return err
	}

	version, err := discovery.ResolveVersion(frameworkPath, args.value("fw-version"))
	if err != nil {
		return fmt.Errorf("resolving version for framework %q: %w", framework, err)
	}
	versionPath := filepath.Join(frameworkPath, version)

	axes, err := discovery.DiscoverAxes(versionPath)
	if err != nil {
		return fmt.Errorf("discovering axes for %s %s: %w", framework, version, err)
	}
	baseAxis, err := discovery.RequiredAxis(axes)
	if err != nil {
		return fmt.Errorf("%s %s: %w", framework, version, err)
	}
	// Path() applies the axis's `path` alias. Joining baseAxis.Name here instead is what made
	// aliasing silently non-functional for axes (design review 2026-07-27 section 2.2).
	templatesPath := baseAxis.Path(versionPath)

	categoryDir, err := discovery.ResolveCategoryDir(templatesPath, category)
	if err != nil {
		return err
	}
	result, err := discovery.WalkCategory(templatesPath, categoryDir, args.flags)
	if err != nil {
		return err // WalkCategory already names the category
	}
	for _, step := range result.Steps {
		args.markConsumed(step.Flag)
	}

	// Optional axes are selected by their declared flag name, not their folder name.
	selectedAxes := map[string]string{}
	for _, axis := range axes {
		if axis.Required {
			continue // the base axis - already resolved via the category walk above
		}
		value, ok := args.get(axis.Flag)
		if !ok {
			continue
		}
		if _, err := axis.ResolveValueDir(versionPath, value); err != nil {
			return err
		}
		selectedAxes[axis.Flag] = value
	}

	args.markConsumed(engineFlags...)
	if err := args.requireAllFlagsConsumed(validFlagsFor(axes, result)); err != nil {
		return err
	}

	output := args.value("output")
	if output == "" {
		output = "."
	}
	targetDir := filepath.Join(output, name)

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "framework=%s version=%s category=%s", framework, version, category)
	if categoryDir != category {
		fmt.Fprintf(out, " (folder: %s)", categoryDir)
	}
	fmt.Fprintf(out, " name=%s\n", name)
	if len(result.Steps) > 0 {
		fmt.Fprint(out, "selector path:")
		for _, step := range result.Steps {
			fmt.Fprintf(out, " --%s=%s", step.Flag, step.Value)
			if step.Defaulted {
				fmt.Fprint(out, "(defaulted)")
			}
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintf(out, "leaf template: %s (%s)\n", result.Leaf.Name, result.LeafDir)
	fmt.Fprintf(out, "would write to: %s\n", targetDir)
	fmt.Fprintf(out, "files declared: %d, dependencies declared: %d, chain nodes: %d\n",
		len(result.Leaf.Files), len(result.Leaf.Dependencies), len(result.Chain))

	for _, axis := range axes {
		if v, ok := selectedAxes[axis.Flag]; ok {
			fmt.Fprintf(out, "axis %q selected via --%s: %s\n", axis.Name, axis.Flag, v)
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "NOTE: this reports what would be generated (Phase 3a - discovery "+
		"mechanism only). Actual manifest merging, template rendering, and file writing is Phase 3b, "+
		"not yet implemented.")
	return nil
}

// validFlagsFor builds the list shown when an unknown flag is rejected: the engine's own flags,
// one per optional axis (by its declared flag name), and every selector consumed on the way to
// the leaf.
func validFlagsFor(axes []discovery.Axis, result *discovery.WalkResult) []string {
	valid := append([]string{}, engineFlags...)
	for _, axis := range axes {
		if !axis.Required {
			valid = append(valid, axis.Flag)
		}
	}
	if result != nil {
		for _, step := range result.Steps {
			valid = append(valid, step.Flag)
		}
	}
	return valid
}
