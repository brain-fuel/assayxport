package golang

import (
	"reflect"
	"testing"

	"goforge.dev/assayxport/internal/schema"
)

// loadCalls extracts testdata/sample and returns symbol ID -> calls per package.
func loadCalls(t *testing.T) map[string]map[string][]schema.Call {
	t.Helper()
	pkgs, err := New().Extract("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]map[string][]schema.Call, len(pkgs))
	for _, p := range pkgs {
		m := make(map[string][]schema.Call, len(p.Symbols))
		for _, s := range p.Symbols {
			m[s.ID] = s.Calls
		}
		out[p.ID] = m
	}
	return out
}

func wantEdge(t *testing.T, calls []schema.Call, want schema.Call) {
	t.Helper()
	for _, c := range calls {
		if reflect.DeepEqual(c, want) {
			return
		}
	}
	t.Errorf("edge %+v not found in %+v", want, calls)
}

func TestCallsCrossPackageInternal(t *testing.T) {
	calls := loadCalls(t)["example.com/sample/cmd/tool"]["main"]
	wantEdge(t, calls, schema.Call{
		Target: "example.com/sample/calc.Add", Kind: "internal",
		Ref: "example.com/sample/calc#Add", Count: 1,
	})
	wantEdge(t, calls, schema.Call{
		Target: "example.com/sample/calc.Accumulator.Push", Kind: "internal",
		Ref: "example.com/sample/calc#Accumulator.Push", Count: 1,
	})
	// Same-package fan-out, two sites merged into one counted edge.
	wantEdge(t, calls, schema.Call{
		Target: "example.com/sample/cmd/tool.report", Kind: "internal",
		Ref: "example.com/sample/cmd/tool#report", Count: 2,
	})
	wantEdge(t, calls, schema.Call{Target: "len", Kind: "builtin", Count: 1})
}

func TestCallsExternal(t *testing.T) {
	calls := loadCalls(t)["example.com/sample/cmd/tool"]["report"]
	wantEdge(t, calls, schema.Call{Target: "fmt.Println", Kind: "external", Count: 1})
}

func TestCallsInterfaceDispatchIsDynamicWithRef(t *testing.T) {
	calls := loadCalls(t)["example.com/sample/cmd/tool"]["dispatch"]
	wantEdge(t, calls, schema.Call{
		Target: "example.com/sample/calc.Adder.Add", Kind: "dynamic",
		Ref: "example.com/sample/calc#Adder.Add", Count: 1,
	})
}

func TestCallsFuncValueIsDynamic(t *testing.T) {
	calls := loadCalls(t)["example.com/sample/cmd/tool"]["apply"]
	wantEdge(t, calls, schema.Call{Target: "f", Kind: "dynamic", Count: 1})
}

func TestCallsConversionIsNotACall(t *testing.T) {
	if calls := loadCalls(t)["example.com/sample/cmd/tool"]["convert"]; calls != nil {
		t.Fatalf("convert has edges %+v, want none (conversion is not a call)", calls)
	}
}

func TestCallsInsideClosureAttributedToEnclosing(t *testing.T) {
	calls := loadCalls(t)["example.com/sample/cmd/tool"]["deferred"]
	wantEdge(t, calls, schema.Call{Target: "fmt.Println", Kind: "external", Count: 1})
}

func TestCallsRecursionIsSelfEdge(t *testing.T) {
	calls := loadCalls(t)["example.com/sample/cmd/tool"]["Recur"]
	wantEdge(t, calls, schema.Call{
		Target: "example.com/sample/cmd/tool.Recur", Kind: "internal",
		Ref: "example.com/sample/cmd/tool#Recur", Count: 1,
	})
}

func TestCallsBuiltinsInLoop(t *testing.T) {
	calls := loadCalls(t)["example.com/sample/cmd/tool"]["Collect"]
	wantEdge(t, calls, schema.Call{Target: "append", Kind: "builtin", Count: 1})
	wantEdge(t, calls, schema.Call{Target: "make", Kind: "builtin", Count: 1})
}

func TestCallsNoBodyNoEdges(t *testing.T) {
	// An interface method declaration has no body, so no calls field at all.
	if calls := loadCalls(t)["example.com/sample/calc"]["Adder.Add"]; calls != nil {
		t.Fatalf("interface method has edges %+v, want none", calls)
	}
}
