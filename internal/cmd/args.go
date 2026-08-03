package cmd

import (
	"fmt"
	"sort"
	"strings"
)

// parsedArgs is the result of splitting a raw argv into positionals and flags, plus enough
// bookkeeping to report which flags nobody ended up using.
type parsedArgs struct {
	positional []string
	flags      map[string]string
	consumed   map[string]bool
	help       bool
	// valuesFiles are the -f/--values paths, in the order given. Repeatable, so it can't live in
	// the flags map; later files override earlier ones.
	valuesFiles []string
	// data is the merged `data:` object from those files. It can't live in the flags map either,
	// since a flag is one scalar and this is a nested document.
	data map[string]any
}

// engineValueFlags are the engine's own flags that may take a space-separated value, mapped from
// every spelling that selects them. This is the one exception to the --key=value flag syntax; it's
// safe because these flags belong to the engine and are known at compile time, unlike jig-declared
// flags whose value-taking status can't be known in advance.
var engineValueFlags = map[string]string{
	"-f":       "values",
	"--values": "values",
}

// parseArgs splits raw args into positional tokens and a --key=value flag map.
//
// list/create disable cobra's own flag parsing and use this instead, since the valid flag set for
// "create" depends on which jigs are selected and isn't known until this exact invocation
// resolves.
//
// Flag syntax:
//   - a flag with a value is always --key=value;
//   - a flag with no "=" is a boolean set to "true";
//   - any token not starting with "--" is positional, wherever it appears.
//
// The two-token "--key value" form is deliberately unsupported: it would force a boolean flag to
// swallow the next token unconditionally, with no safe way to tell a flag's value apart from a
// following positional.
func parseArgs(args []string) (*parsedArgs, error) {
	p := &parsedArgs{
		flags:    map[string]string{},
		consumed: map[string]bool{},
	}
	for i := 0; i < len(args); i++ {
		a := args[i]

		// Engine flags that accept a space-separated value, e.g. `-f values.yaml`. An `=` form is
		// accepted too, so `-f=values.yaml` and `--values=values.yaml` all work.
		if name, spelling, ok := matchEngineValueFlag(a); ok {
			value := spelling
			if value == "" {
				if i+1 >= len(args) {
					return nil, fmt.Errorf("%s needs a value, e.g. %s values.yaml", a, a)
				}
				i++
				value = args[i]
			}
			if name == "values" {
				p.valuesFiles = append(p.valuesFiles, value)
			}
			continue
		}

		if !strings.HasPrefix(a, "--") {
			p.positional = append(p.positional, a)
			continue
		}
		key := strings.TrimPrefix(a, "--")
		value := "true"
		if eq := strings.Index(key, "="); eq >= 0 {
			key, value = key[:eq], key[eq+1:]
		}
		if key == "help" || key == "h" {
			p.help = true
		}
		p.flags[key] = value
	}
	return p, nil
}

// matchEngineValueFlag reports whether a token selects an engine value-flag, returning the flag's
// canonical name and the inline value if one was given with `=`.
func matchEngineValueFlag(token string) (name, inlineValue string, ok bool) {
	spelling, inline, hasInline := strings.Cut(token, "=")
	name, ok = engineValueFlags[spelling]
	if !ok {
		return "", "", false
	}
	if hasInline {
		return name, inline, true
	}
	return name, "", true
}

// get returns a flag's value and marks it consumed, so unusedFlags can report the rest.
func (p *parsedArgs) get(key string) (string, bool) {
	v, ok := p.flags[key]
	if ok {
		p.consumed[key] = true
	}
	return v, ok
}

// value is get() for callers that treat "absent" and "empty" the same.
func (p *parsedArgs) value(key string) string {
	v, _ := p.get(key)
	return v
}

// markConsumed records a flag as handled even when its value was not read through get(),
// e.g. a flag the engine accepts but ignores in this code path.
func (p *parsedArgs) markConsumed(keys ...string) {
	for _, k := range keys {
		p.consumed[k] = true
	}
}

// unusedFlags lists every flag the caller never looked at, sorted for stable error messages.
func (p *parsedArgs) unusedFlags() []string {
	var unused []string
	for k := range p.flags {
		if !p.consumed[k] && k != "help" && k != "h" {
			unused = append(unused, k)
		}
	}
	sort.Strings(unused)
	return unused
}

// requireAllFlagsConsumed turns leftover flags into an error listing what would have been valid,
// so an unrecognised or misspelled flag fails loudly instead of being silently dropped.
func (p *parsedArgs) requireAllFlagsConsumed(valid []string) error {
	unused := p.unusedFlags()
	if len(unused) == 0 {
		return nil
	}
	// Dedupe: a variable declared high in the chain and overridden lower down contributes the same
	// flag name twice, and listing it twice makes the error look buggy.
	seen := map[string]bool{}
	deduped := valid[:0]
	for _, v := range valid {
		if !seen[v] {
			seen[v] = true
			deduped = append(deduped, v)
		}
	}
	valid = deduped
	sort.Strings(valid)
	plural := "flag"
	if len(unused) > 1 {
		plural = "flags"
	}
	return fmt.Errorf("unknown %s: --%s\nvalid flags here: --%s",
		plural, strings.Join(unused, ", --"), strings.Join(valid, ", --"))
}
