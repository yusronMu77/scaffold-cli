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

	"scaffold-engine-go/internal/jig"
)

// The engine's reserved names all live in one place - see internal/jig/reserved.go, which is
// the complete list of words the engine owns. Everything else in a template folder is data.

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

// Source is one folder contributing files, paired with the jig that governs it.
type Source struct {
	Dir      string
	Manifest *jig.Jig
	// Label is a human-readable name used in error messages (e.g. `--style=microservice`).
	Label string
	// Priority orders overlays; higher applies later and therefore wins. The required base
	// dimension (which resolves <template>) is always ordered first regardless of this value.
	Priority int
	// Overlay marks a source that came from an optional dimension (`--style=...`) rather than the
	// inheritance chain. It may override files but may not supply a default for another level's
	// variable.
	Overlay bool
	// Layout is the accumulated, already-rendered set of path-prefix rules in effect for this
	// source - inherited from every level above it. See CollectLayout.
	Layout []ResolvedLayout
	// Partials is the shared set of `{{ define }}` blocks collected from every source, so any
	// template can `include` a fragment declared anywhere up the chain. See CollectPartials.
	Partials *template.Template
}

// ResolvedLayout is a LayoutRule with its `to` already rendered against the context.
type ResolvedLayout struct {
	From string
	To   string
}

// CollectLayout gathers layout rules down the inheritance chain, rendering each `to` against ctx.
// Later sources override an earlier rule with the same `from` (the same deeper-wins precedence as
// everywhere else), and rules are returned longest-prefix-first so the most specific match wins.
func CollectLayout(sources []*jig.Jig, ctx Context) ([]ResolvedLayout, error) {
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

// RenderSource walks one source folder and renders everything in it against ctx. The folder's
// contents are the source of truth: files are rendered by default, and `files:` in the jig only
// overrides specific ones. Placeholders are substituted in both file contents and path names. A
// subdirectory with its own jig.yaml is skipped, since that makes it a discovery node rather than
// template content.
func RenderSource(src Source, ctx Context) ([]File, error) {
	overrides := indexOverrides(src.Manifest)
	mergePaths := map[string]bool{}
	if src.Manifest != nil {
		for _, p := range src.Manifest.Merge {
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
			// A subfolder with its own jig.yaml is a discovery node, not content: its files
			// belong to whichever source resolves to it, not to this one.
			if _, err := os.Stat(filepath.Join(abs, jig.FileName)); err == nil {
				return fs.SkipDir
			}
			return nil // otherwise directories are created implicitly from the files inside them
		}
		if d.Name() == jig.FileName {
			return nil // reserved: the contract itself is never emitted
		}
		if IsPartial(d.Name()) {
			return nil // `_*.tpl` holds definitions for other templates, not output of its own
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

		// Output path, in order of precedence: an explicit `target` on this file's entry, then an
		// inherited layout rule whose `from` prefixes this path, then the source path unchanged.
		// Placeholders are substituted across the whole path, and output directories are created
		// on demand at write time.
		outRel := relSlash
		switch {
		case hasOverride && entry.Target != "":
			outRel = slashPath(entry.Target)
		default:
			outRel = applyLayout(relSlash, src.Layout)
		}
		outRel, err = renderWith(src.Partials, "path "+relSlash, outRel, ctx)
		if err != nil {
			return err
		}
		outRel = path.Clean(outRel)
		if err := checkContained(outRel); err != nil {
			return fmt.Errorf("template file %s: %w", relSlash, err)
		}

		content := raw
		if !hasOverride || entry.ShouldTemplate() {
			rendered, err := renderWith(src.Partials, "file "+relSlash, string(raw), ctx)
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

	// An override pointing at a file that isn't there is a template-authoring bug; silently
	// ignoring it would hide a typo in a path.
	for p := range overrides {
		if !used[p] {
			return nil, fmt.Errorf("%s: jig `files:` lists %q, but no such file exists in %s",
				src.Label, p, src.Dir)
		}
	}
	return out, nil
}

func indexOverrides(m *jig.Jig) map[string]jig.FileEntry {
	out := map[string]jig.FileEntry{}
	if m == nil {
		return out
	}
	for _, f := range m.Files {
		out[path.Clean(slashPath(f.Path))] = f
	}
	return out
}

// renderString executes one template with no partials available. Used for the short strings that
// are resolved before the partial set exists - computed variables, layout rules, dependency fields.
func renderString(what, text string, ctx Context) (string, error) {
	return renderWith(nil, what, text, ctx)
}

// renderWith executes one template inside a partial set, so `{{ include "name" . }}` resolves.
// Sprig is registered either way, giving template authors helpers like `replace`, `title`, and
// `indent`. `missingkey=error` is deliberate: a typo'd variable must fail loudly rather than
// silently render as "<no value>" and leave a broken file behind.
func renderWith(partials *template.Template, what, text string, ctx Context) (string, error) {
	var tmpl *template.Template
	if partials != nil {
		// Clone so one file's parse cannot leak definitions into the next file's set.
		clone, err := partials.Clone()
		if err != nil {
			return "", fmt.Errorf("preparing partials for %s: %w", what, err)
		}
		tmpl = withInclude(clone).New(what)
	} else {
		tmpl = template.New(what).Funcs(sprig.TxtFuncMap())
	}

	tmpl, err := tmpl.Option("missingkey=error").Parse(text)
	if err != nil {
		return "", fmt.Errorf("parsing %s: %w", what, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]any(ctx)); err != nil {
		return "", fmt.Errorf("rendering %s: %w", what, err)
	}
	return buf.String(), nil
}

// checkContained rejects an output path that would escape the target directory.
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
