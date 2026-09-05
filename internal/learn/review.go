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
}

// ContentDiff localizes one content mismatch: the first line the two sides disagree on, plus a
// little context from each side - not a full diff, just enough to point a human or an agent at
// the problem.
type ContentDiff struct {
	Path     string
	Line     int
	Example  []string
	Rendered []string
}

// Clean reports whether the review found nothing to flag.
func (r *ReviewResult) Clean() bool {
	return len(r.Missing) == 0 && len(r.Extra) == 0 && len(r.Mismatched) == 0
}

// IssueCount totals every kind of finding, for a one-line summary.
func (r *ReviewResult) IssueCount() int {
	return len(r.Missing) + len(r.Extra) + len(r.Mismatched)
}

// Review renders the draft jig.yaml at draftDir using only its own declared defaults - the same
// render.RenderSource path `create` uses - then compares the result against exampleDir, re-scanned
// with the same Scan a `learn` run used originally so credential/binary/symlink handling matches
// exactly. It makes no network call and reasons about nothing beyond the draft and the example
// folder already on disk.
func Review(draftDir, exampleDir string) (*ReviewResult, error) {
	jigPath := filepath.Join(draftDir, jig.FileName)
	m, err := jig.Load(jigPath)
	if err != nil {
		return nil, err
	}

	base := render.EngineFacts("", "", "", "", nil, nil)
	vars, err := render.ResolveVariables([]*jig.Jig{m}, render.VariableSource{
		Flags: map[string]string{}, Base: base,
	})
	if err != nil {
		return nil, fmt.Errorf("resolving %s's own defaults: %w", jigPath, err)
	}
	ctx := render.BuildContext(vars, nil, "", "", "", "", nil, nil)
	if err := render.ApplyComputed(ctx, []*jig.Jig{m}); err != nil {
		return nil, err
	}
	files, _, err := render.RenderSource(render.Source{Dir: draftDir, Manifest: m}, ctx)
	if err != nil {
		return nil, fmt.Errorf("rendering %s with its own defaults: %w", draftDir, err)
	}

	sourceFiles, _, err := Scan(exampleDir)
	if err != nil {
		return nil, err
	}

	rendered := make(map[string]string, len(files))
	for _, f := range files {
		rendered[f.Path] = string(f.Content)
	}
	example := make(map[string]string, len(sourceFiles))
	for _, f := range sourceFiles {
		example[f.Path] = f.Content
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
	return result, nil
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
		Path:     path,
		Line:     line + 1,
		Example:  contextAround(exLines, line),
		Rendered: contextAround(reLines, line),
	}
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
