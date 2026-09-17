package learn

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"scaffold-engine-go/internal/jig"
	"scaffold-engine-go/internal/render"
)

// diffContextLines is how many lines of context are shown around a content mismatch, from each
// side - enough to localize the problem without pulling in a diff library.
const diffContextLines = 2

// ReviewResult is the outcome of comparing a draft's own render - using only the `default:`
// values it declares - against the example folder `scaffold learn` originally scanned. A correct
// draft's own defaults must reproduce that example exactly (see the systemPrompt's contract in
// prompt.go: "a variable's default must be the literal value found in the example"), so any
// difference here is a concrete, mechanically-detected sign that the draft over- or
// under-generalized - no further AI call needed.
type ReviewResult struct {
	// Missing lists paths present in the example but absent from the draft's own render.
	Missing []string
	// Extra lists paths the draft's render produces that the example never had.
	Extra []string
	// Mismatched lists paths present on both sides whose content differs.
	Mismatched []ContentDiff
	// ExtraExamples holds one entry per extra example directory (2nd and later, in a multi-example
	// learn-review call) that has any structural mismatch against the draft's render - empty for
	// an ordinary single-example call, and omitted entirely for an extra example whose file set
	// matches exactly.
	ExtraExamples []ExampleStructureResult
}

// ContentDiff localizes one content mismatch: the first line the two sides disagree on, plus a
// little context from each side - not a full diff, just enough to point a human or an agent at
// the problem.
type ContentDiff struct {
	Path     string
	Line     int
	Example  []string
	Rendered []string
	// LineEndingOnly is true when the two sides are identical once every carriage return is
	// stripped - a `\r` doesn't render visibly in a terminal, so without this flag the printed
	// Example/Rendered blocks look byte-identical and leave the reader to guess (issue #55).
	LineEndingOnly bool
}

// ExampleStructureResult is a structural-only (file-existence) comparison of one additional
// example directory against the draft's own render, for a multi-example learn-review call. Only
// example-1 is checked byte-for-byte (see Review): a variable's default is drawn from example-1
// specifically (multiExampleAddendum in prompt.go), so a later example may legitimately differ in
// content, but should still exist under the same set of paths.
type ExampleStructureResult struct {
	Dir string
	// Missing lists paths present in this example but absent from the draft's own render.
	Missing []string
	// Extra lists paths the draft's render produces that this example never had.
	Extra []string
}

// Clean reports whether the review found nothing to flag.
func (r *ReviewResult) Clean() bool {
	return len(r.Missing) == 0 && len(r.Extra) == 0 && len(r.Mismatched) == 0 && len(r.ExtraExamples) == 0
}

// IssueCount totals every kind of finding, for a one-line summary.
func (r *ReviewResult) IssueCount() int {
	n := len(r.Missing) + len(r.Extra) + len(r.Mismatched)
	for _, e := range r.ExtraExamples {
		n += len(e.Missing) + len(e.Extra)
	}
	return n
}

