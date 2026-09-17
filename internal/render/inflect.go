package render

import "strings"

// pluralIrregulars covers the common English nouns whose plural isn't a suffix rule - the ones a
// scaffold's EntityName is realistically ever set to. Not exhaustive: a template hitting a word
// missing here should declare its own `computed:` entry with the literal plural instead of
// stretching this table, the same escape hatch already used for any other non-canonical casing
// (issue #70 - a minimal built-in subset, not a full inflection engine, deliberately not the
// `jinzhu/inflection` dependency).
var pluralIrregulars = map[string]string{
	"child": "children", "person": "people", "man": "men", "woman": "women",
	"foot": "feet", "tooth": "teeth", "mouse": "mice", "goose": "geese", "ox": "oxen",
	"leaf": "leaves", "life": "lives", "knife": "knives", "wife": "wives", "half": "halves",
	"shelf": "shelves", "wolf": "wolves", "loaf": "loaves", "thief": "thieves",
	"potato": "potatoes", "tomato": "tomatoes", "hero": "heroes", "echo": "echoes",
	"cactus": "cacti", "focus": "foci", "fungus": "fungi", "nucleus": "nuclei",
	"analysis": "analyses", "basis": "bases", "crisis": "crises", "thesis": "theses",
	"phenomenon": "phenomena", "criterion": "criteria", "datum": "data",
	"index": "indices", "matrix": "matrices", "vertex": "vertices", "axis": "axes",
	"sheep": "sheep", "deer": "deer", "fish": "fish", "species": "species", "series": "series",
}

// plural returns word's English plural: an irregular from pluralIrregulars when it names one,
// otherwise a suffix rule (consonant+y -> ies; s/x/z/ch/sh -> es; else +s). Covers what a
// scaffold template actually needs (a plural REST path/table name from a singular EntityName),
// not general-purpose inflection - see pluralIrregulars' doc comment.
func plural(word string) string {
	if word == "" {
		return word
	}
	lower := strings.ToLower(word)
	if irregular, ok := pluralIrregulars[lower]; ok {
		return matchCase(word, irregular)
	}

	switch {
	case strings.HasSuffix(lower, "y") && len(lower) > 1 && !isVowel(lower[len(lower)-2]):
		return word[:len(word)-1] + "ies"
	case strings.HasSuffix(lower, "s"), strings.HasSuffix(lower, "x"), strings.HasSuffix(lower, "z"),
		strings.HasSuffix(lower, "ch"), strings.HasSuffix(lower, "sh"):
		return word + "es"
	default:
		return word + "s"
	}
}

func isVowel(b byte) bool {
	switch b {
	case 'a', 'e', 'i', 'o', 'u', 'A', 'E', 'I', 'O', 'U':
		return true
	default:
		return false
	}
}

// matchCase reapplies word's leading-capital pattern to replacement, an irregular whole-word swap
// (e.g. "Child" -> "children" would otherwise lose the capital scaffold templates rely on for a
// PascalCase EntityName).
func matchCase(word, replacement string) string {
	if word == "" || replacement == "" {
		return replacement
	}
	if word[0] >= 'A' && word[0] <= 'Z' {
		return strings.ToUpper(replacement[:1]) + replacement[1:]
	}
	return replacement
}
