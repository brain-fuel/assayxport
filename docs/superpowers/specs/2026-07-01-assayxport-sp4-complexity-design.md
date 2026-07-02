# assayxport SP4 (big-O complexity) - design spec

> Fills the reserved `complexity` slot on function-like symbols with a
> best-effort, intraprocedural, syntactic big-O estimate for time and space,
> across all three supported languages (Go, Python, Java). A shared heuristic
> engine maps a normalized control-flow summary to big-O strings; each language
> contributes only a thin walker over its own parse representation.

- **Date:** 2026-07-01
- **Status:** approved, pre-implementation
- **Builds on:** SP1 (Go), SP2 (Python + `internal/ts`), SP3 (Java). Branch `feat/sp4-complexity` off `main @ 39df164` (tag v0.3.0).
- **License:** MIT. Module `goforge.dev/assayxport`, `go 1.24`.
- **Scope:** a shared complexity engine + per-language control-flow walkers, wired into the three extractors so function-like symbols carry a computed complexity. No schema change.

## What SP4 adds

Every symbol currently carries `complexity: {time: null, space: null, method: "deferred"}` (`schema.DeferredComplexity()`). SP4 replaces that, for function-like symbols only, with a computed estimate. The estimate is deliberately honest: big-O is undecidable in general, so SP4 emits a bound only from a reliable syntactic signal (loop nesting), refuses to guess through recursion (leaves it null), and names the heuristic in `method` so a consumer always knows the estimate is best-effort and how it was derived.

This is **intraprocedural and syntactic**: it analyzes a function's own body, never the cost of the functions it calls, and never resolves types or values. It is a coarse triage signal, not a proof.

## Architecture

```
internal/complexity            shared engine: Summary type + Estimate(Summary) -> schema.Complexity
                               (language-agnostic; the only place big-O strings are produced)
internal/extract/golang        walk *ast.FuncDecl bodies -> complexity.Summary (go/ast)
internal/extract/python        walk function_definition bodies -> complexity.Summary (ts)
internal/extract/java          walk method/constructor bodies -> complexity.Summary (ts)
```

- **`internal/complexity`** owns `Summary` and `Estimate`. It has no dependency on `go/ast` or `internal/ts`; it takes a plain struct and returns a `schema.Complexity`. This keeps the heuristic in one place and independently unit-testable.
- Each extractor gains a small walker that, given a function-like declaration node and the function's own simple name, produces a `Summary`. The walker is the only language-specific part; the mapping to big-O is shared.
- Extractors call the walker for `func`/`method`/`constructor` symbols and set the returned complexity; all other symbol kinds keep `schema.DeferredComplexity()`.

### The shared types

```go
// internal/complexity

// Summary is a normalized, language-agnostic control-flow summary of one
// function body. Each language extractor fills it by walking its own tree.
type Summary struct {
    MaxLoopDepth  int  // deepest nesting of loops in the body (0 = no loops)
    Recursive     bool // the body calls the function's own name (direct recursion)
    AllocInLoop   bool // an allocation occurs inside at least one loop
    AllocDepth    int  // the loop-nesting depth at the deepest in-loop allocation (0 if none)
}

// Estimate maps a Summary to a big-O complexity. It is deterministic and pure.
func Estimate(s Summary) schema.Complexity
```

### Estimate mapping (exact, honest)

- **Recursive** (`s.Recursive == true`): `time = nil`, `space = nil`, `method = "recursive"`. Loop nesting cannot bound a recursive function (it might be O(log n), O(n), or O(2^n)); SP4 refuses to guess.
- **Non-recursive**: `method = "loop-nesting"`.
  - `time` = `bigO(s.MaxLoopDepth)`: depth 0 -> `"O(1)"`, 1 -> `"O(n)"`, 2 -> `"O(n^2)"`, k -> `"O(n^k)"`.
  - `space` = `"O(1)"` if `!s.AllocInLoop`, else `bigO(s.AllocDepth)` (an allocation inside a depth-d loop nest grows with the input to the d-th power).
- `bigO(0) = "O(1)"`, `bigO(1) = "O(n)"`, `bigO(k>=2) = "O(n^k)"`.

`method` values across the tool: `"deferred"` (non-callable symbols and any symbol SP4 does not analyze), `"loop-nesting"` (a bound was computed), `"recursive"` (analysis declined due to recursion).

## Per-language control-flow walkers

Each walker takes the function-like declaration and the function's own simple name, walks the body tracking current loop depth, and returns a `Summary`. Node types are confirmed against the actual parser before use (probe-and-record discipline from SP2/SP3; gotreesitter names recorded in `internal/ts/provenance.md`).

**Loop nodes (increment depth):**
- Go (`go/ast`): `*ast.ForStmt`, `*ast.RangeStmt`.
- Python (`ts`): `for_statement`, `while_statement`, and comprehensions (`list_comprehension`, `set_comprehension`, `dictionary_comprehension`, `generator_expression`) count as one loop level.
- Java (`ts`): `for_statement`, `enhanced_for_statement`, `while_statement`, `do_statement`.

