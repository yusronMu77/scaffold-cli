package cmd

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"scaffold-engine-go/internal/discovery"
	"scaffold-engine-go/internal/jig"
	"scaffold-engine-go/internal/learn"
	"scaffold-engine-go/internal/render"
)

// minMatchFiles is how many files must remain, after subtracting a scaffold's own inherited
// chassis from both sides, before a shape match counts as confident - a smaller floor is
// meaningless once chassis is inherited by every leaf under a scaffold (see match_test.go in
// internal/learn and the issue #22 plan notes).
const minMatchFiles = 2

// matchResult is what tryMatchExistingTemplate/tryMatchScaffold found, if anything: a Confident
// match (invocation set, Confident true, skip learn entirely) or, failing that, the best
// Uncertain-but-not-confident candidate seen across every scaffold checked (see learn.Evaluate) -
// worth a hint to the user, but never worth skipping learn over.
type matchResult struct {
	invocation string
	confident  bool
	uncertain  bool
	score      float64
}

// tryMatchExistingTemplate looks for an already-registered template whose base (no-overlay) shape
// matches the example folder at examplePath, returning the `scaffold create` invocation that
// already covers it. It never calls a provider and never writes anything; any failure anywhere -
// an unreadable registry, a leaf that can't resolve with its own defaults - just means "no match
// found" here, the same never-hard-fail spirit `list` already applies as a browsing command, since
// this is a best-effort optimization in front of `learn`'s own, always-safe fallback.
func tryMatchExistingTemplate(root, examplePath string) matchResult {
	exampleFiles, _, err := learn.Scan(examplePath)
	if err != nil {
		return matchResult{}
	}
	exampleSig := learn.ShapeSignature(sourceFilePaths(exampleFiles))

	rootJig, err := jig.LoadRoot(filepath.Join(root, jig.FileName))
	if err != nil {
		return matchResult{}
	}

	var best matchResult
	for _, scaffold := range rootJig.ValueNames() {
		r := tryMatchScaffold(root, scaffold, exampleSig)
		if r.confident {
			return r
		}
		if r.uncertain && r.score > best.score {
			best = r
		}
	}
	return best
}

func sourceFilePaths(files []learn.SourceFile) []string {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	return paths
}

func renderFilePaths(files []render.File) []string {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	return paths
}

func cloneSignature(sig learn.Signature) learn.Signature {
	out := make(learn.Signature, len(sig))
	for k, v := range sig {
		out[k] = v
	}
	return out
}

// tryMatchScaffold checks every base-shape leaf under one scaffold, preferring the registry's
// default version first so a tie among near-identical leaves (e.g. two versions of the same thin
// library) resolves deterministically rather than depending on registry declaration order. It
// returns immediately on the first confident leaf, but otherwise keeps checking every leaf so the
// best uncertain candidate (see learn.Evaluate) can be reported instead of just the first one
// tried.
func tryMatchScaffold(root, scaffold string, exampleSig learn.Signature) matchResult {
	cases, err := enumerate(root, scaffold)
	if err != nil {
		return matchResult{}
	}
	scaffoldPath, err := discovery.ResolveScaffoldPath(root, scaffold)
	if err != nil {
		return matchResult{}
	}
	defaultVersion, _ := discovery.ResolveVersion(scaffoldPath, "")

	var base []lintCase
	for _, c := range cases {
		if c.overlayFlag == "" && c.err == nil {
			base = append(base, c)
		}
	}
	sort.SliceStable(base, func(i, j int) bool {
		return base[i].version == defaultVersion && base[j].version != defaultVersion
	})

	var chassisSig learn.Signature
	haveChassis := false

	var best matchResult
	for _, c := range base {
		args := &parsedArgs{flags: map[string]string{}, consumed: map[string]bool{}}
		for k, v := range c.selectors {
			args.flags[k] = v
		}
		if c.version != "" {
			if versionFlag, err := discovery.VersionSelectorFlag(scaffoldPath); err == nil {
				args.flags[versionFlag] = c.version
			}
		}

		p, err := resolvePlan(args, root, scaffold, c.template, "match-probe")
		if err != nil {
			// Most commonly a required variable with no default - this leaf simply can't be
			// probed with nothing but its own defaults, so it's skipped rather than matched.
			continue
		}
		files, _, _, err := renderPlan(p)
		if err != nil {
			continue
		}

		if !haveChassis {
			chassisSig = scaffoldChassisSignature(p, scaffoldPath)
			haveChassis = true
		}

		candidateSig := learn.ShapeSignature(renderFilePaths(files))
		exampleRemainder := cloneSignature(exampleSig)
		learn.Subtract(candidateSig, chassisSig)
		learn.Subtract(exampleRemainder, chassisSig)

		confident, score := learn.Evaluate(exampleRemainder, candidateSig, minMatchFiles)
		if confident {
			inv, err := formatCreateInvocation(scaffoldPath, scaffold, c, defaultVersion)
			if err != nil {
				continue
			}
			return matchResult{invocation: inv, confident: true, score: score}
		}
		if score >= learn.UncertainScoreFloor && score > best.score {
			if inv, err := formatCreateInvocation(scaffoldPath, scaffold, c, defaultVersion); err == nil {
				best = matchResult{invocation: inv, uncertain: true, score: score}
			}
		}
	}
	return best
}

// scaffoldChassisSignature renders the scaffold-root source alone (its own physical files, none of
// the version/dimension/leaf content layered on top) using the plan's already-resolved context -
// root-level variables have root-level defaults, so this is consistent for every leaf checked
// under the same scaffold and is computed only once per scaffold by the caller.
func scaffoldChassisSignature(p *plan, scaffoldPath string) learn.Signature {
	for _, s := range p.Sources {
		if s.Dir == scaffoldPath {
			files, _, err := render.RenderSource(s, p.Context)
			if err != nil {
				return learn.Signature{}
			}
			return learn.ShapeSignature(renderFilePaths(files))
		}
	}
	return learn.Signature{}
}

// formatCreateInvocation builds a real, valid `scaffold create ...` command reaching the given
// case - deliberately NOT lintCase.String(), which prints version as a bare positional token and
// an empty <template> token for a leaf version, neither of which the real CLI accepts (see
// cmd/values.go's versionIsLeaf/applyValuesFile for the actual 2-vs-3-positional rule).
func formatCreateInvocation(scaffoldPath, scaffold string, c lintCase, defaultVersion string) (string, error) {
	var b strings.Builder
	b.WriteString("scaffold create ")
	b.WriteString(scaffold)
	if c.template != "" {
		b.WriteString(" ")
		b.WriteString(c.template)
	}
	b.WriteString(" <name>")

	if c.version != "" && c.version != defaultVersion {
		versionFlag, err := discovery.VersionSelectorFlag(scaffoldPath)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, " --%s=%s", versionFlag, c.version)
	}

	keys := make([]string, 0, len(c.selectors))
	for k := range c.selectors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, " --%s=%s", k, c.selectors[k])
	}
	return b.String(), nil
}
