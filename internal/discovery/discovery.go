// Package discovery implements the version resolution, top-level axis discovery, and
// recursive category selector walk described in PRD Section 5/6/7 and crystallized as
// fundamental rules in Section 13.1: the engine never hardcodes a framework, category, axis,
// or selector-value name - everything is discovered from what's actually on disk, decided
// structurally from manifest.yaml content (selector field vs. leaf fields).
package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"scaffold-engine-go/internal/manifest"
)

// ResolveFrameworkPath looks up name (the CLI-facing identifier, e.g. "spring" or
// "spring-boot") in the scaffolding-code root registry and returns the actual on-disk
// directory to use. This is what lets the CLI-facing name differ from the folder name (a
// framework's optional `path` field in the registry, defaulting to its `name`) - resolution
// always goes through the registry, never a raw filepath.Join(root, name) assumption.
func ResolveFrameworkPath(scaffoldingCodeRoot, name string) (string, error) {
	root, err := manifest.LoadRoot(filepath.Join(scaffoldingCodeRoot, "manifest.yaml"))
	if err != nil {
		return "", fmt.Errorf("loading framework registry: %w", err)
	}

	fw, ok := root.FindFramework(name)
	if !ok {
		return "", fmt.Errorf("unknown framework %q (known: %s)",
			name, strings.Join(root.FrameworkNames(), ", "))
	}
	if err := ValidateSegment("framework folder", fw.DirName()); err != nil {
		return "", err
	}

	return filepath.Join(scaffoldingCodeRoot, fw.DirName()), nil
}

// ValidateSegment rejects a value that would escape the directory it is joined onto. Every value
// that reaches filepath.Join from CLI input or from a registry `path` must pass through here:
// PRD Section 8.3 requires <name>, selector values and axis values to be single path
// segments. Without this check, `create fw services ../../pwned` resolved its write target to
// ..\..\pwned - straight through fundamental rule #7's "never writes outside <output>/<name>"
// guarantee (design review 2026-07-27 section 2.4), and a selector value could walk clean out of
// the base axis into a sibling axis (section 2.5).
func ValidateSegment(what, value string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", what)
	}
	if value == "." || value == ".." {
		return fmt.Errorf("%s %q is not a valid name", what, value)
	}
	if strings.ContainsAny(value, `/\`) {
		return fmt.Errorf(`%s %q must be a single path segment (no "/" or "\")`, what, value)
	}
	if filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return fmt.Errorf("%s %q must be a relative single path segment, not an absolute path", what, value)
	}
	return nil
}

// ResolveVersion picks the version folder to use under scaffolding-code/<framework>/. If
// explicit is non-empty it's returned as-is (existence is the caller's concern). Otherwise:
//   - if <framework>/manifest.yaml has a `values:` entry marked `default: true`, that wins
//     (its DirName(), so a version can be aliased just like frameworks/categories);
//   - else, among that same registry's entries if present (or all version folders via plain
//     directory listing if no registry exists at all), the highest version is chosen with a
//     lenient numeric comparator, since folder names like "3.2.x" aren't strict semver.
//
// The framework-level manifest.yaml is optional, same as every other axis-level one - missing
// it just means falling all the way back to directory listing, exactly like before this
// registry existed.
func ResolveVersion(frameworkPath, explicit string) (string, error) {
	m, err := manifest.LoadOptional(filepath.Join(frameworkPath, "manifest.yaml"))
	if err != nil {
		return "", fmt.Errorf("reading version registry under %s: %w", frameworkPath, err)
	}

	// (a) Explicit value: validated against the registry and translated through its `path` alias,
	// exactly like every other level. Previously it was returned untouched - neither checked for
	// existence nor aliased - so a typo surfaced later as a confusing "cannot find the file"
	// pointing at a path the user never typed (design review 2026-07-27 section 2.12).
	if explicit != "" {
		if m == nil || len(m.Values) == 0 {
			if err := ValidateSegment("version", explicit); err != nil {
				return "", err
			}
			return explicit, nil
		}
		entry, ok := m.FindValue(explicit)
		if !ok {
			return "", fmt.Errorf("unknown version %q (known: %s)",
				explicit, strings.Join(m.ValueNames(), ", "))
		}
		return entry.DirName(), ValidateSegment("version folder", entry.DirName())
	}

	// (b) Registry default wins over "highest folder present".
	var versions []string
	if m != nil {
		if entry, ok := m.DefaultValue(); ok {
			return entry.DirName(), ValidateSegment("version folder", entry.DirName())
		}
		for _, entry := range m.Values {
			versions = append(versions, entry.DirName())
		}
	}
	// (c) No registry, or a registry with no default: fall back to the highest folder present.
	if versions == nil {
		versions, err = listSubdirs(frameworkPath)
		if err != nil {
			return "", fmt.Errorf("resolving version under %s: %w", frameworkPath, err)
		}
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("no version folders found under %s", frameworkPath)
	}

	sort.Slice(versions, func(i, j int) bool {
		if c := compareVersions(versions[i], versions[j]); c != 0 {
			return c > 0
		}
		// Break numeric ties deterministically instead of leaving it to directory order:
		// "3.2.0" and "3.2.x" compare equal under the lenient comparator.
		return versions[i] > versions[j]
	})
	return versions[0], nil
}

