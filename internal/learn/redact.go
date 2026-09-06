package learn

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// Redaction records one value-level secret redaction: which file it happened in and which rule
// caught it. Never carries the actual secret text.
type Redaction struct {
	Path string
	Rule string
}

// redactionPlaceholderPattern matches any live redaction token this package ever emits - used by
// WriteDraft to catch a draft that failed to templatize a detected secret, by Review to normalize
// a freshly re-redacted example before comparing it against a draft's render, and internally here
// to stop one rule from re-redacting a token an earlier rule already produced.
var redactionPlaceholderPattern = regexp.MustCompile(`__SCAFFOLD_REDACTED_SECRET_\d+__`)

func redactionPlaceholder(n int) string {
	return fmt.Sprintf("__SCAFFOLD_REDACTED_SECRET_%d__", n)
}

// RedactionProbeValue is the fixed sentinel Review substitutes for every `redacted: true` variable
// when synthesizing a render to self-check a draft (see review.go) - distinct in shape from a real
// numbered placeholder so it can never be confused with one.
const RedactionProbeValue = "__SCAFFOLD_REDACTED_SECRET_PROBE__"

type secretRule struct {
	name string
	re   *regexp.Regexp
}

// secretRules is a FIXED SLICE, never a map - order must be reproducible so redaction numbering is
// deterministic (Review depends on re-deriving the exact same output from the same input). A rule
// with a capture group redacts only group 1, keeping the surrounding key name/scheme/username
// visible; a rule with none redacts the whole match.
var secretRules = []secretRule{
	{"aws-access-key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"google-api-key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"github-token", regexp.MustCompile(`\bgh[pousr]_[0-9A-Za-z]{36,}\b`)},
	{"slack-token", regexp.MustCompile(`\bxox[baprs]-[0-9A-Za-z-]{10,48}\b`)},
	{"jwt", regexp.MustCompile(`\bey[A-Za-z0-9_-]{10,}\.ey[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)},
	{"private-key-block", regexp.MustCompile(`(?s)-----BEGIN[ A-Z0-9_-]*PRIVATE KEY-----.*?-----END[ A-Z0-9_-]*PRIVATE KEY-----`)},
	{"url-credential", regexp.MustCompile(`://[^:/\s'"]+:([^@/\s'"]+)@`)},
	{"generic-secret-assignment", regexp.MustCompile(
		`(?i)(?:password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|credential)s?["']?\s*[:=]\s*["']?([^"'\s]{6,100})["']?`)},
}

// indirectionPatterns match a value that LOOKS like a hardcoded secret by shape but is actually a
// reference resolved elsewhere (an environment variable, a build-time filter, this engine's own
// template syntax) - these must never be redacted, since doing so would replace a legitimate,
// instance-independent indirection with a dead placeholder. Spring's own convention
// (`password: ${DB_PASSWORD}`) is the motivating case.
var indirectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^\$\{[A-Za-z_][\w.:-]*\}$`),  // ${DB_PASSWORD}, ${env:X}, Spring ${X:default}
	regexp.MustCompile(`^\$[A-Za-z_][A-Za-z0-9_]*$`), // $DB_PASSWORD
	regexp.MustCompile(`^%[A-Za-z_][A-Za-z0-9_]*%$`), // %DB_PASSWORD% (Windows-style)
	regexp.MustCompile(`^\{\{.*\}\}$`),               // {{ .Foo }} - this engine's OWN template syntax
	regexp.MustCompile(`^#\{.*\}$`),                  // #{foo} - Spring EL / Ruby interpolation
	regexp.MustCompile(`^<%=?.*%>$`),                 // <% %> / <%= %> - ERB/JSP
	regexp.MustCompile(`^@[A-Za-z_][\w.]*@$`),        // @foo@ - Maven resource filtering
}

func isIndirection(value string) bool {
	for _, p := range indirectionPatterns {
		if p.MatchString(value) {
			return true
		}
	}
	return false
}

const (
	minEntropyCandidateLen = 20
	maxEntropyCandidateLen = 200
	base64EntropyLimit     = 4.5 // detect-secrets' real default for base64-charset candidates
	hexEntropyLimit        = 3.0 // detect-secrets' real default for hex-charset candidates
	dedupMinLen            = 12  // below this, two unrelated short matches must not collapse into one variable
)

const (
	base64Charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/=_-"
	hexCharset    = "0123456789abcdefABCDEF"
)

// quotedLiteralPattern finds quoted string literals in the 20-200 char range (200 caps a
// data-URI/inlined-asset false positive from becoming an unfillable giant "secret" variable) -
// the candidate pool the entropy check runs over.
var quotedLiteralPattern = regexp.MustCompile(
	fmt.Sprintf(`"([^"]{%d,%d})"|'([^']{%d,%d})'`,
		minEntropyCandidateLen, maxEntropyCandidateLen, minEntropyCandidateLen, maxEntropyCandidateLen))

// RedactSecrets scans a set of already-read files for value-level secrets and replaces each with a
// placeholder token, so a real credential never reaches an LLM even when it sits inside a file
// that legitimately belongs to the template - unlike Scan's whole-file credential-store deny-list,
// this operates on content. Numbering is one counter across the whole call, assigned in
// file-then-match order, both already deterministic (files are processed in sorted-path order;
// matches within a file are found left to right), so calling this twice on the same input always
// produces the same output - load-bearing for Review, which re-derives it to check a draft against
// a fresh scan without ever seeing the real secret either.
func RedactSecrets(files []SourceFile) ([]SourceFile, []Redaction) {
	sorted := make([]SourceFile, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	seen := map[string]string{}
	counter := 0
	next := func(value string) string {
		if len(value) >= dedupMinLen {
			if token, ok := seen[value]; ok {
				return token
			}
		}
		counter++
		token := redactionPlaceholder(counter)
		if len(value) >= dedupMinLen {
			seen[value] = token
		}
		return token
	}

	out := make([]SourceFile, len(sorted))
	var redactions []Redaction
	for i, f := range sorted {
		content := f.Content
		for _, rule := range secretRules {
			content = redactRule(content, f.Path, rule, next, &redactions)
		}
		content = redactHighEntropy(content, f.Path, next, &redactions)
		out[i] = SourceFile{Path: f.Path, Content: content}
	}
	return out, redactions
}

// redactRule finds every match of rule in content and replaces it with a placeholder - the whole
// match if the rule has no capture group, or just group 1 if it does. A value that is already a
// redaction placeholder (an earlier rule already replaced it) or a known indirection is left
// untouched.
func redactRule(content, path string, rule secretRule, next func(string) string, redactions *[]Redaction) string {
	locs := rule.re.FindAllStringSubmatchIndex(content, -1)
	if locs == nil {
		return content
	}
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		redactStart, redactEnd := loc[0], loc[1]
		if len(loc) >= 4 && loc[2] >= 0 {
			redactStart, redactEnd = loc[2], loc[3]
		}
		value := content[redactStart:redactEnd]
		if isIndirection(value) || redactionPlaceholderPattern.MatchString(value) {
			continue
		}
		b.WriteString(content[last:redactStart])
		b.WriteString(next(value))
		last = redactEnd
		*redactions = append(*redactions, Redaction{Path: path, Rule: rule.name})
	}
	b.WriteString(content[last:])
	return b.String()
}

// redactHighEntropy runs after every named rule, over whatever content is left, catching a secret
// with no recognizable name or vendor prefix - a hardcoded token in a Java constant, say - via
// Shannon entropy over quoted string literals.
func redactHighEntropy(content, path string, next func(string) string, redactions *[]Redaction) string {
	locs := quotedLiteralPattern.FindAllStringSubmatchIndex(content, -1)
	if locs == nil {
		return content
	}
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		var valStart, valEnd int
		switch {
		case loc[2] >= 0:
			valStart, valEnd = loc[2], loc[3]
		case loc[4] >= 0:
			valStart, valEnd = loc[4], loc[5]
		default:
			continue
		}
		value := content[valStart:valEnd]
		if isIndirection(value) || redactionPlaceholderPattern.MatchString(value) {
			continue
		}
		charset, ok := candidateCharset(value)
		if !ok {
			continue
		}
		limit := base64EntropyLimit
		if charset == "hex" {
			limit = hexEntropyLimit
		}
		if shannonEntropy(value) < limit {
			continue
		}
		b.WriteString(content[last:valStart])
		b.WriteString(next(value))
		last = valEnd
		*redactions = append(*redactions, Redaction{Path: path, Rule: "high-entropy-" + charset})
	}
	b.WriteString(content[last:])
	return b.String()
}

// candidateCharset reports whether value lies entirely within the hex or base64 charset. Hex is
// checked first since every hex string is also valid base64 - the tighter, more specific charset
// should win so a hex-shaped secret gets the hex threshold, not the looser base64 one.
func candidateCharset(value string) (string, bool) {
	if isAllCharset(value, hexCharset) {
		return "hex", true
	}
	if isAllCharset(value, base64Charset) {
		return "base64", true
	}
	return "", false
}

func isAllCharset(value, charset string) bool {
	for _, r := range value {
		if !strings.ContainsRune(charset, r) {
			return false
		}
	}
	return true
}

// shannonEntropy computes the Shannon entropy of s in bits per character.
func shannonEntropy(s string) float64 {
	if s == "" {
		return 0
	}
	counts := map[rune]int{}
	for _, r := range s {
		counts[r]++
	}
	total := float64(len(s))
	var entropy float64
	for _, c := range counts {
		p := float64(c) / total
		entropy -= p * math.Log2(p)
	}
	return entropy
}
