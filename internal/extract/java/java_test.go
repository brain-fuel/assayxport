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

	// Check varargs: Logger.log(String... args) must keep name and base type.
	logSym := symByID(mod.Symbols, "Logger.log")
	if logSym == nil || logSym.Signature == nil || len(logSym.Signature.Params) != 1 {
		t.Fatalf("Logger.log sym = %+v, want one varargs param", logSym)
	}
	p := logSym.Signature.Params[0]
	if p.Name != "args" || p.Type != "String..." {
		t.Fatalf("Logger.log param = %+v, want {name:args type:String...}", p)
	}
	if !strings.Contains(p.Type, "...") {
		t.Fatalf("Logger.log param type = %q, want ... (varargs)", p.Type)
	}

	// Record components appear as public field members with their written type.
	for _, id := range []string{"Point.x", "Point.y"} {
		f := symByID(mod.Symbols, id)
		if f == nil || f.Kind != "field" || f.Type != "int" || f.Visibility != "public" {
			t.Fatalf("record component %s = %+v, want public int field", id, f)
		}
	}

	// Annotation element Marker.value is captured as a public method.
	valueSym := symByID(mod.Symbols, "Marker.value")
	if valueSym == nil || valueSym.Kind != "method" || valueSym.Visibility != "public" {
		t.Fatalf("Marker.value = %+v, want public method", valueSym)
	}
	if valueSym.Signature == nil || len(valueSym.Signature.Returns) != 1 || valueSym.Signature.Returns[0].Type != "String" {
		t.Fatalf("Marker.value signature = %+v, want String return", valueSym.Signature)
	}

	// Bounded generic: Logger.sortIt<T extends Comparable<T>>.
	sortSym := symByID(mod.Symbols, "Logger.sortIt")
	if sortSym == nil || sortSym.Signature == nil || len(sortSym.Signature.TypeParams) != 1 {
		t.Fatalf("Logger.sortIt = %+v, want one type param", sortSym)
	}
	tp := sortSym.Signature.TypeParams[0]
	if tp.Name != "T" || tp.Constraint != "extends Comparable<T>" {
		t.Fatalf("Logger.sortIt type param = %+v, want T extends Comparable<T>", tp)
	}
}

func TestMainVarargsEntrypoint(t *testing.T) {
	ps, err := New().Extract("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}
	// App declares public static void main(String... args): a varargs entrypoint.
	mod := pkgByID(ps, "com.foo.App")
	if mod == nil || mod.Level != "module" {
		t.Fatalf("com.foo.App unit = %+v", mod)
	}
	if !mod.IsEntrypoint || mod.Invocation == nil || mod.Invocation.How != "java com.foo.App" {
		t.Fatalf("com.foo.App entrypoint = %v / %+v", mod.IsEntrypoint, mod.Invocation)
	}
	main := symByID(mod.Symbols, "App.main")
	if main == nil || !main.IsEntrypoint || main.Invocation == nil || main.Invocation.How != "java com.foo.App" {
		t.Fatalf("App.main = %+v (want stamped entrypoint)", main)
	}
	if len(main.Signature.Params) != 1 || main.Signature.Params[0].Type != "String..." {
		t.Fatalf("App.main params = %+v, want one String... param", main.Signature.Params)
	}
	// The module unit and the main symbol must not share one Invocation pointer.
	if mod.Invocation == main.Invocation {
		t.Fatal("module and main symbol share one *Invocation")
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
