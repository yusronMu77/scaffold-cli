// Package manifest defines the manifest.yaml schema shared by every folder under
// scaffolding-code/<framework>/<version>/. PRD Section 7 (v1.4) defines two shapes that live
// in the same file format, distinguished purely by which fields are present:
//   - selector: an intermediate node — its subfolders are values for --<Selector>.
//   - leaf: files/dependencies/variables — the actual template to render.
package manifest

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Variable is a value the engine substitutes into rendered files, sourced from a CLI flag
// (e.g. --name, --package) or its own default.
type Variable struct {
	Name    string `yaml:"name"`
	Prompt  string `yaml:"prompt"`
	Default string `yaml:"default"`
}

// FileEntry is one file or directory copied (and optionally templated) into the output.
type FileEntry struct {
	Path     string `yaml:"path"`
	Template bool   `yaml:"template"`
}

// Dependency is a Maven coordinate to add to the generated pom.xml (groupId+artifactId only —
// the version comes from the Parent POM per PRD Section 6).
type Dependency struct {
	GroupID    string `yaml:"groupId"`
	ArtifactID string `yaml:"artifactId"`
}

// PostHook is a shell command run after generation, gated by a condition expression.
type PostHook struct {
	Command   string `yaml:"command"`
	Condition string `yaml:"condition"`
}

// Manifest is the raw parse of a manifest.yaml. Whether it's a leaf or a selector node is
// decided structurally (see IsSelector/IsLeaf) — there is no separate "kind" field, per PRD
// Section 7 and fundamental rule #3 (Section 13.1).
type Manifest struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Type        string `yaml:"type,omitempty"`

	// Selector marks this node as an intermediate step: its subfolders are valid values for
	// the CLI flag named here (e.g. "function", "protocol", "category"). Empty means this is
	// a leaf.
	Selector string `yaml:"selector,omitempty"`

	// Default is the child value assumed when --<Selector> is omitted at a selector node
	// (e.g. services/manifest.yaml: selector "function", default "web"). Not used for
	// axis-level registries (templates/manifest.yaml, patterns/manifest.yaml) - those use
	// Values' per-entry Default flag instead, see below.
	Default string `yaml:"default,omitempty"`

	// Values explicitly registers what's available under this folder - categories under
	// templates/manifest.yaml, pattern names under patterns/manifest.yaml, or any future
	// axis's own manifest.yaml. Same reasoning as the root framework registry (fundamental
	// rule #2, Section 13.1): the engine reads this list rather than trusting raw directory
	// listing, so a stray folder can't silently become a usable value, and each entry can
	// carry its own description/default/path override. Optional - if empty, callers fall back
	// to directory listing (see discovery.DiscoverAxes).
	Values []Entry `yaml:"values,omitempty"`

	// Required marks this axis-level manifest's own folder as the mandatory base axis (only
	// meaningful in a top-level axis manifest, e.g. templates/manifest.yaml) - discovery reads
	// this instead of hardcoding a folder name, so which axis is "the required one" is a
	// declared fact, not an assumption baked into the engine (Section 13.1, fundamental rule
	// #2). If no axis manifest declares this at all, discovery.DiscoverAxes falls back to
	// treating a folder literally named "templates" as required, for backward compatibility.
	Required bool `yaml:"required,omitempty"`

	Variables    []Variable   `yaml:"variables,omitempty"`
	Files        []FileEntry  `yaml:"files,omitempty"`
	Dependencies []Dependency `yaml:"dependencies,omitempty"`
	PostHooks    []PostHook   `yaml:"post_hooks,omitempty"`

	// MergePriority orders file-overlay precedence when multiple axes are selected at once
	// (implementation-plan Open Question #7). Higher applies later (wins on same-path
	// collisions). The base "templates" axis is implicitly always first regardless of this
	// value.
	MergePriority int `yaml:"merge_priority,omitempty"`

	// IncompatibleWith lists other axis:value selections this leaf/value can't be combined
	// with, e.g. "protocol:rest-http" (implementation-plan Open Question #8).
	IncompatibleWith []string `yaml:"incompatible_with,omitempty"`
}

