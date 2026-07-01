# tree-sitter layer provenance

This package (`internal/ts`) is assayxport's in-house tree-sitter layer. It is the
ONLY place the third-party tree-sitter implementation is referenced; all downstream
Python tasks depend on the stable API in `ts.go`, never on the library directly.

## Library

- **Chosen library:** `github.com/odvcencio/gotreesitter` v0.20.7 (the brief's *fallback*).
- **Runtime model:** pure-Go, **cgo-free** tree-sitter runtime. No WASM runtime, no
  C toolchain, no shared objects; the parser and grammars are plain Go.
- **License:** see the module's `LICENSE`.

### Why the fallback and not the primary (`github.com/malivvan/tree-sitter`)

The primary candidate `malivvan/tree-sitter` v0.0.1 was added, probed via `go doc`
and its source, and rejected for three concrete reasons:

1. **No Python accessor.** It statically links grammars into a single embedded
   `lib/ts.wasm` and exposes only `LanguageC()` / `LanguageCpp()`. There is **no
   grammar-from-bytes / load-from-WASM API** the brief hoped for; grammars are
   compiled into `ts.wasm` at build time via a `zig cc` + codegen Makefile step.
   Adding Python would require forking the library, editing the Makefile, and
   rebuilding the WASM with the vendored `src/python` sources.
2. **Missing `ChildByFieldName`.** The wrapped Node type exposes only
   `Child`/`NamedChild`/`ChildCount`/`Kind`/`StartByte`/`EndByte`. The brief's API
   requires `ChildByFieldName(f)` and the underlying `ts_node_child_by_field_name`
   export is not present in `ts.wasm`.
3. **Missing point (line/col) accessors.** No `StartPoint`/`EndPoint` exports, so
   `StartLine`/`StartCol`/`EndLine` could not be sourced from the library.

`odvcencio/gotreesitter` provides all of these natively: `Parser.Parse`,
`Tree.RootNode`, and a Node type with `Type(lang)`, `NamedChildCount`,
`NamedChild`, `ChildByFieldName(name, lang)`, `StartPoint`/`EndPoint` (a
`Point{Row,Column}`), `StartByte`/`EndByte`, and `Text(src)`. It also ships Python
as a built-in grammar (see below). Both `malivvan/tree-sitter` and its `wazero`
transitive dep were removed by `go mod tidy` after the switch.

## Python grammar

- **Built-in, not vendored.** No `python.wasm` (or any grammar file) is vendored
  into this repo. The `grammars/` directory referenced by the brief is therefore
  intentionally absent.
- Obtained at runtime via `grammars.PythonLanguage()`, which lazily decodes an
  embedded grammar blob shipped inside the library module.
- **Embedded blob:** `grammars/grammar_blobs/python.bin` inside
  `github.com/odvcencio/gotreesitter@v0.20.7`.
  - size: 60172 bytes
  - sha256: `cde4a67dc6af6e1232dbbd1eab8618478d1d73727020e8a8002542390a452d37`
  - (This is a record of the upstream module's embedded artifact for auditing; the
    blob lives in the module cache, not in this repo.)
- **Upstream source:** the blob is a `ts2go`-encoded build of the canonical
  `tree-sitter/tree-sitter-python` grammar (the generated `parser.c` is credited in
  the library's `grammars/python_external_lex_states_gen.go`).
- **tree-sitter language ABI version:** 15 (`RuntimeLanguageVersion`).

## Determinism & concurrency

- The Python `*Language` is loaded once behind a package-level `sync.Once`.
- `Parse` creates a fresh parser per call (`gotreesitter.Parser` is not
  concurrency-safe); parsing is deterministic for equal input, verified by
  `TestParseDeterministic`.

## Coordinate convention

tree-sitter points are 0-based (`Point{Row, Column}`). This layer converts them to
1-based `StartLine`/`StartCol`/`EndLine`. `Content` slices `src[StartByte:EndByte]`.
