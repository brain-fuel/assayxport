package java

import (
	"flag"
	"os"
	"strings"
	"testing"

	"goforge.dev/assayxport/internal/emit"
	"goforge.dev/assayxport/internal/schema"
)

var update = flag.Bool("update", false, "regenerate golden files")

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

	pkg := pkgByID(ps, "com.foo")
	if pkg == nil || pkg.Level != "package" {
		t.Fatalf("com.foo unit = %+v", pkg)
	}
	if pkg.Doc != "The com.foo package." {
		t.Fatalf("com.foo doc = %q", pkg.Doc)
	}
	hasMod, hasSub := false, false
	for _, m := range pkg.Members {
		if m == "com.foo.Bar" {
			hasMod = true
		}
		if m == "com.foo.sub" {
			hasSub = true
		}
	}
	if !hasMod || !hasSub {
		t.Fatalf("com.foo members = %v, want com.foo.Bar + com.foo.sub", pkg.Members)
	}

	mod := pkgByID(ps, "com.foo.Bar")
	if mod == nil || mod.Level != "module" {
		t.Fatalf("com.foo.Bar unit = %+v", mod)
	}
	if !mod.IsEntrypoint || mod.Invocation == nil || mod.Invocation.How != "java com.foo.Bar" {
		t.Fatalf("com.foo.Bar entrypoint = %v / %+v", mod.IsEntrypoint, mod.Invocation)
	}
}

func TestAdditionalCoverage(t *testing.T) {
	ps, err := New().Extract("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}

	// Point.java contains a record, an @interface, and a Logger class with varargs.
	mod := pkgByID(ps, "com.foo.Point")
	if mod == nil {
		t.Fatal("com.foo.Point module not found")
	}

	// Check record type_kind.
	pointSym := symByID(mod.Symbols, "Point")
	if pointSym == nil || pointSym.TypeKind != "record" {
		t.Fatalf("Point sym = %+v, want type_kind=record", pointSym)
	}

	// Check annotation type_kind.
	markerSym := symByID(mod.Symbols, "Marker")
	if markerSym == nil || markerSym.TypeKind != "annotation" {
		t.Fatalf("Marker sym = %+v, want type_kind=annotation", markerSym)
	}

	// Check varargs: Logger.log param type must contain "...".
	logSym := symByID(mod.Symbols, "Logger.log")
	if logSym == nil || logSym.Signature == nil || len(logSym.Signature.Params) == 0 {
		t.Fatalf("Logger.log sym = %+v, want varargs param", logSym)
	}
	pt := logSym.Signature.Params[0].Type
	if !strings.Contains(pt, "...") {
		t.Fatalf("Logger.log param type = %q, want ... (varargs)", pt)
	}
}

func TestGolden(t *testing.T) {
	ps, err := New().Extract("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}
	idx, shards := emit.Manifest(ps, "", []string{"java"})
	got, err := emit.Combined(idx, shards)
	if err != nil {
		t.Fatal(err)
	}
	const golden = "testdata/sample.golden.json"
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("golden mismatch; run with -update")
	}
}

func TestDoubleRunByteIdentical(t *testing.T) {
	run := func() []byte {
		ps, err := New().Extract("testdata/proj")
		if err != nil {
			t.Fatal(err)
		}
		idx, shards := emit.Manifest(ps, "", []string{"java"})
		b, err := emit.Combined(idx, shards)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if string(run()) != string(run()) {
		t.Fatal("extract+emit is not byte-identical across runs")
	}
}
