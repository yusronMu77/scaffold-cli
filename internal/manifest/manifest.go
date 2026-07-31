// Package manifest defines the manifest.yaml schema shared by every folder under
// scaffolding-code/<framework>/<version>/. PRD Section 7 defines the shapes that live
// in the same file format, distinguished purely by which fields are present:
//   - selector: an intermediate node — its subfolders are values for --<Selector>.
//   - leaf: files/dependencies/variables — the actual template to render.
package manifest

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Variable is a value the engine substitutes into rendered files (PRD Section 7.4).
//
// Where the value comes from is resolved in this order: the CLI flag named by Flag (defaulting
// to the kebab-case of Name), then FromPositional, then Default, then an error if Required.
// Prompt is help text only - this CLI is never interactive, so a missing value produces an error
// naming the flag rather than a prompt that would hang in a script.
type Variable struct {
	Name   string `yaml:"name"`
	Prompt string `yaml:"prompt,omitempty"`

	// Flag is the CLI flag that fills this variable. Empty means kebab-case of Name
	// (ProjectName -> --project-name). Declaring it keeps flag naming a manifest decision,
	// exactly like `selector:` on a node and `flag:` on a registry entry.
	Flag string `yaml:"flag,omitempty"`

	// FromPositional binds this variable to a positional argument; the only supported value is
	// "name". This is what avoids hardcoding a variable called "ProjectName" in the engine -
	// which template author's variable receives <name> is their choice, not ours (rule #2).
	FromPositional string `yaml:"from_positional,omitempty"`

	Default  string `yaml:"default,omitempty"`
	Required bool   `yaml:"required,omitempty"`
}

// LayoutRule rewrites a source path prefix into an output path prefix, and is INHERITED down the
// chain (PRD Section 7.3).
//
// It exists so writing a template stays cheap. The only genuinely awkward part of a Java layout is
// the package directory, whose name is derived from a dotted variable and so cannot exist
// literally on disk. Declaring the mapping once, high up:
//
//	layout:
//	  - from: "java"
//	    to: "src/main/java/{{ .PackagePath }}"
//
// means every template below - mvc/, and the ddd/ or hexagonal/ siblings added later - just drops
// files into a plain `java/` folder and declares nothing at all. Real `controller/`, `service/`
// subfolders stay visible in the source tree, and the deep output directories are created on
// demand when the files are written.
//
// A deeper level redeclaring the same `from` replaces it, so a template with an unusual layout can
// still opt out. To is a template, evaluated against the render context.
type LayoutRule struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// Computed is a variable derived from other variables rather than supplied by the user. Its
// Value is itself a template, evaluated against the context built from the resolved variables.
//
// This exists because some derived value is always needed and the engine must not know what it
// is. The motivating case is a Java package: `com.company.app` has to become the directory
// `com/company/app`, but a path segment on disk cannot contain a separator, so the source folder
// is named `{{ .PackagePath }}` and PackagePath is computed here. Teaching the engine about Java
// packages instead would break fundamental rules #1 and #2; a computed variable keeps the
// knowledge in the data where it belongs, and works for any framework's equivalent need.
//
// Computed variables are evaluated in declaration order along the inheritance chain, so a later
// one may reference an earlier one.
type Computed struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// FileEntry is an OPTIONAL per-file override, not a table of contents (PRD Section 7.3).
//
// The source folder's actual contents are the source of truth: the renderer walks it and renders
// everything it finds. An entry is only needed for something the filesystem cannot express - where
// the file should land, whether to template it, or whether to emit it at all.
type FileEntry struct {
	// Path is a file OR a directory, relative to the source folder, before placeholder
	// substitution. Naming a directory maps its whole subtree in one entry.
	Path string `yaml:"path"`

	// Target is where Path lands in the generated project, as a template. When empty the output
	// path mirrors Path. When Path is a directory, Target is the destination prefix and everything
	// underneath keeps its relative structure.
	//
	// This is what keeps a template folder shallow without turning `files:` back into a mandatory
	// table of contents. The awkward part of a Java layout is one thing only - the package
	// directory, whose name has to be derived from a dotted variable and so cannot exist literally
	// on disk. So the source keeps a plain `java/` folder with real `controller/`, `service/`
	// subfolders inside it, and a single entry moves the lot:
	//
	//	files:
	//	  - path: "java"
	//	    target: "{{ .JavaDir }}"
	//
	// Adding a file under java/controller/ then needs no manifest change at all, and the output
	// directories are created on demand when the files are written.
	Target string `yaml:"target,omitempty"`

	// Template controls whether the file is rendered. It is a *bool so that "absent" is
	// distinguishable from "false": absent means the default (render it), false means copy the
	// bytes verbatim. A plain bool would silently turn every listed file into a binary copy.
	Template *bool `yaml:"template,omitempty"`

	// Condition names a variable that must be truthy for this file to be emitted.
	Condition string `yaml:"condition,omitempty"`
}

