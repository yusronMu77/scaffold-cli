package jig

import "strings"

// This file is the complete fixed vocabulary between the engine and scaffolding-code — if a name
// isn't here, the engine doesn't know it and the data is free to choose it. Every other name is
// data; `.gitignore`, for example, is stored as `gitignore.tpl` and renamed via a jig's `files:`
// block, not known to the engine. Helm draws the same line with `Chart.yaml`, `templates/` and
// `_*.tpl`.

const (
	// FileName is the jig itself. Reserved at every depth: it is the engine's contract with
	// the template author and must never appear in generated output.
	FileName = "jig.yaml"

	// PartialPrefix and PartialSuffix mark a file as `{{ define }}` blocks for other templates to
	// `include`, rather than output of its own. Same convention as Helm's `_helpers.tpl`.
	PartialPrefix = "_"
	PartialSuffix = ".tpl"
)

// IsPartial reports whether a file name holds named template definitions rather than output.
func IsPartial(name string) bool {
	return strings.HasPrefix(name, PartialPrefix) && strings.HasSuffix(name, PartialSuffix)
}

// Reserved keys in a values file. A jig naming a selector, axis flag, or variable after one of
// these is rejected, since the two meanings would be indistinguishable in a values file.
const (
	KeyFramework = "framework"
	KeyCategory  = "category"
	KeyName      = "name"
	KeyData      = "data"
)

// ReservedValueKeys maps each reserved values-file key to the reason it is taken.
var ReservedValueKeys = map[string]string{
	KeyFramework: "one of the three positional arguments",
	KeyCategory:  "one of the three positional arguments",
	KeyName:      "one of the three positional arguments",
	KeyData:      "the structured `data:` block",
}
