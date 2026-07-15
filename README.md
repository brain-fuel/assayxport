# assayxport

> Assaying analyzes a metal to report its exact composition. `assayxport`
> analyzes a codebase to report its exact API composition - where each symbol
> lives, what package it belongs to, its signature, its docs, whether it is a
> runnable entrypoint, and who it calls - as a deterministic JSON manifest at
> the project root, so an LLM, docgen, or tool reads one map instead of
> reparsing everything.

Part of the [goforge](https://goforge.dev) suite. Supports Go (native
`go/packages`), Python, and Java (both via a pure-Go, cgo-free tree-sitter
parser). One scan produces a single deterministic manifest covering every
supported language found in the tree.

## Install

```bash
go install goforge.dev/assayxport/cmd/ax@latest
```

The binary is `ax` — short for AssayXport, and short to type.

## Use

```bash
ax scan .                # writes assayxport.json + .assayxport/ shards
ax scan ./pkg --stdout   # print combined JSON, write nothing
ax scan . --lang java    # restrict to one language (repeatable)
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

## Call graph

Every function-like symbol carries a `calls` list: its distinct callees, each
with a call-site `count`, sorted for byte-stable output. The graph is stored
as edges-at-the-symbol, so the whole-program graph assembles by following
`ref` links from shard to shard, and it bottoms out - all the way down to
language primitives, or as far as the project's semantics allow - at the four
non-internal kinds:

- `internal` - the callee is a symbol in this manifest; `ref` locates it as
  `<package-id>#<symbol-id>`. Traversal continues here.
- `builtin` - a language primitive (Go `len`/`make`/`append`..., Python
  `print`/`len`/`range`...). The floor of the graph.
- `external` - resolved to a package outside the scan (stdlib or a
  dependency). The scan boundary.
- `dynamic` - a call through a func value or interface method; the static
  target is unknowable. An interface-method call still names (and, when the
  interface is scanned, links) the interface method itself.
- `unresolved` - a syntactic extractor ran out of information; the callee is
  recorded as written.

Resolution depth follows extraction depth. Go edges are fully semantic
(`go/types`): package functions, methods, builtins, and interface dispatch
are classified exactly, and type conversions `T(x)` are never mistaken for
calls. Python and Java edges are syntactic and honest about it: bare names
resolve against the module's own defs/classes (Python) or the file's own
types and their enclosing-type chain (Java), then the top-level import table
(aliases expanded: `np.array` reports as `numpy.array`), then the builtin /
`java.lang` floor; a receiver-typed call (`obj.method(...)`) whose receiver
is not the method's own `self`/`cls`/type is `unresolved` because inheritance
and instance types are invisible to syntax. `super` in Java is always
`unresolved` for the same reason.

Java edges additionally record the call site as written, so overloads can be
drawn accurately: `arity` is the argument count, and `arg_types` is
per-position type evidence - the entry is the type when the source states it
(a literal, a cast, a `new T(...)`, `this`, a string concatenation) and
`null` when it does not (an identifier, a call result). Casts at call sites
are the programmer's own overload disambiguation, and the vector preserves
them exactly. Deduplication is keyed on the full vector, so `f(1)` and
`f(x)` stay distinct edges into an overloaded name; a consumer joins the
evidence against each candidate overload's `signature.params` and gets an
exact edge when arity or evidence distinguishes, an honest fan-out when it
does not. Evidence never upgrades a kind - no widening, boxing, or hierarchy
is assumed - but it can downgrade one: a call whose arity matches no locally
declared overload is `unresolved` rather than linked to the wrong local
method (the real callee is likely inherited). Varargs accept `>= n-1`
arguments; a record's canonical constructor counts as declared, arity taken
from its components.

Closures, lambdas, and nested defs are not symbols of their own, so calls
made inside them are attributed to the enclosing declared function - an edge
behind a `defer func() { ... }()` or a lambda argument is not lost, but the
graph does not record that its execution is conditional on the closure
running. Overloads of a Java method share one manifest id; the `arity` and
`arg_types` on each edge (and `signature.params` on each declaration) are
what tell them apart.

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
can report a different `space` bound per language.

Recursion detection is scope-aware but syntactic. A self-call from a method
must be qualified (`self.f(...)` in Python, a `recv.f(...)` selector in Go), so
a method that calls an imported or package-level function of the same name is
not mistaken for recursion; a plain function uses the bare-name rule. In Java,
which has no free functions but does have overloading, a self-call must match
both name and argument count, which rejects overload delegation such as
`f(x)` calling `f(x, 0)`. The one case that remains indistinguishable is two
Java overloads of the same arity but different parameter types; there, delegation
may still read as `recursive`. When in doubt the estimator prefers to null the
bound rather than fabricate one.

## License

MIT. Third-party components (the tree-sitter runtime and the Python and Java
grammars, all MIT) are attributed in [NOTICE](NOTICE).