// ShouldTemplate reports whether this entry's file should be rendered as a template.
func (f FileEntry) ShouldTemplate() bool {
	return f.Template == nil || *f.Template
}

// Dependency is one entry in a manifest's `dependencies:` list. Its FIELD NAMES ARE NOT FIXED.
//
// The engine reads it as an opaque set of key/value pairs, renders each value as a template, and
// hands the whole thing to the template as an element of .Dependencies. It never inspects a field,
// never requires one, and attaches no meaning to any name.
//
// This is what fundamental rule #1 demands. An earlier version declared `groupId`/`artifactId` as
// struct fields, which put Maven's vocabulary inside the engine: a Node manifest could not express
// `"react": "^18.0"`, and a Go one could not express `github.com/x/y v1.2.3`. That made the Phase 4
// promise - add a framework with zero engine changes - impossible to keep, because the second
// framework would have forced exactly such a change.
//
// So Maven's shape now lives entirely in the data:
//
//	dependencies:
//	  - groupId: org.springframework.boot
//	    artifactId: spring-boot-starter-web
//
// and npm's would be `- name: react` / `version: "^18.0"`. Both work, neither is privileged.
type Dependency map[string]string

// Get returns a field, or "" when absent. Templates see every dependency with the same key set
// (see the render package), so they can write `{{ .scope }}` without guarding.
func (d Dependency) Get(key string) string { return d[key] }

// MergePaths returns the output paths to deep-merge, accepting both the current `merge:` field and
// the older `merge_yaml:` spelling.
func (m *Manifest) MergePaths() []string {
	if len(m.MergeYAML) == 0 {
		return m.Merge
	}
	return append(append([]string{}, m.Merge...), m.MergeYAML...)
}

// Identity builds the value used to deduplicate a dependency against others.
//
// keys comes from the manifest's `dependency_key`; when it is empty every field participates,
// which is the safe default (it only ever merges entries that are genuinely identical).
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

// UnmarshalYAML accepts a mapping of scalars and rejects anything nested, because a dependency
// coordinate is a flat set of fields in every build tool the engine is likely to meet. Saying so
// plainly beats rendering "map[]" into a build file.
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

	Variables []Variable   `yaml:"variables,omitempty"`
	Computed  []Computed   `yaml:"computed,omitempty"`
	Layout    []LayoutRule `yaml:"layout,omitempty"`
	Files     []FileEntry  `yaml:"files,omitempty"`

	// DependencyFields declares every field a dependency may have, for this framework. Inherited
	// like `layout`; the deepest declaration wins.
	//
	// It does two jobs, both of which the opaque `dependencies` shape would otherwise lose:
	//
	//   - Templates run with missingkey=error, so `{{ .version }}` is a hard error on an entry
	//     that does not set it. Every dependency is normalised to exactly this field set, missing
	//     ones filled with "", so a template can reference any declared field unguarded. Inferring
	//     the set from whatever happens to be declared is not enough: a library whose dependencies
	//     all omit `version` would break the very same shared pom.xml that a service renders fine.
	//   - A field NOT in this list is a typo and is rejected by name. Without it, `artifctId:`
	//     would silently render nothing - exactly the class of silent failure rule #8 forbids.
	//
	// Leave it unset and the engine falls back to the union of fields actually declared, which is
	// fine for a small framework but gives up both benefits above.
	DependencyFields []string `yaml:"dependency_fields,omitempty"`

	// DependencyKey names the fields that together identify a dependency, for deduplication when
	// several levels of the chain declare the same one. Inherited like `layout`; deepest wins.
	//
	// It has to be data rather than an engine constant, because what identifies a dependency is a
	// property of the build tool: Maven says groupId+artifactId (a second entry differing only in
	// `scope` is the same dependency), npm says name alone. With it unset, every field
	// participates - safe, but it will keep two entries that differ in any way at all.
	DependencyKey []string `yaml:"dependency_key,omitempty"`

	// Exclude drops inherited output paths that this level does not want. Entries are glob
	// patterns matched against the OUTPUT path (the same key collisions use), e.g.
	// "src/test/java/**/ApplicationTests.java" or "Dockerfile".
	//
	// This is the third of the three things a level can do to what it inherits: add, override, or
	// remove. Without it, anything contributed high up would be impossible to opt out of, which
	// would push template authors back towards duplicating a whole subtree just to leave one file
	// out - the exact duplication the inheritance chain exists to remove.
	Exclude      []string     `yaml:"exclude,omitempty"`
	Dependencies []Dependency `yaml:"dependencies,omitempty"`
	PostHooks    []PostHook   `yaml:"post_hooks,omitempty"`

	// Merge lists output paths that must be DEEP-MERGED when more than one source contributes
	// them, instead of the later source replacing the earlier one wholesale (PRD Section 6).
	// application.yml is the motivating case: a pattern overlay needs to add keys to it, not
	// discard the base service's copy.
	//
	// The format is inferred from the extension - .yml/.yaml and .json today. Naming the field
	// `merge` rather than `merge_yaml` is the point: package.json and Cargo.toml need exactly this
	// behaviour, and a field name that hardcodes one format announces a limitation the engine has
	// no reason to have.
	Merge []string `yaml:"merge,omitempty"`

	// MergeYAML is the former name of Merge, still accepted so existing manifests keep working.
	MergeYAML []string `yaml:"merge_yaml,omitempty"`

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

