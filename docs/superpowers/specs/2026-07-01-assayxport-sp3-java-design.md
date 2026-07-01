# assayxport SP3 (Java) - design spec

> Adds Java support to assayxport, reusing the SP2 pure-Go tree-sitter layer
> (`internal/ts`) with the built-in Java grammar, and a Java extractor that emits
> the same deterministic manifest schema. Extends polyglot dispatch so `--lang
> java` becomes valid.

- **Date:** 2026-07-01
- **Status:** approved, pre-implementation
- **Builds on:** SP1 (Go extractor, schema, emitter, CLI) and SP2 (Python + the
  shared `internal/ts` tree-sitter layer + registry/polyglot dispatch). Local
  `main @ c8095eb`.
- **License:** MIT. Module `goforge.dev/assayxport`, `go 1.24`.
- **Scope:** Java extractor + wiring the existing `internal/ts` layer to the
  Java grammar + registering `java` in the polyglot dispatch. big-O (SP4) later.

## What SP3 adds

SP1 extracts Go natively; SP2 added Python via tree-sitter. SP3 adds Java the
same way SP2 did Python: the `internal/ts` layer already wraps
`github.com/odvcencio/gotreesitter` (pure-Go, cgo-free), which ships a built-in
Java grammar (`grammars.JavaLanguage()`, embedded `java.bin`, tree-sitter ABI
15). We add a `Java` value to the `ts.Language` enum and a Java extractor under
`internal/extract/java` that maps Java syntax into the existing
`schema.Package`/`schema.Symbol` types. The registry gains a third extractor;
`--lang java` stops erroring.

tree-sitter is syntactic, not semantic. SP3 reliably extracts type
declarations, members, access modifiers, signatures with type annotations as
written, generics as written, annotations, Javadoc, structure, and entrypoints.
It does NOT resolve imported types to their definitions, expand generics, or
compute inheritance closures. This is the stated and acceptable limit for an API
manifest.

## Architecture

```
internal/ts                 add Java to the Language enum + a lazy java() loader
                            (mirrors the existing python() loader). No new deps.
internal/extract/java       Java extractor: discover.go + symbols.go + java.go
internal/extract/registry   register java.New() alongside go + python
cmd/assayxport              unchanged (repeatable --lang already generic)
```

- **`internal/ts`** gains `Java ts.Language` and a `sync.Once`-guarded
  `java() *gts.Language` returning `grammars.JavaLanguage()`, exactly parallel to
  `python()`. `Parse(Java, src)` then works. This is the ONLY place the Java
  grammar is referenced.
- **`internal/extract/java`** parses each `.java` file and builds
  `schema.Package` values (module + package units - see Mapping).
- **`internal/extract/registry`** adds `java.New()` to `All()` (stable order:
  `go`, `java`, `python` - alphabetical, matching the existing sorted-`available`
  behavior). No other registry change; `Select`/`Run`/dedup are reused as-is.

## Schema additions (additive; `schema_version` stays "1")

SP3 needs exactly ONE new field. Everything else is new string values in
existing free-string fields (no schema change).

`Symbol` gains:
- `annotations []string` (`omitempty`) - Java only: annotation names applied to
  the symbol, in source order, name only with arguments dropped (e.g.
  `["Override"]`, `["Entity", "Deprecated"]`). This is the Java parallel to
  SP2's Python-only `decorators`; kept as a separate field because the two
  idioms are distinct and consumers may want to tell them apart.

Existing fields, new values (no schema change - all are free strings):
- `visibility` gains `protected`, `private`, `package-private` (in addition to
  the existing `exported`/`unexported`). Java uses the 4-way set; a consumer
  interprets `visibility` through `visibility_idiom`.
- `visibility_idiom` gains `"access-modifier"` (Go = `"capitalized"`, Python =
  `"underscore"`).
- `kind` gains `constructor` and `enum-constant` (alongside the existing
  `func`/`method`/`type`/`const`/`var`/`field`). Java type declarations use
  `kind: "type"` with `type_kind` distinguishing the flavor.
