package render

import "testing"

func TestPlural(t *testing.T) {
	cases := map[string]string{
		"Product":  "Products",
		"Category": "Categories",
		"Boy":      "Boys",
		"Class":    "Classes",
		"Box":      "Boxes",
		"Church":   "Churches",
		"Dish":     "Dishes",
		"Bus":      "Buses",
		"Child":    "Children",
		"child":    "children",
		"Person":   "People",
		"Sheep":    "Sheep",
		"":         "",
	}
	for in, want := range cases {
		if got := plural(in); got != want {
			t.Errorf("plural(%q) = %q, want %q", in, got, want)
		}
	}
}

// plural is a template function, not just a Go func - the composed pipeline a leaf template
// would actually use for a plural REST path from a PascalCase EntityName.
func TestPlural_AsTemplateFunc(t *testing.T) {
	got, err := renderString("t", `{{ "Category" | plural | lower }}`, Context{})
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if got != "categories" {
		t.Errorf("got %q, want %q", got, "categories")
	}
}
