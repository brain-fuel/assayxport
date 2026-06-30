# assayxport SP1 — design spec

> A single Go binary that extracts a codebase's public/exported API, docs,
> structure, and entrypoints into a deterministic JSON manifest, so an LLM,
> docgen, or tool can read one file at the project root instead of reparsing
> everything.

- **Date:** 2026-06-30
- **Status:** approved, pre-implementation
- **Tool:** `assayxport` (assay + export; fits the goforge assayer's-workshop naming world)
- **Repo / module:** `github.com/brain-fuel/assayxport`, Go module `goforge.dev/assayxport`
- **Local path:** `goforge.dev/assayxport`
- **License:** MIT
- **This spec covers SP1 only.** SP2 (Python), SP3 (Java), SP4 (big-O) are later sub-projects with their own specs.

## What assayxport is

`assayxport` scans a codebase and assembles a deterministic JSON manifest of its
API surface: where each symbol lives (folder/file), what package it belongs to,
its kind and visibility, its signature, its doc comment, whether it is a runnable
entrypoint, and a reserved slot for complexity. The manifest sits at the project
root so consumers (LLMs, docgen sites, tooling) load one map instead of parsing
the whole tree.

The metaphor: assaying analyzes a metal to report its exact composition;
assayxport analyzes code to report its exact API composition.

### Full program (context, not all in SP1)

A single self-contained Go binary, Go ecosystem only, **no external runtimes**
(no JVM, no CPython). Languages:

- **Go** — native `go/packages` + `go/doc` + `go/ast`, in-process, full semantic
  fidelity. (SP1.)
- **Python, Java** — tree-sitter grammars run inside the binary via **pure-Go
  wazero + WASM grammars** (no cgo; single static binary; trivial cross-compile).
  tree-sitter is syntactic, not semantic: names, kinds, visibility, signatures
  with types *as-written*, doc comments, structure, and entrypoints are
  reliable; resolved imported-type FQNs and inferred-where-unannotated types are
  not. (SP2/SP3.)
- **Big-O complexity** — heuristic, experimental, clearly labeled best-effort;
  exact static big-O is undecidable in general. (SP4.)

**SP1 scope:** schema + emitter + CLI + the Go extractor. 100% native Go,
dogfoodable on goforge's own Go repos. The tree-sitter / wazero mechanism is NOT
part of SP1.

## Architecture (SP1)

```
cmd/assayxport          CLI entry: `assayxport scan [path]`
internal/schema         Go structs for Index, PackageEntry, Shard, Symbol; versioned JSON
internal/extract        Extractor interface (language-agnostic contract)
internal/extract/golang Go extractor: go/packages + go/doc + go/ast -> []Package
internal/emit           Deterministic writer: root index + per-package shards
```

The `Extractor` interface is the seam future languages plug into; the JSON schema
is the cross-language contract (SP2/SP3 emit the same shape Go does).

```go
// internal/extract
type Extractor interface {
    // Language reports the language id this extractor handles, e.g. "go".
    Language() string
    // Extract loads all packages under root and returns them in a stable order.
    Extract(root string) ([]schema.Package, error)
}
```

## Output layout

Two artifacts, both deterministic:

- **`assayxport.json`** at the scan root — the **index**: project metadata,
  languages present, and the package list (each with id, relative path, name,
  package doc, symbol/entrypoint counts, and the shard path).
- **`.assayxport/<relative-pkg-dir>.json`** — one **shard** per package holding
  the full symbol list. `.assayxport/` is a dot-dir, treated like a cache. The
  root package (if any) uses `.assayxport/_root.json`.

Shard filenames mirror the package's module-relative directory, POSIX slashes,
with `.json` appended (e.g. package `goforge.dev/assayxport/internal/schema` at
dir `internal/schema` → `.assayxport/internal/schema.json`).

### Index schema

```json
{
  "schema_version": "1",
  "tool": "assayxport",
  "languages": ["go"],
  "root": ".",
  "module": "goforge.dev/assayxport",
  "packages": [
    {
      "id": "goforge.dev/assayxport/internal/schema",
      "language": "go",
      "path": "internal/schema",
      "name": "schema",
      "doc": "Package schema defines the assayxport manifest types.",
      "symbol_count": 12,
      "entrypoint_count": 0,
      "shard": ".assayxport/internal/schema.json"
    }
  ]
}
```

`packages` is sorted by `id`. No timestamp, no absolute path, no host data.

### Shard schema

```json
{
  "schema_version": "1",
  "package": {
    "id": "goforge.dev/assayxport/internal/schema",
    "language": "go",
    "path": "internal/schema",
    "name": "schema",
    "doc": "Package schema defines the assayxport manifest types."
  },
  "symbols": [ /* Symbol, sorted by (file, line, name) */ ]
}
```

### Symbol schema

Common shape; kind-dependent fields noted.

```json
{
  "id": "Foo.Bar",
  "name": "Bar",
  "kind": "method",
  "visibility": "exported",
  "visibility_idiom": "capitalized",
  "location": { "file": "internal/schema/foo.go", "line": 42, "col": 1, "end_line": 47 },
  "owner": "Foo",
  "doc": { "raw": "Bar reports whether ...", "format": "godoc" },
  "is_entrypoint": false,
  "complexity": { "time": null, "space": null, "method": "deferred" },

  "signature": {
    "params":  [ { "name": "x", "type": "int" } ],
    "returns": [ { "name": "", "type": "error" } ],
    "type_params": [ { "name": "T", "constraint": "any" } ],
    "receiver": { "name": "f", "type": "*Foo" },
    "variadic": false
  }
}
```

Field rules:

- `id` — package-local, stable. Package-level symbol → its name (`Foo`).
  Method → `Recv.Method` (`Foo.Bar`). Field/interface-method → `Owner.Member`.
  Cross-package references (future) use `package_id + "#" + id`.
- `kind` ∈ `func | method | type | const | var | field`. Type symbols also carry
  `type_kind` ∈ `struct | interface | alias | defined` and `underlying` (the
  underlying type rendered as text). `const`/`var`/`field` carry `type` (text);
  values are NOT captured (size/determinism).
- `visibility` ∈ `exported | unexported` via `token.IsExported(name)`;
  `visibility_idiom` = `"capitalized"` for Go. Both exported AND unexported
  symbols are captured — visibility is a label, not a filter.
- `owner` — enclosing symbol id, or `null` for package-level symbols.
- `signature` present only for `func`/`method`. `receiver` present only for
  `method`. `type_params` empty unless generic. Types rendered via
  `go/types.TypeString` with a deterministic module-path qualifier (same string
  on every machine).
- `is_entrypoint` — `true` when `package main` && top-level `func main()` (no
  receiver, no params, no results). When true, add
  `"invocation": { "kind": "binary", "how": "go run ./<pkg-dir>" }`.
- `complexity` — reserved; always `{ "time": null, "space": null, "method": "deferred" }`
  in SP1. SP4 fills it without a schema change.

## Go extractor details

- Load with `go/packages` (`Mode` covering Name, Files, Syntax, Types,
  TypesInfo, Module, Imports, Deps) using the pattern `./...` rooted at the scan
  path. Loading **errors** (won't compile, missing deps) cause a non-zero exit
  with the package errors reported; assayxport does not emit a partial manifest
  for a broken build in SP1.
- Doc comments via `go/doc` (`doc.New`/`doc.NewFromFiles`) so `//`/`/* */`
  markers are already stripped; `format` = `"godoc"`.
- Walk each package's syntax: top-level `FuncDecl` (funcs + methods), `GenDecl`
  for `type`/`const`/`var`. For `type` structs, emit field sub-symbols; for
  `type` interfaces, emit method sub-symbols. Receiver presence distinguishes
  method from func.
- `location` from the `token.FileSet`: relative POSIX file path, 1-based line,
  1-based column, end line.
- Entrypoint detection as defined above.

## CLI

```
assayxport scan [path]      # default path "."

  --out <dir>      where to write assayxport.json + .assayxport/ (default: scan root)
  --stdout         print one combined JSON {index, shards} to stdout; write no files
  --quiet          suppress progress on stderr
```

- Exit `0` on success; non-zero on package load/parse error (with errors on
  stderr).
- SP1 handles Go only; language is auto-detected as `go`. (A `--lang` flag is
  deferred to when a second language exists.)

## Determinism (hard requirement)

Same inputs → byte-identical output, so manifests diff cleanly and cache well.

- Relative POSIX paths only (`filepath.ToSlash`); never absolute paths.
- No timestamps, no usernames, no host/env data anywhere in output.
- `packages` sorted by `id`; `symbols` sorted by `(file, line, name)`; all arrays
  stable.
- Types rendered with a fixed `go/types` qualifier (module-path based), identical
  across machines.
- JSON: 2-space indent, `\n` line endings, trailing newline, UTF-8.
- A test re-runs extraction+emit twice and asserts identical bytes.

## Testing

- **Golden-file tests:** a fixture Go module under `internal/extract/golang/testdata/`
  with packages exercising: exported/unexported funcs, methods (value + pointer
  receiver), structs with fields, interfaces with methods, consts, vars,
  generics (type params), a variadic func, and a `package main` with `func main`.
  Extract → compare against committed golden JSON. A `-update` flag regenerates
  goldens.
- **Determinism test:** extract+emit the fixture twice; assert byte-identical.
- **Schema unit tests:** marshal/round-trip the schema types; assert field
  presence rules per kind (e.g. `signature` absent on a `const`).
- **Dogfood smoke (CI):** run `assayxport scan` on the assayxport repo itself;
  assert it exits 0 and the index validates (non-empty packages, schema_version
  "1").

## Out of scope (SP1)

- Python (SP2), Java (SP3), tree-sitter, wazero/WASM grammars.
- Big-O complexity values (SP4) — the slot exists, stays `deferred`.
- Daemon/watch mode (no external runtime to keep warm; dropped from the program).
- Cross-package reference resolution, call graphs, value capture for const/var.
- Partial manifests for a non-compiling Go build.

## Risks / open items

- **Generics rendering** — `type_params` and instantiated types must render
  deterministically; covered by a generics fixture in the golden tests.
- **go/doc + go/packages pairing** — combining type info (packages) with cleaned
  doc strings (doc) needs care so docs attach to the right decl; fixture-tested.
- **Type-string stability across machines** — mitigated by a fixed module-path
  qualifier; asserted by the determinism test.
- **Build-dependent loading** — `go/packages` needs the target to compile; SP1
  errors out rather than emitting partial data. Revisit (best-effort partial
  extraction) if it proves annoying in practice.
