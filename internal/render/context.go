// Package render turns a resolved template chain into a rendered file tree.
//
// It implements PRD Sections 6 (steps 5-7), 7.3 (rendering contract), 7.4 (variable resolution)
// and 7.5 (render context). The order there is load-bearing: every source is rendered first and
// merged afterwards, because a templated application.yml is not valid YAML and file collisions
// can only be keyed on post-substitution paths.
package render

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"scaffold-engine-go/internal/manifest"
)

// Dependency is one entry of .Dependencies as a template sees it: a plain map, whose keys are
// whatever the manifest wrote. `{{ .groupId }}` for Maven, `{{ .name }}` for npm - the engine has
// no opinion (fundamental rule #1).
type Dependency map[string]string

// Context is the data every template is executed against (PRD Section 7.5).
//
// Resolved variables are promoted to top-level keys so a template writes `{{ .ProjectName }}`
// rather than `{{ .Vars.ProjectName }}`. The engine's own keys are seeded first and then
// overwritten by variables, so a manifest can shadow them deliberately - but it can never
// collide by accident with an internal field, because Context is a plain map.
type Context map[string]any

// BuildContext assembles the render context from resolved variables plus the engine's own facts.
func BuildContext(vars map[string]string, deps []Dependency, name, framework, version, category string,
	selectors map[string]string, axes map[string]string) Context {

	ctx := Context{
		"Name":         name,
		"Framework":    framework,
		"Version":      version,
		"Category":     category,
		"Dependencies": deps,
		"Selectors":    selectors,
		"Axes":         axes,
	}
	for k, v := range vars {
		ctx[k] = v
	}
	return ctx
}

// VariableSource carries everything the CLI knows that could fill a variable.
type VariableSource struct {
	// Flags is the raw --key=value map from the command line.
	Flags map[string]string
	// Positional is the <name> argument.
	Positional string
	// MarkConsumed is called for each flag actually used to fill a variable, so the caller can
	// still reject flags nobody claimed (PRD Section 8.6).
	MarkConsumed func(flag string)
}

// ResolveVariables fills every declared variable following PRD Section 7.4's precedence:
// explicit CLI flag -> from_positional -> manifest default -> error when required.
//
// Variables are collected from the whole source list in order, with later sources winning on a
// name clash - callers pass them in merge_priority order so the same rule governs variables as
// governs files and dependencies.
//
// `prompt` is never interactive: it is help text. A missing required variable produces an error
// naming the flag to pass, never a hanging prompt, so the CLI stays safe to call from scripts.
func ResolveVariables(sources []*manifest.Manifest, src VariableSource) (map[string]string, error) {
	declared := map[string]manifest.Variable{}
	var order []string
	for _, m := range sources {
		if m == nil {
			continue
		}
		for _, v := range m.Variables {
			if _, seen := declared[v.Name]; !seen {
				order = append(order, v.Name)
			}
			declared[v.Name] = v
		}
	}

	resolved := make(map[string]string, len(order))
	var missing []string

	for _, varName := range order {
		v := declared[varName]
		flagName := VariableFlagName(v)

		if value, ok := src.Flags[flagName]; ok {
			resolved[v.Name] = value
			if src.MarkConsumed != nil {
				src.MarkConsumed(flagName)
			}
			continue
		}
		if v.FromPositional == "name" {
			resolved[v.Name] = src.Positional
			continue
		}
		if v.Default != "" {
			resolved[v.Name] = v.Default
			continue
		}
		if v.Required {
			missing = append(missing, fmt.Sprintf("--%s (%s)", flagName, describeVariable(v)))
			continue
		}
		resolved[v.Name] = ""
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("missing required variable(s):\n  %s", strings.Join(missing, "\n  "))
	}
	return resolved, nil
}

func describeVariable(v manifest.Variable) string {
	if v.Prompt != "" {
		return v.Prompt
	}
	return v.Name
}

// VariableFlagName returns the CLI flag that fills a variable: its `flag` field when declared,
// otherwise the kebab-case form of its name (PRD Section 7.4). Same principle as axes and
// selectors - the manifest names the flag, the engine never invents it from something else.
func VariableFlagName(v manifest.Variable) string {
	if v.Flag != "" {
		return v.Flag
	}
	return kebab(v.Name)
}

