package python

import (
	"reflect"
	"testing"

	"goforge.dev/assayxport/internal/schema"
)

// loadTestCalls extracts a module from source and maps symbol ID -> calls.
func loadTestCalls(t *testing.T, src string) map[string][]schema.Call {
	t.Helper()
	syms, _, _, _, err := moduleSymbols("mod.py", []byte(src), "mod")
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string][]schema.Call, len(syms))
	for _, s := range syms {
		out[s.ID] = s.Calls
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

const callsSample = `import os.path
import numpy as np
from json import dumps as jd

def helper(x):
    return jd(x)

def top(xs):
    helper(xs)          # internal: module function
    helper(xs)          # second site merges into count
    print(len(xs))      # builtin x2
    os.path.join("a")   # external via plain import
    np.array(xs)        # external via alias
    Thing(1)            # internal: constructor
    xs.strip()          # unresolved: receiver type unknown
    return (lambda v: helper(v))(xs)  # lambda body attributed to top

def sneaky(fs):
    return fs[0]()      # dynamic: callee is not a name

class Thing:
    def __init__(self, v):
        self.v = v

    def bump(self):
        self.grow()      # internal: sibling method
        self.vanish()    # unresolved: no such method
        helper(self.v)   # internal: module function from a method

    def grow(self):
        return top([self.v])
`

func TestPyCallsResolutionLadder(t *testing.T) {
	calls := loadTestCalls(t, callsSample)

	top := calls["top"]
	wantEdge(t, top, schema.Call{Target: "helper", Kind: "internal", Ref: "mod#helper", Count: 3})
	wantEdge(t, top, schema.Call{Target: "print", Kind: "builtin", Count: 1})
	wantEdge(t, top, schema.Call{Target: "len", Kind: "builtin", Count: 1})
	wantEdge(t, top, schema.Call{Target: "os.path.join", Kind: "external", Count: 1})
	wantEdge(t, top, schema.Call{Target: "numpy.array", Kind: "external", Count: 1})
	wantEdge(t, top, schema.Call{Target: "Thing", Kind: "internal", Ref: "mod#Thing", Count: 1})
	wantEdge(t, top, schema.Call{Target: "xs.strip", Kind: "unresolved", Count: 1})

	wantEdge(t, calls["helper"], schema.Call{Target: "json.dumps", Kind: "external", Count: 1})
	wantEdge(t, calls["sneaky"], schema.Call{Target: "fs[0]", Kind: "dynamic", Count: 1})
}

func TestPyCallsMethodResolution(t *testing.T) {
	calls := loadTestCalls(t, callsSample)

	bump := calls["Thing.bump"]
	wantEdge(t, bump, schema.Call{Target: "Thing.grow", Kind: "internal", Ref: "mod#Thing.grow", Count: 1})
	wantEdge(t, bump, schema.Call{Target: "self.vanish", Kind: "unresolved", Count: 1})
	wantEdge(t, bump, schema.Call{Target: "helper", Kind: "internal", Ref: "mod#helper", Count: 1})

	wantEdge(t, calls["Thing.grow"], schema.Call{Target: "top", Kind: "internal", Ref: "mod#top", Count: 1})
}

func TestPyCallsShadowedBuiltinIsInternal(t *testing.T) {
	calls := loadTestCalls(t, `def print(x):
    pass

def user():
    print("hi")
`)
	wantEdge(t, calls["user"], schema.Call{Target: "print", Kind: "internal", Ref: "mod#print", Count: 1})
}

func TestPyCallsNoCallsOmitsField(t *testing.T) {
	calls := loadTestCalls(t, `def quiet(x):
    return x + 1
`)
	if calls["quiet"] != nil {
		t.Fatalf("quiet has edges %+v, want none", calls["quiet"])
	}
}
