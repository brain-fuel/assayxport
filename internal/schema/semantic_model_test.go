package schema

import (
	"testing"

	"goforge.dev/goplus/std/result"
)

func mustRef(t *testing.T, text string) SymbolRef {
	t.Helper()
	ref, failure := result.Unpack(ParseSymbolRef(text))
	if failure != nil {
		t.Fatalf("ParseSymbolRef(%q): %#v", text, failure)
	}
	return ref
}

func TestCallResolutionRequiresInternalRef(t *testing.T) {
	if _, ok := CallResolutionOf(Call{Kind: "internal"}).(result.Err[CallResolution, SemanticFailure]); !ok {
		t.Fatal("internal call without ref was accepted")
	}
	r := CallResolutionOf(Call{Kind: "internal", Ref: "pkg#symbol"})
	value, failure := result.Unpack(r)
	if failure != nil {
		t.Fatalf("valid internal call: %#v", failure)
	}
	if _, ok := value.(CallInternal); !ok {
		t.Fatalf("got %T", value)
	}
}

func TestOverloadResolutionUsesStatedEvidence(t *testing.T) {
	intRef := mustRef(t, "pkg#f-int")
	strRef := mustRef(t, "pkg#f-string")
	candidates := []OverloadCandidate{
		{Target: intRef, Params: []string{"int"}},
		{Target: strRef, Params: []string{"string"}},
	}
	got := ResolveOverload(candidates, Arity(1), []TypeEvidence{EvidenceStated{TypeName: "string"}})
	exact, ok := got.(ResolutionExact)
	if !ok || SymbolRefString(exact.Target) != "pkg#f-string" {
		t.Fatalf("resolution = %#v", got)
	}
	if _, ok := ResolveOverload(candidates, Arity(1), []TypeEvidence{EvidenceUnknown{}}).(ResolutionAmbiguous); !ok {
		t.Fatal("unknown evidence should preserve honest ambiguity")
	}
}

func TestSemanticCodecsRejectUnknownValues(t *testing.T) {
	checks := []bool{
		result.IsErr(LanguageOf("rust")),
		result.IsErr(SymbolKindOf("macro")),
		result.IsErr(VisibilityOf("friend")),
		result.IsErr(ConcernKindOf("mystery")),
	}
	for i, failed := range checks {
		if !failed {
			t.Fatalf("codec %d accepted unknown value", i)
		}
	}
}

func TestComplexityMeaningEnforcesPayload(t *testing.T) {
	if !result.IsErr(ComplexityMeaning(Complexity{Method: "loop-nesting"})) {
		t.Fatal("loop-nesting without bounds was accepted")
	}
	time, space := "O(n^3)", "O(1)"
	meaning, failure := result.Unpack(ComplexityMeaning(Complexity{Method: "loop-nesting", Time: &time, Space: &space}))
	if failure != nil {
		t.Fatalf("valid complexity: %#v", failure)
	}
	if _, ok := meaning.(ComplexityLoopNesting); !ok {
		t.Fatalf("meaning = %T", meaning)
	}
}

func TestOpaqueIdentitiesAndUnits(t *testing.T) {
	for _, invalid := range []string{"", "pkg#part"} {
		if !result.IsErr(NewPackageID(invalid)) {
			t.Fatalf("accepted package id %q", invalid)
		}
	}
	for _, invalid := range []string{"/abs.go", "a/../b.go", `a\b.go`} {
		if !result.IsErr(NewRelativeSourcePath(invalid)) {
			t.Fatalf("accepted source path %q", invalid)
		}
	}
	if !result.IsErr(PositiveSourceLine(0)) || !result.IsErr(NonnegativeArity(-1)) || !result.IsErr(PositiveWorkerCount(0)) {
		t.Fatal("accepted an out-of-range semantic unit")
	}
}

func TestCallCanonicalPreservesAggregatedCounts(t *testing.T) {
	raw := []Call{{Target: "f", Kind: "builtin", Count: 2}, {Target: "f", Kind: "builtin", Count: 3}}
	once := DedupeCalls(raw)
	twice := DedupeCalls(once)
	if len(once) != 1 || once[0].Count != 5 || twice[0].Count != 5 {
		t.Fatalf("canonical counts: once=%v twice=%v", once, twice)
	}
}