// kebab converts PascalCase/camelCase to kebab-case: ProjectName -> project-name.
// Runs of capitals stay together, so HTTPPort -> http-port rather than h-t-t-p-port.
func kebab(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			prevLower := i > 0 && unicode.IsLower(runes[i-1])
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if i > 0 && (prevLower || nextLower) {
				b.WriteByte('-')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ApplyComputed evaluates each `computed:` entry against the context and adds the result to it
// (PRD Section 7.4). Entries are processed in chain order, so a deeper level may build on a value
// computed higher up, and a later entry may reference an earlier one.
func ApplyComputed(ctx Context, sources []*manifest.Manifest) error {
	for _, m := range sources {
		if m == nil {
			continue
		}
		for _, c := range m.Computed {
			value, err := renderString("computed variable "+c.Name, c.Value, ctx)
			if err != nil {
				return err
			}
			ctx[c.Name] = value
		}
	}
	return nil
}

// MergeDependencies unions the `dependencies:` declared across the chain, deduplicates them, and
// hands the result to templates as .Dependencies (PRD Section 6 step 7). First-seen order is
// preserved so generated build files are stable between runs.
//
// Three things happen to each entry, none of which assume anything about its field names:
//
//   - every value is rendered against ctx, so `groupId: "{{ .GroupId }}"` means what it looks
//     like. Without this a placeholder survived verbatim into the build file and failed much
//     later, in the build tool, naming neither the manifest nor the variable;
//   - identity for deduplication comes from `dependency_key` in the manifests (deepest wins), so
//     what counts as "the same dependency" is the build tool's rule, declared in data;
//   - every entry ends up with the SAME key set, missing ones filled with "". Templates run with
//     missingkey=error, so without this `{{ .scope }}` would be a hard error on the entries that
//     happen not to set it - forcing a guard around every optional field.
func MergeDependencies(sources []*manifest.Manifest, ctx Context) ([]Dependency, error) {
	var key, declaredFields []string
	for _, m := range sources {
		if m == nil {
			continue
		}
		if len(m.DependencyKey) > 0 {
			key = m.DependencyKey // deepest declaration wins
		}
		if len(m.DependencyFields) > 0 {
			declaredFields = m.DependencyFields
		}
	}

	allowed := map[string]bool{}
	for _, f := range declaredFields {
		allowed[f] = true
	}

	seen := map[string]bool{}
	observed := map[string]bool{}
	var out []Dependency

	for _, m := range sources {
		if m == nil {
			continue
		}
		for _, d := range m.Dependencies {
			if len(allowed) > 0 {
				var unknown []string
				for k := range d {
					if !allowed[k] {
						unknown = append(unknown, k)
					}
				}
				if len(unknown) > 0 {
					sort.Strings(unknown)
					return nil, fmt.Errorf("dependency has unknown field(s) %s\n"+
						"declared dependency_fields are: %s",
						strings.Join(unknown, ", "), strings.Join(declaredFields, ", "))
				}
			}
			rendered, err := renderDependency(d, ctx)
			if err != nil {
				return nil, err
			}
			id := manifest.Dependency(rendered).Identity(key)
			if seen[id] {
				continue
			}
			seen[id] = true
			for k := range rendered {
				observed[k] = true
			}
			out = append(out, rendered)
		}
	}

	// Normalise to one key set so templates can reference any field unguarded. Prefer the declared
	// list: inferring from what happens to be present breaks the moment an artefact kind omits a
	// field the shared template reads.
	fill := declaredFields
	if len(fill) == 0 {
		for k := range observed {
			fill = append(fill, k)
		}
		sort.Strings(fill)
	}
	for _, d := range out {
		for _, k := range fill {
			if _, ok := d[k]; !ok {
				d[k] = ""
			}
		}
	}
	return out, nil
}

func renderDependency(d manifest.Dependency, ctx Context) (Dependency, error) {
	out := make(Dependency, len(d))
	for k, v := range d {
		if v == "" {
			out[k] = ""
			continue
		}
		rendered, err := renderString("dependency field "+k, v, ctx)
		if err != nil {
			return nil, err
		}
		out[k] = rendered
	}
	return out, nil
}

// RenderStrings renders a list of manifest strings (exclude patterns, merge_yaml paths) against
// the context, so those may contain placeholders like every other path in the system.
func RenderStrings(what string, in []string, ctx Context) ([]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		v, err := renderString(what+" "+s, s, ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
