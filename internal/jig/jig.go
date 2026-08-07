// Package jig defines the jig.yaml schema shared by every folder under
// scaffolding-code/<scaffold>/<version>/. A jig is a selector node, a registry, or a leaf
// template, distinguished by which fields are present.
package jig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Variable is a value the engine substitutes into rendered files. It resolves from a CLI flag,
// then a positional argument, then a default, erroring if required and still missing — this CLI
// never prompts interactively.
type Variable struct {
	Name   string `yaml:"name"`
	Prompt string `yaml:"prompt,omitempty"`

	// Flag is the CLI flag that fills this variable; empty means the kebab-case of Name.
	Flag string `yaml:"flag,omitempty"`

	// FromPositional binds this variable to a positional argument; the only supported value is
	// "name".
	FromPositional string `yaml:"from_positional,omitempty"`

	Default  string `yaml:"default,omitempty"`
	Required bool   `yaml:"required,omitempty"`
}

// LayoutRule rewrites a source path prefix into an output path prefix, and is inherited down the
// chain. It lets a template keep a plain source folder (e.g. "java/") while the real output path
// — such as a Java package directory — is declared once, high up, instead of per file. A deeper
// level redeclaring the same `from` replaces it.
type LayoutRule struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// Computed is a variable derived from other variables via a template, rather than supplied by the
// user — e.g. turning a dotted Java package name into its directory path. Computed variables
// evaluate in declaration order along the inheritance chain, so a later one may reference an
// earlier one.
type Computed struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// FileEntry is an optional per-file override, not a table of contents. The source folder's actual
// contents are the source of truth; an entry is only needed to change where a file lands, whether
// it's templated, or whether it's emitted at all.
type FileEntry struct {
	// Path is a file or directory, relative to the source folder, before placeholder
	// substitution. Naming a directory maps its whole subtree in one entry.
	Path string `yaml:"path"`

	// Target is where Path lands in the generated project, as a template; empty mirrors Path.
	// For a directory, Target is the destination prefix and everything underneath keeps its
	// relative structure.
	Target string `yaml:"target,omitempty"`

	// Template controls whether the file is rendered. It is a *bool so "absent" (render, the
	// default) is distinguishable from "false" (copy the bytes verbatim).
	Template *bool `yaml:"template,omitempty"`

	// Condition names a variable that must be truthy for this file to be emitted.
	Condition string `yaml:"condition,omitempty"`
}

// ShouldTemplate reports whether this entry's file should be rendered as a template.
func (f FileEntry) ShouldTemplate() bool {
	return f.Template == nil || *f.Template
}

// Dependency is one entry in a jig's `dependencies:` list — an opaque map of key/value pairs the
// engine never inspects, requires, or attaches meaning to. Its shape belongs entirely to the
// framework's build tool: Maven's groupId/artifactId, npm's name/version, and so on.
type Dependency map[string]string

// Get returns a field, or "" when absent. Templates see every dependency with the same key set
// (see the render package), so they can write `{{ .scope }}` without guarding.
func (d Dependency) Get(key string) string { return d[key] }

// Identity builds the value used to deduplicate a dependency against others, using the fields
// named by the jig's `dependency_key` (or every field, if that's unset).
func (d Dependency) Identity(keys []string) string {
	if len(keys) == 0 {
		keys = make([]string, 0, len(d))
		for k := range d {
			keys = append(keys, k)
		}
		sort.Strings(keys)
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+d[k])
	}
	return strings.Join(parts, "\x00")
}

// UnmarshalYAML accepts a mapping of scalars and rejects anything nested — a dependency
// coordinate is a flat set of fields in every build tool this engine is likely to meet.
func (d *Dependency) UnmarshalYAML(value *yaml.Node) error {
	var raw map[string]any
	if err := value.Decode(&raw); err != nil {
		return fmt.Errorf("a dependency must be a mapping of fields: %w", err)
	}
	out := make(Dependency, len(raw))
	for k, v := range raw {
		s, ok := scalarString(v)
		if !ok {
			return fmt.Errorf("dependency field %q must be a single value, not a list or a nested map", k)
		}
		out[k] = s
	}
	*d = out
	return nil
}

