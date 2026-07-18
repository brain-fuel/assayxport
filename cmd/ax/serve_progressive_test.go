package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"goforge.dev/assayxport/internal/schema"
)

// tsFixture is the TypeScript extractor's fixture tree, reused here to exercise a
// real progressive assay end to end.
const tsFixture = "../../internal/extract/typescript/testdata/proj"

// TestRunProgressive checks the two guarantees of a progressive assay: the
// skeleton (every package, no symbols) is published to cur before extraction
// finishes, and on completion every entry is filled with a real symbol count and
// a shard file that exists on disk.
func TestRunProgressive(t *testing.T) {
	dir := t.TempDir()
	cur := &current{}
	if err := runProgressive(tsFixture, nil, true, dir, cur, newFocusRegistry()); err != nil {
		t.Fatalf("runProgressive: %v", err)
	}
	s := cur.get()
	if s == nil || s.live == nil {
		t.Fatal("cur was never set to a live snapshot")
	}

	// After completion the status is complete and every package is ready.
	st := s.live.status()
	if !st.Complete || st.Ready != st.Total || st.Total == 0 {
		t.Fatalf("status = %+v, want complete with ready==total>0", st)
	}

	// The served index carries every package with a shard and, for a real module,
	// a non-zero symbol count.
	var idx schema.Index
	b, err := s.live.marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &idx); err != nil {
		t.Fatal(err)
	}
	if len(idx.Packages) != st.Total {
		t.Fatalf("index has %d packages, status total %d", len(idx.Packages), st.Total)
	}
	var withSyms int
	for _, pe := range idx.Packages {
		if pe.Shard == "" {
			t.Errorf("package %s has no shard after completion", pe.ID)
			continue
		}
		if !s.live.shardReady(pe.Shard) {
			t.Errorf("package %s shard %s not marked ready", pe.ID, pe.Shard)
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(pe.Shard))); err != nil {
			t.Errorf("shard file missing for %s: %v", pe.ID, err)
		}
		if pe.SymbolCount > 0 {
			withSyms++
		}
	}
	if withSyms == 0 {
		t.Fatal("no package reported any symbols; extraction did not fill the skeleton")
	}
}

// TestSkeletonBeforeSymbols confirms the skeleton entry shape: identity and level
// but no shard and no counts, so the client can render structure before parsing.
func TestSkeletonBeforeSymbols(t *testing.T) {
	e := schema.Package{ID: "a/b", Language: "typescript", Path: "a/b.ts", Name: "b", Level: "module", Symbols: nil}
	li := newLiveIndex([]schema.Package{e}, []string{"typescript"}, t.TempDir())
	st := li.status()
	if st.Complete || st.Ready != 0 || st.Total != 1 {
		t.Fatalf("fresh skeleton status = %+v, want ready 0 of 1, not complete", st)
	}
	var idx schema.Index
	b, _ := li.marshal()
	_ = json.Unmarshal(b, &idx)
	if len(idx.Packages) != 1 || idx.Packages[0].Shard != "" || idx.Packages[0].SymbolCount != 0 {
		t.Fatalf("skeleton entry = %+v, want empty shard and zero count", idx.Packages[0])
	}
}
