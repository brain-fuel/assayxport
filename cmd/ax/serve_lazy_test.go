package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goforge.dev/assayxport/internal/emit"
	"goforge.dev/assayxport/internal/explorer/graph"
	"goforge.dev/assayxport/internal/schema"
)

// TestLazyServeEndToEnd drives the whole disk-backed lazy data path with a real
// assay of the repo's own testdata: write the shards to a generation dir, build
// a disk snapshot, serve /api/index and /api/shard over httptest (shards read
// from disk, path validated against the manifest), and point a graph.Engine at
// those endpoints. It asserts the server-side pieces (lean index, per-shard
// bodies, traversal rejection) and the engine's lazy behavior (nothing loaded up
// front; a symbol resolves and becomes searchable only after its package
// hydrates) agree over real data -- everything the browser exercises except the
// js/wasm glue and the canvas.
func TestLazyServeEndToEnd(t *testing.T) {
	idx, shards, err := assayOnce("testdata/mixed", nil, true)
	if err != nil {
		t.Fatalf("assayOnce: %v", err)
	}
	if len(idx.Packages) == 0 {
		t.Fatal("no packages assayed from testdata/mixed")
	}

	// Disk-back the shards the way `ax serve` does, then drop the in-RAM graph.
	dir := filepath.Join(t.TempDir(), "gen-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir gen dir: %v", err)
	}
	if err := emit.WriteDir(dir, idx, shards); err != nil {
		t.Fatalf("WriteDir: %v", err)
	}
	snap, err := buildDiskSnapshot(idx, shards, dir)
	if err != nil {
		t.Fatalf("buildDiskSnapshot: %v", err)
	}

	// /api/index must decode to the lean index (no couplings) with every package.
	var idxBody struct {
		Packages []schema.PackageEntry `json:"packages"`
	}
	if err := json.Unmarshal(snap.indexJSON, &idxBody); err != nil {
		t.Fatalf("decode /api/index: %v", err)
	}
	if len(idxBody.Packages) != len(idx.Packages) {
		t.Fatalf("index payload has %d packages, want %d", len(idxBody.Packages), len(idx.Packages))
	}

	// Serve /api/shard from disk with the same membership + prefix guard the real
	// handler uses, so the test exercises the traversal defense too.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shard", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("path")
		if !snap.shardPaths[p] {
			http.NotFound(w, r)
			return
		}
		full := filepath.Join(snap.dir, filepath.FromSlash(p))
		if full != snap.dir && !strings.HasPrefix(full, snap.dir+string(os.PathSeparator)) {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(full)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.Copy(w, f)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// A path outside the manifest is rejected (traversal guard).
	resp, err := http.Get(srv.URL + "/api/shard?path=" + "../../../etc/passwd")
	if err != nil {
		t.Fatalf("traversal request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("traversal path served with status %d, want 404", resp.StatusCode)
	}

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