**Recursion (direct self-call):** a call expression whose callee is an identifier equal to the function's own simple name.
- Go: `*ast.CallExpr` with `*ast.Ident` fun matching the func name (methods: match the method name on a selector where feasible; a bare-name match is the baseline).
- Python: `call` whose function child is an `identifier` equal to the def name.
- Java: `method_invocation` whose name equals the method name and has no scoping object (or `this`).

**Allocation (for space):** an allocation node anywhere; `AllocInLoop`/`AllocDepth` record whether/at-what-depth it occurs inside a loop.
- Go: `*ast.CallExpr` to builtin `make`/`new`/`append`, and `*ast.CompositeLit`.
- Python: list/dict/set displays (`list`, `dictionary`, `set`), comprehensions, and `.append`/`.extend`/`.add`/`.update` method calls.
- Java: `object_creation_expression`, `array_creation_expression`, and collection mutators `.add`/`.put`/`.addAll`.

The walker records `MaxLoopDepth` = the maximum depth reached, `AllocInLoop` = any allocation seen at depth >= 1, `AllocDepth` = the maximum loop depth at which an allocation was seen.

## Wiring into extractors

- Each extractor already sets `Complexity: schema.DeferredComplexity()` on every symbol. For `func`/`method`/`constructor` symbols, it instead computes a `Summary` via its walker and sets `complexity.Estimate(summary)`.
- Non-callable symbols (`type`, `field`, `var`, `const`, `enum-constant`, `property`, `annotation` elements are methods so they DO get analyzed) keep `schema.DeferredComplexity()`. (Property getters in Python are functions and are analyzed; annotation-type elements in Java are methods with empty bodies and resolve to `O(1)`.)
- Go analysis uses the already-loaded `*ast.FuncDecl` (the Go extractor already has `go/ast`). Python/Java analysis reuses the same `ts` function node the symbol was built from.

## Determinism

Same hard requirement as SP1-SP3: the estimate is a pure function of the parse tree, so output is deterministic and byte-identical on re-run. All three language goldens (`internal/extract/golang/testdata/sample.golden.json`, `.../python/testdata/sample.golden.json`, `.../java/testdata/sample.golden.json`) regenerate because function-like symbols flip from `deferred` to computed values; the change is confined to `complexity` objects on those symbols and is otherwise byte-identical. `schema_version` stays `"1"` (the `complexity` object shape is unchanged; only values change). A double-run byte-identical test per language remains.

## Testing

- **Engine unit tests** (`internal/complexity`): table-driven over `Summary` inputs asserting exact `Estimate` output for O(1), O(n), O(n^2), recursive (nil/nil/"recursive"), and alloc-in-loop space.
- **Per-language walker tests:** a fixture function of each shape in each language, asserting the computed `complexity` on the symbol:
  - `O(1)`: straight-line body, no loops, no alloc.
  - `O(n)`: one loop.
  - `O(n^2)`: two nested loops.
  - recursive: a self-calling function -> `{nil, nil, "recursive"}`.
  - space `O(n)`: an allocation (append/list-append/collection-add) inside one loop.
- **Golden regeneration:** the three existing goldens are regenerated; a reviewer confirms the diff touches only `complexity` objects on function-like symbols (types/fields/etc. still `deferred`).
- **Double-run byte-identical** per language (existing tests continue to hold).
- **Dogfood smoke:** `assayxport scan .` on assayxport itself now shows populated complexity on its own Go functions.

## Out of scope (SP4)

- Interprocedural analysis (callee cost propagation), call graphs.
- Constant-bound loop detection: `for i := 0; i < 10; i++` is conservatively counted as `O(n)`. Documented, not special-cased.
- Amortized analysis, average-case, logarithmic detection (binary-search / divide-and-conquer), and any recursion bound (all recursion -> null).
- Data-structure-aware costs (e.g. a hash-map lookup treated as O(1)) beyond the allocation heuristic for space.
- Confidence scores or explanatory notes (the decision was to keep the three existing fields; `method` names the heuristic).

## Risks / open items

- **Heuristic crudeness:** the estimate is a coarse triage signal. The `method` field ("loop-nesting"/"recursive") and this spec's limitations section make the best-effort nature explicit; consumers must not treat it as a proof.
- **gotreesitter node parity (Python/Java):** loop/call/alloc node names must be confirmed against the actual parser (probe-and-record), as in SP2/SP3. Comprehension node names for Python and `do_statement`/`enhanced_for_statement` for Java are the notable ones.
- **Method recursion detection in Go/Java:** a method calling itself via a receiver/`this` selector (`this.foo()` / `p.foo()`) is harder than a bare-name call. The baseline is bare-name self-call detection; selector-based self-calls are a documented best-effort (may under-report recursion, which conservatively yields a loop-nesting bound rather than null).
- **Golden churn:** every function-like symbol in all three goldens changes. This is expected and reviewed for shape (only `complexity` objects change).
