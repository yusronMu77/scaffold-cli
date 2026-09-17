package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Merge combines the per-source rendered trees into the final tree. Collisions are keyed on the
// post-substitution path; a later source wins, except for paths marked merge_yaml, which are
// deep-merged instead of replaced.
func Merge(trees [][]File) ([]File, error) {
	files, _, err := MergeExplained(trees)
	return files, err
}

// Contribution records one source's effect on one output path, used by `--explain` to show why a
// merged file ended up the way it did.
type Contribution struct {
	Source string
	// Action is "added", "overrode" or "merged".
	Action string
}

// MergeExplained is Merge, plus a per-path record of every source that touched it.
func MergeExplained(trees [][]File) ([]File, map[string][]Contribution, error) {
	byPath := map[string]File{}
	contributions := map[string][]Contribution{}
	var order []string

	for _, tree := range trees {
		for _, f := range tree {
			existing, clash := byPath[f.Path]
			if !clash {
				byPath[f.Path] = f
				order = append(order, f.Path)
				contributions[f.Path] = append(contributions[f.Path],
					Contribution{Source: f.Source, Action: "added"})
				continue
			}
			action := "overrode"
			if existing.Merge || f.Merge {
				action = "merged"
			}
			contributions[f.Path] = append(contributions[f.Path],
				Contribution{Source: f.Source, Action: action})
			if existing.Merge || f.Merge {
				merged, err := mergeStructured(f.Path, existing.Content, f.Content)
				if err != nil {
					return nil, nil, fmt.Errorf("deep-merging %s (from %s and %s): %w",
						f.Path, existing.Source, f.Source, err)
				}
				f.Content = merged
				f.Merge = true
			}
			byPath[f.Path] = f // later source wins
		}
	}

	out := make([]File, 0, len(order))
	for _, p := range order {
		out = append(out, byPath[p])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, contributions, nil
}

// Exclude drops files whose output path matches any pattern, applied after the merge on the same
// paths collisions are keyed on. Patterns support `**` meaning "any number of segments". A pattern
// matching nothing is an error, since it usually means the file it referred to was renamed or
// moved.
func Exclude(files []File, patterns []string) ([]File, error) {
	if len(patterns) == 0 {
		return files, nil
	}
	used := make(map[string]bool, len(patterns))
	out := make([]File, 0, len(files))

	for _, f := range files {
		dropped := false
		for _, pattern := range patterns {
			ok, err := matchPath(pattern, f.Path)
			if err != nil {
				return nil, fmt.Errorf("invalid exclude pattern %q: %w", pattern, err)
			}
			if ok {
				used[pattern] = true
				dropped = true
			}
		}
		if !dropped {
			out = append(out, f)
		}
	}

	var stale []string
	for _, pattern := range patterns {
		if !used[pattern] {
			stale = append(stale, pattern)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		return nil, fmt.Errorf("exclude pattern(s) matched nothing: %s\n"+
			"the file was probably renamed or moved - update or remove the pattern",
			strings.Join(stale, ", "))
	}
	return out, nil
}

// matchPath is path.Match plus `**`, which path.Match itself cannot express: a plain `*` never
// crosses a `/`, so "src/**/App.java" needs expanding into a check per possible depth.
func matchPath(pattern, name string) (bool, error) {
	if !strings.Contains(pattern, "**") {
		return path.Match(pattern, name)
	}
	// Expand `**` to zero, one, two, ... intermediate segments and try each. Bounded by the
	// candidate's own depth, so this terminates and stays cheap.
	depth := strings.Count(name, "/") + 1
	for i := 0; i <= depth; i++ {
		var mid string
		switch i {
		case 0:
			mid = "" // `a/**/b` also has to match `a/b`
		default:
			mid = strings.TrimSuffix(strings.Repeat("*/", i), "/")
		}
		candidate := strings.Replace(pattern, "**", mid, 1)
		candidate = path.Clean(strings.ReplaceAll(candidate, "//", "/"))
		ok, err := matchPath(candidate, name)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// mergeStructured deep-merges two documents, choosing the merge strategy from the output path's
// name. requirements.txt gets its own line-based strategy, since pip's format isn't a document any
// parser can unmarshal; everything else goes through the extension-keyed codec table.
func mergeStructured(path string, base, override []byte) ([]byte, error) {
	if isRequirementsFile(path) {
		return mergeRequirementsTxt(base, override), nil
	}

	codec, err := codecFor(path)
	if err != nil {
		return nil, err
	}
	var b, o any
	if err := codec.unmarshal(base, &b); err != nil {
		return nil, fmt.Errorf("base is not valid %s: %w", codec.name, err)
	}
	if err := codec.unmarshal(override, &o); err != nil {
		return nil, fmt.Errorf("overlay is not valid %s: %w", codec.name, err)
	}
	return codec.marshal(mergeValues(b, o))
}

type codec struct {
	name      string
	unmarshal func([]byte, any) error
	marshal   func(any) ([]byte, error)
}

// codecFor picks the parser by extension. An unrecognised extension is an error rather than a
// silent fall-back to whole-file replacement.
func codecFor(path string) (codec, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yml", ".yaml":
		return codec{
			name:      "YAML",
			unmarshal: func(b []byte, v any) error { return yaml.Unmarshal(b, v) },
			marshal:   func(v any) ([]byte, error) { return yaml.Marshal(v) },
		}, nil
	case ".json":
		return codec{
			name:      "JSON",
			unmarshal: func(b []byte, v any) error { return json.Unmarshal(b, v) },
			marshal: func(v any) ([]byte, error) {
				// HTML escaping is off (via Encoder, not json.MarshalIndent), since the default
				// escapes characters like < and > and would mangle npm version ranges.
				var buf bytes.Buffer
				enc := json.NewEncoder(&buf)
				enc.SetEscapeHTML(false)
				enc.SetIndent("", "  ")
				if err := enc.Encode(v); err != nil {
					return nil, err
				}
				return buf.Bytes(), nil // Encode already appends a newline
			},
		}, nil
	default:
		return codec{}, fmt.Errorf("cannot deep-merge %s: no parser for extension %q "+
			"(supported: .yaml, .yml, .json)", path, filepath.Ext(path))
	}
}

func mergeValues(base, override any) any {
	bm, bok := toStringMap(base)
	om, ook := toStringMap(override)
	if !bok || !ook {
		return override // scalars and arrays: replace
	}
	out := make(map[string]any, len(bm)+len(om))
	for k, v := range bm {
		out[k] = v
	}
	for k, v := range om {
		if v == nil {
			delete(out, k) // explicit null deletes
			continue
		}
		if existing, ok := out[k]; ok {
			out[k] = mergeValues(existing, v)
			continue
		}
		out[k] = v
	}
	return out
}

// isRequirementsFile recognises a pip requirements manifest by name rather than by extension,
// since `.txt` alone is too broad a signal - an arbitrary text file listed under `merge:` should
// not be silently parsed as dependency lines.
func isRequirementsFile(p string) bool {
	return strings.EqualFold(filepath.Base(p), "requirements.txt")
}

// requirementsOperators are every version operator pip recognises in a requirements.txt line. `>=`
// and `<=` must be found before the corresponding bare `>`/`<` would also match, but since both
// land on the same starting index that ordering doesn't actually change which index wins.
var requirementsOperators = []string{"==", ">=", "<=", "~=", "!=", ">", "<"}

// requirementsLine is one line of a requirements.txt, already classified. Key is empty for a
// comment or blank line, which passes through unchanged instead of taking part in the merge.
type requirementsLine struct {
	raw string
	key string
}

// mergeRequirementsTxt merges two requirements.txt bodies by package name, the same higher-wins
// precedence mergeValues uses for maps: base keeps its line order and comments, override's pinned
// version replaces base's on a shared package, and any package only override lists is appended.
func mergeRequirementsTxt(base, override []byte) []byte {
	overrideLines := splitRequirementsLines(override)
	overrideByKey := map[string]string{}
	var overrideKeys []string
	for _, l := range overrideLines {
		if l.key == "" {
			continue
		}
		if _, seen := overrideByKey[l.key]; !seen {
			overrideKeys = append(overrideKeys, l.key)
		}
		overrideByKey[l.key] = l.raw
	}

	var out []string
	inBase := map[string]bool{}
	for _, l := range splitRequirementsLines(base) {
		if l.key == "" {
			out = append(out, l.raw)
			continue
		}
		inBase[l.key] = true
		if winner, ok := overrideByKey[l.key]; ok {
			out = append(out, winner)
			continue
		}
		out = append(out, l.raw)
	}
	for _, key := range overrideKeys {
		if !inBase[key] {
			out = append(out, overrideByKey[key])
		}
	}

	if len(out) == 0 {
		return nil
	}
	return []byte(strings.Join(out, "\n") + "\n")
}

// splitRequirementsLines splits a requirements.txt body into lines, keying each dependency line by
// the package name before its first version operator. A blank or comment (`#`) line gets no key.
func splitRequirementsLines(content []byte) []requirementsLine {
	text := strings.TrimSuffix(string(content), "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	out := make([]requirementsLine, 0, len(lines))
	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			out = append(out, requirementsLine{raw: raw})
			continue
		}
		out = append(out, requirementsLine{raw: raw, key: requirementsKey(trimmed)})
	}
	return out
}

// requirementsKey is the package name a dependency line identifies itself by: everything before
// its first version operator, or the whole (trimmed) line for a bare package with no version pin.
func requirementsKey(line string) string {
	cut := len(line)
	for _, op := range requirementsOperators {
		if i := strings.Index(line, op); i >= 0 && i < cut {
			cut = i
		}
	}
	return strings.TrimSpace(line[:cut])
}

// toStringMap normalises yaml.v3's map shapes. Decoding into `any` yields map[string]any, but
// nested documents can still surface map[any]any, so both are handled.
func toStringMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			ks, ok := k.(string)
			if !ok {
				return nil, false
			}
			out[ks] = val
		}
		return out, true
	default:
		return nil, false
	}
}