// DefaultValue returns the Values entry marked `default: true`, if any.
func (m *Manifest) DefaultValue() (Entry, bool) {
	for _, v := range m.Values {
		if v.Default {
			return v, true
		}
	}
	return Entry{}, false
}

// FindValue looks up a registry entry by its CLI-facing name.
func (m *Manifest) FindValue(name string) (Entry, bool) {
	for _, v := range m.Values {
		if v.Name == name {
			return v, true
		}
	}
	return Entry{}, false
}

// ValueNames returns the CLI-facing names of every registered value, for error messages.
func (m *Manifest) ValueNames() []string {
	names := make([]string, 0, len(m.Values))
	for _, v := range m.Values {
		names = append(names, v.Name)
	}
	return names
}

// Shape is the structural classification of a manifest. PRD v1.8 Section 7.1 replaced the old
// binary leaf-vs-selector test with three shapes: v1.7 added the registry form, but IsLeaf was
// still defined as "no selector field", so a registry manifest - and equally an empty one, or one
// whose `selector` key was misspelled - was classified as a renderable leaf and produced an empty
// project with no error at all (design review 2026-07-27 section 2.9).
type Shape int

const (
	// ShapeUnknown is a manifest that declares none of the three forms. It is an error, never a
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

// Shape classifies this manifest structurally - from which fields are present, never from the
// folder's name or depth (fundamental rule #3).
//
// A node may legitimately be both a selector and a registry: `services/manifest.yaml` can declare
// `selector: function` alongside a `values:` list of the valid functions. That is not ambiguous -
// the list is simply the authoritative set of values for that selector - so ShapeSelector wins.
func (m *Manifest) Shape() Shape {
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

// IsSelector reports whether this manifest is an intermediate selector node.
func (m *Manifest) IsSelector() bool { return m.Shape() == ShapeSelector }

// IsLeaf reports whether this manifest is a leaf template to render. Note this is no longer
// simply "not a selector" - a registry manifest is neither (see Shape).
func (m *Manifest) IsLeaf() bool { return m.Shape() == ShapeLeaf }

// IsRegistry reports whether this manifest only registers its children.
func (m *Manifest) IsRegistry() bool { return m.Shape() == ShapeRegistry }

// Validate rejects the one field combination that is genuinely ambiguous at any level.
//
// It deliberately does NOT reject ShapeUnknown: a metadata-only manifest is perfectly legitimate
// at the axis level, where `name`/`description`/`required` are all that is needed and the axis's
// values come from directory listing. ShapeUnknown only becomes an error where a manifest must be
// renderable or navigable - see RequireNavigable, called from the selector walk.
func (m *Manifest) Validate(path string) error {
	if m.Selector != "" && (len(m.Files) > 0 || len(m.Dependencies) > 0) {
		return fmt.Errorf("manifest at %s is ambiguous: it declares `selector: %s` and also "+
			"leaf content (files/dependencies) - split the selector node and the leaf into "+
			"separate folders", path, m.Selector)
	}
	return nil
}

// RequireNavigable rejects a manifest that the selector walk cannot act on: it must either
// recurse (selector) or render (leaf).
//
// This is where the old binary classification did real damage. IsLeaf() was "no selector field",
// so a v1.7 registry manifest, an empty manifest, and a manifest whose `selector` key was
// misspelled were all treated as renderable leaves - each producing an empty project with no
// error at all (design review 2026-07-27 section 2.9). PRD v1.8 Section 7.1 makes these errors.
func (m *Manifest) RequireNavigable(path string) error {
	switch m.Shape() {
	case ShapeSelector, ShapeLeaf:
		return nil
	case ShapeRegistry:
		return fmt.Errorf("manifest at %s only declares `values:` - it is a registry, not a "+
			"selector node or a leaf template, so there is nothing to descend into or render "+
			"(PRD Section 7.1)", path)
	default:
		return fmt.Errorf("manifest at %s declares none of `selector:`, `values:`, or "+
			"`files:`/`dependencies:`/`variables:` - a node on a category's selector chain must "+
			"be either a selector node or a leaf template (PRD Section 7.1). A misspelled "+
			"`selector` key is the usual cause", path)
	}
}

// Load reads, parses and validates manifest.yaml at the given path.
//
// Callers must distinguish "file absent" from "file present but broken": treating every error as
// absence is what let a malformed registry silently downgrade to raw directory listing, changing
// which artefact got generated without telling anyone (design review 2026-07-27 section 2.7).
// Use LoadOptional for the "absent is fine" case - it keeps parse errors fatal.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading manifest at %s: %w", path, err)
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest at %s: %w", path, err)
	}
	if err := m.Validate(path); err != nil {
		return nil, err
	}
	return &m, nil
}

