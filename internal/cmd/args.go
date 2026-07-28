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
}

// parseArgs splits raw args into positional tokens and a --key=value flag map.
//
// list/create disable cobra's own flag parsing and use this instead: the valid flag set for
// "create" varies by category (0/1/2+ selector levels, PRD Section 6/7) and by which axes and
// variables the chosen manifests declare - none of which is knowable before this exact invocation
// resolves, while cobra needs the full flag set to exist before it parses anything.
//
// Flag syntax contract (PRD v1.8 Section 8.1):
//   - a flag with a value is ALWAYS --key=value;
//   - a flag with no "=" is a boolean set to "true";
//   - any token not starting with "--" is positional, wherever it appears.
//
// The two-token "--key value" form is deliberately not supported. Accepting it forced the parser
// to consume the following token unconditionally, which had no safe behaviour: a boolean flag
// swallowed the next positional, so `create fw services --dry-run payment-svc` silently generated
// an artefact named "services", and `--function --protocol rest-http` set function to the literal
// "--protocol". It also made the --force/--skip-existing pair that NFR #2 calls for impossible to
// express at all (design review 2026-07-27 section 2.10).
func parseArgs(args []string) *parsedArgs {
	p := &parsedArgs{
		flags:    map[string]string{},
		consumed: map[string]bool{},
	}
	for _, a := range args {
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
	return p
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

// requireAllFlagsConsumed turns leftover flags into an error listing what would have been valid.
//
// PRD v1.8 Section 8.2 makes this mandatory. Silently ignoring an unrecognised flag is the worst
// failure mode a generator can have: `--style=microservice` (the form every design document uses)
// was accepted and dropped, so the service came out with no pattern overlay, exit code 0, no
// warning. A mistyped `--fucntion=web` behaved the same way, quietly falling back to the default
// (design review 2026-07-27 section 2.3). It matters more, not less, now that flag names are
// themselves configurable from the manifests.
func (p *parsedArgs) requireAllFlagsConsumed(valid []string) error {
	unused := p.unusedFlags()
	if len(unused) == 0 {
		return nil
	}
	sort.Strings(valid)
	plural := "flag"
	if len(unused) > 1 {
		plural = "flags"
	}
	return fmt.Errorf("unknown %s: --%s\nvalid flags here: --%s",
		plural, strings.Join(unused, ", --"), strings.Join(valid, ", --"))
}