func scalarString(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", true
	case string:
		return t, true
	case bool:
		return strconv.FormatBool(t), true
	case int:
		return strconv.Itoa(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	default:
		return "", false
	}
}

// Verify is a command that proves a generated project actually builds, run only by `lint --build`.
// Command is an argv list (never a shell string, so there's nothing to inject into), `create`
// never executes it, and it only runs when the operator explicitly opts in with `--build`. Each
// element of Command is rendered against the context first, so a check can reference the
// project's own variables.
type Verify struct {
	// Name identifies the check; a deeper level declaring the same name overrides it.
	Name string `yaml:"name"`

	// Description is optional prose shown when the check fails.
	Description string `yaml:"description,omitempty"`

	// Command is the program and its arguments, run with the generated project as the working
	// directory.
	Command []string `yaml:"command"`

	// Timeout bounds one run, as a Go duration ("90s", "10m"). Empty means the engine's default.
	Timeout string `yaml:"timeout,omitempty"`
}

// Jig is the raw parse of a jig.yaml. Whether it's a leaf, selector, or registry is decided
// structurally, from which fields are present — there is no separate `type:` field.
type Jig struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	// Selector marks this node as an intermediate step: its subfolders are valid values for
	// the CLI flag named here. Empty means this is a leaf.
	Selector string `yaml:"selector,omitempty"`

	// Default is the child value assumed when --<Selector> is omitted at a selector node.
	// Not used for dimension-level registries — those use Values' per-entry Default flag instead.
	Default string `yaml:"default,omitempty"`

	// Values explicitly registers what's available under this folder (templates, pattern
	// names, or any dimension), so a stray folder can't silently become a usable value. Optional
	// — if empty, callers fall back to directory listing.
	Values []Entry `yaml:"values,omitempty"`

	// Required marks this dimension's own folder as the one whose walk resolves <template> - the
	// second CLI positional. If no dimension jig declares this, discovery falls back to treating
	// a folder literally named "templates" as required, for backward compatibility.
	Required bool `yaml:"required,omitempty"`

	Variables []Variable   `yaml:"variables,omitempty"`
	Computed  []Computed   `yaml:"computed,omitempty"`
	Layout    []LayoutRule `yaml:"layout,omitempty"`
	Files     []FileEntry  `yaml:"files,omitempty"`

	// Data is arbitrary structured content this level contributes to `.Data`, deep-merged down
	// the chain — the home for lists, nested objects, or code blocks that don't fit a flat
	// `variables:` scalar. The engine merges it and renders its strings, forming no opinion
	// about what any key means.
	Data map[string]any `yaml:"data,omitempty"`

	// DependencyFields declares every field a dependency may have for this framework. It
	// normalizes every entry to this field set (so templates can reference any of them
	// unguarded) and rejects any other field by name as a typo. Inherited like `layout`; the
	// deepest declaration wins.
	DependencyFields []string `yaml:"dependency_fields,omitempty"`

	// DependencyKey names the fields that together identify a dependency, for deduplication
	// when several levels declare the same one (e.g. Maven's groupId+artifactId). Inherited
	// like `layout`; deepest wins. Unset means every field participates.
	DependencyKey []string `yaml:"dependency_key,omitempty"`

	// Exclude drops inherited output paths that this level does not want, matched as glob
	// patterns against the output path, e.g. "src/test/java/**/ApplicationTests.java".
	Exclude      []string     `yaml:"exclude,omitempty"`
	Dependencies []Dependency `yaml:"dependencies,omitempty"`

	// Verify lists commands proving the generated project builds, run only by `lint --build`.
	// Inherited down the chain; a deeper level may override one by name.
	Verify []Verify `yaml:"verify,omitempty"`

	// Merge lists output paths that must be deep-merged when more than one source contributes
	// them, instead of the later source replacing the earlier one wholesale — application.yml
	// is the motivating case. Format is inferred from the extension (.yml/.yaml, .json today).
	Merge []string `yaml:"merge,omitempty"`

	// MergePriority orders file-overlay precedence when multiple overlays are selected at once.
	// Higher applies later (wins on same-path collisions). The template dimension itself is
	// implicitly always first regardless of this value.
	MergePriority int `yaml:"merge_priority,omitempty"`

	// IncompatibleWith lists other flag:value selections this leaf/value can't be combined
	// with, e.g. "protocol:rest-http".
	IncompatibleWith []string `yaml:"incompatible_with,omitempty"`
}

