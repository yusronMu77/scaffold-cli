package learn

import (
	"encoding/base64"
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
	// A structurally real (if content-wise synthetic) JWT: each of the first two segments must
	// base64url-decode to a valid JSON object for the jwt rule's validate hook to accept it.
	jwt := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"0000000000"}`)) + "." +
		strings.Repeat("C", 20)
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

// #36: a realistic Slack token is longer than the old {10,48} cap allowed, which left the secret
// tail sitting in plaintext right next to the placeholder. The whole token, tail included, must be
// gone now.
func TestRedactSecrets_SlackTokenFullLengthTokenFullyRedacted(t *testing.T) {
	longSlackToken := "xoxb-" + strings.Repeat("1", 12) + "-" + strings.Repeat("2", 12) +
		"-" + strings.Repeat("a", 32)
	got, redactions := redactOne(t, "token: "+longSlackToken)
	if strings.Contains(got, "aaaa") {
		t.Errorf("expected the entire long slack token (including its tail) to be redacted, got: %s", got)
	}
	if len(redactions) != 1 || redactions[0].Rule != "slack-token" {
		t.Errorf("expected exactly one slack-token redaction, got %+v", redactions)
	}
}

// #37: the jwt rule now requires each segment to actually decode as base64url+JSON, not just look
// like one - a well-formed JWT still redacts...
func TestRedactSecrets_JWTStructurallyValidIsRedacted(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"1234567890","name":"Test"}`))
	sig := strings.Repeat("s", 20)
	jwt := header + "." + payload + "." + sig
	mustRedact(t, "auth: "+jwt, jwt)
}

// ...but text that merely has the eyJ...eyJ...  shape without valid JSON segments must not be
// mistaken for a real JWT and redacted.
func TestRedactSecrets_JWTShapedButStructurallyInvalidIsNotRedacted(t *testing.T) {
	fakeHeader := "eyJ" + strings.Repeat("A", 20)
	fakePayload := "eyJ" + strings.Repeat("B", 20)
	fakeJWT := fakeHeader + "." + fakePayload + "." + strings.Repeat("C", 20)
	mustNotRedact(t, "value: "+fakeJWT)
}

// #40: the fine-grained github_pat_ format (11-char prefix + exactly 82 chars) must be caught
// alongside the classic ghp_/gho_/... prefix this rule already handled.
func TestRedactSecrets_GithubFineGrainedPAT(t *testing.T) {
	pat := "github_pat_" + strings.Repeat("A", 82)
	mustRedact(t, "token: "+pat, pat)
}

// #41: named rules for the vendor secret formats detect-secrets flags, previously only caught
// (less reliably, and mislabeled) by the generic/entropy rules.
func TestRedactSecrets_NewNamedRules(t *testing.T) {
	stripeKey := "sk_live_" + strings.Repeat("0", 25)
	slackWebhook := "https://hooks.slack.com/services/T" + strings.Repeat("0", 9) +
		"/B" + strings.Repeat("1", 9) + "/" + strings.Repeat("2", 21)
	npmToken := "npm_" + strings.Repeat("0", 36)
	pypiToken := "pypi-AgEIcHlwaS5vcmc" + strings.Repeat("0", 55)
	anthropicKey := "sk-ant-" + strings.Repeat("0", 25)
	openaiKey := "sk-" + strings.Repeat("0", 25)

	cases := map[string]struct{ content, secret, rule string }{
		"stripe-key":    {"key: " + stripeKey, stripeKey, "stripe-key"},
		"slack-webhook": {"webhook: " + slackWebhook, slackWebhook, "slack-webhook"},
		"npm-token":     {"registry: " + npmToken, npmToken, "npm-token"},
		"pypi-token":    {"index: " + pypiToken, pypiToken, "pypi-token"},
		"anthropic-key": {"key: " + anthropicKey, anthropicKey, "anthropic-key"},
		"openai-key":    {"key: " + openaiKey, openaiKey, "openai-key"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, redactions := redactOne(t, c.content)
			if strings.Contains(got, c.secret) {
				t.Errorf("expected %q to be redacted, still present in:\n%s", c.secret, got)
			}
			if len(redactions) != 1 || redactions[0].Rule != c.rule {
				t.Errorf("expected exactly one %s redaction, got %+v", c.rule, redactions)
			}
		})
	}
}

