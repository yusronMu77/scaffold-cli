package render

import "testing"

// lowerFirst is the primitive Sprig's misleadingly-named "camelcase" (which is actually
// PascalCase) doesn't provide - issue #65.
func TestLowerFirst(t *testing.T) {
	cases := map[string]string{
		"Order":       "order",
		"OrderStatus": "orderStatus",
		"":            "",
		"already":     "already",
		"É":           "é",
	}
	for in, want := range cases {
		if got := lowerFirst(in); got != want {
			t.Errorf("lowerFirst(%q) = %q, want %q", in, got, want)
		}
	}
}

// camelcase composed with lowerFirst is the documented way to get true lowerCamelCase from a
// multi-word snake/kebab-case source, since Sprig's camelcase alone yields PascalCase.
func TestLowerFirst_ComposesWithCamelcaseForLowerCamelCase(t *testing.T) {
	got, err := renderString("t", `{{ "order_status" | camelcase | lowerFirst }}`, Context{})
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if got != "orderStatus" {
		t.Errorf("got %q, want %q", got, "orderStatus")
	}
}
