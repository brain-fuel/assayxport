package diff

import (
	"reflect"
	"testing"

	"goforge.dev/assayxport/internal/emit"
	"goforge.dev/assayxport/internal/schema"
)

func fn(id string, paramTypes ...string) schema.Symbol {
	params := make([]schema.Param, 0, len(paramTypes))
	for _, t := range paramTypes {
		params = append(params, schema.Param{Type: t})
	}
	return schema.Symbol{
		ID: id, Name: id, Kind: "func",
		Visibility: "exported", VisibilityIdiom: "capitalized",
		Signature:  &schema.Signature{Params: params, Returns: []schema.Param{}, TypeParams: []schema.TypeParam{}},
		Complexity: schema.DeferredComplexity(),
	}
}

func pkg(id, path string, syms ...schema.Symbol) schema.Package {
	return schema.Package{ID: id, Language: "go", Path: path, Name: path, Symbols: syms}
}

func TestDriftIdentical(t *testing.T) {
	a := []schema.Package{pkg("m/p", "p", fn("F", "int"))}
	d := ComputeDrift(a, a, DriftOptions{})
	if d.HasDifferences() {
		t.Fatalf("identical inputs drifted: %+v", d)
	}
	if d.Packages.Added == nil || d.Packages.Removed == nil || d.Packages.Changed == nil {
		t.Fatal("result slices must be non-nil for stable JSON")
	}
}

func TestDriftPackagesAddedRemoved(t *testing.T) {
	a := []schema.Package{pkg("m/old", "old", fn("F"))}
	b := []schema.Package{pkg("m/new", "new", fn("F"), fn("G"))}
	d := ComputeDrift(a, b, DriftOptions{})
	want := DriftPackages{
		Added:   []PackageStub{{ID: "m/new", SymbolCount: 2}},
		Removed: []PackageStub{{ID: "m/old", SymbolCount: 1}},
		Changed: []PackageDrift{},
	}
	if !reflect.DeepEqual(d.Packages, want) {
		t.Fatalf("got %+v, want %+v", d.Packages, want)
	}
	if d.Summary.PackagesAdded != 1 || d.Summary.PackagesRemoved != 1 || d.Summary.SymbolsAdded != 0 {
		t.Fatalf("summary %+v", d.Summary)
	}
}

func TestDriftRenamedModule(t *testing.T) {
	a := []schema.Package{pkg("old.example/mod/util", "util", fn("F", "int"))}
	b := []schema.Package{pkg("new.example/fork/util", "util", fn("F", "int"), fn("G"))}
	d := ComputeDrift(a, b, DriftOptions{})
	if len(d.Packages.Changed) != 1 {
		t.Fatalf("want one changed package, got %+v", d.Packages)
	}
	c := d.Packages.Changed[0]
	if c.ID != "new.example/fork/util" || c.RenamedFromID != "old.example/mod/util" {
		t.Fatalf("rename not detected: %+v", c)
	}
	if !reflect.DeepEqual(c.Symbols.Added, []string{"G"}) {
		t.Fatalf("symbols %+v", c.Symbols)
	}
}