// Review renders the draft jig.yaml at draftDir using only its own declared defaults - the same
// render.RenderSource path `create` uses - then compares the result against exampleDir, re-scanned
// with the same Scan a `learn` run used originally so credential/binary/symlink handling matches
// exactly. It makes no network call and reasons about nothing beyond the draft and the example
// folder(s) already on disk.
//
// extraExampleDirs are the 2nd and later example directories from a multi-example `learn` call
// (see cmd.runLearnWithClientMultiExample). Only exampleDir (example-1) is checked byte-for-byte:
// a variable's default is always drawn from example-1 specifically (multiExampleAddendum in
// prompt.go), so a later example may legitimately differ in content. Each extra dir is instead
// checked structurally only - does it have the same set of files as the draft's render - recorded
// on ReviewResult.ExtraExamples.
func Review(draftDir, exampleDir string, extraExampleDirs ...string) (*ReviewResult, error) {
	leafDir := DraftLeafDir(draftDir)
	jigPath := DraftLeafJigPath(draftDir)
	m, err := jig.Load(jigPath)
	if err != nil {
		return nil, err
	}

	// A `redacted: true` variable has no default by design (jig.Validate rejects one that does) -
	// its real value was never available even to the model, so it can never resolve from "its own
	// defaults" the way every other variable does. Supplying a fixed probe value for exactly these
	// lets ResolveVariables succeed instead of hard-failing on "missing required variable"; an
	// ordinary hand-edited required-no-default variable unrelated to redaction still correctly
	// fails below, same as before this existed.
	probeFlags := map[string]string{}
	for _, v := range m.Variables {
		if v.Redacted {
			probeFlags[render.VariableFlagName(v)] = RedactionProbeValue
		}
	}

	// `.Name` is the CLI's own <name> positional, supplied at `create` time and never declared as a
	// jig variable with a `default:` of its own - review has no such positional to draw from, so it
	// stands in the example directory's own basename instead, the least-surprising value a draft
	// referencing `.Name` in a path could reproduce byte-for-byte (issue #49).
	name := filepath.Base(exampleDir)
	base := render.EngineFacts(name, "", "", "", nil, nil)
	vars, err := render.ResolveVariables([]*jig.Jig{m}, render.VariableSource{
		Flags: probeFlags, Base: base,
	})
	if err != nil {
		return nil, fmt.Errorf("resolving %s's own defaults: %w", jigPath, err)
	}
	ctx := render.BuildContext(vars, nil, name, "", "", "", nil, nil)
	if err := render.ApplyComputed(ctx, []*jig.Jig{m}); err != nil {
		return nil, err
	}
	files, _, err := render.RenderSource(render.Source{Dir: leafDir, Manifest: m}, ctx)
	if err != nil {
		return nil, fmt.Errorf("rendering %s with its own defaults: %w", leafDir, err)
	}

	sourceFiles, _, err := Scan(exampleDir)
	if err != nil {
		return nil, err
	}
	// Re-derive the example's own redacted shape - the same deterministic pass `learn` used
	// originally - rather than comparing against the raw example: the draft never saw the real
	// secret either, so comparing against it would always "mismatch" at that position. Numbered
	// placeholders are then collapsed to one fixed marker, matching probeFlags above, so the
	// comparison doesn't need to know which number corresponds to which declared variable - it
	// only needs both sides to agree "something redacted belongs here", while still catching a
	// genuine over/under-generalization anywhere else in the file.
	redactedExample, _ := RedactSecrets(sourceFiles)

	rendered := make(map[string]string, len(files))
	for _, f := range files {
		rendered[f.Path] = string(f.Content)
	}
	example := make(map[string]string, len(redactedExample))
	for _, f := range redactedExample {
		example[f.Path] = redactionPlaceholderPattern.ReplaceAllString(f.Content, RedactionProbeValue)
	}

	result := &ReviewResult{}
	for p := range example {
		if _, ok := rendered[p]; !ok {
			result.Missing = append(result.Missing, p)
		}
	}
	for p := range rendered {
		if _, ok := example[p]; !ok {
			result.Extra = append(result.Extra, p)
		}
	}
	sort.Strings(result.Missing)
	sort.Strings(result.Extra)

	var common []string
	for p := range example {
		if _, ok := rendered[p]; ok {
			common = append(common, p)
		}
	}
	sort.Strings(common)
	for _, p := range common {
		if example[p] == rendered[p] {
			continue
		}
		result.Mismatched = append(result.Mismatched, buildContentDiff(p, example[p], rendered[p]))
	}

	for _, dir := range extraExampleDirs {
		structResult, err := reviewExampleStructure(dir, rendered)
		if err != nil {
			return nil, err
		}
		if structResult != nil {
			result.ExtraExamples = append(result.ExtraExamples, *structResult)
		}
	}
	return result, nil
}

// reviewExampleStructure compares one extra example directory's file set against rendered (the
// draft's own render), returning nil when they match exactly - content is deliberately not
// compared here, see Review's doc comment on extraExampleDirs.
func reviewExampleStructure(dir string, rendered map[string]string) (*ExampleStructureResult, error) {
	sourceFiles, _, err := Scan(dir)
	if err != nil {
		return nil, err
	}
	examplePaths := make(map[string]bool, len(sourceFiles))
	for _, f := range sourceFiles {
		examplePaths[f.Path] = true
	}

	var missing, extra []string
	for p := range examplePaths {
		if _, ok := rendered[p]; !ok {
			missing = append(missing, p)
		}
	}
	for p := range rendered {
		if !examplePaths[p] {
			extra = append(extra, p)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return nil, nil
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return &ExampleStructureResult{Dir: dir, Missing: missing, Extra: extra}, nil
}

// buildContentDiff finds the first line the two sides disagree on and returns a few lines of
// context around it from each side.
func buildContentDiff(path, example, rendered string) ContentDiff {
	exLines := strings.Split(example, "\n")
	reLines := strings.Split(rendered, "\n")

	line := 0
	for line < len(exLines) && line < len(reLines) && exLines[line] == reLines[line] {
		line++
	}

	return ContentDiff{
		Path:           path,
		Line:           line + 1,
		Example:        contextAround(exLines, line),
		Rendered:       contextAround(reLines, line),
		LineEndingOnly: stripCR(example) == stripCR(rendered),
	}
}

// stripCR removes every carriage return - the one byte distinguishing CRLF from LF - so two sides
// that are otherwise identical can be told apart from a genuine content mismatch.
func stripCR(s string) string {
	return strings.ReplaceAll(s, "\r", "")
}

// contextAround returns up to diffContextLines lines before and after index at, clamped to the
// slice's bounds.
func contextAround(lines []string, at int) []string {
	start := at - diffContextLines
	if start < 0 {
		start = 0
	}
	end := at + diffContextLines + 1
	if end > len(lines) {
		end = len(lines)
	}
	if start >= end {
		return nil
	}
	return lines[start:end]
}
