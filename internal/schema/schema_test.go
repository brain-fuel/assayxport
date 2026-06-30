package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionConstant(t *testing.T) {
	if Version != "1" {
		t.Fatalf("Version = %q, want \"1\"", Version)
	}
}

func TestDeferredComplexity(t *testing.T) {
	c := DeferredComplexity()
	if c.Time != nil || c.Space != nil || c.Method != "deferred" {
		t.Fatalf("DeferredComplexity() = %+v, want nulls + deferred", c)
	}
}

func TestSymbolOmitsSignatureForNonCallable(t *testing.T) {
	s := Symbol{
		ID: "MaxInt", Name: "MaxInt", Kind: "const", Visibility: "exported",
		VisibilityIdiom: "capitalized",
		Location:        Location{File: "x.go", Line: 1, Col: 1, EndLine: 1},
		Doc:             Doc{Raw: "", Format: "godoc"},
		Complexity:      DeferredComplexity(),
		Type:            "int",
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "\"signature\"") {
		t.Fatalf("const symbol must omit signature, got %s", b)
	}
	if !strings.Contains(string(b), "\"type\":\"int\"") {
		t.Fatalf("const symbol must carry type, got %s", b)
	}
}

func TestSymbolIncludesSignatureForFunc(t *testing.T) {
	s := Symbol{
		ID: "Add", Name: "Add", Kind: "func", Visibility: "exported",
		VisibilityIdiom: "capitalized",
		Location:        Location{File: "x.go", Line: 2, Col: 1, EndLine: 4},
		Doc:             Doc{Raw: "Add adds.", Format: "godoc"},
		Complexity:      DeferredComplexity(),
		Signature: &Signature{
			Params:  []Param{{Name: "a", Type: "int"}, {Name: "b", Type: "int"}},
			Returns: []Param{{Name: "", Type: "int"}},
		},
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "\"signature\"") {
		t.Fatalf("func symbol must include signature, got %s", b)
	}
}