// DefaultValue returns the Values entry marked `default: true`, if any.
func (m *Jig) DefaultValue() (Entry, bool) {
	for _, v := range m.Values {
		if v.Default {
			return v, true
		}
	}
	return Entry{}, false
}

// FindValue looks up a registry entry by its CLI-facing name.
func (m *Jig) FindValue(name string) (Entry, bool) {
	for _, v := range m.Values {
		if v.Name == name {
			return v, true
		}
	}
	return Entry{}, false
}

// ValueNames returns the CLI-facing names of every registered value, for error messages.
func (m *Jig) ValueNames() []string {
	names := make([]string, 0, len(m.Values))
	for _, v := range m.Values {
		names = append(names, v.Name)
	}
	return names
}

// Shape is the structural classification of a jig: selector, registry, leaf, or unknown.
type Shape int

const (
	// ShapeUnknown is a jig that declares none of the three forms. It is an error, never a
	// silently-empty leaf.
	ShapeUnknown Shape = iota
	// ShapeSelector has `selector:` - recurse into the chosen subfolder.
	ShapeSelector
	// ShapeRegistry has `values:` but no `selector:` - it registers children, it never renders.
	ShapeRegistry
	// ShapeLeaf has files/dependencies/variables - this is the template to render.
	ShapeLeaf
)

func (s Shape) String() string {
	switch s {
	case ShapeSelector:
		return "selector"
	case ShapeRegistry:
		return "registry"
	case ShapeLeaf:
		return "leaf"
	default:
		return "unknown"
	}
}

// Shape classifies this jig structurally, from which fields are present — never from the
// folder's name or depth. A node with both `selector:` and `values:` counts as a selector, since
// the list is simply its authoritative set of selector values.
func (m *Jig) Shape() Shape {
	hasLeafFields := len(m.Files) > 0 || len(m.Dependencies) > 0 || len(m.Variables) > 0
	switch {
	case m.Selector != "":
		return ShapeSelector
	case hasLeafFields:
		return ShapeLeaf
	case len(m.Values) > 0:
		return ShapeRegistry
	default:
		return ShapeUnknown
	}
}

// IsSelector reports whether this jig is an intermediate selector node.
func (m *Jig) IsSelector() bool { return m.Shape() == ShapeSelector }

// IsLeaf reports whether this jig is a leaf template to render. Note this is no longer
// simply "not a selector" - a registry jig is neither (see Shape).
func (m *Jig) IsLeaf() bool { return m.Shape() == ShapeLeaf }

// IsRegistry reports whether this jig only registers its children.
func (m *Jig) IsRegistry() bool { return m.Shape() == ShapeRegistry }

// Validate checks a jig for combinations that cannot mean anything coherent — e.g. a variable or
// computed value with no name, or a malformed verify command. A metadata-only jig is allowed (a
// dimension needs nothing more), and so is `selector:` combined with content, since every level
// may contribute shared content under the inheritance model.
func (m *Jig) Validate(path string) error {
	for i, c := range m.Computed {
		if c.Name == "" || c.Value == "" {
			return fmt.Errorf("jig at %s: computed[%d] needs both `name` and `value`", path, i)
		}
	}
	for i, v := range m.Variables {
		if v.Name == "" {
			return fmt.Errorf("jig at %s: variables[%d] has no `name`", path, i)
		}
		if v.FromPositional != "" && v.FromPositional != "name" {
			return fmt.Errorf("jig at %s: variable %q has from_positional: %q, but the only "+
				"positional a variable can bind to is \"name\"", path, v.Name, v.FromPositional)
		}
	}
	// Verify commands are validated eagerly here, not discovered later during `lint --build`.
	for i, v := range m.Verify {
		switch {
		case v.Name == "":
			return fmt.Errorf("jig at %s: verify[%d] has no `name`", path, i)
		case len(v.Command) == 0:
			return fmt.Errorf("jig at %s: verify %q has no `command`", path, v.Name)
		case v.Command[0] == "":
			return fmt.Errorf("jig at %s: verify %q starts with an empty program name",
				path, v.Name)
		}
		if v.Timeout != "" {
			if _, err := time.ParseDuration(v.Timeout); err != nil {
				return fmt.Errorf("jig at %s: verify %q has timeout %q, which is not a "+
					"duration (use forms like \"90s\" or \"10m\"): %w", path, v.Name, v.Timeout, err)
			}
		}
	}
	return nil
}