// compareVersions compares two dot-separated version strings component by component. Numeric
// components compare numerically; non-numeric components (e.g. the literal "x" in "3.2.x")
// compare as 0. Returns >0 if a>b, <0 if a<b, 0 if equal-for-ordering-purposes.
func compareVersions(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	max := len(as)
	if len(bs) > max {
		max = len(bs)
	}
	for i := 0; i < max; i++ {
		var av, bv int
		if i < len(as) {
			av, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bv, _ = strconv.Atoi(bs[i])
		}
		if av != bv {
			return av - bv
		}
	}
	return 0
}

// Axis describes one axis registered under <framework>/<version>/. Exactly one axis is the
// required base axis (PRD Section 5); every other one is an optional overlay axis, whatever it's
// named.
//
// The three identities of PRD Section 4.1 are all carried here, and callers must use the
// right one. Carrying only Name is what silently broke `path` aliasing end-to-end: DiscoverAxes
// resolved the alias internally to read the axis's values, then discarded it, and both commands
// rebuilt the path as filepath.Join(versionPath, Name) - so `list` worked while `create` failed
// on a path the registry had explicitly aliased away (design review 2026-07-27 section 2.2).
type Axis struct {
	Name        string // identity: what `scaffold list` shows
	Dir         string // physical folder name on disk (Path alias applied); use this to build paths
	Flag        string // CLI flag name that selects this axis; never derived from the folder name
	Description string // from the axis folder's own manifest.yaml, if present; else empty
	Required    bool
	Values      []string
	// entries is the axis's own registry, when it has one - used to validate and alias a value
	// the user selected. Nil means the axis had no registry and Values came from directory
	// listing.
	entries []manifest.Entry
}

// Path returns the on-disk directory for this axis under versionPath. Always prefer this over
// joining Name yourself.
func (a Axis) Path(versionPath string) string {
	return filepath.Join(versionPath, a.Dir)
}

// ResolveValueDir validates a user-selected value for this axis and returns the folder to read.
// When the axis has a registry, that registry is authoritative: an unregistered folder is not a
// valid choice, and a registered one may be aliased to a different folder name (fundamental rule
// #2). Without a registry it falls back to accepting any existing subfolder.
func (a Axis) ResolveValueDir(versionPath, value string) (string, error) {
	if err := ValidateSegment(fmt.Sprintf("value for --%s", a.Flag), value); err != nil {
		return "", err
	}
	if len(a.entries) > 0 {
		entry, ok := a.findEntry(value)
		if !ok {
			return "", fmt.Errorf("invalid --%s=%q (valid values: %s)",
				a.Flag, value, strings.Join(a.Values, ", "))
		}
		return filepath.Join(a.Path(versionPath), entry.DirName()), nil
	}
	dir := filepath.Join(a.Path(versionPath), value)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("invalid --%s=%q (valid values: %s)",
			a.Flag, value, strings.Join(a.Values, ", "))
	}
	return dir, nil
}

func (a Axis) findEntry(name string) (manifest.Entry, bool) {
	for _, e := range a.entries {
		if e.Name == name {
			return e, true
		}
	}
	return manifest.Entry{}, false
}

