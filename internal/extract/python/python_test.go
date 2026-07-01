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
