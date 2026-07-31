package render

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"

	"scaffold-engine-go/internal/manifest"
)

// manifestFileName is a reserved name: it is the engine's contract with the template author and
// must never appear in generated output, at any depth inside a source folder (PRD Section 7.3).
const manifestFileName = "manifest.yaml"

// File is one rendered file waiting to be written. Path is the output-relative path *after*
// placeholder substitution - which is also the key file collisions are detected on, since two
// sources can produce paths that differ before substitution and match after it.
type File struct {
	Path    string
	Content []byte
	Mode    fs.FileMode
	// Source identifies which template folder produced this file, for collision diagnostics.
	Source string
	// Merge marks this path as deep-merged rather than overwritten on collision.
	Merge bool
}

// Source is one folder contributing files, paired with the manifest that governs it.
type Source struct {
	Dir      string
	Manifest *manifest.Manifest
	// Label is a human-readable name used in error messages (e.g. `--style=microservice`).
	Label string
	// Priority orders overlays; higher applies later and therefore wins. The required base axis
	// is always ordered first regardless of this value.
	Priority int
	// Layout is the accumulated, already-rendered set of path-prefix rules in effect for this
	// source - inherited from every level above it. See CollectLayout.
	Layout []ResolvedLayout
}

// ResolvedLayout is a LayoutRule with its `to` already rendered against the context.
type ResolvedLayout struct {
	From string
	To   string
}

// CollectLayout gathers layout rules down the inheritance chain and renders each `to`.
//
// Later sources override an earlier rule with the same `from`, which is the same
// deeper-level-wins precedence that governs variables, files and dependencies. Rules are returned
// longest-prefix-first so the most specific match is applied.
func CollectLayout(sources []*manifest.Manifest, ctx Context) ([]ResolvedLayout, error) {
	byFrom := map[string]string{}
	var order []string
	for _, m := range sources {
		if m == nil {
			continue
		}
		for _, rule := range m.Layout {
			from := path.Clean(slashPath(rule.From))
			to, err := renderString("layout rule for "+rule.From, rule.To, ctx)
			if err != nil {
				return nil, err
			}
			if _, seen := byFrom[from]; !seen {
				order = append(order, from)
			}
			byFrom[from] = path.Clean(slashPath(to))
		}
	}

	out := make([]ResolvedLayout, 0, len(order))
	for _, from := range order {
		out = append(out, ResolvedLayout{From: from, To: byFrom[from]})
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i].From) > len(out[j].From) })
	return out, nil
}

// applyLayout rewrites a source-relative path through the first matching layout rule.
func applyLayout(rel string, rules []ResolvedLayout) string {
	for _, r := range rules {
		if rel == r.From {
			return r.To
		}
		if strings.HasPrefix(rel, r.From+"/") {
			return path.Join(r.To, strings.TrimPrefix(rel, r.From+"/"))
		}
	}
	return rel
}

