package python

import "os"
import "testing"

func load(t *testing.T) ([]symView, map[string]bool, bool) {
	t.Helper()
	src, err := os.ReadFile("testdata/sample.py")
	if err != nil {
		t.Fatal(err)
	}
	syms, _, all, hasMain, err := moduleSymbols("sample.py", src)
	if err != nil {
		t.Fatal(err)
	}
	var vs []symView
	for _, s := range syms {
		v := symView{id: s.ID, kind: s.Kind, vis: s.Visibility, owner: s.Owner, doc: s.Doc.Raw}
		if s.InAll != nil {
			v.inAll = *s.InAll
			v.hasInAll = true
		}
		v.decorators = append(v.decorators, s.Decorators...)
		if s.Signature != nil {
			v.mods = append(v.mods, s.Signature.Modifiers...)
		}
		vs = append(vs, v)
	}
	return vs, all, hasMain
}

type symView struct {
	id, kind, vis, owner, doc string
	inAll, hasInAll           bool
	decorators, mods          []string
}

func get(vs []symView, id string) *symView {
	for i := range vs {
		if vs[i].id == id {
			return &vs[i]
		}
	}
	return nil
}

func TestModuleSymbols(t *testing.T) {
	vs, all, hasMain := load(t)
	if !hasMain {
		t.Fatal("expected __main__ guard detected")
	}
	if !all["Widget"] || !all["make"] || len(all) != 2 {
		t.Fatalf("__all__ = %v, want {Widget, make}", all)
	}
	if m := get(vs, "make"); m == nil || m.kind != "function" || m.vis != "exported" || !m.hasInAll || !m.inAll || m.doc == "" {
		t.Fatalf("make = %+v", m)
	}
	if h := get(vs, "_helper"); h == nil || h.vis != "unexported" || !h.hasInAll || h.inAll {
		t.Fatalf("_helper = %+v (want unexported, in_all=false)", h)
	}
	if f := get(vs, "fetch"); f == nil || len(f.mods) == 0 || f.mods[0] != "async" {
		t.Fatalf("fetch = %+v (want async modifier)", f)
	}
	if w := get(vs, "Widget"); w == nil || w.kind != "class" || w.doc == "" {
		t.Fatalf("Widget = %+v", w)
	}
	if lbl := get(vs, "Widget.label"); lbl == nil || lbl.kind != "property" || lbl.owner != "Widget" {
		t.Fatalf("Widget.label = %+v (want property owner Widget)", lbl)
	}
	if ini := get(vs, "Widget.__init__"); ini == nil || ini.vis != "exported" {
		t.Fatalf("Widget.__init__ = %+v (dunder must be exported)", ini)
	}
	if pv := get(vs, "Widget._private"); pv == nil || pv.vis != "unexported" {
		t.Fatalf("Widget._private = %+v (want unexported)", pv)
	}
	if c := get(vs, "Widget.count"); c == nil || c.kind != "variable" || c.owner != "Widget" {
		t.Fatalf("Widget.count = %+v (want class attribute variable)", c)
	}
	// Finding 1: nested class + its members appear with dotted owners.
	if o := get(vs, "Outer"); o == nil || o.kind != "class" || o.owner != "" {
		t.Fatalf("Outer = %+v (want top-level class)", o)
	}
	if in := get(vs, "Outer.Inner"); in == nil || in.kind != "class" || in.owner != "Outer" {
		t.Fatalf("Outer.Inner = %+v (want nested class owned by Outer)", in)
	}
	if p := get(vs, "Outer.Inner.ping"); p == nil || p.kind != "method" || p.owner != "Outer.Inner" {
		t.Fatalf("Outer.Inner.ping = %+v (want method owned by Outer.Inner)", p)
	}
	// Finding 3: docstring preceded by a leading comment is still captured.
	if d := get(vs, "documented"); d == nil || d.doc != "Documented via comment-preceded docstring." {
		t.Fatalf("documented = %+v (want comment-preceded docstring captured)", d)
	}
}

// TestMainGuardSingleQuote covers Finding 2: a single-quoted __main__ guard is
// detected just like the double-quoted form.
func TestMainGuardSingleQuote(t *testing.T) {
	src := []byte("def main():\n    pass\n\n\nif __name__ == '__main__':\n    main()\n")
	syms, _, _, hasMain, err := moduleSymbols("guard.py", src)
	if err != nil {
		t.Fatal(err)
	}
	if !hasMain {
		t.Fatal("single-quote __main__ guard not detected")
	}
	// The double-quote form must still work.
	src2 := []byte("if __name__ == \"__main__\":\n    pass\n")
	if _, _, _, hasMain2, err := moduleSymbols("guard2.py", src2); err != nil || !hasMain2 {
		t.Fatalf("double-quote guard: hasMain=%v err=%v", hasMain2, err)
	}
	_ = syms
}
