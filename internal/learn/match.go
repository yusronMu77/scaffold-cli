package learn

import (
	"path"
	"path/filepath"
	"strings"
)

// UncertainScoreFloor is the minimum Score a non-confident candidate needs to clear before it's
// worth surfacing as "similar but not confident" rather than silently ignored - tuned so a
// coincidental one-or-two-key overlap between unrelated leaves doesn't get flagged as a near-miss.
const UncertainScoreFloor = 0.5

// ShapeKey is a file's structural fingerprint for template-matching purposes: deliberately
// ignoring its exact name/casing and full path depth (a Java package path's dot-count carries no
// fixed shape - a template's own default package and a real project's package legitimately have
// different depths) and keeping only where it sits (its immediate parent directory name) and what
// kind of file it is.
//
// Known limitation, confirmed against the real scaffold-templates registry: a leaf whose files
// sit directly inside the package-path directory with no literal role subdirectory of their own
// (e.g. spring-boot's "hello-world" - Application/Controller sitting straight in
// src/main/java/{{ .PackagePath }}/) gets a ParentDir equal to the LAST SEGMENT OF THE DEFAULT
// PACKAGE (e.g. "app" from "com.company.app"), which essentially never coincides with a real
// project's own, different package - so this leaf will rarely be offered as a match even for a
// genuinely identical example. A leaf with a literal role directory (controller/, dto/, model/,
// ...) is unaffected, since that segment is fixed text regardless of package. This is a false
// negative (falls through to `learn` exactly as if nothing matched), never a false positive, so it
// was accepted for v1 rather than chasing a role-name vocabulary or content-based confirmation.
type ShapeKey struct {
	ParentDir string
	Ext       string
}

// Signature is a multiset of ShapeKeys - the count of each (parent directory, extension) pair
// present in a set of files.
type Signature map[ShapeKey]int

// ShapeSignature builds a Signature from a set of relative paths. Paths are compared with "/"
// separators throughout, matching every other path-handling convention in this engine.
func ShapeSignature(paths []string) Signature {
	sig := make(Signature, len(paths))
	for _, p := range paths {
		clean := path.Clean(filepath.ToSlash(p))
		dir := path.Dir(clean)
		parent := "."
		if dir != "." {
			parent = path.Base(dir)
		}
		key := ShapeKey{ParentDir: parent, Ext: strings.ToLower(path.Ext(clean))}
		sig[key]++
	}
	return sig
}

// Subtract removes up to base's count of each key from sig, clamped at zero. Used to strip a
// scaffold's own inherited chassis files (declared once at the scaffold root, present in every
// leaf underneath) out of both sides of a comparison before judging confidence - otherwise chassis
// alone satisfies any minimum file count, on every leaf, regardless of whether the leaf's own
// distinguishing content matches anything.
func Subtract(sig, base Signature) {
	for key, n := range base {
		if have, ok := sig[key]; ok {
			if have <= n {
				delete(sig, key)
			} else {
				sig[key] = have - n
			}
		}
	}
}

// count totals every remaining file in a signature, for the minimum-file-count gate.
func count(sig Signature) int {
	total := 0
	for _, n := range sig {
		total += n
	}
	return total
}

// Confident reports whether two post-subtraction signatures are equal multisets AND the candidate
// side has at least minFiles remaining. Both conditions matter: a chassis-only leaf's remainder is
// often 0-1 files and must never count as confident regardless of how well it happens to match: an
// empty signature trivially equals another empty signature.
func Confident(example, candidate Signature, minFiles int) bool {
	if count(candidate) < minFiles {
		return false
	}
	if len(example) != len(candidate) {
		return false
	}
	for key, n := range candidate {
		if example[key] != n {
			return false
		}
	}
	return true
}

// Score reports the Jaccard similarity of two signatures as multisets - |intersection| / |union|,
// counting each key's overlap by the smaller of its two counts - in [0,1]. 1.0 means the same
// shapes as Confident would accept (modulo the minFiles floor, which Score doesn't apply); 0.0
// means no shared (parentDir, ext) key at all. This is the graded signal behind the "uncertain"
// match band: a candidate that Confident rejects but Score rates highly is worth surfacing to a
// human rather than silently falling through to a full `learn` call.
func Score(example, candidate Signature) float64 {
	keys := make(map[ShapeKey]bool, len(example)+len(candidate))
	for key := range example {
		keys[key] = true
	}
	for key := range candidate {
		keys[key] = true
	}
	if len(keys) == 0 {
		return 1.0
	}
	var intersection, union int
	for key := range keys {
		e, c := example[key], candidate[key]
		if e < c {
			intersection += e
			union += c
		} else {
			intersection += c
			union += e
		}
	}
	if union == 0 {
		return 0.0
	}
	return float64(intersection) / float64(union)
}

// Evaluate reports whether candidate confidently matches example (same rule as Confident) and,
// when it doesn't, a graded uncertainScore for how close it came. Both share the same minFiles
// floor Confident applies: a candidate too thin to ever be confident (a chassis-only or
// single-file remainder) is never flagged as an uncertain match either, since a coincidental
// one-file shape overlap is common and not a meaningful signal on its own.
func Evaluate(example, candidate Signature, minFiles int) (confident bool, uncertainScore float64) {
	if count(candidate) < minFiles {
		return false, 0
	}
	if Confident(example, candidate, minFiles) {
		return true, 1.0
	}
	return false, Score(example, candidate)
}
