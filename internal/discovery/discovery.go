// Package discovery implements version resolution, top-level axis discovery, and the
// recursive category selector walk. Frameworks, categories, axes, and selector values are
// never hardcoded - everything is discovered from what's on disk and from jig.yaml content.
package discovery

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"scaffold-engine-go/internal/jig"
)

// ResolveFrameworkPath looks up name (the CLI-facing identifier) in the scaffolding-code root
// registry and returns the actual on-disk directory to use, applying a framework's optional
// `path` alias rather than assuming the folder matches its name.
func ResolveFrameworkPath(scaffoldingCodeRoot, name string) (string, error) {
	root, err := jig.LoadRoot(filepath.Join(scaffoldingCodeRoot, jig.FileName))
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
// that reaches filepath.Join from CLI input or a registry `path` must pass through here, so a
// crafted value like "../../pwned" can't walk the write target outside <output>/<name>.
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

// ResolveVersion picks the version folder to use under scaffolding-code/<framework>/. An
// explicit value is validated and aliased through the registry; otherwise the registry's
// `default: true` entry wins, or failing that the highest version folder present, compared
// with a lenient numeric comparator since folder names like "3.2.x" aren't strict semver. The
// framework-level jig.yaml is optional - missing it falls back to plain directory listing.
func ResolveVersion(frameworkPath, explicit string) (string, error) {
	m, err := jig.LoadOptional(filepath.Join(frameworkPath, jig.FileName))
	if err != nil {
		return "", fmt.Errorf("reading version registry under %s: %w", frameworkPath, err)
	}

	// (a) Explicit value: validated against the registry and translated through its `path`
	// alias, same as every other level.
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
		// Break ties deterministically: "3.2.0" and "3.2.x" compare equal under the lenient
		// comparator.
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

// ResolveVersionChain resolves a version and every version it inherits from, returning the
// folder names base-first: ["3.2.x", "2.7.x"] means 2.7.x layers on top of 3.2.x. This lets a
// derived version declare only its differences instead of copying the whole template tree.
func ResolveVersionChain(frameworkPath, explicit string) ([]string, error) {
	leaf, err := ResolveVersion(frameworkPath, explicit)
	if err != nil {
		return nil, err
	}
	m, err := jig.LoadOptional(filepath.Join(frameworkPath, jig.FileName))
	if err != nil {
		return nil, err
	}
	if m == nil {
		return []string{leaf}, nil
	}

	// Walk up by folder name, guarding against a cycle - a typo in `inherits` would otherwise spin
	// forever rather than say what is wrong.
	byDir := map[string]jig.Entry{}
	for _, e := range m.Values {
		byDir[e.DirName()] = e
	}

	chain := []string{leaf}
	seen := map[string]bool{leaf: true}
	for current := leaf; ; {
		entry, ok := byDir[current]
		if !ok || entry.Inherits == "" {
			break
		}
		parent, ok := m.FindValue(entry.Inherits)
		if !ok {
			return nil, fmt.Errorf("version %q inherits %q, which is not a registered version (known: %s)",
				current, entry.Inherits, strings.Join(m.ValueNames(), ", "))
		}
		dir := parent.DirName()
		if seen[dir] {
			return nil, fmt.Errorf("version inheritance loops back to %q - check the `inherits` fields", dir)
		}
		if err := ValidateSegment("version folder", dir); err != nil {
			return nil, err
		}
		seen[dir] = true
		chain = append(chain, dir)
		current = dir
	}

	// Reverse into base-first order, which is how every other part of the engine applies layers.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, nil
}

// Axis describes one axis registered under <framework>/<version>/. Exactly one axis is the
// required base axis; every other one is an optional overlay axis, whatever it's named. Name,
// Dir, and Flag are distinct identities and callers must use the right one - building a path
// from Name instead of Dir silently drops a `path` alias.
type Axis struct {
	Name        string // identity: what `scaffold list` shows
	Dir         string // physical folder name on disk (Path alias applied); use this to build paths
	Flag        string // CLI flag name that selects this axis; never derived from the folder name
	Description string // from the axis folder's own jig.yaml, if present; else empty
	Required    bool
	Values      []string
	// entries is the axis's own registry, if any, used to validate and alias a selected value.
	// Nil means Values came from directory listing.
	entries []jig.Entry
}

// Path returns the on-disk directory for this axis under versionPath. Always prefer this over
// joining Name yourself.
func (a Axis) Path(versionPath string) string {
	return filepath.Join(versionPath, a.Dir)
}

// ResolveValueDir validates a user-selected value for this axis and returns the folder to read.
// When the axis has a registry, that registry is authoritative - an unregistered folder is
// rejected and a registered one may be aliased to a different folder name. Without a registry
// it accepts any existing subfolder.
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

func (a Axis) findEntry(name string) (jig.Entry, bool) {
	for _, e := range a.entries {
		if e.Name == name {
			return e, true
		}
	}
	return jig.Entry{}, false
}

// DiscoverAxes scans <framework>/<version>/ and returns one Axis per registered axis. Must run
// before any CLI flags are registered, since flags can't be added reliably from inside a cobra
// Run callback.
//
// Which axes exist is itself an optional registry: a jig.yaml directly under versionPath can
// list its axes via `values:`, each with an optional `path` alias, making axis names and count
// entirely data-driven. With no version-level jig.yaml, discovery falls back to plain
// directory listing.
//
// Each axis-level jig.yaml is separately optional; if present its `values:` list becomes
// authoritative for Axis.Values and its Description/Required fields enrich the axis, otherwise
// Axis.Values falls back to directory listing of that axis's own folder.
//
// Required is preferred from an axis's own `required: true` field; a folder literally named
// "templates" with no such declaration still falls back to being treated as required.
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

		m, err := jig.LoadOptional(filepath.Join(axisPath, jig.FileName))
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

	// The "templates" name fallback applies only when no axis declared `required: true` anywhere,
	// to avoid ending up with two required axes.
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

// listAxisEntries returns the registry entries for the axes under versionPath: the registry's
// `values:` list when non-empty, otherwise one synthesized entry per subfolder so downstream
// code has a single shape to work with.
func listAxisEntries(versionPath string) ([]jig.Entry, error) {
	m, err := jig.LoadOptional(filepath.Join(versionPath, jig.FileName))
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
	entries := make([]jig.Entry, 0, len(names))
	for _, n := range names {
		entries = append(entries, jig.Entry{Name: n})
	}
	return entries, nil
}

// RequiredAxis returns the one Axis marked Required from an already-discovered list - the base
// axis whose values are the categories selectable via `scaffold create <framework> <category>
// <name>`. More than one required axis is a configuration error.
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
		return Axis{}, fmt.Errorf("no axis marked required (expected exactly one: a folder named "+
			"\"templates\", or an axis with `required: true` in its %s)", jig.FileName)
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
	Defaulted bool // true if Value came from the jig's `default` field, not a CLI flag
}

// WalkCategoryChain is WalkCategory across a version inheritance chain. templatesPaths is
// base-first: ["…/3.2.x/templates", "…/2.7.x/templates"]. At every node the walk uses the jig
// from the most derived version that has one, and records every version that has the node so
// all of them can contribute content - resolved per node rather than per version, since a
// derived version overriding two leaf files still has the intervening directories on disk but
// no jigs in them.
func WalkCategoryChain(templatesPaths []string, category string, selections map[string]string) (*WalkResult, error) {
	if len(templatesPaths) == 0 {
		return nil, fmt.Errorf("no template roots to walk")
	}
	if err := ValidateSegment("category folder", category); err != nil {
		return nil, err
	}

	rel := category
	var steps []SelectorStep
	var chain []ChainNode

	for {
		dirs, m, err := resolveNode(templatesPaths, rel, category)
		if err != nil {
			return nil, err
		}
		node := ChainNode{Dir: dirs[len(dirs)-1], Dirs: dirs, Manifest: m}
		chain = append(chain, node)

		if err := m.RequireNavigable(filepath.Join(node.Dir, jig.FileName)); err != nil {
			return nil, fmt.Errorf("category %q: %w", category, err)
		}
		if m.Shape() != jig.ShapeSelector {
			return &WalkResult{Leaf: m, LeafDir: node.Dir, Steps: steps, Chain: chain}, nil
		}

		flag := m.Selector
		value, ok := selections[flag]
		defaulted := false
		if !ok {
			if m.Default == "" {
				return nil, fmt.Errorf("category %q requires --%s (one of: %s)",
					category, flag, strings.Join(selectorValues(m, node.Dir), ", "))
			}
			value = m.Default
			defaulted = true
		}

		child, err := resolveSelectorName(m, node.Dir, flag, value)
		if err != nil {
			return nil, fmt.Errorf("category %q: %w", category, err)
		}
		steps = append(steps, SelectorStep{Flag: flag, Value: value, Defaulted: defaulted})
		rel = path.Join(rel, child)
	}
}

// resolveNode finds every version that has the node at rel, base-first, and returns a jig
// describing how to navigate it. Navigational fields are merged per field, deepest declaration
// winning, rather than taken wholesale from the most derived jig - otherwise a derived version
// contributing just one file could erase an inherited selector. Content fields are not merged
// here: each directory is returned separately as its own render source.
func resolveNode(roots []string, rel, category string) ([]string, *jig.Jig, error) {
	var dirs []string
	nav := &jig.Jig{}
	found := false

	for _, root := range roots {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		m, err := jig.LoadOptional(filepath.Join(dir, jig.FileName))
		if err != nil {
			return nil, nil, fmt.Errorf("walking category %q: %w", category, err)
		}
		if m == nil {
			continue
		}
		dirs = append(dirs, dir)
		found = true

		if m.Name != "" {
			nav.Name = m.Name
		}
		if m.Selector != "" {
			nav.Selector = m.Selector
		}
		if m.Default != "" {
			nav.Default = m.Default
		}
		if len(m.Values) > 0 {
			nav.Values = m.Values
		}
		// Enough of the content fields to keep Shape() honest: a node that contributes files in any
		// version is a leaf, not a shapeless jig.
		nav.Files = append(nav.Files, m.Files...)
		nav.Dependencies = append(nav.Dependencies, m.Dependencies...)
		nav.Variables = append(nav.Variables, m.Variables...)
	}

	if !found {
		return nil, nil, fmt.Errorf("walking category %q: no %s for %q in any version",
			category, jig.FileName, rel)
	}
	return dirs, nav, nil
}

// resolveSelectorName validates a selector value and returns the FOLDER name to descend into,
// leaving the caller to resolve that name against each version in the chain.
func resolveSelectorName(m *jig.Jig, dir, flag, value string) (string, error) {
	if err := ValidateSegment(fmt.Sprintf("value for --%s", flag), value); err != nil {
		return "", err
	}
	valid := selectorValues(m, dir)
	if len(m.Values) > 0 {
		entry, ok := m.FindValue(value)
		if !ok {
			return "", fmt.Errorf("invalid --%s=%q (valid values: %s)", flag, value, strings.Join(valid, ", "))
		}
		if err := ValidateSegment(fmt.Sprintf("folder for --%s=%s", flag, value), entry.DirName()); err != nil {
			return "", err
		}
		return entry.DirName(), nil
	}
	if info, err := os.Stat(filepath.Join(dir, value)); err != nil || !info.IsDir() {
		return "", fmt.Errorf("invalid --%s=%q (valid values: %s)", flag, value, strings.Join(valid, ", "))
	}
	return value, nil
}

// ChainNode is one node visited on the way down to the leaf, in root-to-leaf order. Intermediate
// selector nodes can contribute files and dependencies, merged root-to-leaf with deeper nodes
// winning, so the walk records the whole chain rather than just its last node.
type ChainNode struct {
	// Dir is the most derived version's copy of this node, where the jig was read from.
	Dir string
	// Dirs is every version that has this node, base-first; with no version inheritance this is
	// just [Dir].
	Dirs     []string
	Manifest *jig.Jig
}

// WalkResult is the outcome of walking a category down to its leaf jig.
type WalkResult struct {
	Leaf    *jig.Jig
	LeafDir string
	Steps   []SelectorStep
	// Chain is every node from the category root down to and including the leaf, in merge order.
	Chain []ChainNode
}

// WalkCategory walks templates/<category>/ down to its leaf jig, following the choices in
// selections (flag name -> chosen value). At each level it reads jig.yaml: a selector node
// recurses into the subfolder named by selections[node.Selector], falling back to the node's
// own `default` field if the flag wasn't given (recorded as Defaulted); a leaf node ends the
// walk. The same code path handles any depth, with no category-specific branching.
func WalkCategory(templatesPath, category string, selections map[string]string) (*WalkResult, error) {
	if err := ValidateSegment("category folder", category); err != nil {
		return nil, err
	}
	currentDir := filepath.Join(templatesPath, category)
	var steps []SelectorStep
	var chain []ChainNode

	for {
		m, err := jig.Load(filepath.Join(currentDir, jig.FileName))
		if err != nil {
			return nil, fmt.Errorf("walking category %q: %w", category, err)
		}
		chain = append(chain, ChainNode{Dir: currentDir, Manifest: m})

		// A registry jig on a category's chain means the walk has gone somewhere it should
		// not be - it neither descends nor renders.
		if err := m.RequireNavigable(filepath.Join(currentDir, jig.FileName)); err != nil {
			return nil, fmt.Errorf("category %q: %w", category, err)
		}
		// Anything that isn't a selector ends the walk, including a jig that declares nothing of
		// its own - such a leaf is made entirely of what the levels above it contribute.
		if m.Shape() != jig.ShapeSelector {
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
// node declares a `values:` registry, that registry is authoritative and may alias a value to
// a different folder; the segment check additionally stops a value like "../../patterns/x"
// from walking out of the base axis.
func resolveSelectorDir(m *jig.Jig, currentDir, flag, value string) (string, error) {
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
func selectorValues(m *jig.Jig, dir string) []string {
	if len(m.Values) > 0 {
		return m.ValueNames()
	}
	values, _ := listSubdirs(dir)
	return values
}

// DescribeCategory reads the jig at the root of a category without walking further, letting
// `scaffold list <framework> <category>` show what selector (if any) comes next one level at a
// time.
func DescribeCategory(templatesPath, category string) (*jig.Jig, error) {
	return jig.Load(filepath.Join(templatesPath, category, jig.FileName))
}

// DefaultCategory reads templates/jig.yaml to find the CLI-facing category name assumed when
// `scaffold create <framework> <name>` is run with <category> omitted. It prefers the
// `values:` registry's entry marked `default: true` (pass the result through
// ResolveCategoryDir before walking), falling back to a bare top-level `default:` string.
// Returns an error if neither form of default is set.
func DefaultCategory(templatesPath string) (string, error) {
	m, err := jig.Load(filepath.Join(templatesPath, jig.FileName))
	if err != nil {
		return "", fmt.Errorf("reading the base axis jig for a default category failed: %w", err)
	}
	if entry, ok := m.DefaultValue(); ok {
		return entry.Name, nil
	}
	if m.Default != "" {
		return m.Default, nil
	}
	return "", fmt.Errorf("the base axis jig declares no default category (neither a `values:` entry with `default: true` nor a top-level `default` field)")
}

// ResolveCategoryDir resolves a CLI-facing category name to its on-disk folder name via the
// base axis jig's `values:` registry, same aliasing mechanism as ResolveFrameworkPath. When
// that registry exists it is authoritative and rejects an unregistered category; with no
// registry, any existing folder is accepted.
func ResolveCategoryDir(templatesPath, name string) (string, error) {
	if err := ValidateSegment("category", name); err != nil {
		return "", err
	}
	m, err := jig.LoadOptional(filepath.Join(templatesPath, jig.FileName))
	if err != nil {
		return "", fmt.Errorf("reading the base axis jig: %w", err)
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
// selections, so `scaffold list <framework> <category>` can display every branch at once.
type TreeNode struct {
	Value    string
	Selector string
	IsLeaf   bool
	Children []TreeNode
}

// DescribeTree recursively explores every branch of a category's selector chain, regardless of
// depth, with no category-specific branching.
func DescribeTree(templatesPath, category string) (*TreeNode, error) {
	return describeTreeAt(filepath.Join(templatesPath, category), category)
}

func describeTreeAt(dir, name string) (*TreeNode, error) {
	m, err := jig.Load(filepath.Join(dir, jig.FileName))
	if err != nil {
		return nil, err
	}
	if m.Shape() != jig.ShapeSelector {
		return &TreeNode{Value: name, IsLeaf: true}, nil
	}

	node := &TreeNode{Value: name, Selector: m.Selector}

	// Respect the node's registry when it has one: the tree shows CLI-facing names, and an
	// unregistered stray folder must not appear as a selectable value.
	if len(m.Values) > 0 {
		for _, entry := range m.Values {
			childDir := filepath.Join(dir, entry.DirName())
			// A registered value with no jig yet is a real error, but `list` is a browsing
			// command: report it in place rather than killing the whole tree. `create` still
			// fails hard on it.
			if _, statErr := os.Stat(filepath.Join(childDir, jig.FileName)); statErr != nil {
				node.Children = append(node.Children, TreeNode{
					Value:  fmt.Sprintf("%s  (registered, but no %s yet)", entry.Name, jig.FileName),
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
		// A subfolder with no jig.yaml is a supporting asset folder, not a selector value.
		if _, statErr := os.Stat(filepath.Join(dir, v, jig.FileName)); statErr != nil {
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

// listSubdirs lists immediate subdirectories of path, skipping dotfiles and hidden folders
// (.git, .vscode, etc.), which are never meaningful axis/selector values.
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