// LoadOptional loads a manifest that is allowed not to exist, returning (nil, nil) in that case.
// Any other failure - unreadable, malformed YAML, or an unclassifiable shape - is still returned
// as an error, so an optional registry never degrades silently into a directory listing.
func LoadOptional(path string) (*Manifest, error) {
	m, err := Load(path)
	if err != nil && errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return m, err
}

// Entry is one named, described item in an explicit registry list - a framework in
// scaffolding-code/manifest.yaml, an axis in <version>/manifest.yaml, a category in
// templates/manifest.yaml, a pattern name in patterns/manifest.yaml, or any future axis's own
// list. Always the same shape at every level.
//
// PRD v1.8 Section 4.1 separates three identities that earlier versions conflated into one or
// two fields - which was the root cause of two real bugs (design review 2026-07-27 sections 2.2
// and 2.3):
//
//	Name - the entry's identity: what `scaffold list` shows, and what the user types for a
//	       positional argument (<framework>, <category>).
//	Path - the physical folder on disk. Defaults to Name.
//	Flag - the CLI flag that selects this entry, for entries chosen by flag rather than
//	       positionally (an axis). Defaults to Name.
//
// Keeping Flag distinct from Name is what lets an axis whose folder is patterns/ be driven by
// --style, which is the form every design document uses. It also means a flag name is always a
// declared fact from the manifest, never something the engine infers from a folder name -
// the same principle as the `selector: <flag-name>` field on selector nodes.
type Entry struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Path        string `yaml:"path,omitempty"`
	Flag        string `yaml:"flag,omitempty"`
	Default     bool   `yaml:"default,omitempty"`
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

// Framework is an Entry in the root registry (scaffolding-code/manifest.yaml) - kept as a
// distinct name for readability at call sites, same underlying shape as any other Entry.
type Framework = Entry

// RootManifest is scaffolding-code/manifest.yaml - the registry of supported frameworks. It
// exists so "which frameworks are available" is an explicit, manifest-driven answer (per
// fundamental rule #2, PRD Section 13.1) rather than raw directory listing, which would
// otherwise also pick up .git, .vscode, or any stray folder at the scaffolding-code root.
type RootManifest struct {
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	Frameworks  []Framework `yaml:"frameworks"`
}

// FindFramework looks up a framework by its CLI-facing name.
func (r *RootManifest) FindFramework(name string) (Framework, bool) {
	for _, f := range r.Frameworks {
		if f.Name == name {
			return f, true
		}
	}
	return Framework{}, false
}

// FrameworkNames returns every registered framework's CLI-facing name, for error messages.
func (r *RootManifest) FrameworkNames() []string {
	names := make([]string, 0, len(r.Frameworks))
	for _, f := range r.Frameworks {
		names = append(names, f.Name)
	}
	return names
}

// LoadRoot reads and parses the framework registry at scaffolding-code/manifest.yaml. Unlike
// every other level, this one is mandatory (PRD v1.8 Section 4.1): falling back to a directory
// listing here would register .git, .vscode and any stray folder as a "framework".
func LoadRoot(path string) (*RootManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading root manifest at %s: %w", path, err)
	}
	var m RootManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing root manifest at %s: %w", path, err)
	}
	if len(m.Frameworks) == 0 {
		return nil, fmt.Errorf("root manifest at %s registers no frameworks (a `frameworks:` "+
			"list is required - see PRD Section 4.1)", path)
	}
	return &m, nil
}
