package emit

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"goforge.dev/assayxport/internal/schema"
)

// collisionPkgs returns two packages whose base shard paths collide:
// Path="" and Path="_root" both map to .assayxport/_root.json without the guard.
func collisionPkgs() []schema.Package {
	return []schema.Package{
		{
			ID: "example.com/root", Language: "go", Path: "", Name: "root", Doc: "Root package.",
			Symbols: []schema.Symbol{
				{ID: "RootFunc", Name: "RootFunc", Kind: "func", Visibility: "exported",
					VisibilityIdiom: "capitalized", Location: schema.Location{File: "root.go", Line: 1, Col: 1, EndLine: 1},
					Complexity: schema.DeferredComplexity(), Doc: schema.Doc{Format: "godoc"},
					Signature: &schema.Signature{Params: []schema.Param{}, Returns: []schema.Param{}}},
			},
		},
		{
			ID: "example.com/_root", Language: "go", Path: "_root", Name: "_root", Doc: "Underscore-root package.",
			Symbols: []schema.Symbol{
				{ID: "URoot", Name: "URoot", Kind: "func", Visibility: "exported",
					VisibilityIdiom: "capitalized", Location: schema.Location{File: "_root/_root.go", Line: 1, Col: 1, EndLine: 1},
					Complexity: schema.DeferredComplexity(), Doc: schema.Doc{Format: "godoc"},
					Signature: &schema.Signature{Params: []schema.Param{}, Returns: []schema.Param{}}},
			},
		},
	}
}

func samplePkgs() []schema.Package {
	return []schema.Package{
		{
			ID: "example.com/s/b", Language: "go", Path: "b", Name: "b", Doc: "Package b.",
			Symbols: []schema.Symbol{
				{ID: "Z", Name: "Z", Kind: "func", Visibility: "exported", VisibilityIdiom: "capitalized",
					Location: schema.Location{File: "b/b.go", Line: 9, Col: 1, EndLine: 9}, Complexity: schema.DeferredComplexity(),
					Doc: schema.Doc{Format: "godoc"}, Signature: &schema.Signature{Params: []schema.Param{}, Returns: []schema.Param{}}},
				{ID: "A", Name: "A", Kind: "func", Visibility: "exported", VisibilityIdiom: "capitalized",
					Location: schema.Location{File: "b/b.go", Line: 3, Col: 1, EndLine: 3}, Complexity: schema.DeferredComplexity(),
					Doc: schema.Doc{Format: "godoc"}, Signature: &schema.Signature{Params: []schema.Param{}, Returns: []schema.Param{}}},
			},
		},
		{
			ID: "example.com/s/a", Language: "go", Path: "a", Name: "a", Doc: "Package a.",
			Level:        "package",
			Members:      []string{"x.y"},
			IsEntrypoint: true,
			Invocation:   &schema.Invocation{Kind: "binary", How: "go run ./a"},
			Symbols: []schema.Symbol{{ID: "Main", Name: "main", Kind: "func", Visibility: "unexported",
				VisibilityIdiom: "capitalized", Location: schema.Location{File: "a/a.go", Line: 1, Col: 1, EndLine: 1},
				Complexity: schema.DeferredComplexity(), Doc: schema.Doc{Format: "godoc"}, IsEntrypoint: true}},
		},
	}
}

func TestManifestCopiesPackageUnitFields(t *testing.T) {
	idx, shards := Manifest(samplePkgs(), "example.com/s", []string{"go"})
	// idx.Packages[0] is example.com/s/a (sorted by ID).
	pe := idx.Packages[0]
	if pe.ID != "example.com/s/a" {
		t.Fatalf("Packages[0].ID = %q, want example.com/s/a (test probes the wrong entry)", pe.ID)
	}
	if pe.Level != "package" {
		t.Errorf("PackageEntry.Level = %q, want %q", pe.Level, "package")
	}
	if len(pe.Members) != 1 || pe.Members[0] != "x.y" {
		t.Errorf("PackageEntry.Members = %v, want [x.y]", pe.Members)
	}
	if !pe.IsEntrypoint {
		t.Errorf("PackageEntry.IsEntrypoint = false, want true")
	}
	if pe.Invocation == nil || pe.Invocation.Kind != "binary" || pe.Invocation.How != "go run ./a" {
		t.Errorf("PackageEntry.Invocation = %+v, want {binary, go run ./a}", pe.Invocation)
	}
	// The shard's PackageInfo must also carry Level and Members.
	pi := shards[pe.Shard].Package
	if pi.Level != "package" {
		t.Errorf("PackageInfo.Level = %q, want %q", pi.Level, "package")
	}
	if len(pi.Members) != 1 || pi.Members[0] != "x.y" {
		t.Errorf("PackageInfo.Members = %v, want [x.y]", pi.Members)
	}
	if !pi.IsEntrypoint {
		t.Errorf("PackageInfo.IsEntrypoint = false, want true")
	}
	if pi.Invocation == nil || pi.Invocation.Kind != "binary" || pi.Invocation.How != "go run ./a" {
		t.Errorf("PackageInfo.Invocation = %+v, want {binary, go run ./a}", pi.Invocation)
	}
}

