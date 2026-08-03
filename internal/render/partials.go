package render

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"

	"scaffold-engine-go/internal/jig"
)

// IsPartial reports whether a file holds `{{ define }}` blocks rather than output. The naming rule
// lives with the rest of the engine's reserved vocabulary in internal/jig/reserved.go.
func IsPartial(name string) bool { return jig.IsPartial(name) }

// CollectPartials gathers every `_*.tpl` across the sources into one template set, so a fragment
// repeated inside many files (a license header, a logging block) can be defined once and included
// by name instead of copied everywhere. Sources are parsed base-first, so a deeper level redefining
// the same name wins, and a partial declared at the framework level is available to every template
// under it.
func CollectPartials(sources []Source) (*template.Template, error) {
	set := template.New("_partials").Funcs(partialFuncs())

	for _, src := range sources {
		err := filepath.WalkDir(src.Dir, func(abs string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// Same rule as content: a subfolder with its own jig belongs to another
				// source, and its partials are collected when that source is visited.
				if abs != src.Dir {
					if _, statErr := os.Stat(filepath.Join(abs, jig.FileName)); statErr == nil {
						return fs.SkipDir
					}
				}
				return nil
			}
			if !IsPartial(d.Name()) {
				return nil
			}
			raw, err := os.ReadFile(abs)
			if err != nil {
				return fmt.Errorf("reading partial %s: %w", abs, err)
			}
			if _, err := set.Parse(string(raw)); err != nil {
				return fmt.Errorf("parsing partial %s: %w", abs, err)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return set, nil
}

// partialFuncs is sprig plus `include`, which returns rendered text as a string so it can be piped
// (e.g. for indentation) - something Go's built-in `template` action cannot do.
func partialFuncs() template.FuncMap {
	funcs := sprig.TxtFuncMap()
	// Placeholder so the set parses; withInclude replaces it once the real set exists.
	funcs["include"] = func(string, any) (string, error) { return "", nil }
	return funcs
}

// withInclude returns a copy of set whose `include` resolves names against that same set. It must
// be bound per set rather than once at construction, since `include` needs a reference to the
// finished set.
func withInclude(set *template.Template) *template.Template {
	set.Funcs(template.FuncMap{
		"include": func(name string, data any) (string, error) {
			var buf strings.Builder
			if err := set.ExecuteTemplate(&buf, name, data); err != nil {
				return "", fmt.Errorf("include %q: %w", name, err)
			}
			return buf.String(), nil
		},
	})
	return set
}