// DiscoverAxes scans <framework>/<version>/ and returns one Axis per registered axis. Must run
// before any CLI flags are registered - see feasibility-analysis-engines.md's cobra pre-scan
// constraint (flags can't be added reliably from inside a cobra Run callback).
//
// Which axes exist at all is itself an optional registry, same pattern as every other level
// (root frameworks:, framework values:, category values:): a manifest.yaml directly under
// versionPath can list its axes explicitly via `values:` (each entry's Name is the CLI-facing
// axis name, e.g. "templates"/"patterns"/whatever a template author calls it; an optional
// `path` aliases it to a differently-named folder, exactly like frameworks/categories/versions).
// When this registry is present it is authoritative - a stray unregistered folder never shows
// up as an axis, and axis names/count become entirely data-driven: a template author can add,
// rename, or drop an axis just by editing this one file, without the engine assuming "templates"
// and "patterns" are the only two names that will ever exist. When no version-level
// manifest.yaml exists at all, discovery falls back to plain directory listing, exactly as
// before this registry existed.
//
// An axis-level manifest.yaml (e.g. templates/manifest.yaml, patterns/manifest.yaml) is
// separately optional - a missing one is not an error, and Axis.Values then falls back to raw
// directory listing of that axis's own folder. If present, its `values:` list becomes
// authoritative for Axis.Values - each entry's CLI-facing Name (not yet resolved to a folder;
// see ResolveCategoryDir for that) - and its Description/Required fields enrich the axis
// itself. Same reasoning as every registry in this package (fundamental rule #2, Section 13.1):
// explicit registration over trusting whatever folders happen to exist.
//
// Which axis is Required works the same way: preferring the axis's own manifest.yaml
// `required: true` field over a hardcoded name check. A folder literally named "templates"
// with no manifest (or one that doesn't set `required`) still falls back to being treated as
// required, so nothing breaks for a setup that hasn't bothered declaring it explicitly - but
// nothing in this function assumes the name "templates" is special beyond that fallback.
func DiscoverAxes(versionPath string) ([]Axis, error) {
	entries, err := listAxisEntries(versionPath)
	if err != nil {
		return nil, fmt.Errorf("discovering axes under %s: %w", versionPath, err)
	}

	axes := make([]Axis, 0, len(entries))
	anyDeclaredRequired := false

	for _, entry := range entries {
		if err := ValidateSegment("axis folder", entry.DirName()); err != nil {
			return nil, err
		}
		axisPath := filepath.Join(versionPath, entry.DirName())

		axis := Axis{
			Name: entry.Name,
			Dir:  entry.DirName(),
			Flag: entry.FlagName(),
		}

		m, err := manifest.LoadOptional(filepath.Join(axisPath, "manifest.yaml"))
		if err != nil {
			return nil, fmt.Errorf("reading axis %q: %w", entry.Name, err)
		}
		if m != nil {
			axis.Description = m.Description
			axis.Required = m.Required
			axis.entries = m.Values
			for _, v := range m.Values {
				axis.Values = append(axis.Values, v.Name)
			}
		}
		if axis.Required {
			anyDeclaredRequired = true
		}
		if axis.Values == nil {
			axis.Values, err = listSubdirs(axisPath)
			if err != nil {
				return nil, err
			}
		}

		axes = append(axes, axis)
	}

	// The "templates" name fallback applies only when NO axis declared `required: true` anywhere
	// (PRD Section 4.1 / fundamental rule #4). Evaluating it per-axis, as this used to, produced
	// two required axes whenever a folder named templates/ coexisted with an axis that declared
	// the field - and RequiredAxis then silently picked whichever the filesystem listed first
	// (design review 2026-07-27 section 2.8).
	if !anyDeclaredRequired {
		for i := range axes {
			if axes[i].Name == "templates" {
				axes[i].Required = true
				break
			}
		}
	}

	return axes, nil
}

