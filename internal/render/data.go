package render

import (
	"fmt"
	"sort"
	"strings"

	"scaffold-engine-go/internal/jig"
)

// MergeData folds the `data:` block declared at every level of the chain into the single object
// templates see as `.Data`, then renders every string inside it. Unlike `variables:` (a flat
// scalar map), `data:` can carry lists, nested objects, and multi-line snippets, merged with the
// same rule `merge:` uses on files: maps merge recursively, lists replace wholesale, an explicit
// null deletes a key. Merging happens before rendering because data arrives already parsed, unlike
// files which must render before they parse. overrides is the `data:` block from a -f values file,
// applied last so a user can adjust generated content without editing scaffolding-code.
func MergeData(sources []*jig.Jig, overrides map[string]any, ctx Context) (map[string]any, error) {
	merged := map[string]any{}
	for _, m := range sources {
		if m == nil || len(m.Data) == 0 {
			continue
		}
		merged = DeepMerge(merged, m.Data)
	}
	merged = DeepMerge(merged, overrides)

	rendered, err := renderData("data", merged, ctx)
	if err != nil {
		return nil, err
	}
	out, ok := rendered.(map[string]any)
	if !ok {
		// Unreachable: renderData preserves shape and the input is a map.
		return map[string]any{}, nil
	}
	return out, nil
}

// DeepMerge combines two decoded documents under the engine's single merge rule (see MergeData).
// Exported because values files need the same behaviour when several -f files each carry `data:`.
func DeepMerge(base, override map[string]any) map[string]any {
	switch {
	case len(override) == 0:
		return base
	case len(base) == 0:
		return override
	}
	out, ok := toStringMap(mergeValues(base, override))
	if !ok {
		return override
	}
	return out
}

// renderData walks the merged object and renders every string against the context, so a snippet
// can reference variables like `{{ .PackageName }}`. Keys are never rendered, only values. The
// path argument accumulates as `data.observability.tracing.sampler`, so an error can name exactly
// where in the object it occurred.
func renderData(path string, v any, ctx Context) (any, error) {
	switch t := v.(type) {
	case string:
		// Nothing to substitute means nothing to parse, keeping snippets full of non-template
		// braces (a struct literal, a JSON blob) out of the parser entirely.
		if !strings.Contains(t, "{{") {
			return t, nil
		}
		return renderString(path, t, ctx)

	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			rendered, err := renderData(fmt.Sprintf("%s[%d]", path, i), item, ctx)
			if err != nil {
				return nil, err
			}
			out[i] = rendered
		}
		return out, nil

	default:
		m, ok := toStringMap(v)
		if !ok {
			return v, nil // numbers, booleans, null - carried through as their own type
		}
		// Sorted so that a jig with two broken entries reports the same one every run.
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		out := make(map[string]any, len(m))
		for _, k := range keys {
			rendered, err := renderData(path+"."+k, m[k], ctx)
			if err != nil {
				return nil, err
			}
			out[k] = rendered
		}
		return out, nil
	}
}
