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

func TestScore_IdenticalSignaturesScoreOne(t *testing.T) {
	sig := ShapeSignature([]string{"java/controller/Foo.java", "java/dto/Bar.java"})
	if got := Score(sig, sig); got != 1.0 {
		t.Fatalf("expected identical signatures to score 1.0, got %v", got)
	}
}

func TestScore_DisjointSignaturesScoreZero(t *testing.T) {
	example := ShapeSignature([]string{"java/controller/Foo.java"})
	candidate := ShapeSignature([]string{"python/models/bar.py"})
	if got := Score(example, candidate); got != 0.0 {
		t.Fatalf("expected disjoint signatures to score 0.0, got %v", got)
	}
}

func TestScore_PartialOverlapScoresBetweenZeroAndOne(t *testing.T) {
	// 3 shared (controller,.java) + 1 example-only (dto,.java) + 1 candidate-only (model,.java):
	// intersection = 3, union = 3+1+1 = 5, so 3/5 = 0.6.
	example := ShapeSignature([]string{
		"java/controller/A.java", "java/controller/B.java", "java/controller/C.java",
		"java/dto/D.java",
	})
	candidate := ShapeSignature([]string{
		"java/controller/A.java", "java/controller/B.java", "java/controller/C.java",
		"java/model/E.java",
	})
	got := Score(example, candidate)
	if got <= 0.0 || got >= 1.0 {
		t.Fatalf("expected a partial overlap to score strictly between 0 and 1, got %v", got)
	}
	if want := 0.6; got != want {
		t.Fatalf("expected Jaccard score %v, got %v", want, got)
	}
}

func TestScore_BothEmptyScoresOne(t *testing.T) {
	if got := Score(Signature{}, Signature{}); got != 1.0 {
		t.Fatalf("expected two empty signatures to score 1.0, got %v", got)
	}
}

// Evaluate must apply the same minFiles floor to the uncertain score it does to confidence - a
// thin one-file remainder must never be flagged as even an uncertain match, matching Confident's
// own chassis-only rejection above.
func TestEvaluate_ThinRemainderNeverUncertainEitherSideOfFloor(t *testing.T) {
	example := ShapeSignature([]string{"java/Unrelated.java"})
	candidate := ShapeSignature([]string{"java/Whatever.java"})
	confident, score := Evaluate(example, candidate, 2)
	if confident {
		t.Fatal("expected a single-file remainder to never be confident")
	}
	if score != 0 {
		t.Fatalf("expected a single-file remainder below minFiles to score 0 (not flagged as uncertain either), got %v", score)
	}
}

func TestEvaluate_ConfidentMatchScoresOne(t *testing.T) {
	example := ShapeSignature([]string{"java/A.java", "java/B.java"})
	candidate := ShapeSignature([]string{"java/X.java", "java/Y.java"})
	confident, score := Evaluate(example, candidate, 2)
	if !confident || score != 1.0 {
		t.Fatalf("expected a confident match to report confident=true, score=1.0, got confident=%v score=%v", confident, score)
	}
}

func TestEvaluate_PartialOverlapAboveMinFilesIsUncertainNotConfident(t *testing.T) {
	example := ShapeSignature([]string{"java/controller/A.java", "java/controller/B.java", "java/dto/C.java"})
	candidate := ShapeSignature([]string{"java/controller/X.java", "java/controller/Y.java", "java/model/Z.java"})
	confident, score := Evaluate(example, candidate, 2)
	if confident {
		t.Fatal("expected a differing shape to never be confident")
	}
	if score <= 0 || score >= 1.0 {
		t.Fatalf("expected a graded uncertain score strictly between 0 and 1, got %v", score)
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
