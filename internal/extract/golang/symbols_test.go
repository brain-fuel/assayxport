package golang

import (
	"testing"

	"goforge.dev/assayxport/internal/schema"
)

func findSym(pkgs []symPkg, pkgID, id string) *sym {
	for _, p := range pkgs {
		if p.id == pkgID {
			for i := range p.syms {
				if p.syms[i].id == id {
					return &p.syms[i]
				}
			}
		}
	}
	return nil
}

// symPkg/sym are tiny local views to keep assertions readable.
type symPkg struct {
	id   string
	syms []sym
}
type sym struct {
	id, kind, vis, recv string
	variadic            bool
	params, returns     int
	typeParams          int
	entry               bool
	how                 string
}

func loadSyms(t *testing.T) []symPkg {
	t.Helper()
	pkgs, err := New().Extract("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	var out []symPkg
	for _, p := range pkgs {
		sp := symPkg{id: p.ID}
		for _, s := range p.Symbols {
			v := sym{id: s.ID, kind: s.Kind, vis: s.Visibility, entry: s.IsEntrypoint}
			if s.Signature != nil {
				v.params = len(s.Signature.Params)
				v.returns = len(s.Signature.Returns)
				v.typeParams = len(s.Signature.TypeParams)
				v.variadic = s.Signature.Variadic
				if s.Signature.Receiver != nil {
					v.recv = s.Signature.Receiver.Type
				}
			}
			if s.Invocation != nil {
				v.how = s.Invocation.How
			}
			sp.syms = append(sp.syms, v)
		}
		out = append(out, sp)
	}
	return out
}

func TestFuncAndUnexported(t *testing.T) {
	pkgs := loadSyms(t)
	add := findSym(pkgs, "example.com/sample/calc", "Add")
	if add == nil || add.kind != "func" || add.vis != "exported" || add.params != 2 || add.returns != 1 {
		t.Fatalf("Add = %+v", add)
	}
	sub := findSym(pkgs, "example.com/sample/calc", "sub")
	if sub == nil || sub.vis != "unexported" {
		t.Fatalf("sub = %+v (want unexported func captured)", sub)
	}
}

func TestVariadicAndGeneric(t *testing.T) {
	pkgs := loadSyms(t)
	sum := findSym(pkgs, "example.com/sample/calc", "Sum")
	if sum == nil || !sum.variadic {
		t.Fatalf("Sum = %+v (want variadic)", sum)
	}
	max := findSym(pkgs, "example.com/sample/calc", "Max")
	if max == nil || max.typeParams != 1 {
		t.Fatalf("Max = %+v (want 1 type param)", max)
	}
}

func TestMethodReceiver(t *testing.T) {
	pkgs := loadSyms(t)
	push := findSym(pkgs, "example.com/sample/calc", "Accumulator.Push")
	if push == nil || push.kind != "method" || push.recv != "*Accumulator" {
		t.Fatalf("Accumulator.Push = %+v (want method recv *Accumulator)", push)
	}
}

func TestSamePackageTypeUnqualified(t *testing.T) {
	pkgs, err := New().Extract("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	var cloneSym *schema.Symbol
	for i := range pkgs {
		if pkgs[i].ID != "example.com/sample/calc" {
			continue
		}
		for j := range pkgs[i].Symbols {
			if pkgs[i].Symbols[j].Name == "Clone" {
				cloneSym = &pkgs[i].Symbols[j]
				break
			}
		}
	}
	if cloneSym == nil {
		t.Fatal("Clone symbol not found in example.com/sample/calc")
	}
	sig := cloneSym.Signature
	if sig == nil || len(sig.Params) != 1 || len(sig.Returns) != 1 {
		t.Fatalf("Clone signature unexpected: %+v", sig)
	}
	if got := sig.Params[0].Type; got != "*Accumulator" {
		t.Errorf("Clone param type = %q, want %q", got, "*Accumulator")
	}
	if got := sig.Returns[0].Type; got != "*Accumulator" {
		t.Errorf("Clone return type = %q, want %q", got, "*Accumulator")
	}
}

func TestEntrypoint(t *testing.T) {
	pkgs := loadSyms(t)
	main := findSym(pkgs, "example.com/sample/cmd/tool", "main")
	if main == nil || !main.entry || main.how != "go run ./cmd/tool" {
		t.Fatalf("main = %+v (want entrypoint how=go run ./cmd/tool)", main)
	}
}

func findFull(t *testing.T, pkgID, id string) (kind, typeKind, underlying, typ, owner string, found bool) {
	t.Helper()
	pkgs, err := New().Extract("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkgs {
		if p.ID != pkgID {
			continue
		}
		for _, s := range p.Symbols {
			if s.ID == id {
				return s.Kind, s.TypeKind, s.Underlying, s.Type, s.Owner, true
			}
		}
	}
	return "", "", "", "", "", false
}

func TestConstAndVar(t *testing.T) {
	if k, _, _, typ, _, ok := findFull(t, "example.com/sample/calc", "MaxInt"); !ok || k != "const" || typ == "" {
		t.Fatalf("MaxInt kind=%q type=%q ok=%v", k, typ, ok)
	}
	if k, _, _, _, _, ok := findFull(t, "example.com/sample/calc", "Default"); !ok || k != "var" {
		t.Fatalf("Default kind=%q ok=%v", k, ok)
	}
}

func TestStructAndField(t *testing.T) {
	if k, tk, _, _, _, ok := findFull(t, "example.com/sample/calc", "Point"); !ok || k != "type" || tk != "struct" {
		t.Fatalf("Point kind=%q typeKind=%q ok=%v", k, tk, ok)
	}
	if k, _, _, typ, owner, ok := findFull(t, "example.com/sample/calc", "Point.X"); !ok || k != "field" || typ != "int" || owner != "Point" {
		t.Fatalf("Point.X kind=%q type=%q owner=%q ok=%v", k, typ, owner, ok)
	}
}

func TestInterfaceAndMethod(t *testing.T) {
	if k, tk, _, _, _, ok := findFull(t, "example.com/sample/calc", "Adder"); !ok || k != "type" || tk != "interface" {
		t.Fatalf("Adder kind=%q typeKind=%q ok=%v", k, tk, ok)
	}
	if k, _, _, _, owner, ok := findFull(t, "example.com/sample/calc", "Adder.Add"); !ok || k != "method" || owner != "Adder" {
		t.Fatalf("Adder.Add kind=%q owner=%q ok=%v", k, owner, ok)
	}
}

func TestDefinedType(t *testing.T) {
	if k, tk, under, _, _, ok := findFull(t, "example.com/sample/calc", "Celsius"); !ok || k != "type" || tk != "defined" || under != "float64" {
		t.Fatalf("Celsius kind=%q typeKind=%q underlying=%q ok=%v", k, tk, under, ok)
	}
}

func TestAliasType(t *testing.T) {
	k, tk, _, _, _, ok := findFull(t, "example.com/sample/calc", "Counter")
	if !ok {
		t.Fatal("Counter symbol not found")
	}
	if k != "type" || tk != "alias" {
		t.Fatalf("Counter kind=%q typeKind=%q, want kind=type typeKind=alias", k, tk)
	}
}

func TestSpanEmbeddedAndMultiName(t *testing.T) {
	// Embedded field: Span.Point
	k, _, _, typ, owner, ok := findFull(t, "example.com/sample/calc", "Span.Point")
	if !ok || k != "field" || typ != "Point" || owner != "Span" {
		t.Fatalf("Span.Point kind=%q type=%q owner=%q ok=%v, want field Point Span", k, typ, owner, ok)
	}
	// Multi-name field Lo
	k, _, _, typ, owner, ok = findFull(t, "example.com/sample/calc", "Span.Lo")
	if !ok || k != "field" || typ != "int" || owner != "Span" {
		t.Fatalf("Span.Lo kind=%q type=%q owner=%q ok=%v, want field int Span", k, typ, owner, ok)
	}
	// Multi-name field Hi
	k, _, _, typ, owner, ok = findFull(t, "example.com/sample/calc", "Span.Hi")
	if !ok || k != "field" || typ != "int" || owner != "Span" {
		t.Fatalf("Span.Hi kind=%q type=%q owner=%q ok=%v, want field int Span", k, typ, owner, ok)
	}
}