// anthropic-key must win the label over the looser openai-key pattern, since "sk-ant-" is a
// prefix openai-key's "sk-" would also match - rule order in secretRules decides this.
func TestRedactSecrets_AnthropicKeyWinsOverOpenAIPattern(t *testing.T) {
	got, redactions := redactOne(t, "key: sk-ant-"+strings.Repeat("0", 25))
	if len(redactions) != 1 {
		t.Fatalf("expected exactly one redaction, got %+v", redactions)
	}
	if redactions[0].Rule != "anthropic-key" {
		t.Errorf("expected anthropic-key to win, got %+v", redactions[0])
	}
	if strings.Count(got, "__SCAFFOLD_REDACTED_SECRET_") != 1 {
		t.Errorf("expected exactly one placeholder token, got: %s", got)
	}
}

// azure-connection-string must redact only the AccountKey value, keeping AccountName visible for
// context - same convention as url-credential.
func TestRedactSecrets_AzureConnectionStringRedactsOnlyAccountKey(t *testing.T) {
	got, redactions := redactOne(t, "conn: AccountName=mystorageacct;AccountKey="+strings.Repeat("0", 40)+";")
	if !strings.Contains(got, "AccountName=mystorageacct;") {
		t.Errorf("expected AccountName to survive, got: %s", got)
	}
	if strings.Contains(got, strings.Repeat("0", 40)) {
		t.Errorf("expected the AccountKey value to be redacted, got: %s", got)
	}
	if len(redactions) != 1 || redactions[0].Rule != "azure-connection-string" {
		t.Errorf("expected exactly one azure-connection-string redaction, got %+v", redactions)
	}
}

// #42: a quoted hex string whose length matches a common digest (MD5/SHA-1/SHA-256) must survive
// even when its entropy would otherwise clear the high-entropy threshold - a pinned commit hash or
// checksum, not a secret.
func TestRedactSecrets_HashLengthHexIsExemptFromEntropyCheck(t *testing.T) {
	// A realistic hex digest cycles through all 16 hex digits rather than repeating one character
	// (which would trivially score 0 entropy regardless of this exemption) - this stays above
	// hexEntropyLimit on entropy alone, so only the length exemption explains it surviving.
	for _, n := range []int{32, 40, 64} {
		digest := strings.Repeat("0123456789abcdef", n/16)
		t.Run(digest[:8], func(t *testing.T) {
			mustNotRedact(t, `sha = "`+digest+`"`)
		})
	}
}

// A same-shape hex string at a non-digest length must still be caught by the entropy check - the
// exemption is specific to known digest lengths, not a blanket hex carve-out.
func TestRedactSecrets_NonDigestLengthHexStillRedacted(t *testing.T) {
	digest := strings.Repeat("0123456789abcdef", 3) // 48 chars: high entropy, not a digest length
	mustRedact(t, `token = "`+digest+`"`, digest)
}

// RedactSecrets must preserve ExampleIndex on its output SourceFiles and on any Redaction it
// records - dropping it would silently break multi-example prompt selection downstream (see
// prompt.go's isMultiExample/promptForFiles), since redaction always runs before the model call.
func TestRedactSecrets_PreservesExampleIndex(t *testing.T) {
	files := []SourceFile{
		{Path: "a.properties", Content: "password=supersecretvalue123", ExampleIndex: 2},
	}
	out, redactions := RedactSecrets(files)
	if len(out) != 1 || out[0].ExampleIndex != 2 {
		t.Fatalf("expected the output file to keep ExampleIndex=2, got %+v", out)
	}
	if len(redactions) != 1 || redactions[0].ExampleIndex != 2 {
		t.Fatalf("expected the recorded redaction to carry ExampleIndex=2, got %+v", redactions)
	}
}
