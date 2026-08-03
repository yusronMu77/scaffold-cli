package cmd

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"scaffold-engine-go/internal/jig"
	"scaffold-engine-go/internal/render"
)

// Aliases for the reserved values-file keys, which live in internal/jig/reserved.go.
const (
	keyFramework = jig.KeyFramework
	keyCategory  = jig.KeyCategory
	keyName      = jig.KeyName
	keyData      = jig.KeyData
)

// loadValuesFiles reads every -f file in order, returning the flat flag map and the merged `data:`
// object. Top-level keys mirror flag names (`--package=x` is `package: x`), so an unknown key
// fails just like an unknown flag. `data:` is the exception - a nested object, deep-merged across
// files. Later files override earlier ones; the caller then lets explicit flags override the
// result.
func loadValuesFiles(paths []string) (map[string]string, map[string]any, error) {
	merged := map[string]string{}
	var data map[string]any
	for _, path := range paths {
		values, fileData, err := loadValuesFile(path)
		if err != nil {
			return nil, nil, err
		}
		for k, v := range values {
			merged[k] = v
		}
		data = render.DeepMerge(data, fileData)
	}
	return merged, data, nil
}

func loadValuesFile(path string) (map[string]string, map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading values file %s: %w", path, err)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, nil, fmt.Errorf("parsing values file %s: %w", path, err)
	}
	if doc == nil {
		return nil, nil, fmt.Errorf("values file %s is empty", path)
	}

	var data map[string]any
	if v, ok := doc[keyData]; ok {
		delete(doc, keyData)
		if v != nil {
			m, isMap := v.(map[string]any)
			if !isMap {
				return nil, nil, fmt.Errorf("values file %s: `data:` must be a mapping of keys to "+
					"values, not %T", path, v)
			}
			data = m
		}
	}

	out := make(map[string]string, len(doc))
	var bad []string
	for k, v := range doc {
		s, ok := scalarToString(v)
		if !ok {
			// Nested maps/lists aren't valid at the top level, since every key there is a
			// single-value flag.
			bad = append(bad, k)
			continue
		}
		out[k] = s
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return nil, nil, fmt.Errorf("values file %s: key(s) %s hold a list or a nested map, but "+
			"every top-level key is one CLI flag and so must be a single scalar value.\n"+
			"Structured content goes under `data:`, where it is deep-merged with the jigs' "+
			"own and reaches templates as .Data", path, strings.Join(bad, ", "))
	}
	return out, data, nil
}

// scalarToString converts a YAML scalar like `8080` or `true` to a string, so common types don't
// need quoting just because the engine passes values around as strings.
func scalarToString(v any) (string, bool) {
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

// applyValuesFile folds file-supplied values into the parsed arguments, with command-line flags
// always winning over values-file entries. Returns the resolved positionals (framework, category,
// name), which may come from either source.
func applyValuesFile(args *parsedArgs) (framework, category, name string, err error) {
	values, data, err := loadValuesFiles(args.valuesFiles)
	if err != nil {
		return "", "", "", err
	}

	for k, v := range values {
		if _, fromCLI := args.flags[k]; !fromCLI {
			args.flags[k] = v
		}
	}
	args.data = data

	// The three positionals may be given either way; reserved keys are marked consumed so the
	// unknown-flag check does not complain about them.
	args.markConsumed(keyFramework, keyCategory, keyName, keyData)
	positional := []string{keyFramework, keyCategory, keyName}
	resolved := make([]string, 3)
	for i, key := range positional {
		switch {
		case i < len(args.positional):
			resolved[i] = args.positional[i]
		default:
			resolved[i] = args.flags[key]
		}
	}

	if len(args.positional) > 3 {
		return "", "", "", fmt.Errorf("too many positional arguments: %v", args.positional[3:])
	}

	var missing []string
	for i, key := range positional {
		if resolved[i] == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		hint := "pass them positionally, or set them in a values file passed with -f"
		if len(args.valuesFiles) == 0 {
			hint = "usage: scaffold create <framework> <category> <name> [--flag=value ...]\n" +
				"or supply them in a values file: scaffold create -f values.yaml"
		}
		return "", "", "", fmt.Errorf("missing required argument(s): %s\n%s",
			strings.Join(missing, ", "), hint)
	}

	return resolved[0], resolved[1], resolved[2], nil
}
