# assayxport

> Assaying analyzes a metal to report its exact composition. `assayxport`
> analyzes a codebase to report its exact API composition - where each symbol
> lives, what package it belongs to, its signature, its docs, and whether it is
> a runnable entrypoint - as a deterministic JSON manifest at the project root,
> so an LLM, docgen, or tool reads one map instead of reparsing everything.

Part of the [goforge](https://goforge.dev) suite. Supports Go (native
`go/packages`), Python, and Java (both via a pure-Go, cgo-free tree-sitter
parser). One scan produces a single deterministic manifest covering every
supported language found in the tree.

## Install

```bash
go install goforge.dev/assayxport/cmd/assayxport@latest
```

## Use

```bash
assayxport scan .            # writes assayxport.json + .assayxport/ shards
assayxport scan ./pkg --stdout   # print combined JSON, write nothing
assayxport scan . --lang java    # restrict to one language (repeatable)
```

### Languages

By default `scan` runs every registered extractor (Go, Python, Java) and merges
the results into one manifest whose `languages` field lists what was found. Use
`--lang` to restrict the run; it is repeatable, so `--lang python --lang go`
runs only those two. An unregistered language name is an error that lists the
available ones.

Go extraction is fully semantic via `go/packages`. Python and Java extraction is
syntactic (tree-sitter): names, kinds, visibility, signatures as written, doc
comments, decorators/annotations, and entrypoints are reliable; imported-name
resolution and inferred types are not. Java visibility is the 4-way access
modifier (`public`/`protected`/`private`/`package-private`); consumers read the
per-symbol `visibility_idiom` to interpret the `visibility` field across
languages.

## Output

- `assayxport.json` - root index: project metadata + one entry per package.
- `.assayxport/<package-dir>.json` - per-package shard with the full symbol list.

Output is deterministic: relative paths, no timestamps, stable ordering. Equal
inputs produce byte-identical files.

## Complexity

Every function-like symbol (function, method, constructor) carries a
best-effort big-O estimate in its `complexity` object (`time`, `space`,
`method`). The `method` field says how the estimate was derived:

- `loop-nesting` - a bound derived from the deepest loop nesting in the
  function body. `time` is `O(1)`, `O(n)`, `O(n^2)`, ...; `space` is `O(1)`
  unless an allocation occurs inside a loop.
- `recursive` - the function calls itself, so nesting cannot bound it; `time`
  and `space` are `null` (the estimator refuses to guess).
- `deferred` - the symbol was not analyzed (a type, field, variable, or
  annotation element); `time` and `space` are `null`.

This is a coarse triage signal, not a proof. It is intraprocedural (the cost of
functions this one calls is NOT propagated), it conservatively counts a
constant-bound loop such as `for i := 0; i < 10; i++` as `O(n)`, it never
bounds recursion, and `space` is a weak allocation-based heuristic. Loops
inside a nested closure, lambda, or local class are attributed to that inner
scope, not the enclosing function.

Two honesty notes on the heuristic edges. The `space` allocation signal is not
identical across languages: Go composite literals and Java `new` are counted as
allocations, but a Python constructor call (`Foo()`) is syntactically
indistinguishable from any other call and is NOT counted, so equivalent code
can report a different `space` bound per language. And recursion detection is
name-based: it can over-detect (a same-named local shadow, or a same-named
overload) and mark a function `recursive`, which nulls its bound rather than
fabricating a wrong one. Both are deliberate conservative choices for a
best-effort signal.

## License

MIT. Third-party components (the tree-sitter runtime and the Python and Java
grammars, all MIT) are attributed in [NOTICE](NOTICE).
