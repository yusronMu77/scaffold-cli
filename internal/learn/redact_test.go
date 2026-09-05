package learn

import (
	"strings"
	"testing"
)

func redactOne(t *testing.T, content string) (string, []Redaction) {
	t.Helper()
	out, redactions := RedactSecrets([]SourceFile{{Path: "f.txt", Content: content}})
	if len(out) != 1 {
		t.Fatalf("expected 1 file back, got %d", len(out))
	}
	return out[0].Content, redactions
}

func mustRedact(t *testing.T, content, mustContain string) {
	t.Helper()
	got, redactions := redactOne(t, content)
	if strings.Contains(got, mustContain) {
		t.Errorf("expected %q to be redacted, still present in:\n%s", mustContain, got)
	}
	if len(redactions) == 0 {
		t.Errorf("expected at least one Redaction to be recorded for:\n%s", content)
	}
	if !redactionPlaceholderPattern.MatchString(got) {
		t.Errorf("expected a placeholder token in the redacted output, got:\n%s", got)
	}
}

func mustNotRedact(t *testing.T, content string) {
	t.Helper()
	got, redactions := redactOne(t, content)
	if got != content {
		t.Errorf("expected content to be left untouched, got:\n%s\nwant:\n%s", got, content)
	}
	if len(redactions) != 0 {
		t.Errorf("expected no redactions, got %+v", redactions)
	}
}

func TestRedactSecrets_NamedRules(t *testing.T) {
	// Every value below is deliberately synthetic (repeated/placeholder characters, split across
	// concatenated literals) rather than a realistic-looking token - shaped just enough to satisfy
	// this package's own regexes, not real secret-scanner detectors, so a test fixture can never
	// itself be flagged as a leaked credential.
	awsKey := "AKIA" + strings.Repeat("0", 16)
	googleKey := "AIza" + strings.Repeat("0", 35)
	githubToken := "ghp_" + strings.Repeat("0", 36)
	slackToken := "xox" + "b-" + strings.Repeat("1", 10) + "-" + strings.Repeat("2", 10) +
		"-" + strings.Repeat("a", 16)
	jwt := "ey" + strings.Repeat("A", 20) + "." + "ey" + strings.Repeat("B", 20) +
		"." + strings.Repeat("C", 20)
	privateKey := "-----BEGIN " + "RSA PRIVATE KEY-----\n" +
		strings.Repeat("x", 60) + "\n" +
		"-----END " + "RSA PRIVATE KEY-----"

	cases := map[string]struct{ content, secret string }{
		"aws-access-key":    {"aws_key = " + awsKey, awsKey},
		"google-api-key":    {"key: " + googleKey, googleKey},
		"github-token":      {"token: " + githubToken, githubToken},
		"slack-token":       {"token: " + slackToken, slackToken},
		"jwt":               {"auth: " + jwt, jwt},
		"private-key-block": {privateKey, privateKey},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			mustRedact(t, c.content, c.secret)
		})
	}
}

// url-credential must redact only the password, keeping the scheme/user/host visible for context.
func TestRedactSecrets_URLCredentialRedactsOnlyPassword(t *testing.T) {
	got, redactions := redactOne(t, "url: postgres://dbuser:hunter2secret@localhost:5432/mydb")
	if !strings.Contains(got, "postgres://dbuser:") || !strings.Contains(got, "@localhost:5432/mydb") {
		t.Errorf("expected the scheme/user/host to survive, got: %s", got)
	}
	if strings.Contains(got, "hunter2secret") {
		t.Errorf("expected the password to be redacted, got: %s", got)
	}
	if len(redactions) != 1 || redactions[0].Rule != "url-credential" {
		t.Errorf("expected exactly one url-credential redaction, got %+v", redactions)
	}
}

func TestRedactSecrets_GenericAssignmentQuotedAndBare(t *testing.T) {
	mustRedact(t, `db.password=hunter2secretvalue`, "hunter2secretvalue")
	mustRedact(t, `"apiKey": "sk-abc123def456ghijkl"`, "sk-abc123def456ghijkl")
}

