package cmd

import (
	"fmt"
	"path/filepath"
	"sort"

	"scaffold-engine-go/internal/discovery"
	"scaffold-engine-go/internal/manifest"
	"scaffold-engine-go/internal/render"
)

// plan is everything resolving one invocation produces, before anything is written.
//
// It exists because three commands need exactly the same answer and used to be able to disagree:
// `create` generates from it, `list` reports the variables it would ask for, and `lint` renders it
// to memory to prove a template works. Each computing its own version of "what would happen" is
// how a lint that passes and a create that fails end up coexisting.
type plan struct {
	Framework string
	Category  string
	Name      string
	Version   string

	Axes      []discovery.Axis
	Walk      *discovery.WalkResult
	Selectors map[string]string
	// SelectedAxes maps an optional axis's flag to the chosen value.
	SelectedAxes map[string]string

	Sources   []render.Source
	Manifests []*manifest.Manifest
	Context   render.Context
	Variables map[string]string
}

// resolvePlan walks the registries and manifests for one invocation without rendering anything.
func resolvePlan(args *parsedArgs, root, framework, category, name string) (*plan, error) {
	frameworkPath, err := discovery.ResolveFrameworkPath(root, framework)
	if err != nil {
		return nil, err
	}
	version, err := discovery.ResolveVersion(frameworkPath, args.value("fw-version"))
	if err != nil {
		return nil, fmt.Errorf("resolving version for framework %q: %w", framework, err)
	}
	versionPath := filepath.Join(frameworkPath, version)

	axes, err := discovery.DiscoverAxes(versionPath)
	if err != nil {
		return nil, fmt.Errorf("discovering axes for %s %s: %w", framework, version, err)
	}
	baseAxis, err := discovery.RequiredAxis(axes)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", framework, version, err)
	}
	templatesPath := baseAxis.Path(versionPath)

	categoryDir, err := discovery.ResolveCategoryDir(templatesPath, category)
	if err != nil {
		return nil, err
	}
	walk, err := discovery.WalkCategory(templatesPath, categoryDir, args.flags)
	if err != nil {
		return nil, err
	}
	for _, step := range walk.Steps {
		args.markConsumed(step.Flag)
	}

	// Sources in application order: framework, version, axis, then the category chain, then the
	// optional axis overlays sorted by merge_priority.
	p := &plan{
		Framework: framework, Category: category, Name: name, Version: version,
		Axes: axes, Walk: walk,
		Selectors: map[string]string{},
	}
	for _, dir := range []string{frameworkPath, versionPath, templatesPath} {
		src, err := loadLevel(dir)
		if err != nil {
			return nil, err
		}
		if src != nil {
			p.Sources = append(p.Sources, *src)
		}
	}
	for _, node := range walk.Chain {
		p.Sources = append(p.Sources, render.Source{
			Dir: node.Dir, Manifest: node.Manifest,
			Label: filepath.Base(node.Dir), Priority: node.Manifest.MergePriority,
		})
	}

	overlays, selectedAxes, err := resolveOverlays(args, axes, versionPath)
	if err != nil {
		return nil, err
	}
	p.Sources = append(p.Sources, overlays...)
	p.SelectedAxes = selectedAxes

	for _, s := range p.Sources {
		p.Manifests = append(p.Manifests, s.Manifest)
	}
	for _, step := range walk.Steps {
		p.Selectors[step.Flag] = step.Value
	}

	p.Variables, err = render.ResolveVariables(p.Manifests, render.VariableSource{
		Flags:        args.flags,
		Positional:   name,
		MarkConsumed: func(f string) { args.markConsumed(f) },
	})
	if err != nil {
		return nil, err
	}

	p.Context = render.BuildContext(p.Variables, nil,
		name, framework, version, category, p.Selectors, selectedAxes)
	if err := render.ApplyComputed(p.Context, p.Manifests); err != nil {
		return nil, err
	}
	deps, err := render.MergeDependencies(p.Manifests, p.Context)
	if err != nil {
		return nil, err
	}
	p.Context["Dependencies"] = deps

	if err := checkReservedFlagNames(walk, axes, p.Manifests); err != nil {
		return nil, err
	}
	if err := checkIncompatibilities(p.Manifests, p.Selectors, selectedAxes); err != nil {
		return nil, err
	}
	return p, nil
}

// renderPlan turns a resolved plan into the final file tree, plus a per-path record of who
// contributed what (used by --explain).
func renderPlan(p *plan) ([]render.File, map[string][]render.Contribution, error) {
	layout, err := render.CollectLayout(p.Manifests, p.Context)
	if err != nil {
		return nil, nil, err
	}

	trees := make([][]render.File, 0, len(p.Sources))
	for _, s := range p.Sources {
		s.Layout = layout
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