// Shape is the structural classification of a manifest. PRD Section 7.1 defines three shapes.
// An earlier binary leaf-vs-selector test predated the registry form, so IsLeaf was
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

// Validate checks a manifest for combinations that cannot mean anything coherent.
//
// It deliberately does NOT reject ShapeUnknown: a metadata-only manifest is perfectly legitimate
// at the axis level, where `name`/`description`/`required` are all that is needed and the axis's
// values come from directory listing. ShapeUnknown only becomes an error where a manifest must be
// renderable or navigable - see RequireNavigable, called from the selector walk.
//
// Nor does it reject `selector:` alongside files/dependencies/variables. Under the inheritance
// model every level may contribute content (PRD Section 6 step 3), so a selector node carrying a
// shared skeleton is the normal case, not an ambiguity: `selector:` decides that the node is
// navigable, and its content is what it contributes on the way past. Rejecting the combination
// would force every leaf to duplicate whatever its parents have in common - the exact duplication
// the inheritance chain exists to remove.
func (m *Manifest) Validate(path string) error {
	for i, c := range m.Computed {
		if c.Name == "" || c.Value == "" {
			return fmt.Errorf("manifest at %s: computed[%d] needs both `name` and `value`", path, i)
		}
	}
	for i, v := range m.Variables {
		if v.Name == "" {
			return fmt.Errorf("manifest at %s: variables[%d] has no `name`", path, i)
		}
		if v.FromPositional != "" && v.FromPositional != "name" {
			return fmt.Errorf("manifest at %s: variable %q has from_positional: %q, but the only "+
				"positional a variable can bind to is \"name\"", path, v.Name, v.FromPositional)
		}
	}
	return nil
}

// RequireNavigable rejects a manifest that the selector walk cannot act on.
//
// Only the registry shape is rejected: `values:` with no `selector:` declares what a level's
// children are, which says nothing about descending or rendering, so hitting one on a category's
// chain means the walk has gone somewhere it should not be.
//
// A manifest declaring nothing at all (just `name`/`description`) is accepted as a leaf. Under
// the inheritance model that is a real and useful case: a category whose entire content comes
// from the levels above it - the shared pom.xml and nothing else - has nothing of its own to
// declare, and forcing it to invent a field would be noise. The failure this used to guard
// against, a misspelled `selector` key silently producing an empty project, is now caught by two
// better checks: the flags meant for the missing selector are rejected as unknown (rule #8), and
// a chain that renders zero files is an error at write time.
func (m *Manifest) RequireNavigable(path string) error {
	if m.Shape() == ShapeRegistry {
		return fmt.Errorf("manifest at %s only declares `values:` - it is a registry, not a "+
			"selector node or a leaf template, so there is nothing to descend into or render "+
			"(PRD Section 7.1)", path)
	}
	return nil
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
// PRD Section 4.1 separates three identities that earlier versions conflated into one or
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
// every other level, this one is mandatory (PRD Section 4.1): falling back to a directory
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