- `type_kind` gains `class`, `interface`, `enum`, `record`, `annotation`
  (alongside Go's `struct`/`interface`/`alias`/`defined`).
- `signature.modifiers` carries Java modifiers when present: any of `static`,
  `final`, `abstract`, `default`, `synchronized`, `native` (reuses the
  `[]string` SP2 used for `"async"`).

## Java -> schema mapping

**Units (both levels):**
- **Compilation unit (module)** = one `.java` file. `id` = the file's package
  path plus the file stem, dotted (`com.foo.Bar` for `Bar.java` declaring
  `package com.foo;`); if there is no `package` declaration (default package),
  `id` = the file stem. `path` = relative POSIX file path; `name` = file stem;
  `level` = `"module"`; `doc` = empty for a normal file (Java has no file-level
  doc comment), or the Javadoc of `package-info.java` is attributed to the
  package unit, not the module; `symbols` = the file's top-level type
  declarations with their nested members and nested types.
- **Package (package)** = a Java package identified by its `package`
  declaration (NOT by directory). `id` = the dotted package name (`com.foo`);
  `level` = `"package"`; `path` = the relative directory of `package-info.java`
  if present, else the relative directory of the first (sorted) compilation unit
  in that package (deterministic); `name` = the last dotted segment; `doc` = the
  Javadoc from `package-info.java` if present, else empty; `members` = ids of
  direct child compilation units (files whose package equals this package id)
  plus direct child sub-packages (a package id with exactly one more dotted
  segment), sorted. Rollup is by reference (`members`), not symbol duplication.
  Files in the default package (no `package` decl) produce module units but no
  package unit.

**Symbol kinds:**
- Type declarations -> `kind: "type"` with `type_kind` in
  `class | interface | enum | record | annotation` (`@interface`).
- Methods -> `kind: "method"`; constructors -> `kind: "constructor"`; fields ->
  `kind: "field"`; enum constants -> `kind: "enum-constant"`.
- Nested and inner types are owned by their enclosing type (`owner`), with
  dotted ids (`Outer.Inner`, `Outer.Inner.method`), exactly as SP2 handles
  nested Python classes.

**Signature:** methods and constructors carry `params` (name + type as written)
and `returns` (the single return type as written; empty for constructors and
`void`). Generic methods and generic type declarations carry `type_params` (the
`<T extends X>` list as written) on their `signature` (a type declaration gets a
`signature` populated with only `type_params` when generic; `params`/`returns`
stay empty). `signature.modifiers` lists the Java modifiers present. The
`throws` clause is out of scope for SP3 (not captured).

**Visibility (4-way):** `visibility` = the declared access modifier -
`public | protected | private`, or `package-private` when none is declared;
`visibility_idiom = "access-modifier"`. Both accessible and inaccessible
members are captured - visibility is a label, not a filter.

**Annotations:** each declaration's annotations (e.g. `@Override`,
`@Entity(name="x")`) are captured into `annotations` as bare names in source
order, arguments dropped (`"Override"`, `"Entity"`).

**Javadoc:** a `/** ... */` doc comment immediately preceding a declaration ->
`doc.raw` with the `/**`, trailing `*/`, and leading `*` markers stripped;
`doc.format = "javadoc"`. Javadoc tag structure (`@param`, `@return`) is not
parsed in SP3.

**Entrypoint:** a type declaring `public static void main(String[] args)` (or
`String...`) makes its compilation-unit module `is_entrypoint` with
`invocation {kind:"class", how:"java <FQCN>"}` where `<FQCN>` is the enclosing
type's fully qualified name (`com.foo.Main`). The `main` method symbol
additionally gets symbol-level `is_entrypoint` + the same invocation.

`complexity` stays the deferred slot for every Java symbol.

## Determinism