// The standard Spring Boot / env-var-indirection convention must never be redacted - it's a
// reference resolved at runtime, not a hardcoded secret, and redacting it would break the
// template's own portability.
func TestRedactSecrets_IndirectionPatternsAreNeverRedacted(t *testing.T) {
	cases := []string{
		"password: ${DB_PASSWORD}",
		"password: $DB_PASSWORD",
		"password: %DB_PASSWORD%",
		"password: {{ .DbPassword }}",
		"password: #{dbPassword}",
		"password: <%= db_password %>",
		"password: @db.password@",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			mustNotRedact(t, c)
		})
	}
}

func TestRedactSecrets_HighEntropyQuotedStringIsRedacted(t *testing.T) {
	// A realistic base64-shaped blob, well above the 4.5 bits/char base64 threshold.
	mustRedact(t, `token = "dGhpc0lzQVNlY3JldFRva2VuMTIzNDU2Nzg5MEFCQ0RFRg=="`,
		"dGhpc0lzQVNlY3JldFRva2VuMTIzNDU2Nzg5MEFCQ0RFRg==")
}

func TestRedactSecrets_LowEntropyQuotedStringIsNotRedacted(t *testing.T) {
	mustNotRedact(t, `note = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`)
}

func TestRedactSecrets_DedupesRepeatedLongSecretAcrossFiles(t *testing.T) {
	secret := "supersecretpassword123"
	files := []SourceFile{
		{Path: "a.properties", Content: "db.password=" + secret},
		{Path: "b.yaml", Content: "password: " + secret},
	}
	out, redactions := RedactSecrets(files)
	if len(redactions) != 2 {
		t.Fatalf("expected 2 redaction records, got %+v", redactions)
	}
	tokenA := redactionPlaceholderPattern.FindString(out[0].Content)
	tokenB := redactionPlaceholderPattern.FindString(out[1].Content)
	if tokenA == "" || tokenB == "" {
		t.Fatalf("expected both files to contain a placeholder token, got %q and %q", out[0].Content, out[1].Content)
	}
	if tokenA != tokenB {
		t.Errorf("expected the same repeated secret to dedup to the same token, got %q vs %q", tokenA, tokenB)
	}
}

// A short repeated word (below dedupMinLen) must NOT collapse into one variable - two unrelated
// occurrences of a generic word like "changeit" are not necessarily the same secret.
func TestRedactSecrets_ShortRepeatedValueDoesNotDedup(t *testing.T) {
	files := []SourceFile{
		{Path: "a.properties", Content: "password=changeit"},
		{Path: "b.properties", Content: "password=changeit"},
	}
	out, _ := RedactSecrets(files)
	tokenA := redactionPlaceholderPattern.FindString(out[0].Content)
	tokenB := redactionPlaceholderPattern.FindString(out[1].Content)
	if tokenA == "" || tokenB == "" {
		t.Fatalf("expected both to be redacted, got %q and %q", out[0].Content, out[1].Content)
	}
	if tokenA == tokenB {
		t.Errorf("expected two short unrelated matches to get different tokens, both got %q", tokenA)
	}
}

func TestRedactSecrets_DeterministicAcrossCalls(t *testing.T) {
	files := []SourceFile{
		{Path: "z.properties", Content: "password=firstsecretvalue123"},
		{Path: "a.properties", Content: "token=secondsecrettoken456"},
	}
	out1, red1 := RedactSecrets(files)
	out2, red2 := RedactSecrets(files)
	if len(out1) != len(out2) || len(red1) != len(red2) {
		t.Fatalf("expected identical shapes across calls")
	}
	for i := range out1 {
		if out1[i].Path != out2[i].Path || out1[i].Content != out2[i].Content {
			t.Errorf("expected identical output on repeated calls, got %+v vs %+v", out1[i], out2[i])
		}
	}
}

// A named rule's own placeholder must never be re-redacted by a later rule (e.g. a value that was
// already an AWS key match must not also get caught by generic-secret-assignment once replaced).
func TestRedactSecrets_PlaceholderNotReRedactedByLaterRule(t *testing.T) {
	got, redactions := redactOne(t, "password = AKIAIOSFODNN7EXAMPLE")
	if len(redactions) != 1 {
		t.Fatalf("expected exactly one redaction (aws-access-key only), got %+v", redactions)
	}
	if redactions[0].Rule != "aws-access-key" {
		t.Errorf("expected the aws-access-key rule to win, got %+v", redactions[0])
	}
	if strings.Count(got, "__SCAFFOLD_REDACTED_SECRET_") != 1 {
		t.Errorf("expected exactly one placeholder token, got: %s", got)
	}
}
