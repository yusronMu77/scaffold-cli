package cmd

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseArgs_PositionalOnly(t *testing.T) {
	got := mustParseArgs(t, []string{"spring-boot", "services", "payment-service"})
	want := []string{"spring-boot", "services", "payment-service"}
	if !reflect.DeepEqual(got.positional, want) {
		t.Errorf("expected positional %v, got %v", want, got.positional)
	}
	if len(got.flags) != 0 {
		t.Errorf("expected no flags, got %v", got.flags)
	}
}

func TestParseArgs_EqualsForm(t *testing.T) {
	got := mustParseArgs(t, []string{"--function=web", "--protocol=rest-http"})
	want := map[string]string{"function": "web", "protocol": "rest-http"}
	if !reflect.DeepEqual(got.flags, want) {
		t.Errorf("expected flags %v, got %v", want, got.flags)
	}
}

// Covers that a flag with no "=" is a boolean set to true, and never swallows the following
// token as its value.
func TestParseArgs_BareFlagIsBooleanAndDoesNotEatNextToken(t *testing.T) {
	got := mustParseArgs(t, []string{"fw", "services", "--dry-run", "payment-svc"})

	wantPositional := []string{"fw", "services", "payment-svc"}
	if !reflect.DeepEqual(got.positional, wantPositional) {
		t.Errorf("expected positional %v, got %v", wantPositional, got.positional)
	}
	if v := got.flags["dry-run"]; v != "true" {
		t.Errorf(`expected flags["dry-run"]="true", got %q`, v)
	}
}

// The two-token form is deliberately unsupported: "--function web" leaves "web" positional rather
// than guessing it is the value.
func TestParseArgs_SpaceSeparatedFormNotSupported(t *testing.T) {
	got := mustParseArgs(t, []string{"--function", "web"})

	if v := got.flags["function"]; v != "true" {
		t.Errorf(`expected --function to parse as boolean "true", got %q`, v)
	}
	if !reflect.DeepEqual(got.positional, []string{"web"}) {
		t.Errorf("expected 'web' to stay positional, got %v", got.positional)
	}
}

// A flag whose value is itself flag-shaped stays intact, because the value comes from the "="
// split rather than from the next token.
func TestParseArgs_FlagValueLookingLikeAFlag(t *testing.T) {
	got := mustParseArgs(t, []string{"--function=--protocol"})
	if v := got.flags["function"]; v != "--protocol" {
		t.Errorf(`expected flags["function"]="--protocol", got %q`, v)
	}
}

func TestParseArgs_Mixed(t *testing.T) {
	got := mustParseArgs(t, []string{
		"spring-boot", "services", "payment-service",
		"--function=web", "--protocol=rest-http", "--style=microservice",
	})
	wantPositional := []string{"spring-boot", "services", "payment-service"}
	if !reflect.DeepEqual(got.positional, wantPositional) {
		t.Errorf("expected positional %v, got %v", wantPositional, got.positional)
	}
	wantFlags := map[string]string{"function": "web", "protocol": "rest-http", "style": "microservice"}
	if !reflect.DeepEqual(got.flags, wantFlags) {
		t.Errorf("expected flags %v, got %v", wantFlags, got.flags)
	}
}

func TestParseArgs_HelpDetected(t *testing.T) {
	for _, arg := range []string{"--help", "--h"} {
		if !mustParseArgs(t, []string{arg}).help {
			t.Errorf("expected %q to set help", arg)
		}
	}
}

func TestRequireAllFlagsConsumed_ReportsUnknownFlags(t *testing.T) {
	args := mustParseArgs(t, []string{"--function=web", "--stlye=microservice"})
	args.get("function")

	err := args.requireAllFlagsConsumed([]string{"function", "style"})
	if err == nil {
		t.Fatal("expected an unknown-flag error, got nil")
	}
	if !strings.Contains(err.Error(), "--stlye") {
		t.Errorf("expected the error to name the offending flag, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--style") {
		t.Errorf("expected the error to list valid flags, got: %v", err)
	}
}

func TestRequireAllFlagsConsumed_PassesWhenAllUsed(t *testing.T) {
	args := mustParseArgs(t, []string{"--function=web", "--help"})
	args.get("function")

	if err := args.requireAllFlagsConsumed([]string{"function"}); err != nil {
		t.Errorf("expected no error (--help is never an unknown flag), got: %v", err)
	}
}

// mustParseArgs is parseArgs for the many tests that pass syntactically valid input.
func mustParseArgs(t *testing.T, args []string) *parsedArgs {
	t.Helper()
	got, err := parseArgs(args)
	if err != nil {
		t.Fatalf("parseArgs(%v): %v", args, err)
	}
	return got
}

// Covers the -f/--values spellings, the one flag allowed to take a space-separated value since
// the engine knows in advance that it expects one.
func TestParseArgs_ValuesFileSpellings(t *testing.T) {
	for _, args := range [][]string{
		{"-f", "values.yaml"},
		{"-f=values.yaml"},
		{"--values=values.yaml"},
		{"--values", "values.yaml"},
	} {
		got := mustParseArgs(t, args)
		if len(got.valuesFiles) != 1 || got.valuesFiles[0] != "values.yaml" {
			t.Errorf("%v: expected one values file, got %v", args, got.valuesFiles)
		}
		if len(got.positional) != 0 {
			t.Errorf("%v: the value must not leak into positionals, got %v", args, got.positional)
		}
	}
}

// Repeatable, so a base file can be layered with an environment-specific one.
func TestParseArgs_ValuesFileRepeatable(t *testing.T) {
	got := mustParseArgs(t, []string{"-f", "base.yaml", "-f", "prod.yaml"})
	if len(got.valuesFiles) != 2 || got.valuesFiles[1] != "prod.yaml" {
		t.Errorf("expected both files in order, got %v", got.valuesFiles)
	}
}

// A dangling -f must complain rather than silently swallow the next positional.
func TestParseArgs_ValuesFileWithoutValueIsAnError(t *testing.T) {
	if _, err := parseArgs([]string{"create", "-f"}); err == nil {
		t.Fatal("expected a dangling -f to error, got nil")
	}
}
