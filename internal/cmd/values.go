package cmd

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// reservedValueKeys are the three positional arguments, which a values file may supply instead of
// the command line (PRD Section 8.7).
const (
	keyFramework = "framework"
	keyCategory  = "category"
	keyName      = "name"
)

// loadValuesFiles reads every -f file in order and flattens them into one flag map.
//
// The file format is deliberately the flag namespace with the dashes removed: `--package=x` on the
// command line is `package: x` in the file. That means there is no second mental model to learn,
// no second place where a name can be misspelled unnoticed (an unknown key fails exactly like an
// unknown flag), and no mapping table to keep in sync as manifests add variables.
//
// Later files override earlier ones, and the caller lets explicit command-line flags override
// everything - the same "more specific wins" precedence used throughout the engine.
func loadValuesFiles(paths []string) (map[string]string, error) {
	merged := map[string]string{}
	for _, path := range paths {
		values, err := loadValuesFile(path)
		if err != nil {
			return nil, err
		}
		for k, v := range values {
			merged[k] = v
		}
	}
	return merged, nil
}

func loadValuesFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading values file %s: %w", path, err)
	}

	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing values file %s: %w", path, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("values file %s is empty", path)
	}

	out := make(map[string]string, len(raw))
	var bad []string
	for k, v := range raw {
		s, ok := scalarToString(v)
		if !ok {
			// Nested maps and lists have no meaning here: every key is a flag, and a flag is a
			// single value. Saying so plainly beats silently rendering "map[]" into a file.
			bad = append(bad, k)
			continue
		}
		out[k] = s
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return nil, fmt.Errorf("values file %s: key(s) %s hold a list or a nested map, but every "+
			"key must be a single scalar value - each one corresponds to one CLI flag",
			path, strings.Join(bad, ", "))
	}
	return out, nil
}

// scalarToString accepts the YAML scalars a user would reasonably write, so `port: 8080` and
// `enabled: true` do not have to be quoted just because the engine passes values around as strings.
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

// applyValuesFile folds file-supplied values into the parsed arguments.
//
// Command-line flags win: a values file is the baseline, and a flag is the deliberate one-off
// override on top of it - the same reason `--set` exists alongside `-f` in Helm, except here the
// existing `--key=value` flags already serve that purpose, so no extra syntax is needed.
//
// Returns the resolved positionals (framework, category, name), which may come from either source.
func applyValuesFile(args *parsedArgs) (framework, category, name string, err error) {
	values, err := loadValuesFiles(args.valuesFiles)
	if err != nil {
		return "", "", "", err
	}

	for k, v := range values {
		if _, fromCLI := args.flags[k]; !fromCLI {
			args.flags[k] = v
		}
	}

	// The three positionals may be given either way. Reserved keys are marked consumed so the
	// unknown-flag check does not then complain about them.
	args.markConsumed(keyFramework, keyCategory, keyName)
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
