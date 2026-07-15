package java

import (
	"reflect"
	"testing"

	"goforge.dev/assayxport/internal/schema"
)

// loadTestCalls extracts one compilation unit and maps symbol ID -> calls.
func loadTestCalls(t *testing.T, relFile, src string) map[string][]schema.Call {
	t.Helper()
	res, err := compilationUnit(relFile, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string][]schema.Call, len(res.Syms))
	for _, s := range res.Syms {
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

// ar and tv build the evidence fields in expectations; "?" marks an unknown
// position in tv.
func ar(n int) *int { return &n }

func tv(types ...string) []*string {
	out := make([]*string, len(types))
	for i, s := range types {
		if s != "?" {
			v := s
			out[i] = &v
		}
	}
	return out
}

const callsSample = `package demo;

import java.util.ArrayList;
import static java.lang.Math.max;

public class Widget {
    private int size;
    private Helper aide;

    public Widget(int size) {
        this.size = size;
    }

    public Widget() {
        this(0);                       // internal: constructor delegation
    }

    public int grow() {
        helper();                      // internal: own method, bare
        this.helper();                 // internal: own method, this-qualified
        int m = max(1, size);          // external: static import
        var xs = new ArrayList<Integer>();  // external: imported class
        xs.add(size);                  // unresolved: instance receiver
        String s = String.valueOf(m);  // external: java.lang, no import
        var w = new Widget(1);         // internal: declared constructor
        var h = new Helper();          // internal: no declared ctor -> type ref
        Helper.describe();             // internal: static call, declared method
        Helper.vanish();               // unresolved: no such method (inheritance invisible)
        aide.describe();               // unresolved: field receiver, not a type
        Runnable r = () -> helper();   // lambda body attributed to grow
        r.run();                       // unresolved: local receiver
        return s.length() + w.size + h.hashCode();
    }

    private void helper() {}
}

class Helper {
    static void describe() {}
}
`

func TestJavaCallsResolutionLadder(t *testing.T) {
	calls := loadTestCalls(t, "Widget.java", callsSample)

	grow := calls["Widget.grow"]
	// Bare and this-qualified sites of the same zero-arg method merge.
	wantEdge(t, grow, schema.Call{
		Target: "Widget.helper", Kind: "internal",
		Ref: "demo.Widget#Widget.helper", Arity: ar(0), Count: 3,
	})
	wantEdge(t, grow, schema.Call{
		Target: "java.lang.Math.max", Kind: "external",
		Arity: ar(2), ArgTypes: tv("int", "?"), Count: 1,
	})
	wantEdge(t, grow, schema.Call{Target: "new java.util.ArrayList", Kind: "external", Arity: ar(0), Count: 1})
	wantEdge(t, grow, schema.Call{Target: "xs.add", Kind: "unresolved", Arity: ar(1), Count: 1})
	wantEdge(t, grow, schema.Call{Target: "java.lang.String.valueOf", Kind: "external", Arity: ar(1), Count: 1})
	wantEdge(t, grow, schema.Call{
		Target: "new Widget", Kind: "internal",
		Ref: "demo.Widget#Widget.Widget", Arity: ar(1), ArgTypes: tv("int"), Count: 1,
	})
	// Helper has no declared constructor, so the edge anchors on the type.
	wantEdge(t, grow, schema.Call{
		Target: "new Helper", Kind: "internal",
		Ref: "demo.Widget#Helper", Arity: ar(0), Count: 1,
	})
	wantEdge(t, grow, schema.Call{
		Target: "Helper.describe", Kind: "internal",
		Ref: "demo.Widget#Helper.describe", Arity: ar(0), Count: 1,
	})
	wantEdge(t, grow, schema.Call{Target: "Helper.vanish", Kind: "unresolved", Arity: ar(0), Count: 1})
	wantEdge(t, grow, schema.Call{Target: "aide.describe", Kind: "unresolved", Arity: ar(0), Count: 1})
	wantEdge(t, grow, schema.Call{Target: "r.run", Kind: "unresolved", Arity: ar(0), Count: 1})
}

func TestJavaCallsConstructorDelegation(t *testing.T) {
	calls := loadTestCalls(t, "Widget.java", callsSample)
	wantEdge(t, calls["Widget.Widget"], schema.Call{
		Target: "Widget.Widget", Kind: "internal",
		Ref: "demo.Widget#Widget.Widget", Arity: ar(1), ArgTypes: tv("int"), Count: 1,
	})
}

func TestJavaCallsSuperIsUnresolved(t *testing.T) {
	calls := loadTestCalls(t, "Child.java", `package demo;

public class Child extends Base {
    public Child() {
        super();
    }

    public void poke() {
        super.touch();
    }
}
`)
	wantEdge(t, calls["Child.Child"], schema.Call{Target: "super", Kind: "unresolved", Arity: ar(0), Count: 1})
	wantEdge(t, calls["Child.poke"], schema.Call{Target: "super.touch", Kind: "unresolved", Arity: ar(0), Count: 1})
}

func TestJavaCallsNestedTypeResolution(t *testing.T) {
	calls := loadTestCalls(t, "Outer.java", `package demo;

public class Outer {
    void ping() {}

    static class Inner {
        void run() {
            Outer o = new Outer();  // internal: enclosing type, no declared ctor
            pong();                 // internal: own method
        }
        void pong() {}
    }
}
`)
	inner := calls["Outer.Inner.run"]
	wantEdge(t, inner, schema.Call{
		Target: "new Outer", Kind: "internal", Ref: "demo.Outer#Outer", Arity: ar(0), Count: 1,
	})
	wantEdge(t, inner, schema.Call{
		Target: "Outer.Inner.pong", Kind: "internal",
		Ref: "demo.Outer#Outer.Inner.pong", Arity: ar(0), Count: 1,
	})
}

func TestJavaCallsDefaultPackageUnitID(t *testing.T) {
	calls := loadTestCalls(t, "Lone.java", `public class Lone {
    void a() { b(); }
    void b() {}
}
`)
	wantEdge(t, calls["Lone.a"], schema.Call{
		Target: "Lone.b", Kind: "internal", Ref: "Lone#Lone.b", Arity: ar(0), Count: 1,
	})
}

func TestJavaCallsNoCallsOmitsField(t *testing.T) {
	calls := loadTestCalls(t, "Quiet.java", `public class Quiet {
    int add(int a, int b) { return a + b; }
}
`)
	if calls["Quiet.add"] != nil {
		t.Fatalf("Quiet.add has edges %+v, want none", calls["Quiet.add"])
	}
}

func TestJavaCallsEvidenceTable(t *testing.T) {
	calls := loadTestCalls(t, "Ev.java", `package demo;

public class Ev {
    void go(Object o) {
        f(1, 1L, 1.5, 2.5f, "s", 'c', true, null);
        f((Ev) o, new Ev(), new int[3], new String[]{"a"}, this);
        f("a" + o, o + "b", (1), 1 + 2, o.toString());
    }
    void f(Object... xs) {}
}
`)
	go_ := calls["Ev.go"]
	wantEdge(t, go_, schema.Call{
		Target: "Ev.f", Kind: "internal", Ref: "demo.Ev#Ev.f",
		Arity:    ar(8),
		ArgTypes: tv("int", "long", "double", "float", "String", "char", "boolean", "null"),
		Count:    1,
	})
	wantEdge(t, go_, schema.Call{
		Target: "Ev.f", Kind: "internal", Ref: "demo.Ev#Ev.f",
		Arity:    ar(5),
		ArgTypes: tv("Ev", "Ev", "int[]", "String[]", "Ev"),
		Count:    1,
	})
	// Concatenation with a String operand on either side is String; a
	// parenthesized literal classifies through; numeric ops and call
	// results stay unknown.
	wantEdge(t, go_, schema.Call{
		Target: "Ev.f", Kind: "internal", Ref: "demo.Ev#Ev.f",
		Arity:    ar(5),
		ArgTypes: tv("String", "String", "int", "?", "?"),
		Count:    1,
	})
}

func TestJavaCallsVectorKeyedDedup(t *testing.T) {
	calls := loadTestCalls(t, "V.java", `package demo;

public class V {
    void go(int x) {
        f(1);
        f(x);
        f("a");
        f("b");
    }
    void f(int n) {}
    void f(String s) {}
}
`)
	// f(1) and f(x): same target and arity, different evidence -> distinct
	// edges. f("a") and f("b"): identical evidence -> merged.
	go_ := calls["V.go"]
	wantEdge(t, go_, schema.Call{
		Target: "V.f", Kind: "internal", Ref: "demo.V#V.f",
		Arity: ar(1), ArgTypes: tv("int"), Count: 1,
	})
	wantEdge(t, go_, schema.Call{
		Target: "V.f", Kind: "internal", Ref: "demo.V#V.f",
		Arity: ar(1), Count: 1,
	})
	wantEdge(t, go_, schema.Call{
		Target: "V.f", Kind: "internal", Ref: "demo.V#V.f",
		Arity: ar(1), ArgTypes: tv("String"), Count: 2,
	})
	if len(go_) != 3 {
		t.Fatalf("want 3 distinct edges, got %d: %+v", len(go_), go_)
	}
}

func TestJavaCallsArityIncompatibleDowngrades(t *testing.T) {
	calls := loadTestCalls(t, "A.java", `package demo;

public class A extends B {
    void go() {
        f(1, 2);       // no local 2-arg overload: likely inherited -> unresolved
        new A(1);      // no 1-arg constructor -> unresolved
        g("a", "b");   // varargs: compatible
        g();           // varargs lower bound: compatible
    }
    void f(int n) {}
    void g(String... xs) {}
}
`)
	go_ := calls["A.go"]
	wantEdge(t, go_, schema.Call{
		Target: "A.f", Kind: "unresolved",
		Arity: ar(2), ArgTypes: tv("int", "int"), Count: 1,
	})
	wantEdge(t, go_, schema.Call{
		Target: "new A", Kind: "unresolved",
		Arity: ar(1), ArgTypes: tv("int"), Count: 1,
	})
	wantEdge(t, go_, schema.Call{
		Target: "A.g", Kind: "internal", Ref: "demo.A#A.g",
		Arity: ar(2), ArgTypes: tv("String", "String"), Count: 1,
	})
	wantEdge(t, go_, schema.Call{
		Target: "A.g", Kind: "internal", Ref: "demo.A#A.g",
		Arity: ar(0), Count: 1,
	})
}

func TestJavaCallsRecordCanonicalConstructor(t *testing.T) {
	calls := loadTestCalls(t, "R.java", `package demo;

public class R {
    Object go() {
        return new Point(1, 2);   // canonical ctor from components -> type ref
    }
}

record Point(int x, int y) {}
`)
	wantEdge(t, calls["R.go"], schema.Call{
		Target: "new Point", Kind: "internal", Ref: "demo.R#Point",
		Arity: ar(2), ArgTypes: tv("int", "int"), Count: 1,
	})
}