// listAxisEntries returns the registry entries for the axes under versionPath. If
// versionPath/manifest.yaml declares a non-empty `values:` registry, that list is authoritative
// (see DiscoverAxes); otherwise it falls back to plain directory listing, synthesising one entry
// per folder so downstream code has a single shape to work with.
func listAxisEntries(versionPath string) ([]manifest.Entry, error) {
	m, err := manifest.LoadOptional(filepath.Join(versionPath, "manifest.yaml"))
	if err != nil {
		return nil, err
	}
	if m != nil && len(m.Values) > 0 {
		return m.Values, nil
	}

	names, err := listSubdirs(versionPath)
	if err != nil {
		return nil, err
	}
	entries := make([]manifest.Entry, 0, len(names))
	for _, n := range names {
		entries = append(entries, manifest.Entry{Name: n})
	}
	return entries, nil
}

// RequiredAxis returns the one Axis marked Required from an already-discovered list (see
// DiscoverAxes) - the base axis whose values are the categories selectable via `scaffold
// create <framework> <category> <name>`. Callers use axis.Path(versionPath) to build the base
// path, never filepath.Join(versionPath, axis.Name), so a `path` alias survives.
//
// More than one required axis is a configuration error rather than a race won by directory order.
func RequiredAxis(axes []Axis) (Axis, error) {
	var found []Axis
	for _, a := range axes {
		if a.Required {
			found = append(found, a)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return Axis{}, fmt.Errorf("no axis marked required (expected exactly one: a folder named " +
			"\"templates\", or an axis with `required: true` in its manifest.yaml)")
	default:
		names := make([]string, 0, len(found))
		for _, a := range found {
			names = append(names, a.Name)
		}
		return Axis{}, fmt.Errorf("%d axes are marked required (%s) - exactly one may be",
			len(found), strings.Join(names, ", "))
	}
}

// FindAxisByFlag looks up an axis by the CLI flag that selects it (its `flag` field, defaulting
// to its name) - not by folder name.
func FindAxisByFlag(axes []Axis, flag string) (Axis, bool) {
	for _, a := range axes {
		if a.Flag == flag {
			return a, true
		}
	}
	return Axis{}, false
}

// SelectorStep records one level consumed while walking a category's selector chain.
type SelectorStep struct {
	Flag      string
	Value     string
	Defaulted bool // true if Value came from the manifest's `default` field, not a CLI flag
}

// ChainNode is one node visited on the way down to the leaf, in root-to-leaf order.
//
// PRD Section 7 lets intermediate selector nodes contribute files and dependencies, merged
// root-to-leaf with deeper nodes winning. Without that, services/web/rest-http/ and
// services/web/grpc/ each need a full copy of the Spring Boot skeleton - 5-6 duplicates that must
// then be kept in sync by hand, which is the opposite of this project's purpose (design review
// 2026-07-27 section 4.6). The walk therefore records the whole chain, not just its last node.
type ChainNode struct {
	Dir      string
	Manifest *manifest.Manifest
}

// WalkResult is the outcome of walking a category down to its leaf manifest.
type WalkResult struct {
	Leaf    *manifest.Manifest
	LeafDir string
	Steps   []SelectorStep
	// Chain is every node from the category root down to and including the leaf. Phase 3b merges
	// their content in this order.
	Chain []ChainNode
}

// WalkCategory walks templates/<category>/ down to its leaf manifest, following the choices in
// selections (flag name -> chosen value, e.g. {"function": "web", "protocol": "rest-http"}).
// At each level it reads manifest.yaml: a selector node recurses into the subfolder named by
// selections[node.Selector], falling back to that node's own `default` field if the flag
// wasn't given (recorded as Defaulted on the resulting SelectorStep); a leaf node ends the
// walk. The same code path handles 0 levels (parent), 1 level (libs), 2 levels (services), or
// any future depth - no category-specific branching (PRD Section 6/7; fundamental rule #3,
// Section 13.1).
func WalkCategory(templatesPath, category string, selections map[string]string) (*WalkResult, error) {
	if err := ValidateSegment("category folder", category); err != nil {
		return nil, err
	}
	currentDir := filepath.Join(templatesPath, category)
	var steps []SelectorStep
	var chain []ChainNode

	for {
		m, err := manifest.Load(filepath.Join(currentDir, "manifest.yaml"))
		if err != nil {
			return nil, fmt.Errorf("walking category %q: %w", category, err)
		}
		chain = append(chain, ChainNode{Dir: currentDir, Manifest: m})

		// A registry manifest on a category's chain means the walk has gone somewhere it should
		// not be - it neither descends nor renders.
		if err := m.RequireNavigable(filepath.Join(currentDir, "manifest.yaml")); err != nil {
			return nil, fmt.Errorf("category %q: %w", category, err)
		}
		// Anything that isn't a selector ends the walk. That includes a manifest declaring
		// nothing of its own: under the inheritance model such a leaf is entirely made of what
		// the levels above it contribute, which is a legitimate and useful shape.
		if m.Shape() != manifest.ShapeSelector {
			return &WalkResult{Leaf: m, LeafDir: currentDir, Steps: steps, Chain: chain}, nil
		}

		flag := m.Selector
		value, ok := selections[flag]
		defaulted := false
		if !ok {
			if m.Default == "" {
				return nil, fmt.Errorf("category %q requires --%s (one of: %s)",
					category, flag, strings.Join(selectorValues(m, currentDir), ", "))
			}
			value = m.Default
			defaulted = true
		}

		nextDir, err := resolveSelectorDir(m, currentDir, flag, value)
		if err != nil {
			return nil, fmt.Errorf("category %q: %w", category, err)
		}

		steps = append(steps, SelectorStep{Flag: flag, Value: value, Defaulted: defaulted})
		currentDir = nextDir
	}
}

// resolveSelectorDir turns a selector value into the folder to descend into. When the selector
// node declares a `values:` registry, that registry is authoritative and may alias a value to a
// different folder (fundamental rule #2) - previously this level was pure directory listing, so
// an unregistered stray folder was selectable. The segment check additionally stops a value like
// "../../patterns/microservice" from walking out of the base axis entirely (design review
// 2026-07-27 sections 2.5 and 2.6).
func resolveSelectorDir(m *manifest.Manifest, currentDir, flag, value string) (string, error) {
	if err := ValidateSegment(fmt.Sprintf("value for --%s", flag), value); err != nil {
		return "", err
	}

	valid := selectorValues(m, currentDir)
	if len(m.Values) > 0 {
		entry, ok := m.FindValue(value)
		if !ok {
			return "", fmt.Errorf("invalid --%s=%q (valid values: %s)", flag, value, strings.Join(valid, ", "))
		}
		if err := ValidateSegment(fmt.Sprintf("folder for --%s=%s", flag, value), entry.DirName()); err != nil {
			return "", err
		}
		value = entry.DirName()
	}

	nextDir := filepath.Join(currentDir, value)
	if info, err := os.Stat(nextDir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("invalid --%s=%q (valid values: %s)", flag, value, strings.Join(valid, ", "))
	}
	return nextDir, nil
}

// selectorValues lists the valid values at a selector node: its `values:` registry when present,
// otherwise a directory listing.
func selectorValues(m *manifest.Manifest, dir string) []string {
	if len(m.Values) > 0 {
		return m.ValueNames()
	}
	values, _ := listSubdirs(dir)
	return values
}

// DescribeCategory reads the manifest at the root of a category without walking further -
// used by `scaffold list <framework> <category>` to show what selector (if any) comes next,
// one level at a time, without requiring the caller to already know the full chain.
func DescribeCategory(templatesPath, category string) (*manifest.Manifest, error) {
	return manifest.Load(filepath.Join(templatesPath, category, "manifest.yaml"))
}

// DefaultCategory reads templates/manifest.yaml to find the CLI-facing category name assumed
// when `scaffold create <framework> <name>` is run with <category> itself omitted - preferring
// the `values:` registry's entry marked `default: true` (its Name - not yet resolved to a
// folder; pass the result through ResolveCategoryDir before walking), falling back to a bare
// top-level `default: "<name>"` string for a category with no explicit values: list. Returns
// an error if templates/manifest.yaml is missing or neither form of default is set, since
// that's a required prerequisite for the 2-positional form, not an optional nicety.
func DefaultCategory(templatesPath string) (string, error) {
	m, err := manifest.Load(filepath.Join(templatesPath, "manifest.yaml"))
	if err != nil {
		return "", fmt.Errorf("reading the base axis manifest for a default category failed: %w", err)
	}
	if entry, ok := m.DefaultValue(); ok {
		return entry.Name, nil
	}
	if m.Default != "" {
		return m.Default, nil
	}
	return "", fmt.Errorf("the base axis manifest declares no default category (neither a `values:` entry with `default: true` nor a top-level `default` field)")
}

// ResolveCategoryDir resolves a CLI-facing category name to its on-disk folder name via the base
// axis manifest's `values:` registry (same aliasing mechanism as ResolveFrameworkPath).
//
// When that registry exists it is authoritative: an unregistered category is rejected rather than
// passed through to the filesystem (fundamental rule #2). With no registry at all, any existing
// folder is accepted, as before the mechanism existed.
func ResolveCategoryDir(templatesPath, name string) (string, error) {
	if err := ValidateSegment("category", name); err != nil {
		return "", err
	}
	m, err := manifest.LoadOptional(filepath.Join(templatesPath, "manifest.yaml"))
	if err != nil {
		return "", fmt.Errorf("reading the base axis manifest: %w", err)
	}
	if m == nil || len(m.Values) == 0 {
		return name, nil
	}
	entry, ok := m.FindValue(name)
	if !ok {
		return "", fmt.Errorf("unknown category %q (known: %s)", name, strings.Join(m.ValueNames(), ", "))
	}
	return entry.DirName(), ValidateSegment("category folder", entry.DirName())
}

// TreeNode is one node in a category's full selector tree, explored without any user
// selections (every branch), for `scaffold list <framework> <category>` to display the whole
// picture at once - e.g. services -> function:{web -> protocol:{rest-http, grpc}, ...}.
type TreeNode struct {
	Value    string
	Selector string
	IsLeaf   bool
	Children []TreeNode
}

// DescribeTree recursively explores every branch of a category's selector chain, regardless of
// depth (0 for parent, 1 for libs, 2 for services, or any future depth) - no category-specific
// branching, per fundamental rule #3 (Section 13.1).
func DescribeTree(templatesPath, category string) (*TreeNode, error) {
	return describeTreeAt(filepath.Join(templatesPath, category), category)
}

func describeTreeAt(dir, name string) (*TreeNode, error) {
	m, err := manifest.Load(filepath.Join(dir, "manifest.yaml"))
	if err != nil {
		return nil, err
	}
	if m.Shape() != manifest.ShapeSelector {
		return &TreeNode{Value: name, IsLeaf: true}, nil
	}

	node := &TreeNode{Value: name, Selector: m.Selector}

	// Respect the node's registry when it has one: the tree shows CLI-facing names, and an
	// unregistered stray folder must not appear as a selectable value (fundamental rule #2).
	// Previously this was a raw directory listing at every level.
	if len(m.Values) > 0 {
		for _, entry := range m.Values {
			childDir := filepath.Join(dir, entry.DirName())
			// A registered value whose folder has no manifest is a real data error, but `list` is
			// a browsing command: killing the whole tree because one branch is unfinished would
			// hide everything else. Report it in place instead - loud, but not fatal. `create`
			// still fails hard on the same value, which is where it matters.
			if _, statErr := os.Stat(filepath.Join(childDir, "manifest.yaml")); statErr != nil {
				node.Children = append(node.Children, TreeNode{
					Value:  entry.Name + "  (registered, but no manifest.yaml yet)",
					IsLeaf: true,
				})
				continue
			}
			child, err := describeTreeAt(childDir, entry.Name)
			if err != nil {
				return nil, err
			}
			node.Children = append(node.Children, *child)
		}
		return node, nil
	}

	values, err := listSubdirs(dir)
	if err != nil {
		return nil, err
	}
	for _, v := range values {
		// A subfolder with no manifest.yaml is a supporting asset folder, not a selector value.
		// Hard-failing on it would break `scaffold list` as soon as 3b/3c adds shared content
		// (design review 2026-07-27 section 5.19).
		if _, statErr := os.Stat(filepath.Join(dir, v, "manifest.yaml")); statErr != nil {
			continue
		}
		child, err := describeTreeAt(filepath.Join(dir, v), v)
		if err != nil {
			return nil, err
		}
		node.Children = append(node.Children, *child)
	}
	return node, nil
}

// listSubdirs lists immediate subdirectories of path, skipping dotfiles/hidden folders
// (.git, .vscode, .DS_Store, etc.) - these are never meaningful framework/version/axis/
// selector values, regardless of which scaffolding-code folder they happen to appear in.
func listSubdirs(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
