// Package graph is the explorer's data engine: it holds the manifest index
// and lazily hydrates per-package shards on demand, maintaining the derived
// structures the visualization needs (a symbol lookup, a reverse call-edge
// map, and a search index) incrementally as shards load.
//
// It has no dependency on syscall/js, so it builds and tests on every GOOS.
// The one browser-specific piece -- how a shard is actually fetched -- is a
// Fetcher function injected by the caller. In the browser (cmd/axwasm) that
// is a wrapper around the DOM fetch API; in tests it is a map lookup. The
// engine is the reason a 6000-package tree is explorable in a browser: the
// world lays out from the index alone (package identity and symbol counts),
// and a package's symbols are pulled across the wire only when something --
// entering its island, opening a card, a search that reaches into it --
// actually needs them.
package graph

import (
	"fmt"
	"sort"
	"strings"

	"goforge.dev/assayxport/internal/explorer/hierarchy"
	"goforge.dev/assayxport/internal/explorer/layout"
	"goforge.dev/assayxport/internal/explorer/loader"
	"goforge.dev/assayxport/internal/schema"
)

// defaultBudget is the shard cache's default byte budget (a browser-friendly
// ~48 MB of estimated shard weight). Eviction only ever fires above it, and
// never on a pinned shard, so normal sessions never evict.
const defaultBudget int64 = 48 << 20

// prefetchCap bounds how many shard prefetches are in flight at once.
const prefetchCap = 6

// approxSize estimates a shard's in-memory weight for the cache budget. Exact
// bytes do not matter -- the budget is a coarse ceiling -- so a per-symbol
// constant plus a per-call-edge term is enough to rank and bound.
func approxSize(sh schema.Shard) int64 {
	var n int64
	for _, s := range sh.Symbols {
		n += 512 + int64(len(s.Calls))*64
	}
	if n == 0 {
		n = 256
	}
	return n
}

// Fetcher loads one shard by its manifest shard path (the PackageEntry.Shard
// value, e.g. ".assayxport/calc.json"). It blocks until the shard is
// available or errors. The engine calls it at most once per shard path and
// caches the result, so a Fetcher need not memoize.
type Fetcher func(shardPath string) (schema.Shard, error)

// SymLoc locates a symbol: the id of the package it lives in and the symbol
// itself. Returned by Symbol so a caller has both the detail and the owning
// package without a second lookup.
type SymLoc struct {
	PkgID  string        `json:"pkg_id"`
	Symbol schema.Symbol `json:"symbol"`
}

