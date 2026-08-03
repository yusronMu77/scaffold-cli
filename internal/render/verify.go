package render

import (
	"fmt"
	"time"
)

// DefaultVerifyTimeout bounds a check that declares no `timeout:` of its own. Generous enough for a
// cold Maven or npm build, but bounded so a stuck build tool can't hang the whole lint forever.
const DefaultVerifyTimeout = 10 * time.Minute

// Verification is a `verify:` entry with its argv rendered and its timeout resolved, ready to run.
type Verification struct {
	Name        string
	Description string
	Command     []string
	Timeout     time.Duration
	// Source is the chain level that declared it, so a failure says where to go and fix it.
	Source string
}

// String renders the command the way a person would type it, for output only. It is deliberately
// NOT how the command is executed - execution passes the argv straight to the OS, with no shell
// involved (see jig.Verify).
func (v Verification) String() string {
	out := ""
	for i, arg := range v.Command {
		if i > 0 {
			out += " "
		}
		out += arg
	}
	return out
}

// CollectVerifications gathers `verify:` down the inheritance chain and renders each argv element.
// Same precedence as every other inherited thing: a deeper level declaring the same name replaces
// the shallower one, and declaration order is preserved otherwise.
func CollectVerifications(sources []Source, ctx Context) ([]Verification, error) {
	byName := map[string]Verification{}
	var order []string

	for _, src := range sources {
		if src.Manifest == nil {
			continue
		}
		for _, v := range src.Manifest.Verify {
			command, err := RenderStrings("verify "+v.Name+" command", v.Command, ctx)
			if err != nil {
				return nil, err
			}
			timeout := DefaultVerifyTimeout
			if v.Timeout != "" {
				parsed, err := time.ParseDuration(v.Timeout)
				if err != nil {
					// Already validated at load time; reachable only via a hand-built Jig.
					return nil, fmt.Errorf("verify %q: invalid timeout %q: %w", v.Name, v.Timeout, err)
				}
				timeout = parsed
			}
			if _, seen := byName[v.Name]; !seen {
				order = append(order, v.Name)
			}
			byName[v.Name] = Verification{
				Name:        v.Name,
				Description: v.Description,
				Command:     command,
				Timeout:     timeout,
				Source:      src.Label,
			}
		}
	}

	out := make([]Verification, 0, len(order))
	for _, name := range order {
		out = append(out, byName[name])
	}
	return out, nil
}
