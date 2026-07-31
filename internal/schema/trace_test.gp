package schema

import (
	"fmt"
	"reflect"
	"testing"
	"testing/quick"

	"goforge.dev/goplus/std/result"
)

func ExampleArtifactURL() {
	fmt.Println(ArtifactURL(
		"goforge.dev/gotp",
		"v1.0.0",
		CodeArtifact(),
		"vm.interpreter.dispatch",
	))
	// Output:
	// https://goforge.dev/gotp/v1.0.0/artifacts/code/vm.interpreter.dispatch
}

func TestTraceReleaseClosure(t *testing.T) {
	oldDigest := DigestText("old")
	newDigest := DigestText("new")
	decision := TraceArtifact{ID: "adr.runtime", Kind: "adr", Module: "goforge.dev/gotp", ContentDigest: DigestText("decision")}
	baseline := TraceBundle{
		SchemaVersion: TraceSchema,
		Module: "goforge.dev/gotp",
		Artifacts: []TraceArtifact{
			decision,
			{ID: "vm.dispatch", Kind: "code", Module: "goforge.dev/gotp", ContentDigest: oldDigest, SemanticDigest: oldDigest, Deployable: true},
		},
		Relations: []TraceRelation{{From: "adr.runtime", Kind: "decides", To: "vm.dispatch"}},
	}
	current := TraceBundle{
		SchemaVersion: TraceSchema,
		Module: "goforge.dev/gotp",
		Artifacts: []TraceArtifact{
			decision,
			{ID: "release.v0.2.0", Kind: "release-note", Module: "goforge.dev/gotp", ContentDigest: DigestText("release")},
			{ID: "vm.dispatch", Kind: "code", Module: "goforge.dev/gotp", ContentDigest: newDigest, SemanticDigest: newDigest, Deployable: true},
		},
		Relations: []TraceRelation{
			{From: "adr.runtime", Kind: "decides", To: "vm.dispatch"},
			{From: "release.v0.2.0", Kind: "announces", To: "adr.runtime"},
		},
	}
	match VerifyRelease(baseline, current, "release.v0.2.0") {
	case result.Err(failures):
		t.Fatalf("VerifyRelease failed: %v", failures)
	case result.Ok(report):
		if !reflect.DeepEqual(report.Changed, []string{"vm.dispatch"}) {
			t.Fatalf("changed = %v", report.Changed)
		}
	}
}

func TestCanonicalTraceIdempotent(t *testing.T) {
	property := func(left string, right string) bool {
		bundle := TraceBundle{
			Artifacts: []TraceArtifact{{ID: right}, {ID: left}},
			Relations: []TraceRelation{{From: right, Kind: "tests", To: left}},
		}
		once := CanonicalTrace(bundle)
		twice := CanonicalTrace(once)
		return reflect.DeepEqual(once, twice)
	}
	if failure := quick.Check(property, nil); failure != nil { t.Error(failure) }
}
