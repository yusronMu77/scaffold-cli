package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"scaffold-engine-go/internal/discovery"
	"scaffold-engine-go/internal/jig"
	"scaffold-engine-go/internal/render"
)

// plan is everything resolving one invocation produces, before anything is written. It exists so
// `create`, `list`, and `lint` all reach exactly the same answer instead of each computing its own
// version of "what would happen".
type plan struct {
	Scaffold string
	Template string
	Name     string
	Version  string

	Dimensions []discovery.Dimension
	Walk       *discovery.WalkResult
	Selectors  map[string]string
	// SelectedOverlays maps an optional dimension's flag to the chosen value.
	SelectedOverlays map[string]string

	Sources   []render.Source
	Manifests []*jig.Jig
	Context   render.Context
	Variables map[string]string
	// Data is the merged `data:` object templates see as .Data.
	Data map[string]any
}

// resolvePlan walks the registries and manifests for one invocation without rendering anything.
func resolvePlan(args *parsedArgs, root, scaffold, template, name string) (*plan, error) {
	scaffoldPath, err := discovery.ResolveScaffoldPath(root, scaffold)
	if err != nil {
		return nil, err
	}
	// Version is not a special engine concept - it is an ordinary registry level whose entries may
	// declare `inherits:`, resolved through the flag its own jig.yaml names via `selector:` (falling
	// back to "scaffold-version"). What looks like one version is therefore a chain of
	// directories, base first. Structure is read from the most derived version that declares it;
	// content is collected from every version in the chain, so a derived version writes only its
	// differences.
	versionFlag, err := discovery.VersionSelectorFlag(scaffoldPath)
	if err != nil {
		return nil, err
	}
	versionChain, err := discovery.ResolveVersionChain(scaffoldPath, args.value(versionFlag))
	if err != nil {
		return nil, fmt.Errorf("resolving version for scaffold %q: %w", scaffold, err)
	}
	version := versionChain[len(versionChain)-1]
	versionPaths := make([]string, 0, len(versionChain))
	for _, v := range versionChain {
		versionPaths = append(versionPaths, filepath.Join(scaffoldPath, v))
	}

	// Structure - which dimensions exist - is read from the most derived version that declares
	// any. A version existing only to override files declares none, and inherits the shape from
	// its base.
	structurePath, dimensions, err := discoverDimensionsInChain(versionPaths)
	if err != nil {
		return nil, fmt.Errorf("discovering dimensions for %s %s: %w", scaffold, version, err)
	}
	baseDimension, err := discovery.RequiredDimension(dimensions)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", scaffold, version, err)
	}
	templatesPath := baseDimension.Path(structurePath)

	// Below the base dimension, structure is resolved per node rather than per version: a derived
	// version can override one leaf without owning any of the directories above it.
	templatesPaths := make([]string, 0, len(versionPaths))
	for _, vp := range versionPaths {
		templatesPaths = append(templatesPaths, baseDimension.Path(vp))
	}

	templateDir, err := discovery.ResolveTemplateDir(templatesPath, template)
	if err != nil {
		return nil, err
	}
	walk, err := discovery.WalkCategoryChain(templatesPaths, templateDir, args.flags)
	if err != nil {
		return nil, err
	}
	for _, step := range walk.Steps {
		args.markConsumed(step.Flag)
	}

	// Sources in application order: scaffold, version, base dimension, then the template chain,
	// then the optional overlays sorted by merge_priority.
	p := &plan{
		Scaffold: scaffold, Template: template, Name: name, Version: version,
		Dimensions: dimensions, Walk: walk,
		Selectors: map[string]string{},
	}
	levels := []string{scaffoldPath}
	levels = append(levels, versionPaths...)
	for _, dir := range levels {
		src, err := loadLevel(root, dir)
		if err != nil {
			return nil, err
		}
		if src != nil {
			p.Sources = append(p.Sources, *src)
		}
	}

	// The base dimension level itself, across every version that declares it.
	for _, tp := range templatesPaths {
		if _, statErr := os.Stat(filepath.Join(tp, jig.FileName)); statErr != nil {
			continue
		}
		src, err := loadLevel(root, tp)
		if err != nil {
			return nil, err
		}
		if src != nil {
			p.Sources = append(p.Sources, *src)
		}
	}

	// Then every node the walk visited, base version first, so a derived version's copy overrides
	// the inherited one and a version that lacks the node contributes nothing.
	for _, node := range walk.Chain {
		for _, dir := range node.Dirs {
			src, err := loadLevel(root, dir)
			if err != nil {
				return nil, err
			}
			if src != nil {
				p.Sources = append(p.Sources, *src)
			}
		}
	}

	overlays, selectedOverlays, err := resolveOverlays(args, dimensions, structurePath)
	if err != nil {
		return nil, err
	}
	p.Sources = append(p.Sources, overlays...)

	// Nested dimension checkpoints found while walking the template's selector chain (PRD Section
	// 0/4.1) get the exact same treatment as the top-level dimension list - same function, same
	// merge semantics, just resolved against wherever the checkpoint was found instead of the
	// version root.
	for _, cp := range walk.Checkpoints {
		cpOverlays, cpSelected, err := resolveOverlays(args, cp.Overlays, cp.Dir)
		if err != nil {
			return nil, err
		}
		p.Sources = append(p.Sources, cpOverlays...)
		for flag, value := range cpSelected {
			selectedOverlays[flag] = value
		}
	}
	p.SelectedOverlays = selectedOverlays

	for _, s := range p.Sources {
		p.Manifests = append(p.Manifests, s.Manifest)
	}
	for _, step := range walk.Steps {
		p.Selectors[step.Flag] = step.Value
	}

	if err := checkOverlayDefaults(p.Sources); err != nil {
		return nil, err
	}

	// Manifest defaults are templates, so resolution needs the engine's own facts up front.
	base := render.EngineFacts(name, scaffold, version, template, p.Selectors, selectedOverlays)
	p.Variables, err = render.ResolveVariables(p.Manifests, render.VariableSource{
		Flags:        args.flags,
		Positional:   name,
		MarkConsumed: func(f string) { args.markConsumed(f) },
		Base:         base,
	})
	if err != nil {
		return nil, err
	}

	p.Context = render.BuildContext(p.Variables, nil,
		name, scaffold, version, template, p.Selectors, selectedOverlays)
	if err := render.ApplyComputed(p.Context, p.Manifests); err != nil {
		return nil, err
	}

	// Data comes after computed variables so a snippet may reference one, and before dependencies
	// so a coordinate may read a version out of it. It cannot reference itself: strings in `data:`
	// are rendered against the variable context, not against the object they belong to.
	p.Data, err = render.MergeData(p.Manifests, args.data, p.Context)
	if err != nil {
		return nil, err
	}
	p.Context["Data"] = p.Data

	deps, err := render.MergeDependencies(p.Manifests, p.Context)
	if err != nil {
		return nil, err
	}
	p.Context["Dependencies"] = deps

	if err := checkReservedFlagNames(walk, dimensions, p.Manifests); err != nil {
		return nil, err
	}
	if err := checkIncompatibilities(p.Manifests, p.Selectors, selectedOverlays); err != nil {
		return nil, err
	}
	return p, nil
}

