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

## Java grammar

- **Built-in, not vendored.** Obtained at runtime via `grammars.JavaLanguage()`, which
  lazily decodes an embedded grammar blob shipped inside the library module.
- **Embedded blob:** `grammars/grammar_blobs/java.bin` inside
  `github.com/odvcencio/gotreesitter@v0.20.7`.
  - size: 46587 bytes
  - sha256: `530c7257b13e1ce356edd251cac347b5e41f04f74343473c72f43bf1177ffa9c`
  - (Record of the upstream module's embedded artifact for auditing; the blob lives in
    the module cache, not in this repo.)
- **tree-sitter language ABI version:** 15 (same ABI as Python).

### Confirmed node types (from TestProbeJavaShape, 2026-07-01)

Probe source: `package com.foo;`, Javadoc `/** Doc. */`, `@Deprecated public class Bar<T>
extends Base implements I` with a `private int n` field, `public Bar(int n)` constructor,
`@Override public static void main(String[] args) {}` method, nested `interface Inner {}`,
and top-level `enum E { A, B }`.

**Root:**
- `program` -- root node type (canonical, confirmed).

**Top-level declarations:**
- `package_declaration` -- contains `scoped_identifier` (for dotted names like `com.foo`).
- `class_declaration` -- canonical name confirmed.
- `enum_declaration` -- canonical name confirmed.
- `interface_declaration` -- confirmed as a nested member inside `class_body`.

**Bodies:**
- `class_body` -- canonical name confirmed.
- `enum_body` -- canonical name confirmed.
- `interface_body` -- canonical name confirmed.

**Members:**
- `field_declaration` -- canonical name confirmed; type child is `integral_type` (for
  primitive `int`), declarator is `variable_declarator`.
- `constructor_declaration` -- canonical name confirmed; body is `constructor_body` (NOT
  `block`).
- `method_declaration` -- canonical name confirmed; body is `block` (empty methods have
  an empty `block` node).

**Enum members:**
- `enum_constant` -- canonical name confirmed; child is `identifier`.

**Modifiers and annotations:**
- `modifiers` -- canonical name confirmed; appears as a named child of declarations and
  members, containing annotation nodes and keyword nodes.
- `marker_annotation` -- confirmed for annotations without arguments (`@Deprecated`,
  `@Override`); contains an `identifier` child for the annotation name.
- `annotation` -- NOT observed in this probe (no annotation-with-arguments in the probe
  source); canonical name is expected to be correct for `@SuppressWarnings("...")` style.

**Generics:**
- `type_parameters` -- canonical name confirmed (on `class Bar<T>`).
- `type_parameter` -- canonical name confirmed; contains `type_identifier`.

**Parameters:**
- `formal_parameters` -- canonical name confirmed.
- `formal_parameter` -- canonical name confirmed; type child is `integral_type` for `int`,
  `array_type` for `String[]` (which itself contains `type_identifier` + `dimensions`).

**Identifiers:**
- `identifier` -- used for variable names, method names, constructor names, annotation
  names, enum constant names.
- `type_identifier` -- used for type names in declarations (`Bar`, `Base`, `I`, `String`);
  DISTINCT from bare `identifier`.
- `scoped_identifier` -- used for dotted names (e.g. `com.foo` in package_declaration).

**Type nodes:**
- `integral_type` -- for primitive integer types (`int`, `long`, etc.).
- `void_type` -- for `void` return type.
- `array_type` -- for array types; contains base type + `dimensions`.
- `dimensions` -- confirmed (appears inside `array_type`).

**Super-type clauses (deviation from canonical brief field names):**
- `superclass` -- node type for the `extends` clause wrapper inside `class_declaration`
  (contains a `type_identifier`). The brief lists `superclass` as BOTH the field name
  and the node type; confirmed correct.
- `super_interfaces` -- node type for the `implements` clause wrapper (contains a
  `type_list`, which contains `type_identifier` children). DEVIATION: the brief lists
  the field name as `interfaces`; the node type is `super_interfaces`. Task 3 should use
  `ChildByFieldName("interfaces")` to navigate the field, but expect a node of type
  `super_interfaces`.

**Comments:**
- `block_comment` -- confirmed for Javadoc (`/** Doc. */`); it appears as a SIBLING of
  the class_declaration node, NOT as a child. Javadoc is a `block_comment` with no
  special node type; the content starts with `/**`. This matches the expected behavior.
- `line_comment` -- not tested; expect `line_comment` type for `//` comments.

**Not observed (not in probe source, canonical names expected):**
- `record_declaration`, `annotation_type_declaration`, `annotation_type_body`
- `spread_parameter` (varargs)
- `line_comment`
- `variable_declarator` with `value` field (initializer)

## Determinism & concurrency

- The Python `*Language` is loaded once behind a package-level `sync.Once`.
- `Parse` creates a fresh parser per call (`gotreesitter.Parser` is not
  concurrency-safe); parsing is deterministic for equal input, verified by
  `TestParseDeterministic`.

## Coordinate convention

tree-sitter points are 0-based (`Point{Row, Column}`). This layer converts them to
1-based `StartLine`/`StartCol`/`EndLine`. `Content` slices `src[StartByte:EndByte]`.