// RequireNavigable rejects a jig that the selector walk cannot act on: a registry
// (`values:` with no `selector:`) declares children but has nothing to descend into or render. A
// jig declaring nothing at all is accepted as a valid, contentless leaf.
func (m *Jig) RequireNavigable(path string) error {
	if m.Shape() == ShapeRegistry {
		return fmt.Errorf("jig at %s only declares `values:` - it is a registry, not a "+
			"selector node or a leaf template, so there is nothing to descend into or render "+
			"(PRD Section 7.1)", path)
	}
	return nil
}

// Load reads, parses and validates jig.yaml at the given path, distinguishing "file absent" from
// "file present but broken". Use LoadOptional for the "absent is fine" case.
func Load(path string) (*Jig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading jig at %s: %w", path, err)
	}
	var m Jig
	if err := decodeStrict(data, &m, path); err != nil {
		return nil, err
	}
	if err := m.Validate(path); err != nil {
		return nil, err
	}
	return &m, nil
}

// LoadOptional loads a jig that is allowed not to exist, returning (nil, nil) in that case.
// Any other failure - unreadable, malformed YAML, or an unclassifiable shape - is still returned
// as an error, so an optional registry never degrades silently into a directory listing.
func LoadOptional(path string) (*Jig, error) {
	m, err := Load(path)
	if err != nil && errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return m, err
}

// Entry is one named, described item in an explicit registry list — a scaffold in
// scaffolding-code/jig.yaml, a version, a dimension, a template, a pattern, or any future
// dimension's own list. Name is its CLI-facing identity, Path is its folder on disk (defaults to
// Name), and Flag is the CLI flag that selects it when chosen by flag rather than positionally
// (defaults to Name).
type Entry struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Path        string `yaml:"path,omitempty"`
	Flag        string `yaml:"flag,omitempty"`
	Default     bool   `yaml:"default,omitempty"`

	// Inherits names another entry at the same level whose content this one builds on — e.g. a
	// Spring Boot 3.2 entry inheriting from 2.7 so only the actual differences need declaring.
	// Available at any registry level, not just versions - a version is simply the level that
	// happens to use it most.
	Inherits string `yaml:"inherits,omitempty"`
}

// DirName returns the on-disk folder name for this entry: Path if set, else Name.
func (e Entry) DirName() string {
	if e.Path != "" {
		return e.Path
	}
	return e.Name
}

// FlagName returns the CLI flag name for this entry: Flag if set, else Name.
func (e Entry) FlagName() string {
	if e.Flag != "" {
		return e.Flag
	}
	return e.Name
}

// decodeStrict parses YAML with unknown fields rejected, so a misspelled field (e.g.
// "dependancies") fails loudly instead of being silently ignored.
func decodeStrict(data []byte, into any, path string) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(into); err != nil {
		if errors.Is(err, io.EOF) {
			return nil // an empty file is a legitimate, if useless, jig
		}
		return fmt.Errorf("parsing jig at %s: %w", path, err)
	}
	return nil
}

// LoadRoot reads and parses the mandatory scaffold registry at scaffolding-code/jig.yaml. It is an
// ordinary Jig, same shape as every other registry level - the root's only special behaviour is
// that, unlike every other level, falling back to directory listing here is not allowed: without
// an explicit `values:` list, a stray folder like .git or .vscode would be registered as a
// scaffold.
func LoadRoot(path string) (*Jig, error) {
	m, err := Load(path)
	if err != nil {
		return nil, fmt.Errorf("loading root jig: %w", err)
	}
	if len(m.Values) == 0 {
		return nil, fmt.Errorf("root jig at %s registers no scaffolds (a `values:` "+
			"list is required - see PRD Section 4.1)", path)
	}
	return m, nil
}
