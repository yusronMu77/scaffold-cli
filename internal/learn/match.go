package learn

import (
	"path"
	"path/filepath"
	"strings"
)

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
