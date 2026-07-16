# Explorer: hierarchical navigation + priority-based, memory-bounded loading

Status: design proposal (targets v0.9.0). Supersedes the flat force-directed
archipelago as the explorer's scale model.

## The problem (confirmed)

`ax serve` on Kafka (~6,390 packages) loads the shell and then freezes. The
cause is **compute, not transfer**: `buildWorld()` lays islands out with an
iterative force-directed relaxation —

    for it in 0..420:            # 420 passes
      for i in islands:
        for j > i:               # every pair
          separate/repulse

— i.e. `420 · n²/2` ≈ **8.6 billion** operations, synchronously, before the
first island is drawn. This layout predates the lazy WASM work (the v0.7.0
embedded explorer has it too, on top of a 314 MB parse). Two deeper truths:

1. Laying out *every* package at once is inherently unscalable — O(n²) here,
   but even an O(n) layout still renders and hit-tests thousands of nodes per
   frame, and holds the whole graph in memory (a real risk of exhausting the
   browser on a giant repo).
2. Thousands of flat islands are not *navigable*. A large codebase is a
   hierarchy; exploration is drill-down, not "see everything."

## Goals

1. **Hierarchical navigation** — lay out and render only the current level
   (bounded breadth), descend/ascend through the package hierarchy.
2. **Priority-based loading** — use idle capacity to pre-load what is *likely
   to be needed next*, so we neither stall while capacity is free nor insist on
   loading everything at once. Moving the viewport re-prioritizes.
3. **Memory-bounded** — under memory pressure, evict detail that is not
   currently needed (keep the skeleton + the visible level), so a gigantic repo
   degrades gracefully instead of crashing the tab.
4. **A loading *algebra*** — model the above with `goforge.dev/cadence`'s
   strategy vocabulary rather than ad-hoc flags, so "when/where/how a region
   loads" is a declared, composable, swappable value.

## The cadence fit

cadence separates a region's *definition* from its *strategy*:

    Strategy{Kind, Where, On}
      Kind:  Eager | Deferred | Live
      Where: Server | Client            // Deferred: who computes it
      On:    OnLoad | OnVisible | OnHover // Deferred: what triggers it
    Policy.StrategyFor(regionID, ctx, Profile) Strategy   // per-region assignment
    Profile: "the seam adaptive policies read cost and timing data from"

This is almost exactly the model we need. The explorer becomes the **client-side
interpreter** cadence's docs anticipate ("a TEA-style client interpreter" that
"computes from shipped data" and does "true compute-skip — never rendering an
off-screen region"). We import cadence for the *value vocabulary*
(`Strategy`/`Policy`/`Trigger`/`Profile`) — pure, stdlib-only, host-agnostic —
and supply our own interpreter over graph data. We do **not** use cadence's
`Tree`/`Diff` HTML substrate; our content is canvas, not markup.

Mapping:

| Explorer region              | Strategy                          |
|------------------------------|-----------------------------------|
| current level's children     | `Eager` (arrives with the level)  |
| a package scrolled/zoomed in  | `Deferred{Client, OnVisible}`     |
| a hovered / adjacent target  | `Deferred{Client, OnHover}` (prefetch) |
| `ax serve --watch` re-assay  | `Live`                            |

The priority scheduler + eviction are precisely the **adaptive `Policy`**
cadence leaves for "later," reading a `Profile` we fill with viewport intent
and memory pressure.

## Data model

### Generation / serving (no on-disk schema change)

The index already carries every `PackageEntry` (id, path, `symbol_count`,
`shard`, `is_entrypoint`, …). The server derives a **hierarchy tree** by
splitting package paths into segments (module → directories → package), and
computes per-node aggregates:

- `descendantSymbols`, `childCount`, `hasEntrypoint`
- inter-subtree couplings at each level (roll the existing call-edge
  aggregation up to the level's grouping) — for gentle intra-level clustering.

New endpoints (shards unchanged):

    GET /api/tree                      -> root level: top-level groups only
    GET /api/tree?node=<path>          -> the children of one node (one level)
    GET /api/shard?path=<shardPath>    -> one package's symbols (as today)

Up-front payload becomes O(top-level breadth) — Kafka's root is a handful of
groups — instead of O(all packages). Descending fetches one level at a time.

### Layout (per level)

Replace the O(420·n²) relaxation with **O(n) deterministic placement**: the
hashed-sunflower the intra-island planet layout already uses, optionally
followed by a single O(n log n) Barnes-Hut / grid pass driven by sibling
couplings for soft clustering. `n` is one level's breadth, so this is instant
and stable.

## Loading algebra (native Go, then wired into WASM)

A new `internal/explorer/loader` package, natively testable (no syscall/js),
importing `goforge.dev/cadence`:

- **Region ids**: `level:<path>`, `pkg:<id>`, `sym:<ref>`.
- **Scheduler**: a bounded-concurrency queue keyed by priority. Priority is
  derived from intent — `visible > hovered > adjacent-to-visible > distant`.
  Runs work while in-flight < cap and capacity is free (browser side:
  `requestIdleCallback`); coalesces duplicate requests; **cancels/deprioritizes
  stale work** when the viewport moves (re-prioritization is the point).
- **Eviction**: an LRU over hydrated shards/detail under a byte budget. Under
  pressure, drop least-recently-needed detail while pinning the tree skeleton
  and the current level. The pressure signal rides in cadence's `Profile`.
- **Policy**: an adaptive `cadence.Policy` that reads the `Profile` (intent +
  pressure) and returns the `Strategy` per region — the single place the
  "algebra" lives.

The scheduler, eviction LRU, hierarchy builder, and layout math are all pure Go
with unit tests. The browser layer (viewport → intent, `requestIdleCallback`,
`performance.memory` pressure) is the thin, browser-only wiring on top.

## Navigation UX

Start at root groups. Zoom/click a group → descend (fetch its one level).
Breadcrumb ascends. A package → its symbols (the existing island interior).
Symbol → card. Only the current level is laid out, rendered, and hit-tested;
descending/ascending swaps levels. Prefetch warms the next likely level/package
during idle time; eviction reclaims far-away detail under pressure.

## Phasing

- **P1 — unblock + hierarchy.** Server `/api/tree`; O(n) layout; client
  level-navigation. Kafka renders and is navigable. (Go-testable: tree builder,
  aggregates, layout math. Canvas nav: hand off for visual check.)
- **P2 — loading algebra.** `internal/explorer/loader` (scheduler + LRU +
  adaptive Policy over cadence), fully unit-tested; wire into `cmd/axwasm`.
- **P3 — intent + pressure.** Viewport→priority, `requestIdleCallback`
  prefetch, `performance.memory`-driven eviction; adaptive Policy tuning.

## Verification constraint

This dev environment has no browser or JS runtime, so canvas/WASM *runtime*
behavior cannot be self-verified here. Every phase lands its logic as
native-testable Go (hierarchy, aggregates, layout, scheduler, eviction, policy)
with tests, and hands the canvas/browser wiring off for a visual check before
any tag. No public release ships on unverified canvas behavior.
```