func TestManifestSortsAndCounts(t *testing.T) {
	idx, shards := Manifest(samplePkgs(), "example.com/s", []string{"go"})
	if idx.SchemaVersion != "1" || idx.Tool != "assayxport" {
		t.Fatalf("index header = %+v", idx)
	}
	if len(idx.Packages) != 2 || idx.Packages[0].ID != "example.com/s/a" {
		t.Fatalf("packages not sorted by id: %+v", idx.Packages)
	}
	b := idx.Packages[1]
	if b.ID != "example.com/s/b" || b.SymbolCount != 2 || b.Shard != ".assayxport/b.json" {
		t.Fatalf("pkg b entry = %+v", b)
	}
	if idx.Packages[0].EntrypointCount != 1 {
		t.Fatalf("pkg a entrypoint_count = %d, want 1", idx.Packages[0].EntrypointCount)
	}
	// symbols sorted by (file,line,name): A(line3) before Z(line9)
	sh := shards[".assayxport/b.json"]
	if sh.Symbols[0].ID != "A" || sh.Symbols[1].ID != "Z" {
		t.Fatalf("shard b symbols not sorted: %+v", sh.Symbols)
	}
}

func TestWriteDirDeterministic(t *testing.T) {
	dir := t.TempDir()
	idx, shards := Manifest(samplePkgs(), "example.com/s", []string{"go"})
	if err := WriteDir(dir, idx, shards); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(dir, "assayxport.json"))
	if err != nil {
		t.Fatal(err)
	}
	if first[len(first)-1] != '\n' {
		t.Fatalf("index must end with newline")
	}
	if !bytes.Contains(first, []byte("\"schema_version\": \"1\"")) {
		t.Fatalf("index not 2-space indented JSON: %s", first)
	}
	// Re-emit; bytes identical.
	dir2 := t.TempDir()
	if err := WriteDir(dir2, idx, shards); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(dir2, "assayxport.json"))
	if !bytes.Equal(first, second) {
		t.Fatalf("index not deterministic across runs")
	}
	if _, err := os.Stat(filepath.Join(dir, ".assayxport", "b.json")); err != nil {
		t.Fatalf("shard b.json not written: %v", err)
	}
}

func TestManifestShardCollisionGuard(t *testing.T) {
	idx, shards := Manifest(collisionPkgs(), "example.com", []string{"go"})
	if len(idx.Packages) != 2 {
		t.Fatalf("want 2 packages, got %d", len(idx.Packages))
	}
	if len(shards) != 2 {
		t.Fatalf("want 2 shards, got %d: collision guard dropped a shard", len(shards))
	}
	// Each index entry's Shard must point at a real key in the shards map.
	for _, pe := range idx.Packages {
		if _, ok := shards[pe.Shard]; !ok {
			t.Errorf("package %q: Shard=%q not in shards map (lost shard)", pe.ID, pe.Shard)
		}
	}
	// The two shard paths must be distinct.
	s0, s1 := idx.Packages[0].Shard, idx.Packages[1].Shard
	if s0 == s1 {
		t.Errorf("both packages share the same shard path %q; collision guard did not fire", s0)
	}
	// Each shard must contain the right symbols (no silent overwrite).
	for _, pe := range idx.Packages {
		shard := shards[pe.Shard]
		if shard.Package.ID != pe.ID {
			t.Errorf("shard at %q: package ID=%q, want %q", pe.Shard, shard.Package.ID, pe.ID)
		}
		if len(shard.Symbols) == 0 {
			t.Errorf("shard at %q (package %q): no symbols", pe.Shard, pe.ID)
		}
	}
}

func TestWriteDirPrunesStaleShards(t *testing.T) {
	dir := t.TempDir()
	pkgs := samplePkgs()
	idx, shards := Manifest(pkgs, "example.com/s", []string{"go"})

	// First write.
	if err := WriteDir(dir, idx, shards); err != nil {
		t.Fatal(err)
	}

	// Plant a stale shard that the second write should remove.
	stale := filepath.Join(dir, ".assayxport", "stale.json")
	if err := os.WriteFile(stale, []byte(`{"stale":true}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second write with same packages.
	if err := WriteDir(dir, idx, shards); err != nil {
		t.Fatal(err)
	}

	// Stale file must be gone.
	if _, err := os.Stat(stale); err == nil {
		t.Error("stale.json was not pruned after rescan")
	}

	// Current shards must still exist.
	for sp := range shards {
		full := filepath.Join(dir, filepath.FromSlash(sp))
		if _, err := os.Stat(full); err != nil {
			t.Errorf("current shard %q missing after prune: %v", sp, err)
		}
	}

	// Root index must still exist.
	if _, err := os.Stat(filepath.Join(dir, "assayxport.json")); err != nil {
		t.Errorf("assayxport.json missing after prune: %v", err)
	}
}