Same hard requirement as SP1/SP2: relative POSIX paths, no timestamps / absolute
paths / host data, packages sorted by id, symbols by `(file, line, name)`,
`members` sorted, `languages` sorted, 2-space JSON + trailing newline,
byte-identical on re-run. The package `id` is derived from the parsed `package`
declaration, so it never embeds host directory names (this is why we key on the
declaration, not the directory). Nested-type symbols are appended in source
(tree) order, which is deterministic. A double-run byte-identical test covers
Java, and a polyglot double-run covers all three languages together.

## CLI / dispatch

No CLI change. `--lang` is already generic and repeatable; SP3 makes `java` a
registered language, so `--lang java` runs the Java extractor and `--lang go
--lang java` runs those two. A default `scan` now merges Go + Python + Java into
one manifest whose `languages` lists all found.

## Testing

- **tree-sitter layer:** a unit test parses a trivial Java snippet via
  `ts.Parse(ts.Java, ...)` and asserts the tree shape (a `program` with a
  `class_declaration`), proving the Java grammar loads in-process.
- **Java extractor golden:** a fixture Java source tree under
  `internal/extract/java/testdata/proj/` exercising: a package with multiple
  compilation units and a sub-package; `package-info.java` with Javadoc;
  class/interface/enum/record/annotation type declarations; methods,
  constructors, fields, enum constants; nested/inner types (dotted owners);
  public/protected/private/package-private members; a generic class and generic
  method (`type_params`); annotations; a Javadoc comment preceded by a license
  comment (regression parallel to SP2 finding 3); and a class with
  `public static void main(String[])`. Extract -> compare to a committed golden;
  `-update` regenerates.
- **Determinism test:** double-run byte-identical for the Java fixture through
  `emit.Manifest` + `emit.Combined`.
- **Focused unit tests:** visibility mapping (all four levels), entrypoint FQCN
  derivation, package id from `package` declaration (incl. default package),
  members direct-children-only rollup.
- **Polyglot dispatch test:** extend the mixed fixture (or add a Java file to
  it) so `Run(All(), mixed)` reports `["go","java","python"]` and `--lang java`
  yields only Java units; `Select` of an unknown language still errors.
- **Dogfood smoke:** unchanged (assayxport is Go; `scan .` still works).

## Licensing

The Java grammar (`tree-sitter-java`, MIT) is embedded in the already-attributed
`gotreesitter` module (its blob lives in the module cache, not vendored here),
exactly like the Python grammar. `NOTICE` gains a `tree-sitter-java` entry
(MIT, copyright the tree-sitter authors) parallel to the existing
`tree-sitter-python` entry; `internal/ts/provenance.md` records the Java blob's
source/checksum/ABI. assayxport stays MIT.

## Out of scope (SP3)

- big-O complexity values (SP4) - the slot exists, stays `deferred`.
- Cross-file / import type resolution, generic expansion, inheritance closures.
- Javadoc tag structure (`@param`/`@return`/`@throws` parsing).
- The `throws` clause, annotation argument values, and annotation-element
  defaults.
- Kotlin / Scala / other JVM languages.
- console-launcher / manifest `Main-Class` discovery (source-only; the
  `public static void main` method is the source signal).

## Risks / open items

- **gotreesitter Java grammar parity** - as with Python (SP2), the canonical
  tree-sitter-java node/field names must be confirmed against gotreesitter's
  actual output before trusting them. The plan's first extractor task probes the
  parser (prints node types/fields for a fixture) and adapts; any deviation is
  recorded, mirroring the SP2 provenance discipline.
- **Multiple top-level types per file** - a `.java` file may declare more than
  one top-level type (only one `public`). The module id uses the file stem
  (deterministic), and every top-level type is emitted as a symbol; the file
  stem need not equal a type name.
- **Package path representativeness** - since packages are keyed by declaration,
  a package's `path` is a representative directory (package-info.java's dir, else
  the first file's dir). If one logical package is split across directories, the
  chosen path is still deterministic and documented.
- **Visibility contract divergence** - Java's 4-way `visibility` deliberately
  departs from the Go/Python binary `exported`/`unexported`. Consumers must key
  on `visibility_idiom`; this is documented in the schema notes.
