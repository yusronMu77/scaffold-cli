// Package render turns a resolved template chain into a rendered file tree. Every source is
// rendered first and merged afterward, since a templated application.yml isn't valid YAML and
// file collisions can only be keyed on post-substitution paths.
package render

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"scaffold-engine-go/internal/jig"
)

// Dependency is one entry of .Dependencies as a template sees it: a plain map whose keys are
// whatever the jig wrote. The engine has no opinion about what those keys are.
type Dependency map[string]string

// Context is the data every template is executed against. Resolved variables are promoted to
// top-level keys, so a template writes `{{ .ProjectName }}` rather than `{{ .Vars.ProjectName }}`.
// The engine's own keys are seeded first, then overwritten by variables so a jig can shadow them
// deliberately.
type Context map[string]any

// EngineFacts is the part of the context the engine knows before any variable is resolved: what
// was selected, and what it's called. Split out from BuildContext because a variable's default is
// itself a template that needs a context to render against before resolution happens.
func EngineFacts(name, framework, version, category string,
	selectors map[string]string, axes map[string]string) Context {

	return Context{
		"Name":         name,
		"Framework":    framework,
		"Version":      version,
		"Category":     category,
		"Dependencies": []Dependency(nil),
		"Selectors":    selectors,
		"Axes":         axes,
		// Seeded empty so `.Data` is always a map, even in a chain where nobody declared any.
		"Data": map[string]any{},
	}
}

// BuildContext assembles the render context from resolved variables plus the engine's own facts.
func BuildContext(vars map[string]string, deps []Dependency, name, framework, version, category string,
	selectors map[string]string, axes map[string]string) Context {

	ctx := EngineFacts(name, framework, version, category, selectors, axes)
	ctx["Dependencies"] = deps
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
	// still reject flags nobody claimed.
	MarkConsumed func(flag string)
	// Base is what the engine already knows - see EngineFacts. Jig DEFAULTS are rendered
	// against it plus the variables resolved so far.
	Base Context
}

// ResolveVariables fills every declared variable following precedence: explicit CLI flag, then
// from_positional, then jig default, then error if required. Variables are collected from the
// whole source list in order, with later sources winning on a name clash. A jig default is
// itself a template, evaluated against the engine facts plus variables resolved earlier in
// declaration order — referencing a variable declared later fails loudly instead of resolving to
// nothing. A value from a flag or values file is never rendered, since user input is data, not a
// template. `prompt` is help text only: a missing required variable produces an error naming the
// flag, never a hanging prompt.
func ResolveVariables(sources []*jig.Jig, src VariableSource) (map[string]string, error) {
	declared := map[string]jig.Variable{}
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

	// Grows as we go, so a default may build on a variable resolved before it.
	ctx := Context{}
	for k, v := range src.Base {
		ctx[k] = v
	}
	record := func(name, value string) {
		resolved[name] = value
		ctx[name] = value
	}

	for _, varName := range order {
		v := declared[varName]
		flagName := VariableFlagName(v)

		if value, ok := src.Flags[flagName]; ok {
			record(v.Name, value)
			if src.MarkConsumed != nil {
				src.MarkConsumed(flagName)
			}
			continue
		}
		if v.FromPositional == "name" {
			record(v.Name, src.Positional)
			continue
		}
		if v.Default != "" {
			value, err := renderString("default for variable "+v.Name, v.Default, ctx)
			if err != nil {
				return nil, err
			}
			record(v.Name, value)
			continue
		}
		if v.Required {
			missing = append(missing, fmt.Sprintf("--%s (%s)", flagName, describeVariable(v)))
			continue
		}
		record(v.Name, "")
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("missing required variable(s):\n  %s", strings.Join(missing, "\n  "))
	}
	return resolved, nil
}

func describeVariable(v jig.Variable) string {
	if v.Prompt != "" {
		return v.Prompt
	}
	return v.Name
}

// VariableFlagName returns the CLI flag that fills a variable: its `flag` field when declared,
// otherwise the kebab-case form of its name. Same principle as axes and selectors — the jig names
// the flag, the engine never invents one.
func VariableFlagName(v jig.Variable) string {
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

// ApplyComputed evaluates each `computed:` entry against the context and adds the result to it.
// Entries are processed in chain order, so a deeper level may build on a value computed higher
// up, and a later entry may reference an earlier one.
func ApplyComputed(ctx Context, sources []*jig.Jig) error {
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
// hands the result to templates as .Dependencies. Order comes from where a coordinate is first
// seen, so generated build files stay stable between runs; values come from the last declaration,
// field by field, so a leaf can override a field without restating the rest. Each entry is
// rendered against ctx, and identity for deduplication comes from `dependency_key` in the jigs
// (deepest wins). Every entry ends up with the same key set, missing fields filled with "", since
// templates run with missingkey=error.
func MergeDependencies(sources []*jig.Jig, ctx Context) ([]Dependency, error) {
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

	position := map[string]int{}
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
			id := jig.Dependency(rendered).Identity(key)
			for k := range rendered {
				observed[k] = true
			}
			if at, seen := position[id]; seen {
				// Same coordinate, declared again deeper down: keep its place in the list, take
				// its fields. Only fields this declaration actually sets are applied, so it can't
				// blank out a field the parent already supplied.
				for k, v := range rendered {
					if v != "" {
						out[at][k] = v
					}
				}
				continue
			}
			position[id] = len(out)
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

func renderDependency(d jig.Dependency, ctx Context) (Dependency, error) {
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

// RenderStrings renders a list of jig strings (exclude patterns, merge_yaml paths) against
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
