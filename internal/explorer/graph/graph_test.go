package graph

import (
	"errors"
	"testing"

	"goforge.dev/assayxport/internal/schema"
)

// buildFixture returns a two-package index and a fetcher over their shards,
// plus a counter of how many times each shard path was fetched so a test can
// assert lazy loading actually deferred (and cached) the fetch.
func buildFixture() (schema.Index, Fetcher, map[string]int) {
	idx := schema.Index{
		SchemaVersion: "2",
		Packages: []schema.PackageEntry{
			{ID: "app/a", Name: "a", Path: "a", Shard: ".assayxport/a.json", SymbolCount: 2},
			{ID: "app/b", Name: "b", Path: "b", Shard: ".assayxport/b.json", SymbolCount: 1},
		},
	}
	shards := map[string]schema.Shard{
		".assayxport/a.json": {
			Package: schema.PackageInfo{ID: "app/a", Name: "a", Path: "a"},
			Symbols: []schema.Symbol{
				{ID: "Alpha", Name: "Alpha", Kind: "func", Visibility: "exported",
					Calls: []schema.Call{{Target: "b.Beta", Kind: "internal", Ref: "app/b#Beta", Count: 3}}},
				{ID: "alphaHelper", Name: "alphaHelper", Kind: "func", Visibility: "unexported"},
			},
		},
		".assayxport/b.json": {
			Package: schema.PackageInfo{ID: "app/b", Name: "b", Path: "b"},
			Symbols: []schema.Symbol{
				{ID: "Beta", Name: "Beta", Kind: "func", Visibility: "exported"},
			},
		},
	}
	fetches := map[string]int{}
	fetch := func(path string) (schema.Shard, error) {
		fetches[path]++
		sh, ok := shards[path]
		if !ok {
			return schema.Shard{}, errors.New("no such shard")
		}
		return sh, nil
	}
	return idx, fetch, fetches
}

func TestIndexAvailableWithoutFetch(t *testing.T) {
	idx, fetch, fetches := buildFixture()
	e := New(idx, fetch)
	if got := len(e.Index().Packages); got != 2 {
		t.Fatalf("index packages = %d, want 2", got)
	}
	if len(fetches) != 0 {
		t.Fatalf("index access fetched shards: %v", fetches)
	}
	// A symbol whose shard is not loaded is simply unknown, not an error.
	if _, ok := e.Symbol("app/a#Alpha"); ok {
		t.Fatal("Symbol resolved before its shard was loaded")
	}
}

func TestEnsureShardLoadsAndCaches(t *testing.T) {
	idx, fetch, fetches := buildFixture()
	e := New(idx, fetch)

	if _, err := e.EnsureShardForPkg("app/a"); err != nil {
		t.Fatalf("EnsureShardForPkg: %v", err)
	}
	loc, ok := e.Symbol("app/a#Alpha")
	if !ok || loc.Symbol.Name != "Alpha" {
		t.Fatalf("Symbol app/a#Alpha not resolved after load: %+v ok=%v", loc, ok)
	}
	// Second load is a cache hit: no additional fetch.
	if _, err := e.EnsureShard(".assayxport/a.json"); err != nil {
		t.Fatalf("re-EnsureShard: %v", err)
	}
	if fetches[".assayxport/a.json"] != 1 {
		t.Fatalf("shard a fetched %d times, want 1", fetches[".assayxport/a.json"])
	}
	// b was never touched.
	if fetches[".assayxport/b.json"] != 0 {
		t.Fatalf("shard b fetched though never requested: %d", fetches[".assayxport/b.json"])
	}
}

func TestCallersAccumulateIncrementally(t *testing.T) {
	idx, fetch, _ := buildFixture()
	e := New(idx, fetch)

	// Before a's shard loads, nothing is known to call Beta.
	if cs := e.Callers("app/b#Beta"); len(cs) != 0 {
		t.Fatalf("callers of Beta before load = %d, want 0", len(cs))
	}
	if _, err := e.EnsureShardForPkg("app/a"); err != nil {
		t.Fatalf("load a: %v", err)
	}
	cs := e.Callers("app/b#Beta")
	if len(cs) != 1 || cs[0].From != "app/a#Alpha" || cs[0].Count != 3 {
		t.Fatalf("callers of Beta after loading a = %+v, want [{app/a#Alpha internal 3}]", cs)
	}
}

func TestSearchSpansIndexAndLoadedShards(t *testing.T) {
	idx, fetch, fetches := buildFixture()
	e := New(idx, fetch)

	// Package names come from the index with no fetch.
	got := e.Search("a", 40)
	if len(got) == 0 || got[0].Kind != "package" {
		t.Fatalf("search 'a' should surface package a first, got %+v", got)
	}
	if len(fetches) != 0 {
		t.Fatalf("search triggered a fetch: %v", fetches)
	}
	// A symbol only in b's shard is invisible until b loads.
	if hits := e.Search("Beta", 40); len(hits) != 0 {
		t.Fatalf("Beta found before b loaded: %+v", hits)
	}
	if _, err := e.EnsureShardForPkg("app/b"); err != nil {
		t.Fatalf("load b: %v", err)
	}
	hits := e.Search("Beta", 40)
	if len(hits) != 1 || hits[0].Ref != "app/b#Beta" {
		t.Fatalf("Beta not found after b loaded: %+v", hits)
	}
}

func TestEnsureShardForUnknownPkg(t *testing.T) {
	idx, fetch, _ := buildFixture()
	e := New(idx, fetch)
	if _, err := e.EnsureShardForPkg("app/nope"); err == nil {
		t.Fatal("expected error for unknown package")
	}
}

func TestSearchRanksExactAndExportedHigher(t *testing.T) {
	idx, fetch, _ := buildFixture()
	e := New(idx, fetch)
	if _, err := e.EnsureShardForPkg("app/a"); err != nil {
		t.Fatalf("load a: %v", err)
	}
	// "alpha" substring-matches both Alpha (exported) and alphaHelper
	// (unexported, prefix). Exact exported "Alpha" must outrank the helper.
	got := e.Search("alpha", 40)
	if len(got) < 2 {
		t.Fatalf("want >=2 hits for 'alpha', got %+v", got)
	}
	if got[0].Name != "Alpha" {
		t.Fatalf("top hit for 'alpha' = %q, want Alpha", got[0].Name)
	}
}
