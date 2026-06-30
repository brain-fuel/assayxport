# assayxport SP2 (Python) — design spec

> Adds Python support to assayxport: a pure-Go tree-sitter layer (wazero + WASM
> grammar) and a Python extractor that emits the same deterministic manifest
> schema, plus polyglot dispatch so one `scan` covers every supported language.

- **Date:** 2026-06-30
- **Status:** approved, pre-implementation
- **Builds on:** SP1 (Go extractor, schema, emitter, CLI) — shipped, `main @ 60b8a0f`.
- **License:** MIT. Module `goforge.dev/assayxport`, `go 1.24`.
- **Scope:** Python extractor + the shared tree-sitter/wazero layer + polyglot dispatch. Java (SP3) reuses the layer; big-O (SP4) later.

## What SP2 adds

SP1 extracts Go via native `go/packages`. SP2 adds Python via tree-sitter, with
zero external runtime: a pure-Go tree-sitter runtime (wazero executing a vendored,
`go:embed`'d WASM grammar) parses `.py` source into a syntax tree, and the Python
extractor maps it into the existing `schema.Package`/`schema.Symbol` types. The CLI
becomes polyglot: it dispatches each registered language extractor over the scan
root and merges results into one manifest.

tree-sitter is syntactic, not semantic. SP2 reliably extracts names, kinds,
visibility, signatures with type annotations **as-written**, docstrings, structure,
decorators, and entrypoints. It does NOT resolve imported names to definitions or
infer unannotated types. This is the stated and acceptable limit for an API
manifest.

## Architecture

```
internal/ts                 shared tree-sitter layer (wazero + embedded WASM grammar);
                            Parse(source) -> *Tree; query helpers. Reused by SP3.
internal/ts/grammars/       vendored *.wasm grammar files (python.wasm here)
internal/extract/python     Python extractor: implements extract.Extractor via internal/ts
internal/extract/registry   extractor registry + polyglot dispatch over the scan root
cmd/assayxport              CLI: scan with repeatable --lang override
```

- **`internal/ts`** wraps a pure-Go tree-sitter runtime. Primary candidate:
  `github.com/malivvan/tree-sitter` (wraps canonical tree-sitter's WASM build via
  wazero, no cgo). Fallback: `github.com/odvcencio/gotreesitter` (pure-Go runtime).
  The plan begins with a build probe that loads the Python grammar and parses a
  trivial snippet; whichever works deterministically is pinned. The grammar
  `python.wasm` is vendored under `internal/ts/grammars/` and `go:embed`'d so the
  binary needs no network or external files.
- **`internal/extract/python`** runs tree-sitter queries against each `.py` file and
  builds `schema.Package` values (both module and package units — see Mapping).
- **`internal/extract/registry`** holds the registered extractors (`go`, `python`),
  selects which to run (all by default, or the `--lang` subset), runs each over the
  scan root, and concatenates their `[]schema.Package`. The emitter is unchanged; it
  already sorts by id, so merged multi-language packages order deterministically.

## CLI changes

```
assayxport scan [path]
  --lang <name>   (repeatable) restrict to these languages; default = all registered.
                  e.g. --lang python --lang java  -> only python + java extractors run.
                  An unregistered language name is an error listing the available ones.
  --out, --stdout, --quiet   (unchanged from SP1)
```

Default `scan` runs every registered extractor; each detects its own files (Go via
`go/packages`, Python via `.py` discovery). A repo with both yields one manifest
whose `languages` lists both. `--lang` filters the registry to the named languages.
(In SP2 only `go` and `python` are registered; `--lang java` errors until SP3.)

## Schema additions (additive; `schema_version` stays "1")

All new fields are `omitempty` and language-scoped. Adding them is backward
compatible, but Go output gains `level` and unit-level entrypoint info, so **the SP1
Go golden is regenerated** as part of SP2 (acceptable: no external consumers).

`PackageEntry` and `PackageInfo` gain:
- `level string` — `"package"` or `"module"`. Go packages and Python dir-packages =
  `"package"`; Python modules = `"module"`.
- `members []string` — for a package unit, the ids of its direct child modules and
  sub-packages. Empty/omitted for module units and Go packages.
- `is_entrypoint bool` + `invocation *Invocation` — unit-level runnability. Set for a
  Go `package main` (`{kind:"binary", how:"go run ./<dir>"}`) and a Python module
  containing an `if __name__ == "__main__":` guard (`{kind:"module", how:"python -m <module>"}`).

`Symbol` gains:
- `in_all *bool` — Python only: `true` if the name is in the module's `__all__`,
  `false` if `__all__` is defined and the name is absent, `nil` (omitted) if
  `__all__` is not defined or not applicable.
- `decorators []string` — Python only: decorator names applied to the symbol, in
  source order (e.g. `["staticmethod"]`, `["property"]`).

The Go extractor (`internal/extract/golang`) is updated minimally to set `level:
"package"` on its packages and unit-level `is_entrypoint`+`invocation` on `package
main`. Its symbol-level entrypoint on `func main` is unchanged.

## Python → schema mapping

**Units (both levels):**
- **Module** = one `.py` file. `id` = dotted module path; `path` = relative POSIX
  file path; `name` = module name (file stem, or package name for `__init__.py`);
  `level` = `"module"`; `doc` = module docstring; `symbols` = its top-level
  functions, classes (with nested methods/properties/class-attributes), and
  module-level assignments.
- **Package** = a directory containing `__init__.py`. `id` = dotted package path;
  `path` = relative POSIX dir; `level` = `"package"`; `doc` = the `__init__.py`
  docstring; `members` = ids of direct child modules + sub-packages; `symbols` = the
  `__init__.py`'s own top-level surface (the curated re-export point). Rollup is by
  reference (`members`), not symbol duplication.

**Dotted-path derivation (deterministic):** for a `.py` file, climb parent
directories while each contains `__init__.py`, collecting their names; the dotted
module is those names (top-down) joined by `.` plus the file stem (omitted for
`__init__.py`, which names the package itself). The first ancestor lacking
`__init__.py` is the source root.

**Symbol kinds:** `function`, `method` (def inside a class), `class`, `property` (a
def decorated `@property`), `variable` (module- or class-level assignment / annotated
assignment). `async def` sets a `modifiers` entry `"async"`. Nested classes/functions
are owned by their enclosing symbol (`owner`).

**Signature:** params with annotations and defaults as-written (text), return
annotation as-written; absent annotations render empty. `self`/`cls` included as the
first param of methods.

**Visibility (capture both):** `visibility` from the underscore rule — a leading
single or double underscore that is NOT a dunder (`__x__`) → `unexported`, else
`exported`; `visibility_idiom = "underscore"`. Independently, `in_all` records
`__all__` membership when `__all__` is defined.

**Docstrings:** the first string-literal statement in a module/class/def →
`doc.raw`; `doc.format = "docstring"`. (Google/NumPy/reST styles are not
distinguished in SP2.)

**Entrypoint:** a module whose body contains `if __name__ == "__main__":` → that
module unit gets `is_entrypoint` + `invocation {kind:"module", how:"python -m <module>"}`.
If the module also defines a top-level `def main`, that symbol additionally gets
symbol-level `is_entrypoint` + the same invocation.

`complexity` stays the deferred slot for every Python symbol.

## Determinism

Same hard requirement as SP1: relative POSIX paths, no timestamps/absolute/host data,
packages sorted by id, symbols by `(file, line, name)`, 2-space JSON + trailing
newline, byte-identical on re-run. tree-sitter parsing is deterministic; the grammar
`.wasm` is vendored and embedded (no version drift). The wazero runtime executes the
grammar deterministically. A double-run byte-identical test covers it for Python.

## Testing

- **tree-sitter layer:** a unit test parses a trivial Python snippet and asserts the
  tree shape / a query result, proving the wazero+grammar path works in-process.
- **Python extractor golden:** a fixture Python package under
  `internal/extract/python/testdata/` exercising: module + package units, functions,
  classes with methods/properties/class-attributes, `async def`, decorators,
  underscore-private + dunder names, a module with `__all__`, module-level
  variables, and a module with an `if __name__ == "__main__":` guard and a `main`.
  Extract → compare to a committed golden; `-update` regenerates.
- **Determinism test:** double-run byte-identical for the Python fixture.
- **Go golden regenerated** for the new `level`/unit-entrypoint fields; its other
  content must be otherwise unchanged (proving the Go path is untouched in behavior).
- **Polyglot dispatch test:** a fixture tree containing both a Go package and a Python
  module; `scan` produces one manifest listing both languages; `--lang python` yields
  only Python; `--lang go` only Go; `--lang java` errors (unregistered).
- **Dogfood smoke:** unchanged (assayxport is Go; `scan .` still works).

## Licensing

tree-sitter and tree-sitter-python are MIT. The vendored `python.wasm` grammar and
the wazero-wrapper Go dependency are third-party; add their attribution to the
existing `NOTICE` (or a `THIRD-PARTY-NOTICES` file) with the upstream copyright and
MIT text. assayxport itself stays MIT.

## Out of scope (SP2)

- Java (SP3 — reuses `internal/ts`), big-O complexity values (SP4).
- Cross-module/import resolution, inferred types, call graphs.
- Docstring-style parsing (Google/NumPy/reST structure).
- console_scripts / pyproject packaging entrypoints (source-only; the `__main__`
  guard is the source signal).

## Risks / open items

- **tree-sitter-via-wazero maturity** — `malivvan/tree-sitter` is the primary
  candidate; the plan's first task is a build probe (load grammar, parse a snippet,
  confirm deterministic output). If it cannot load the grammar reliably, fall back to
  `gotreesitter`; if neither works, escalate before proceeding (do NOT add cgo).
- **Grammar acquisition/build** — obtaining a `python.wasm` grammar build to vendor.
  The plan pins a specific grammar release and records its provenance + checksum.
- **Module-path derivation for loose scripts** — a `.py` not under any `__init__.py`
  package is a top-level module; its id is the file stem and it has no package unit.
- **wazero startup cost** — one-time grammar instantiation per run; acceptable (no
  per-file process spawn, no JVM/CPython). Daemon mode remains unneeded.
