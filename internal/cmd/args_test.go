package cmd

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseArgs_PositionalOnly(t *testing.T) {
	got := parseArgs([]string{"spring-boot", "services", "payment-service"})
	want := []string{"spring-boot", "services", "payment-service"}
	if !reflect.DeepEqual(got.positional, want) {
		t.Errorf("expected positional %v, got %v", want, got.positional)
	}
	if len(got.flags) != 0 {
		t.Errorf("expected no flags, got %v", got.flags)
	}
}

func TestParseArgs_EqualsForm(t *testing.T) {
	got := parseArgs([]string{"--function=web", "--protocol=rest-http"})
	want := map[string]string{"function": "web", "protocol": "rest-http"}
	if !reflect.DeepEqual(got.flags, want) {
		t.Errorf("expected flags %v, got %v", want, got.flags)
	}
}

// PRD v1.8 Section 8.1: a flag with no "=" is a boolean set to true, and it must NOT swallow the
// following token. Before this, `--dry-run payment-svc` consumed "payment-svc" as the flag's
// value, so the artefact silently got named after the category instead (design review section
// 2.10).
func TestParseArgs_BareFlagIsBooleanAndDoesNotEatNextToken(t *testing.T) {
	got := parseArgs([]string{"fw", "services", "--dry-run", "payment-svc"})

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
	got := parseArgs([]string{"--function", "web"})

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
	got := parseArgs([]string{"--function=--protocol"})
	if v := got.flags["function"]; v != "--protocol" {
		t.Errorf(`expected flags["function"]="--protocol", got %q`, v)
	}
}

func TestParseArgs_Mixed(t *testing.T) {
	got := parseArgs([]string{
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
		if !parseArgs([]string{arg}).help {
			t.Errorf("expected %q to set help", arg)
		}
	}
}

func TestRequireAllFlagsConsumed_ReportsUnknownFlags(t *testing.T) {
	args := parseArgs([]string{"--function=web", "--stlye=microservice"})
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
	args := parseArgs([]string{"--function=web", "--help"})
	args.get("function")

	if err := args.requireAllFlagsConsumed([]string{"function"}); err != nil {
		t.Errorf("expected no error (--help is never an unknown flag), got: %v", err)
	}
}
