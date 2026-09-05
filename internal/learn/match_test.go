package learn

import "testing"

func TestShapeSignature_CountsParentDirAndExtension(t *testing.T) {
	sig := ShapeSignature([]string{
		"java/controller/OrderController.java",
		"java/controller/OrderNotFoundHandler.java",
		"java/dto/OrderRequest.java",
		"pom.xml",
	})
	want := Signature{
		{ParentDir: "controller", Ext: ".java"}: 2,
		{ParentDir: "dto", Ext: ".java"}:        1,
		{ParentDir: ".", Ext: ".xml"}:           1,
	}
	if len(sig) != len(want) {
		t.Fatalf("got %d distinct keys, want %d: %+v", len(sig), len(want), sig)
	}
	for k, n := range want {
		if sig[k] != n {
			t.Errorf("key %+v: got %d, want %d (full signature: %+v)", k, sig[k], n, sig)
		}
	}
}

func TestShapeSignature_BackslashPathsNormalized(t *testing.T) {
	a := ShapeSignature([]string{`java\controller\Foo.java`})
	b := ShapeSignature([]string{"java/controller/Foo.java"})
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected one key each, got %+v and %+v", a, b)
	}
	for k := range a {
		if b[k] != 1 {
			t.Errorf("backslash and forward-slash paths produced different keys: %+v vs %+v", a, b)
		}
	}
}

func TestSubtract_ClampsAtZeroAndRemovesEmptyKeys(t *testing.T) {
	sig := ShapeSignature([]string{"a/x.txt", "a/x.txt", "b/y.txt"})
	base := ShapeSignature([]string{"a/x.txt", "a/x.txt", "a/x.txt", "b/y.txt"})
	Subtract(sig, base)
	if len(sig) != 0 {
		t.Fatalf("expected subtraction to clamp at zero and remove the key entirely, got %+v", sig)
	}
}

func TestSubtract_LeavesRemainderWhenCandidateHasMore(t *testing.T) {
	sig := ShapeSignature([]string{"a/x.txt", "a/x.txt", "a/x.txt"})
	base := ShapeSignature([]string{"a/x.txt"})
	Subtract(sig, base)
	key := ShapeKey{ParentDir: "a", Ext: ".txt"}
	if sig[key] != 2 {
		t.Fatalf("expected 2 remaining after subtracting 1 of 3, got %+v", sig)
	}
}

// Mirrors the real registry finding: a thin single-file leaf's inherited chassis (e.g. .gitignore,
// pom.xml) must never, by itself, be mistaken for a confident match once subtracted away - the
// remainder is too thin (below minFiles) regardless of whether it happens to equal another thin
// leaf's remainder.
func TestConfident_ChassisOnlyRemainderIsNeverConfident(t *testing.T) {
	chassis := ShapeSignature([]string{".gitignore", ".editorconfig", "pom.xml"})
	example := ShapeSignature([]string{".gitignore", ".editorconfig", "pom.xml"})
	candidate := ShapeSignature([]string{".gitignore", ".editorconfig", "pom.xml"})
	Subtract(example, chassis)
	Subtract(candidate, chassis)

	if Confident(example, candidate, 2) {
		t.Fatalf("an empty post-subtraction remainder must never be confident, got example=%+v candidate=%+v",
			example, candidate)
	}
}

// A one-file distinguishing remainder (e.g. one bare .java file directly under a "java" dir, the
// shape of a minimal library leaf) is exactly the case the minFiles=2 floor exists to reject -
// too generic a signature to safely call "confident" against an unrelated one-file example.
func TestConfident_SingleFileRemainderBelowMinFilesIsRejected(t *testing.T) {
	example := ShapeSignature([]string{"java/Unrelated.java"})
	candidate := ShapeSignature([]string{"java/Whatever.java"})
	if Confident(example, candidate, 2) {
		t.Fatal("expected a single-file remainder to be rejected by the minFiles floor")
	}
}

func TestConfident_MatchesEqualShapeAtOrAboveMinFiles(t *testing.T) {
	example := ShapeSignature([]string{
		"java/HelloWorldApplication.java",
		"java/HelloWorldController.java",
	})
	candidate := ShapeSignature([]string{
		"java/ItemApplication.java",
		"java/ItemController.java",
	})
	if !Confident(example, candidate, 2) {
		t.Fatalf("expected equal (parentDir,ext) shapes at minFiles to be confident: example=%+v candidate=%+v",
			example, candidate)
	}
}

func TestConfident_RejectsDifferentShapes(t *testing.T) {
	example := ShapeSignature([]string{
		"java/controller/OrderController.java",
		"java/dto/OrderRequest.java",
	})
	candidate := ShapeSignature([]string{
		"java/HelloWorldApplication.java",
		"java/HelloWorldController.java",
	})
	if Confident(example, candidate, 2) {
		t.Fatal("expected differing (parentDir,ext) shapes to be rejected")
	}
}
