package emit

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"goforge.dev/assayxport/internal/schema"
)

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
			Symbols: []schema.Symbol{{ID: "Main", Name: "main", Kind: "func", Visibility: "unexported",
				VisibilityIdiom: "capitalized", Location: schema.Location{File: "a/a.go", Line: 1, Col: 1, EndLine: 1},
				Complexity: schema.DeferredComplexity(), Doc: schema.Doc{Format: "godoc"}, IsEntrypoint: true}},
		},
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