// RenderSource walks one source folder and renders everything in it against ctx.
//
// The folder's contents are the source of truth (PRD Section 7.3) - the template author drops
// files in and they get rendered; `files:` in the manifest is only consulted for per-file
// overrides. Placeholders are substituted in file *contents* and in *path names*, so a folder
// literally named `{{ .PackageName | replace "." "/" }}` expands into a package directory tree.
//
// A subdirectory that contains its own manifest.yaml is skipped, because that makes it a node in
// the discovery tree rather than template content. This is what lets any level of the tree carry
// shared files: spring-boot/ can hold the one pom.xml every version inherits, and its 3.2.x/
// subfolder is not mistaken for content because 3.2.x/ has a manifest of its own. The rule is
// structural, in keeping with fundamental rule #3 - no reserved folder name is involved.
func RenderSource(src Source, ctx Context) ([]File, error) {
	overrides := indexOverrides(src.Manifest)
	mergePaths := map[string]bool{}
	if src.Manifest != nil {
		for _, p := range src.Manifest.MergePaths() {
			mergePaths[path.Clean(slashPath(p))] = true
		}
	}
	used := map[string]bool{}

	var out []File
	err := filepath.WalkDir(src.Dir, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src.Dir, abs)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		relSlash := slashPath(rel)

		if d.IsDir() {
			// A subfolder with its own manifest.yaml is a discovery node, not content: its files
			// belong to whichever source resolves to it, not to this one.
			if _, err := os.Stat(filepath.Join(abs, manifestFileName)); err == nil {
				return fs.SkipDir
			}
			return nil // otherwise directories are created implicitly from the files inside them
		}
		if d.Name() == manifestFileName {
			return nil // reserved: the contract itself is never emitted
		}

		entry, hasOverride := overrides[relSlash]
		if hasOverride {
			used[relSlash] = true
			if entry.Condition != "" && !truthy(ctx[entry.Condition]) {
				return nil
			}
		}

		raw, err := os.ReadFile(abs)
		if err != nil {
			return fmt.Errorf("reading template file %s: %w", relSlash, err)
		}

		// Output path, in order of precedence:
		//   1. an explicit `target` on this file's own entry - the per-file escape hatch;
		//   2. an inherited layout rule whose `from` prefixes this path - the cheap common case,
		//      declared once high up so templates below need no entries at all;
		//   3. mirror the source path.
		// Placeholders are then substituted across the whole path, so directory names can be
		// templated, and the output directories are created on demand at write time.
		outRel := relSlash
		switch {
		case hasOverride && entry.Target != "":
			outRel = slashPath(entry.Target)
		default:
			outRel = applyLayout(relSlash, src.Layout)
		}
		outRel, err = renderString("path "+relSlash, outRel, ctx)
		if err != nil {
			return err
		}
		outRel = path.Clean(outRel)
		if err := checkContained(outRel); err != nil {
			return fmt.Errorf("template file %s: %w", relSlash, err)
		}

		content := raw
		if !hasOverride || entry.ShouldTemplate() {
			rendered, err := renderString("file "+relSlash, string(raw), ctx)
			if err != nil {
				return err
			}
			content = []byte(rendered)
		}

		info, err := d.Info()
		mode := fs.FileMode(0o644)
		if err == nil {
			mode = info.Mode().Perm()
		}

		out = append(out, File{
			Path:    outRel,
			Content: content,
			Mode:    mode,
			Source:  src.Label,
			Merge:   mergePaths[relSlash] || mergePaths[outRel],
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// An override pointing at a file that isn't there is a template-authoring bug, and silently
	// ignoring it would hide a typo in a path (PRD Section 7.3).
	for p := range overrides {
		if !used[p] {
			return nil, fmt.Errorf("%s: manifest `files:` lists %q, but no such file exists in %s",
				src.Label, p, src.Dir)
		}
	}
	return out, nil
}

func indexOverrides(m *manifest.Manifest) map[string]manifest.FileEntry {
	out := map[string]manifest.FileEntry{}
	if m == nil {
		return out
	}
	for _, f := range m.Files {
		out[path.Clean(slashPath(f.Path))] = f
	}
	return out
}

// renderString executes one template. Sprig is registered so template authors get `replace`,
// `title`, `camelcase` and friends - notably `{{ .PackageName | replace "." "/" }}`, which is how
// a dotted Java package becomes a directory path (PRD Section 6 step 6).
//
// `missingkey=error` is deliberate: a typo'd `{{ .PackagName }}` must fail loudly rather than
// render as "<no value>" and leave a broken file behind (fundamental rule #8).
func renderString(what, text string, ctx Context) (string, error) {
	tmpl, err := template.New(what).
		Funcs(sprig.TxtFuncMap()).
		Option("missingkey=error").
		Parse(text)
	if err != nil {
		return "", fmt.Errorf("parsing %s: %w", what, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]any(ctx)); err != nil {
		return "", fmt.Errorf("rendering %s: %w", what, err)
	}
	return buf.String(), nil
}

// checkContained rejects an output path that would escape the target directory - enforcing the
// second half of fundamental rule #7, which `<name>` validation alone doesn't cover: a template
// could just as easily declare `../../evil.txt`.
func checkContained(rel string) error {
	if rel == "" || rel == "." {
		return fmt.Errorf("renders to an empty output path")
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return fmt.Errorf("renders to an absolute path %q", rel)
	}
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("renders to %q, which escapes the output directory", rel)
	}
	return nil
}

func slashPath(p string) string {
	return filepath.ToSlash(p)
}

// truthy decides whether a `condition:` variable enables its file. Variables are strings, so
// this defines exactly which strings count as false rather than leaving it to chance.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "", "false", "0", "no", "off":
			return false
		}
		return true
	default:
		return true
	}
}
