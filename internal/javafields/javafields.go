// Package javafields extracts field declarations from an existing Java source file with a
// line-based regex heuristic - not a Java parser or AST - so a straightforward POJO/@Entity class
// can seed data.entity.fields without a model call (issue #71). Anything irregular enough to trip
// the heuristic is still better served by `scaffold learn`, which this package deliberately does
// not depend on.
package javafields

import (
	"regexp"
	"strings"
)

// Field is one extracted declaration, shaped to match data.entity.fields exactly as the mvc leaf's
// entity.params helper and a -f values file already consume it (see
// values/spring-boot/service-rest-mvc-entity.yaml): a name, a Java type, and the raw validation
// annotations, if any, found directly above the declaration.
type Field struct {
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"`
	Constraints []string `yaml:"constraints,omitempty"`
}

// fieldPattern matches a single-statement field declaration: one or more modifiers, a type
// (optionally dotted, generic, or an array), a name, an optional initializer, and a trailing
// semicolon. Requiring at least one modifier keeps it from firing on ordinary code inside a method
// body, which rarely starts a line with one.
var fieldPattern = regexp.MustCompile(
	`^\s*(?:(?:public|private|protected|static|final|transient|volatile)\s+)+` +
		`([A-Za-z_$][\w.$]*(?:<[^;]*>)?(?:\[\])*)\s+([A-Za-z_$]\w*)\s*(?:=.*)?;\s*$`)

// annotationLinePattern matches a line made up of nothing but one or more annotations, bare or
// with a single (non-nested) argument list - the shape a validation annotation actually takes above
// a field. A line that mixes an annotation with other code, e.g. a block's closing brace, is
// deliberately excluded so it can't be mistaken for part of the field's annotation run.
var annotationLinePattern = regexp.MustCompile(`^(?:@[\w.]+(?:\([^()]*\))?\s*)+$`)

// annotationToken pulls the individual annotations out of a line annotationLinePattern already
// accepted, since more than one can share a line.
var annotationToken = regexp.MustCompile(`@[\w.]+(?:\([^()]*\))?`)

// ExtractFields scans Java source line by line and returns every field declaration it finds, in
// source order, each carrying whatever annotations sit directly above it. It never returns an
// error today - the return keeps the signature stable for a future check, e.g. rejecting a file
// that isn't valid UTF-8 - so callers should still handle one.
func ExtractFields(source []byte) ([]Field, error) {
	lines := strings.Split(string(source), "\n")

	var fields []Field
	for i, line := range lines {
		m := fieldPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		fields = append(fields, Field{
			Name:        m[2],
			Type:        m[1],
			Constraints: annotationsAbove(lines, i),
		})
	}
	return fields, nil
}

// annotationsAbove walks upward from the line directly above fieldLine, collecting annotations
// from consecutive annotation-only lines and skipping over blank ones, stopping as soon as it
// meets a line that is neither - typically the previous field's own declaration.
func annotationsAbove(lines []string, fieldLine int) []string {
	var found []string
	for i := fieldLine - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		if !annotationLinePattern.MatchString(trimmed) {
			break
		}
		// Prepend this line's tokens: walking upward means each earlier line found belongs before
		// what has already been collected, restoring top-to-bottom source order.
		found = append(annotationToken.FindAllString(trimmed, -1), found...)
	}
	return found
}
