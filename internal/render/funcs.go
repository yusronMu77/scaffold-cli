package render

import (
	"text/template"
	"unicode"
	"unicode/utf8"

	"github.com/Masterminds/sprig/v3"
)

// baseFuncs is sprig plus scaffold-cli's own small set of additions, shared by every template
// execution path (with or without a partial set) so a function added here is available everywhere.
func baseFuncs() template.FuncMap {
	funcs := sprig.TxtFuncMap()
	funcs["lowerFirst"] = lowerFirst
	funcs["plural"] = plural
	return funcs
}

// lowerFirst lowercases only the first rune, leaving the rest untouched - the primitive Sprig is
// missing (its `camelcase` produces PascalCase, not lowerCamelCase - issue #65). Composed with
// `camelcase`, it gives a true lowerCamelCase identifier from a snake_case/kebab-case source for
// the multi-word case; for a single already-PascalCase word, `lower` alone is simpler and correct.
func lowerFirst(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 {
		return s
	}
	return string(unicode.ToLower(r)) + s[size:]
}
