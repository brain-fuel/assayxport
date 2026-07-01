package python

import (
	"flag"
	"os"
	"testing"

	"goforge.dev/assayxport/internal/emit"
	"goforge.dev/assayxport/internal/schema"
)

var update = flag.Bool("update", false, "update golden file")

func pkgByID(ps []schema.Package, id string) *schema.Package {
	for i := range ps {
		if ps[i].ID == id {
			return &ps[i]
		}
	}
	return nil
}

func symByID(p *schema.Package, id string) *schema.Symbol {
	if p == nil {
		return nil
	}
	for i := range p.Symbols {
		if p.Symbols[i].ID == id {
			return &p.Symbols[i]
		}
	}
	return nil
}

func TestExtractUnits(t *testing.T) {
	ps, err := New().Extract("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}
	// pkg (package unit) exists with members including pkg.mod and pkg.sub
	pkg := pkgByID(ps, "pkg")
	if pkg == nil || pkg.Level != "package" {
		t.Fatalf("pkg unit = %+v", pkg)
	}
	hasMod, hasSub := false, false
	for _, m := range pkg.Members {
		if m == "pkg.mod" {
			hasMod = true
		}
		if m == "pkg.sub" {
			hasSub = true
		}
	}
	if !hasMod || !hasSub {
		t.Fatalf("pkg.Members = %v, want pkg.mod + pkg.sub", pkg.Members)
	}
	// module unit for pkg.mod
	mod := pkgByID(ps, "pkg.mod")
	if mod == nil || mod.Level != "module" {
		t.Fatalf("pkg.mod unit = %+v", mod)
	}
	// pkg.mod should be an entrypoint (has __main__ guard)
	if !mod.IsEntrypoint {
		t.Fatalf("pkg.mod should be entrypoint (has __main__ guard)")
	}
	if mod.Invocation == nil || mod.Invocation.How != "python -m pkg.mod" {
		t.Fatalf("pkg.mod invocation = %+v, want 'python -m pkg.mod'", mod.Invocation)
	}
	// pkg.sub should be a package unit
	sub := pkgByID(ps, "pkg.sub")
	if sub == nil || sub.Level != "package" {
		t.Fatalf("pkg.sub unit = %+v", sub)
	}
	// Finding 1 (end-to-end): nested class in pkg.mod carries dotted owners.
	if in := symByID(mod, "Outer.Inner"); in == nil || in.Kind != "class" || in.Owner != "Outer" {
		t.Fatalf("pkg.mod Outer.Inner = %+v (want nested class owned by Outer)", in)
	}
	if ping := symByID(mod, "Outer.Inner.ping"); ping == nil || ping.Kind != "method" || ping.Owner != "Outer.Inner" {
		t.Fatalf("pkg.mod Outer.Inner.ping = %+v (want method owned by Outer.Inner)", ping)
	}
	// Finding 2 (end-to-end): single-quote guard module is an entrypoint,
	// and its module docstring survives a leading license comment (Finding 3).
	entry := pkgByID(ps, "entry")
	if entry == nil || !entry.IsEntrypoint {
		t.Fatalf("entry unit = %+v (want single-quote-guard entrypoint)", entry)
	}
	if entry.Doc != "Module entry: single-quote guard entrypoint." {
		t.Fatalf("entry.Doc = %q (want comment-preceded module docstring)", entry.Doc)
	}
	if em := symByID(entry, "main"); em == nil || !em.IsEntrypoint {
		t.Fatalf("entry.main = %+v (want entrypoint symbol)", em)
	}
}

// TestDoubleRunByteIdentical covers the spec's determinism deliverable: two
// full extract+emit passes over the Python fixture produce byte-identical output.
func TestDoubleRunByteIdentical(t *testing.T) {
	run := func() []byte {
		ps, err := New().Extract("testdata/proj")
		if err != nil {
			t.Fatal(err)
		}
		idx, shards := emit.Manifest(ps, "", []string{"python"})
		out, err := emit.Combined(idx, shards)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	a := run()
	b := run()
	if string(a) != string(b) {
		t.Fatalf("Python extract+emit not byte-identical across runs")
	}
}

func TestGolden(t *testing.T) {
	ps, err := New().Extract("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}
	idx, shards := emit.Manifest(ps, "", []string{"python"})
	got, err := emit.Combined(idx, shards)
	if err != nil {
		t.Fatal(err)
	}
	const golden = "testdata/sample.golden.json"
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update first): %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("golden mismatch; re-run with -update if intended.\n--- got ---\n%s", got)
	}
}