// discoverDimensionsInChain finds the dimension structure, searching the version chain from most
// derived to base and returning the first version that declares a usable one. A version that
// exists only to override a handful of files declares no dimensions, so falling back to its base
// is what makes `inherits:` mean what it says.
func discoverDimensionsInChain(versionPaths []string) (string, []discovery.Dimension, error) {
	var lastErr error
	for i := len(versionPaths) - 1; i >= 0; i-- {
		vp := versionPaths[i]
		dimensions, err := discovery.DiscoverDimensions(vp)
		if err != nil {
			lastErr = err
			continue
		}
		base, err := discovery.RequiredDimension(dimensions)
		if err != nil {
			lastErr = err
			continue
		}
		// The dimension folder existing is not enough - it must declare itself via a manifest, or
		// a version that merely has the directory on the way to an overridden file would be
		// mistaken for one that defines the structure.
		if _, statErr := os.Stat(filepath.Join(base.Path(vp), jig.FileName)); statErr != nil {
			lastErr = fmt.Errorf("dimension %q under %s has no %s", base.Name, vp, jig.FileName)
			continue
		}
		return vp, dimensions, nil
	}
	if lastErr != nil {
		return "", nil, lastErr
	}
	return "", nil, fmt.Errorf("no version in the chain declares any dimension")
}

// renderPlan turns a resolved plan into the final file tree, plus a per-path record of who
// contributed what (used by --explain).
func renderPlan(p *plan) ([]render.File, map[string][]render.Contribution, error) {
	layout, err := render.CollectLayout(p.Manifests, p.Context)
	if err != nil {
		return nil, nil, err
	}

	// Partials are collected across every source before anything renders, so a fragment declared
	// at the scaffold level is available to a leaf template seven levels down without being
	// mentioned again. Deeper definitions of the same name win, like everything else.
	partials, err := render.CollectPartials(p.Sources)
	if err != nil {
		return nil, nil, err
	}

	trees := make([][]render.File, 0, len(p.Sources))
	for _, s := range p.Sources {
		s.Layout = layout
		s.Partials = partials
		tree, err := render.RenderSource(s, p.Context)
		if err != nil {
			return nil, nil, err
		}
		trees = append(trees, tree)
	}

	files, contributions, err := render.MergeExplained(trees)
	if err != nil {
		return nil, nil, err
	}

	var excludes []string
	for _, m := range p.Manifests {
		if m == nil {
			continue
		}
		rendered, err := render.RenderStrings("exclude pattern", m.Exclude, p.Context)
		if err != nil {
			return nil, nil, err
		}
		excludes = append(excludes, rendered...)
	}
	files, err = render.Exclude(files, excludes)
	if err != nil {
		return nil, nil, err
	}
	return files, contributions, nil
}

// declaredVariables lists every variable the resolved chain declares, sorted by flag name, so the
// CLI can tell a user what they may set without making them read manifests.
type declaredVariable struct {
	Name     string
	Flag     string
	Default  string
	Prompt   string
	Required bool
	From     string // the manifest that declared it, for "where does this come from?"
}

func declaredVariables(p *plan) []declaredVariable {
	seen := map[string]int{}
	var out []declaredVariable
	for i, m := range p.Manifests {
		if m == nil {
			continue
		}
		for _, v := range m.Variables {
			d := declaredVariable{
				Name: v.Name, Flag: render.VariableFlagName(v), Default: v.Default,
				Prompt: v.Prompt, Required: v.Required, From: filepath.Base(p.Sources[i].Dir),
			}
			if v.FromPositional == "name" {
				d.Default = "<name>"
			}
			if idx, ok := seen[v.Name]; ok {
				out[idx] = d // deeper declaration wins, same as resolution
				continue
			}
			seen[v.Name] = len(out)
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Flag < out[j].Flag })
	return out
}