func TestDriftSymbolChanges(t *testing.T) {
	sigChanged := fn("Changed", "int")
	sigChangedB := fn("Changed", "string")

	visChanged := fn("Vis")
	visChangedB := fn("Vis")
	visChangedB.Visibility = "internal"

	idiomChanged := fn("Idiom")
	idiomChangedB := fn("Idiom")
	idiomChangedB.VisibilityIdiom = "export"

	entry := fn("Main")
	entryB := fn("Main")
	entryB.IsEntrypoint = true

	on := "O(n)"
	o1 := "O(1)"
	cx := fn("Cx")
	cx.Complexity = schema.Complexity{Time: &o1, Space: &o1, Method: "loop-nesting"}
	cxB := fn("Cx")
	cxB.Complexity = schema.Complexity{Time: &on, Space: &o1, Method: "loop-nesting"}

	calls := fn("Caller")
	calls.Calls = []schema.Call{{Target: "old.Helper", Kind: "external", Count: 1}}
	callsB := fn("Caller")
	callsB.Calls = []schema.Call{{Target: "new.Helper", Kind: "external", Count: 2}}

	kind := fn("Shape")
	kindB := fn("Shape")
	kindB.Kind = "method"

	a := []schema.Package{pkg("m/p", "p", sigChanged, visChanged, idiomChanged, entry, cx, calls, kind, fn("Gone"))}
	b := []schema.Package{pkg("m/p", "p", sigChangedB, visChangedB, idiomChangedB, entryB, cxB, callsB, kindB, fn("New"))}
	d := ComputeDrift(a, b, DriftOptions{})

	if len(d.Packages.Changed) != 1 {
		t.Fatalf("want one changed package: %+v", d.Packages)
	}
	sym := d.Packages.Changed[0].Symbols
	if !reflect.DeepEqual(sym.Added, []string{"New"}) || !reflect.DeepEqual(sym.Removed, []string{"Gone"}) {
		t.Fatalf("added/removed %+v %+v", sym.Added, sym.Removed)
	}
	byID := map[string]SymbolDrift{}
	for _, c := range sym.Changed {
		byID[c.ID] = c
	}
	assertChange := func(id string, kinds ...string) SymbolDrift {
		t.Helper()
		c, ok := byID[id]
		if !ok {
			t.Fatalf("no change recorded for %s", id)
		}
		if !reflect.DeepEqual(c.Changes, kinds) {
			t.Fatalf("%s changes = %v, want %v", id, c.Changes, kinds)
		}
		return c
	}
	if c := assertChange("Changed", "signature"); c.Signature.A != "(int)" || c.Signature.B != "(string)" {
		t.Errorf("signature AB %+v", c.Signature)
	}
	if c := assertChange("Vis", "visibility"); c.Visibility.A != "exported" || c.Visibility.B != "internal" {
		t.Errorf("visibility AB %+v", c.Visibility)
	}
	if c := assertChange("Idiom", "visibility"); c.Visibility.A != "capitalized" || c.Visibility.B != "export" {
		t.Errorf("idiom AB %+v", c.Visibility)
	}
	if c := assertChange("Main", "entrypoint"); c.Entrypoint.A || !c.Entrypoint.B {
		t.Errorf("entrypoint AB %+v", c.Entrypoint)
	}
	if c := assertChange("Cx", "complexity"); *c.Complexity.B.Time != "O(n)" {
		t.Errorf("complexity AB %+v", c.Complexity)
	}
	c := assertChange("Caller", "calls")
	if !reflect.DeepEqual(c.Calls.Added, []CallStub{{Target: "new.Helper", Kind: "external"}}) ||
		!reflect.DeepEqual(c.Calls.Removed, []CallStub{{Target: "old.Helper", Kind: "external"}}) {
		t.Errorf("calls AB %+v", c.Calls)
	}
	assertChange("Shape", "kind")
	if d.Summary.SymbolsChanged != 7 {
		t.Errorf("summary %+v", d.Summary)
	}
}

func TestDriftIgnoreParamNames(t *testing.T) {
	withName := fn("F")
	withName.Signature.Params = []schema.Param{{Name: "count", Type: "int"}}
	renamed := fn("F")
	renamed.Signature.Params = []schema.Param{{Name: "n", Type: "int"}}
	a := []schema.Package{pkg("m/p", "p", withName)}
	b := []schema.Package{pkg("m/p", "p", renamed)}
	if d := ComputeDrift(a, b, DriftOptions{}); d.Summary.SymbolsChanged != 1 {
		t.Fatalf("param rename must count by default: %+v", d.Summary)
	}
	if d := ComputeDrift(a, b, DriftOptions{IgnoreParamNames: true}); d.HasDifferences() {
		t.Fatalf("param rename must be ignored under the flag: %+v", d.Summary)
	}
}

// Java overloads share one manifest id; a changed overload must pair with
// its counterpart, and a brand-new overload reports the id as added.
func TestDriftOverloads(t *testing.T) {
	a := []schema.Package{pkg("com.example", "com/example", fn("f", "int"), fn("f", "int", "int"))}
	b := []schema.Package{pkg("com.example", "com/example", fn("f", "int"), fn("f", "int", "String"), fn("f", "int", "int", "int"))}
	d := ComputeDrift(a, b, DriftOptions{})
	sym := d.Packages.Changed[0].Symbols
	if !reflect.DeepEqual(sym.Added, []string{"f"}) {
		t.Errorf("added %v", sym.Added)
	}
	if len(sym.Changed) != 1 || sym.Changed[0].Signature.A != "(int, int)" || sym.Changed[0].Signature.B != "(int, String)" {
		t.Errorf("changed %+v", sym.Changed)
	}
}

// Input order must not affect output: reversed package and symbol order
// produces byte-identical JSON.
func TestDriftOrderIndependence(t *testing.T) {
	a := []schema.Package{
		pkg("m/a", "a", fn("A", "int"), fn("B")),
		pkg("m/b", "b", fn("C")),
	}
	b := []schema.Package{
		pkg("m/a", "a", fn("A", "string")),
		pkg("m/c", "c", fn("D")),
	}
	rev := func(pkgs []schema.Package) []schema.Package {
		out := make([]schema.Package, 0, len(pkgs))
		for i := len(pkgs) - 1; i >= 0; i-- {
			p := pkgs[i]
			syms := make([]schema.Symbol, 0, len(p.Symbols))
			for j := len(p.Symbols) - 1; j >= 0; j-- {
				syms = append(syms, p.Symbols[j])
			}
			p.Symbols = syms
			out = append(out, p)
		}
		return out
	}
	d1, err1 := emit.Marshal(ComputeDrift(a, b, DriftOptions{}))
	d2, err2 := emit.Marshal(ComputeDrift(rev(a), rev(b), DriftOptions{}))
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if string(d1) != string(d2) {
		t.Fatalf("order-dependent output:\n%s\n---\n%s", d1, d2)
	}
}
