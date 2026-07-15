package schema

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestVersionConstant(t *testing.T) {
	if Version != "2" {
		t.Fatalf("Version = %q, want \"2\"", Version)
	}
}

func TestDedupeCalls(t *testing.T) {
	got := DedupeCalls([]Call{
		{Target: "fmt.Println", Kind: "external"},
		{Target: "len", Kind: "builtin"},
		{Target: "fmt.Println", Kind: "external"},
		{Target: "example.com/m/calc.Add", Kind: "internal", Ref: "example.com/m/calc#Add"},
	})
	want := []Call{
		{Target: "example.com/m/calc.Add", Kind: "internal", Ref: "example.com/m/calc#Add", Count: 1},
		{Target: "fmt.Println", Kind: "external", Count: 2},
		{Target: "len", Kind: "builtin", Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DedupeCalls = %+v, want %+v", got, want)
	}
}

// intp/strp build the pointer-valued evidence fields in tests.
func intp(n int) *int       { return &n }
func strp(s string) *string { return &s }

func TestDedupeCallsVectorKeyed(t *testing.T) {
	// Same target and arity, different evidence: the sites must NOT merge —
	// they may resolve to different overloads.
	got := DedupeCalls([]Call{
		{Target: "Widget.grow", Kind: "internal", Ref: "d#Widget.grow", Arity: intp(1), ArgTypes: []*string{strp("int")}},
		{Target: "Widget.grow", Kind: "internal", Ref: "d#Widget.grow", Arity: intp(1)},
		{Target: "Widget.grow", Kind: "internal", Ref: "d#Widget.grow", Arity: intp(1), ArgTypes: []*string{strp("int")}},
	})
	want := []Call{
		{Target: "Widget.grow", Kind: "internal", Ref: "d#Widget.grow", Arity: intp(1), Count: 1},
		{Target: "Widget.grow", Kind: "internal", Ref: "d#Widget.grow", Arity: intp(1), ArgTypes: []*string{strp("int")}, Count: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DedupeCalls = %+v, want %+v", got, want)
	}
}

func TestDedupeCallsOrderIsDeterministic(t *testing.T) {
	// Unknown-position vectors sort before known at the same position, and
	// absent arity sorts before present.
	got := DedupeCalls([]Call{
		{Target: "f", Kind: "internal", Arity: intp(1), ArgTypes: []*string{strp("int")}},
		{Target: "f", Kind: "internal", Arity: intp(1), ArgTypes: []*string{nil}},
		{Target: "f", Kind: "internal"},
	})
	if got[0].Arity != nil || got[1].ArgTypes[0] != nil || got[2].ArgTypes[0] == nil {
		t.Fatalf("unexpected order: %+v", got)
	}
}

func TestCallJSONShape(t *testing.T) {
	b, err := json.Marshal(Call{
		Target: "Widget.grow", Kind: "internal", Ref: "d#Widget.grow",
		Arity: intp(3), ArgTypes: []*string{strp("String"), nil, strp("int")}, Count: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"target":"Widget.grow","kind":"internal","ref":"d#Widget.grow","arity":3,"arg_types":["String",null,"int"],"count":1}`
	if string(b) != want {
		t.Fatalf("json = %s, want %s", b, want)
	}
	// Evidence-free edges must omit both fields.
	b, err = json.Marshal(Call{Target: "len", Kind: "builtin", Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "arity") || strings.Contains(string(b), "arg_types") {
		t.Fatalf("evidence fields not omitted: %s", b)
	}
}

func TestDedupeCallsEmpty(t *testing.T) {
	if got := DedupeCalls(nil); got != nil {
		t.Fatalf("DedupeCalls(nil) = %+v, want nil (field must be omitted)", got)
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
