package golang

import "testing"

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

func TestEntrypoint(t *testing.T) {
	pkgs := loadSyms(t)
	main := findSym(pkgs, "example.com/sample/cmd/tool", "main")
	if main == nil || !main.entry || main.how != "go run ./cmd/tool" {
		t.Fatalf("main = %+v (want entrypoint how=go run ./cmd/tool)", main)
	}
}
