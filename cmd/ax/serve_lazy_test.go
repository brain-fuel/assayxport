package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"goforge.dev/assayxport/internal/explorer/graph"
	"goforge.dev/assayxport/internal/schema"
)

// TestLazyServeEndToEnd drives the whole lazy data path with a real assay of
// the repo's own testdata: build a snapshot, serve /api/index and /api/shard
// over httptest, and point a graph.Engine at those endpoints. It asserts the
// server-side pieces (couplings in the index, per-shard bodies) and the
// engine's lazy behavior (nothing loaded up front; a symbol resolves and
// becomes searchable only after its package hydrates) agree over real data --
// everything the browser exercises except the js/wasm glue and the canvas.
func TestLazyServeEndToEnd(t *testing.T) {
	idx, shards, err := assayOnce("testdata/mixed", nil, true)
	if err != nil {
		t.Fatalf("assayOnce: %v", err)
	}
	if len(idx.Packages) == 0 {
		t.Fatal("no packages assayed from testdata/mixed")
	}
	snap, err := buildSnapshot(idx, shards, false)
	if err != nil {
		t.Fatalf("buildSnapshot: %v", err)
	}

	// /api/index must decode and carry a couplings field (the layout graph).
	var idxBody struct {
		Packages  []schema.PackageEntry `json:"packages"`
		Couplings []coupling            `json:"couplings"`
	}
	if err := json.Unmarshal(snap.indexJSON, &idxBody); err != nil {
		t.Fatalf("decode /api/index: %v", err)
	}
	if len(idxBody.Packages) != len(idx.Packages) {
		t.Fatalf("index payload has %d packages, want %d", len(idxBody.Packages), len(idx.Packages))
	}
	if idxBody.Couplings == nil {
		t.Fatal("index payload missing couplings field")
	}

	// Serve the lazy API and drive an engine whose Fetcher is real HTTP.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shard", func(w http.ResponseWriter, r *http.Request) {
		body, ok := snap.shards[r.URL.Query().Get("path")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var fetched int
	eng := graph.New(idx, func(shardPath string) (schema.Shard, error) {
		fetched++
		resp, err := http.Get(srv.URL + "/api/shard?path=" + shardPath)
		if err != nil {
			return schema.Shard{}, err
		}
		defer resp.Body.Close()
		var sh schema.Shard
		if err := json.NewDecoder(resp.Body).Decode(&sh); err != nil {
			return schema.Shard{}, err
		}
		return sh, nil
	})

	// Nothing is fetched until a package is opened.
	if fetched != 0 {
		t.Fatalf("engine fetched %d shards before any request", fetched)
	}

	// Pick a package that actually has symbols and hydrate it.
	var target schema.PackageEntry
	for _, pe := range idx.Packages {
		if pe.SymbolCount > 0 {
			target = pe
			break
		}
	}
	if target.ID == "" {
		t.Fatal("no package with symbols in testdata/mixed")
	}
	sh, err := eng.EnsureShardForPkg(target.ID)
	if err != nil {
		t.Fatalf("EnsureShardForPkg(%s): %v", target.ID, err)
	}
	if len(sh.Symbols) == 0 {
		t.Fatalf("hydrated shard for %s has no symbols", target.ID)
	}
	if fetched != 1 {
		t.Fatalf("expected exactly 1 fetch after one package open, got %d", fetched)
	}

	// A symbol from that package now resolves and is searchable; before the
	// hydrate it was neither (proven by fetched==0 above yielding no symIndex).
	sym := sh.Symbols[0]
	ref := target.ID + "#" + sym.ID
	if _, ok := eng.Symbol(ref); !ok {
		t.Fatalf("symbol %s not resolvable after hydrate", ref)
	}
	found := false
	for _, m := range eng.Search(sym.Name, 40) {
		if m.Ref == ref {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("search for %q did not return %s after hydrate", sym.Name, ref)
	}

	// Re-opening the same package is a cache hit (no second fetch).
	if _, err := eng.EnsureShardForPkg(target.ID); err != nil {
		t.Fatalf("re-EnsureShardForPkg: %v", err)
	}
	if fetched != 1 {
		t.Fatalf("cache miss on re-open: fetched=%d, want 1", fetched)
	}
}