// Caller is one inbound call edge to a symbol: the ref of the calling symbol
// (package-id#symbol-id), the edge kind, and the merged call-site count. The
// reverse of a schema.Call, accumulated as shards load.
type Caller struct {
	From  string `json:"from"`
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// Match is one search hit. Kind is "package" or a symbol kind; Ref is the
// package-id#symbol-id for a symbol hit or the package id for a package hit;
// PkgID is the owning package. Score orders results (higher is better).
type Match struct {
	Ref   string `json:"ref"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	PkgID string `json:"pkg_id"`
	Score int    `json:"score"`
}

// Engine holds the index and whatever shards have been hydrated so far. It is
// not safe for concurrent use; the browser drives it from a single goroutine
// (see cmd/axwasm), and tests are single-threaded.
type Engine struct {
	fetch Fetcher

	idx        schema.Index
	pkgByID    map[string]schema.PackageEntry // index entries, by package id
	pkgByShard map[string]string              // shard path -> package id
	tree       *hierarchy.Tree                // package hierarchy for level navigation

	loaded   map[string]bool     // shard paths already hydrated
	symIndex map[string]SymLoc   // ref -> location, for loaded shards
	callers  map[string][]Caller // callee ref -> inbound edges, incremental

	// Loading algebra: the priority scheduler drives background prefetch, the
	// cache bounds memory with LRU eviction (pinned shards excepted).
	sched *loader.Scheduler
	cache *loader.Cache
}

// New builds an Engine over idx, using fetch to hydrate shards on demand. The
// index is retained as-is; no shards are loaded until EnsureShard (directly
// or via Search) asks for one.
func New(idx schema.Index, fetch Fetcher) *Engine {
	e := &Engine{
		fetch:      fetch,
		idx:        idx,
		pkgByID:    make(map[string]schema.PackageEntry, len(idx.Packages)),
		pkgByShard: make(map[string]string, len(idx.Packages)),
		tree:       hierarchy.Build(idx),
		loaded:     make(map[string]bool),
		symIndex:   make(map[string]SymLoc),
		callers:    make(map[string][]Caller),
		sched:      loader.NewScheduler(prefetchCap),
		cache:      loader.NewCache(defaultBudget),
	}
	for _, pe := range idx.Packages {
		e.pkgByID[pe.ID] = pe
		e.pkgByShard[pe.Shard] = pe.ID
	}
	return e
}

// Prefetch queues the shards of pkgIDs for background loading at the given
// intent's priority. Already-loaded packages are skipped. It only enqueues;
// NextPrefetch hands the actual work out under the concurrency cap.
func (e *Engine) Prefetch(pkgIDs []string, in loader.Intent) {
	prio := loader.Priority(in)
	for _, id := range pkgIDs {
		pe, ok := e.pkgByID[id]
		if !ok || e.loaded[pe.Shard] {
			continue
		}
		e.sched.Want(id, prio)
	}
}

// NextPrefetch returns the next shard paths to fetch now (highest priority,
// up to the free concurrency slots). The browser fetches each via EnsureShard,
// whose completion frees a slot for the next NextPrefetch call.
func (e *Engine) NextPrefetch() []string { return e.sched.Next() }

// PinPkg / UnpinAll protect shards from eviction. The browser pins the open
// package (and clears pins on navigate) so what you are looking at is never
// dropped.
func (e *Engine) PinPkg(pkgID string) { e.cache.Pin(pkgID) }
func (e *Engine) UnpinAll()           { e.cache.UnpinAll() }

// SetBudget adjusts the cache's byte budget (the browser lowers it under memory
// pressure). A budget <= 0 disables eviction.
func (e *Engine) SetBudget(b int64) { e.cache.Budget = b }

// CacheBytes reports the estimated bytes currently held (for diagnostics).
func (e *Engine) CacheBytes() int64 { return e.cache.Total() }

// Evict drops the least-recently-used unpinned shards until the cache fits its
// budget, returning the package ids evicted so the browser can release their
// symbols too. Each dropped shard's symbols leave the symbol index and its
// loaded flag is cleared, so re-opening it re-fetches. Returns nil when within
// budget.
func (e *Engine) Evict() []string {
	pids := e.cache.Overflow() // package ids, LRU-first, unpinned
	if len(pids) == 0 {
		return nil
	}
	for _, pid := range pids {
		for ref, loc := range e.symIndex {
			if loc.PkgID == pid {
				delete(e.symIndex, ref)
			}
		}
		if pe, ok := e.pkgByID[pid]; ok {
			delete(e.loaded, pe.Shard)
		}
		e.cache.Forget(pid)
	}
	return pids
}

// LevelNode is one child of a navigation level with its placed position: a
// hierarchy entry (group or package, with subtree aggregates) plus the center
// and radius the client draws it at.
type LevelNode struct {
	hierarchy.LevelEntry
	X float64 `json:"x"`
	Y float64 `json:"y"`
	R float64 `json:"r"`
}

// LevelView is one navigable level: the node at path and its immediate children,
// each already laid out. The client renders only this -- a handful to a few
// hundred nodes -- never the whole tree, which is what makes a 6000-package
// repo explorable. ok is false for an unknown path.
type LevelView struct {
	Path        string      `json:"path"`
	Name        string      `json:"name"`
	IsPkg       bool        `json:"is_pkg"`
	SelfPkgID   string      `json:"self_pkg_id,omitempty"`
	SelfShard   string      `json:"self_shard,omitempty"`
	SelfSymbols int         `json:"self_symbols,omitempty"`
	Nodes       []LevelNode `json:"nodes"`
}

// Level returns the positioned children of the node at path (path "" is the
// root). Positions are deterministic (layout.Place), so revisiting a level
// redraws it identically. It touches no shards: a level is pure index-derived
// structure, so navigation is instant and never blocks on the network.
func (e *Engine) Level(path string) (LevelView, bool) {
	lv, ok := e.tree.Level(path)
	if !ok {
		return LevelView{}, false
	}
	items := make([]layout.Item, len(lv.Children))
	for i, c := range lv.Children {
		items[i] = layout.Item{ID: c.Path, Radius: layout.RadiusFor(c.Symbols)}
	}
	pos := layout.Place(items)
	view := LevelView{
		Path: lv.Path, Name: lv.Name, IsPkg: lv.IsPkg,
		SelfPkgID: lv.SelfPkgID, SelfShard: lv.SelfShard, SelfSymbols: lv.SelfSymbols,
		Nodes: make([]LevelNode, len(lv.Children)),
	}
	for i, c := range lv.Children {
		view.Nodes[i] = LevelNode{LevelEntry: c, X: pos[i].X, Y: pos[i].Y, R: items[i].Radius}
	}
	return view, true
}

// Index returns the manifest index. The world (islands sized by symbol count,
// placed by a hash of the package id) is built entirely from this, so the map
// is drawable before any shard is fetched.
func (e *Engine) Index() schema.Index { return e.idx }

// EnsureShard hydrates the shard at shardPath if it is not already loaded,
// registering every symbol in the symbol index and folding its outbound call
// edges into the reverse-edge (callers) map. Loading the same shard twice is a
// no-op that returns the cached copy, so callers can call it freely before any
// per-package operation without tracking load state themselves.
func (e *Engine) EnsureShard(shardPath string) (schema.Shard, error) {
	if e.loaded[shardPath] {
		return e.shardOf(shardPath), nil
	}
	sh, err := e.fetch(shardPath)
	if err != nil {
		return schema.Shard{}, fmt.Errorf("load shard %s: %w", shardPath, err)
	}
	e.integrate(sh)
	e.loaded[shardPath] = true
	return sh, nil
}

// EnsureShardForPkg hydrates the shard owning package pkgID. It resolves the
// package's shard path from the index, so a caller that knows only a package
// id (an island click, a symbol ref split on '#') does not need the shard
// path.
func (e *Engine) EnsureShardForPkg(pkgID string) (schema.Shard, error) {
	pe, ok := e.pkgByID[pkgID]
	if !ok {
		return schema.Shard{}, fmt.Errorf("unknown package %q", pkgID)
	}
	if e.loaded[pe.Shard] {
		e.cache.Touch(pkgID) // re-access keeps it hot (survives eviction)
		e.sched.Done(pkgID)  // clear any queued/inflight mark so no slot leaks
		e.sched.Forget(pkgID)
		return e.shardOf(pe.Shard), nil
	}
	sh, err := e.EnsureShard(pe.Shard)
	// Loading-algebra bookkeeping, keyed by package id: free the scheduler slot
	// (success or failure) and record the shard's weight in the LRU cache.
	e.sched.Done(pkgID)
	e.sched.Forget(pkgID)
	if err != nil {
		return schema.Shard{}, err
	}
	e.cache.Note(pkgID, approxSize(sh))
	return sh, nil
}

// integrate registers a freshly loaded shard's symbols and reverse edges. It
// is idempotent per shard only because EnsureShard guards on e.loaded; calling
// it twice for the same shard would double-count callers.
func (e *Engine) integrate(sh schema.Shard) {
	pkgID := sh.Package.ID
	for _, s := range sh.Symbols {
		ref := pkgID + "#" + s.ID
		e.symIndex[ref] = SymLoc{PkgID: pkgID, Symbol: s}
		from := ref
		for _, c := range s.Calls {
			// Only internal edges name a resolvable target in this manifest;
			// external/builtin/dynamic/unresolved edges have no Ref to hang a
			// reverse edge on.
			if c.Ref == "" {
				continue
			}
			e.callers[c.Ref] = append(e.callers[c.Ref], Caller{From: from, Kind: c.Kind, Count: c.Count})
		}
	}
}

// Symbol returns the location of ref (package-id#symbol-id) if its shard is
// loaded. ok is false when ref is unknown or its shard has not been hydrated
// yet; a caller that wants to force the load calls EnsureShardForPkg first.
func (e *Engine) Symbol(ref string) (SymLoc, bool) {
	loc, ok := e.symIndex[ref]
	return loc, ok
}

// Callers returns the inbound call edges to ref discovered in the shards
// loaded so far, most-called first. Because it is incremental, the list grows
// as more shards load; it is complete for ref only once every package that
// could call ref has been hydrated. Callers of a symbol therefore reflect
// "what has been explored", which matches the lazy model: the map fills in as
// you travel it.
func (e *Engine) Callers(ref string) []Caller {
	cs := e.callers[ref]
	if len(cs) == 0 {
		return nil
	}
	out := make([]Caller, len(cs))
	copy(out, cs)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

// Loaded reports whether the shard at shardPath has been hydrated.
func (e *Engine) Loaded(shardPath string) bool { return e.loaded[shardPath] }

// shardOf reconstructs a Shard view for an already-loaded shard path from the
// symbol index. It is used only to satisfy EnsureShard's return on a cache
// hit; the visualization keeps its own copy of the symbols from the first
// load, so this is a convenience, not the source of truth.
func (e *Engine) shardOf(shardPath string) schema.Shard {
	for _, pe := range e.idx.Packages {
		if pe.Shard != shardPath {
			continue
		}
		sh := schema.Shard{
			SchemaVersion: e.idx.SchemaVersion,
			Package: schema.PackageInfo{
				ID: pe.ID, Language: pe.Language, Path: pe.Path,
				Name: pe.Name, Doc: pe.Doc, Level: pe.Level,
				Members: pe.Members, IsEntrypoint: pe.IsEntrypoint,
				Invocation: pe.Invocation,
			},
		}
		for ref, loc := range e.symIndex {
			if loc.PkgID == pe.ID {
				_ = ref
				sh.Symbols = append(sh.Symbols, loc.Symbol)
			}
		}
		sort.Slice(sh.Symbols, func(i, j int) bool { return sh.Symbols[i].ID < sh.Symbols[j].ID })
		return sh
	}
	return schema.Shard{}
}

// Search ranks packages (always, from the index) and symbols (from loaded
// shards) against query q. It never triggers a fetch: results are drawn from
// the index plus whatever shards are already hydrated, so search is instant
// and its symbol coverage grows as the user explores. limit caps the results
// (<=0 means the built-in default). Matching is case-insensitive substring;
// an exact or prefix hit scores above a mid-string hit, and packages and
// exported symbols are nudged above unexported ones so the obvious target
// surfaces first.
func (e *Engine) Search(q string, limit int) []Match {
	q = strings.TrimSpace(strings.ToLower(q))
	if q == "" {
		return nil
	}
	if limit <= 0 {
		limit = 40
	}
	var out []Match
	for _, pe := range e.idx.Packages {
		if s := score(pe.Name, q); s > 0 {
			out = append(out, Match{Ref: pe.ID, Name: pe.Name, Kind: "package", PkgID: pe.ID, Score: s + 5})
		} else if s := score(pe.ID, q); s > 0 {
			out = append(out, Match{Ref: pe.ID, Name: pe.Name, Kind: "package", PkgID: pe.ID, Score: s + 3})
		}
	}
	for ref, loc := range e.symIndex {
		s := score(loc.Symbol.Name, q)
		if s == 0 {
			continue
		}
		if loc.Symbol.Visibility == "exported" || loc.Symbol.Visibility == "public" {
			s += 2
		}
		out = append(out, Match{Ref: ref, Name: loc.Symbol.Name, Kind: loc.Symbol.Kind, PkgID: loc.PkgID, Score: s})
	}
	// Stable order: score desc, then name, then ref, so equal-score results
	// don't reshuffle between keystrokes (symIndex map iteration is random).
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Ref < out[j].Ref
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// score rates how well name matches the lowercased query q: 0 for no match,
// higher for a tighter match (exact > prefix > word-boundary > substring).
func score(name, q string) int {
	n := strings.ToLower(name)
	switch {
	case n == q:
		return 100
	case strings.HasPrefix(n, q):
		return 60
	}
	i := strings.Index(n, q)
	if i < 0 {
		return 0
	}
	// A match right after a separator (., /, _) reads as a word-boundary hit.
	if i > 0 {
		switch n[i-1] {
		case '.', '/', '_':
			return 40
		}
	}
	return 20
}
