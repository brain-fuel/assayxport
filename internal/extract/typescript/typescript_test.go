package typescript

import (
	"flag"
	"os"
	"sort"
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

func hasConcern(s *schema.Symbol, c string) bool {
	if s == nil {
		return false
	}
	for _, x := range s.Concerns {
		if x == c {
			return true
		}
	}
	return false
}

// TestExtractShape covers the module-level structure and the core symbol kinds:
// typed .ts modules yield typescript packages, plain .js yields javascript, and
// interface/alias/enum/class/function/method/property all surface.
func TestExtractShape(t *testing.T) {
	ps, err := New().Extract("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}

	geo := pkgByID(ps, "core/geometry")
	if geo == nil || geo.Level != "module" || geo.Language != "typescript" {
		t.Fatalf("core/geometry unit = %+v (want typescript module)", geo)
	}

	// The union type alias records its underlying.
	if shape := symByID(geo, "Shape"); shape == nil || shape.TypeKind != "alias" || shape.Underlying != "Circle | Rect" {
		t.Fatalf("Shape = %+v (want alias of Circle | Rect)", shape)
	}
	if enum := symByID(geo, "Unit"); enum == nil || enum.TypeKind != "enum" {
		t.Fatalf("Unit = %+v (want enum)", enum)
	}
	// A typed exported function resolves a builtin call and carries a signature.
	dist := symByID(geo, "distance")
	if dist == nil || dist.Kind != "function" || dist.Signature == nil {
		t.Fatalf("distance = %+v (want typed function)", dist)
	}
	if len(dist.Concerns) != 0 {
		t.Fatalf("distance.Concerns = %v (a fully typed function is clean)", dist.Concerns)
	}
	// Class members carry dotted ids, owners, and access-modifier visibility.
	if pts := symByID(geo, "Path.points"); pts == nil || pts.Kind != "property" || pts.Owner != "Path" || pts.Visibility != "private" {
		t.Fatalf("Path.points = %+v (want private property owned by Path)", pts)
	}
	if add := symByID(geo, "Path.add"); add == nil || add.Kind != "method" || add.Owner != "Path" {
		t.Fatalf("Path.add = %+v (want method owned by Path)", add)
	}

	// Plain JS is tagged javascript, not typescript.
	util := pkgByID(ps, "legacy/util")
	if util == nil || util.Language != "javascript" {
		t.Fatalf("legacy/util unit = %+v (want javascript)", util)
	}
}

// TestConcerns covers the type-honesty deliverable: constructs that compile/run
// but forfeit their type contract must each raise the right flag.
func TestConcerns(t *testing.T) {
	ps, err := New().Extract("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}
	loose := pkgByID(ps, "legacy/loose")
	if loose == nil {
		t.Fatal("legacy/loose module missing")
	}
	cases := []struct {
		sym, concern string
	}{
		{"midpoint", "untyped-param"},
		{"midpoint", "untyped-return"},
		{"parse", "any"},
		{"forcePoint", "as-any"},
		{"firstX", "non-null-assertion"},
		{"bad", "ts-ignore"},
		{"reinterpret", "as-unknown"},
	}
	for _, c := range cases {
		if s := symByID(loose, c.sym); !hasConcern(s, c.concern) {
			t.Errorf("legacy/loose %s: want concern %q, got %v", c.sym, c.concern, concernsOf(s))
		}
	}

	// Plain JS: every function is "untyped", var is flagged, == is loose.
	util := pkgByID(ps, "legacy/util")
	if s := symByID(util, "equalsLoose"); !hasConcern(s, "untyped") || !hasConcern(s, "loose-equality") {
		t.Errorf("equalsLoose concerns = %v (want untyped + loose-equality)", concernsOf(symByID(util, "equalsLoose")))
	}
	if s := symByID(util, "LEGACY_MODE"); !hasConcern(s, "var") {
		t.Errorf("LEGACY_MODE concerns = %v (want var)", concernsOf(symByID(util, "LEGACY_MODE")))
	}

	// An untyped exported const (no annotation, non-literal value) is flagged.
	web := pkgByID(ps, "web/app")
	if s := symByID(web, "registry"); !hasConcern(s, "untyped-export") {
		t.Errorf("registry concerns = %v (want untyped-export)", concernsOf(symByID(web, "registry")))
	}
}

func concernsOf(s *schema.Symbol) []string {
	if s == nil {
		return nil
	}
	return s.Concerns
}

// TestDoubleRunByteIdentical is the determinism deliverable: two full
// extract+emit passes over the fixture produce byte-identical output despite the
// parallel worker pool.
func TestDoubleRunByteIdentical(t *testing.T) {
	run := func() []byte {
		ps, err := New().Extract("testdata/proj")
		if err != nil {
			t.Fatal(err)
		}
		langs := langSet(ps)
		idx, shards := emit.Manifest(ps, "", langs)
		out, err := emit.Combined(idx, shards)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if string(run()) != string(run()) {
		t.Fatalf("TS/JS extract+emit not byte-identical across runs")
	}
}

func langSet(ps []schema.Package) []string {
	seen := map[string]bool{}
	for _, p := range ps {
		seen[p.Language] = true
	}
	var out []string
	for l := range seen {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

func TestGolden(t *testing.T) {
	ps, err := New().Extract("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}
	idx, shards := emit.Manifest(ps, "", langSet(ps))
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
		t.Fatalf("golden mismatch; re-run with -update if intended.")
	}
}
