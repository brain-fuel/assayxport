package extract

import (
	"errors"
	"testing"

	"goforge.dev/assayxport/internal/schema"
)

func TestProgressivePackageTransitions(t *testing.T) {
	skeleton := schema.Package{ID: "pkg", Language: "go"}
	discovered := Discover(skeleton)
	scheduled := Schedule(discovered)
	active := BeginExtraction(scheduled)
	full := skeleton
	full.Symbols = []schema.Symbol{{ID: "F"}}
	ready := FinishExtraction(active, full)
	if got := ReadyValue(ready); len(got.Symbols) != 1 {
		t.Fatalf("ready package = %#v", got)
	}

	failed := FailExtraction(BeginExtraction(Schedule(Discover(skeleton))), errors.New("parse"))
	if _, ok := failed.(FailedPackage); !ok {
		t.Fatalf("failure transition = %T", failed)
	}
}
