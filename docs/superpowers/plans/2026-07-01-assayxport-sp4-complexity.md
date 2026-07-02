# assayxport SP4 (big-O complexity) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fill the reserved `complexity` slot on function-like symbols with a best-effort, intraprocedural, syntactic big-O estimate (time + space) across Go, Python, and Java.

**Architecture:** A shared `internal/complexity` engine owns a language-agnostic `Summary` struct and a pure `Estimate(Summary) schema.Complexity`. Each extractor gains a thin walker that produces a `Summary` from its own representation (Go via `go/ast` + `astutil.Apply`; Python/Java via `internal/ts` tree recursion) and sets the computed complexity on `func`/`method`/`constructor` symbols. No schema change.

**Tech Stack:** Go 1.24, `go/ast` + `golang.org/x/tools/go/ast/astutil` (already a dependency), `internal/ts` (gotreesitter), `internal/schema`, `internal/emit`.

## Global Constraints

- License MIT. Module `goforge.dev/assayxport`, `go 1.24` (do NOT bump; do NOT add cgo). Run every go command as `GOTOOLCHAIN=local go ...`.
- No em-dashes or en-dashes anywhere in code, comments, or docs.
- All tree-sitter access via `internal/ts` ONLY. `internal/complexity` must NOT import `go/ast` or `internal/ts` (it takes a plain `Summary` and returns `schema.Complexity`).
- No schema change: the `complexity` object shape (`time *string`, `space *string`, `method string`) is fixed; `schema_version` stays `"1"`. Only values change.
- Determinism (hard): estimate is a pure function of the parse tree; output byte-identical on re-run. Goldens regenerate but the diff must be confined to `complexity` objects on function-like symbols.
- Honesty: emit a bound only from loop nesting; recursion yields `{nil, nil, "recursive"}`; the estimate is intraprocedural (callee cost NOT propagated) and constant-bound loops are conservatively counted as `O(n)`.
- `method` values across the tool: `"deferred"` (non-analyzed symbols), `"loop-nesting"` (a bound was computed), `"recursive"` (declined due to recursion).

**Reference interfaces (already in the tree):**
- `schema.Complexity{Time *string, Space *string, Method string}`; `schema.DeferredComplexity()` returns `{nil, nil, "deferred"}`.
- Go extractor `internal/extract/golang/symbols.go`: `funcSymbol(p *packages.Package, fd *ast.FuncDecl, moduleDir string) (schema.Symbol, bool)` sets `Complexity: schema.DeferredComplexity()` at line ~270; `fd.Body` is the `*ast.BlockStmt` body (nil for bodiless decls), `fd.Name.Name` the function name.
- Python extractor `internal/extract/python/symbols.go`: `funcSymbol(node ts.Node, owner string, src []byte, relFile string, decorators []string, isAsync bool) schema.Symbol` sets `Complexity: schema.DeferredComplexity()`; `node` is the `function_definition` ts.Node.
- Java extractor `internal/extract/java/symbols.go`: `methodSymbol(node ts.Node, owner string, src []byte, relFile string, doc schema.Doc) schema.Symbol` and `constructorSymbol(node ts.Node, owner, typeName string, src []byte, relFile string, doc schema.Doc) schema.Symbol` set `Complexity: schema.DeferredComplexity()`; `node` is the `method_declaration` / `constructor_declaration` ts.Node.
- `internal/ts` Node API: `Type() string`, `NamedChildCount() int`, `NamedChild(i) ts.Node`, `ChildByFieldName(f) (ts.Node, bool)`, `Content(src) string`, `IsNull() bool`.

---

## File Structure

- `internal/complexity/complexity.go` (create) - `Summary` + `Estimate` + `bigO`.
- `internal/complexity/complexity_test.go` (create).
- `internal/extract/golang/complexity.go` (create) - `goSummary(fd *ast.FuncDecl) complexity.Summary`.
- `internal/extract/golang/symbols.go` (modify) - call it in `funcSymbol`.
- `internal/extract/golang/testdata/*` + golden (modify/regenerate); add complexity fixture funcs.
- `internal/extract/python/complexity.go` (create) - `pySummary(node ts.Node, src []byte, name string) complexity.Summary`.
- `internal/extract/python/symbols.go` (modify) - call it in `funcSymbol`.
- `internal/extract/python/testdata/*` + golden (modify/regenerate).
- `internal/extract/java/complexity.go` (create) - `javaSummary(node ts.Node, src []byte, name string) complexity.Summary`.
- `internal/extract/java/symbols.go` (modify) - call it in `methodSymbol`/`constructorSymbol`.
- `internal/extract/java/testdata/*` + golden (modify/regenerate).
- `README.md` (modify) - document populated complexity + method values + limitations.

---

### Task 1: The shared complexity engine

**Files:**
- Create: `internal/complexity/complexity.go`
- Test: `internal/complexity/complexity_test.go`

**Interfaces:**
- Produces:
  ```go
  package complexity
  type Summary struct { MaxLoopDepth int; Recursive bool; AllocInLoop bool; AllocDepth int }
  func Estimate(s Summary) schema.Complexity
  ```

- [ ] **Step 1: Write the failing test**

Create `internal/complexity/complexity_test.go`:

```go
package complexity

import "testing"

func str(p *string) string {
	if p == nil {
		return "nil"
	}
	return *p
}

func TestEstimate(t *testing.T) {
	cases := []struct {
		name              string
		in                Summary
		time, space, meth string
	}{
		{"constant", Summary{}, "O(1)", "O(1)", "loop-nesting"},
		{"linear", Summary{MaxLoopDepth: 1}, "O(n)", "O(1)", "loop-nesting"},
		{"quadratic", Summary{MaxLoopDepth: 2}, "O(n^2)", "O(1)", "loop-nesting"},
		{"cubic", Summary{MaxLoopDepth: 3}, "O(n^3)", "O(1)", "loop-nesting"},
		{"alloc in loop", Summary{MaxLoopDepth: 1, AllocInLoop: true, AllocDepth: 1}, "O(n)", "O(n)", "loop-nesting"},
		{"recursive", Summary{Recursive: true}, "nil", "nil", "recursive"},
		{"recursive beats loops", Summary{MaxLoopDepth: 2, Recursive: true}, "nil", "nil", "recursive"},
	}
	for _, c := range cases {
		got := Estimate(c.in)
		if str(got.Time) != c.time || str(got.Space) != c.space || got.Method != c.meth {
			t.Errorf("%s: got {%s, %s, %s}, want {%s, %s, %s}",
				c.name, str(got.Time), str(got.Space), got.Method, c.time, c.space, c.meth)
		}
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `GOTOOLCHAIN=local go test ./internal/complexity/`
Expected: FAIL - `undefined: Estimate` / `undefined: Summary`.

- [ ] **Step 3: Implement the engine**

Create `internal/complexity/complexity.go`:

```go
// Package complexity is assayxport's best-effort, intraprocedural, syntactic
// big-O estimator. It maps a language-agnostic control-flow Summary (produced by
// each language extractor) to a schema.Complexity. The estimate is a coarse
// triage signal, not a proof: it counts loop nesting, declines to bound
// recursion, and never propagates callee cost.
package complexity

import "goforge.dev/assayxport/internal/schema"

// Summary is a normalized control-flow summary of one function body.
type Summary struct {
	MaxLoopDepth int  // deepest nesting of loops (0 = no loops)
	Recursive    bool // the body calls the function's own name (direct recursion)
	AllocInLoop  bool // an allocation occurs inside at least one loop
	AllocDepth   int  // loop-nesting depth at the deepest in-loop allocation
}

// Estimate maps a Summary to a big-O complexity. Pure and deterministic.
func Estimate(s Summary) schema.Complexity {
	if s.Recursive {
		// Loop nesting cannot bound a recursive function; refuse to guess.
		return schema.Complexity{Time: nil, Space: nil, Method: "recursive"}
	}
	t := bigO(s.MaxLoopDepth)
	space := "O(1)"
	if s.AllocInLoop {
		space = bigO(s.AllocDepth)
	}
	return schema.Complexity{Time: &t, Space: &space, Method: "loop-nesting"}
}

// bigO renders a polynomial degree as a big-O string.
func bigO(degree int) string {
	switch {
	case degree <= 0:
		return "O(1)"
	case degree == 1:
		return "O(n)"
	default:
		return "O(n^" + itoa(degree) + ")"
	}
}

// itoa avoids a strconv import for small non-negative integers.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
```

- [ ] **Step 4: Run it and watch it pass**

Run: `GOTOOLCHAIN=local go test ./internal/complexity/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/complexity/
git commit -m "feat(complexity): shared big-O estimation engine"
```

---

### Task 2: Go walker + wiring + golden

**Files:**
- Create: `internal/extract/golang/complexity.go`
- Modify: `internal/extract/golang/symbols.go` (funcSymbol, line ~270)
- Test: `internal/extract/golang/complexity_test.go`
- Modify: fixture + `internal/extract/golang/testdata/sample.golden.json` (regenerate)

**Interfaces:**
- Consumes: `complexity.Summary`/`complexity.Estimate` (Task 1); `*ast.FuncDecl`.
- Produces: `goSummary(fd *ast.FuncDecl) complexity.Summary`.

- [ ] **Step 1: Write the failing test**

Add complexity-shaped functions to the Go fixture. Find the fixture directory (the one whose golden is `internal/extract/golang/testdata/sample.golden.json`; read `internal/extract/golang/golang_test.go` to confirm the fixture root, commonly `internal/extract/golang/testdata/sample`). Add a file `shapes.go` in that fixture's main package:

```go
package main

// Constant does no looping.
func Constant(x int) int { return x + 1 }

// Linear loops once.
func Linear(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	return total
}

// Quadratic nests two loops.
func Quadratic(xs []int) int {
	n := 0
	for range xs {
		for range xs {
			n++
		}
	}
	return n
}

// Collect allocates inside a loop (space O(n)).
func Collect(xs []int) []int {
	out := make([]int, 0)
	for _, x := range xs {
		out = append(out, x*2)
	}
	return out
}

// Recur calls itself.
func Recur(n int) int {
	if n <= 0 {
		return 0
	}
	return Recur(n - 1)
}
```

Create `internal/extract/golang/complexity_test.go`:

```go
package golang

import "testing"

func symComplexity(t *testing.T, id string) (time, space, method string) {
	t.Helper()
	pkgs, err := New().Extract("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkgs {
		for _, s := range p.Symbols {
			if s.ID == id {
				tm, sp := "nil", "nil"
				if s.Complexity.Time != nil {
					tm = *s.Complexity.Time
				}
				if s.Complexity.Space != nil {
					sp = *s.Complexity.Space
				}
				return tm, sp, s.Complexity.Method
			}
		}
	}
	t.Fatalf("symbol %q not found", id)
	return "", "", ""
}

func TestGoComplexity(t *testing.T) {
	cases := []struct{ id, time, space, method string }{
		{"Constant", "O(1)", "O(1)", "loop-nesting"},
		{"Linear", "O(n)", "O(1)", "loop-nesting"},
		{"Quadratic", "O(n^2)", "O(1)", "loop-nesting"},
		{"Collect", "O(n)", "O(n)", "loop-nesting"},
		{"Recur", "nil", "nil", "recursive"},
	}
	for _, c := range cases {
		tm, sp, m := symComplexity(t, c.id)
		if tm != c.time || sp != c.space || m != c.method {
			t.Errorf("%s: got {%s,%s,%s} want {%s,%s,%s}", c.id, tm, sp, m, c.time, c.space, c.method)
		}
	}
}
```

If the fixture root is not `testdata/sample`, use the actual path in both the test and the `-update` command below.

- [ ] **Step 2: Run it and watch it fail**

Run: `GOTOOLCHAIN=local go test ./internal/extract/golang/ -run TestGoComplexity`
Expected: FAIL - complexity is still `deferred` (method mismatch) and `undefined: goSummary` once referenced.

- [ ] **Step 3: Implement the walker**

Create `internal/extract/golang/complexity.go`:

```go
package golang

import (
	"go/ast"

	"golang.org/x/tools/go/ast/astutil"

	"goforge.dev/assayxport/internal/complexity"
)

// goSummary walks a function body and produces a control-flow Summary. It uses
// astutil.Apply's pre/post callbacks to track loop-nesting depth. Recursion is
// detected by a bare-name self-call (selector self-calls like p.foo() are not
// detected, which conservatively yields a loop-nesting bound rather than nil).
func goSummary(fd *ast.FuncDecl) complexity.Summary {
	var sum complexity.Summary
	if fd == nil || fd.Body == nil {
		return sum // bodiless (external/asm) or nil -> O(1)
	}
	name := fd.Name.Name
	depth := 0
	astutil.Apply(fd.Body, func(c *astutil.Cursor) bool {
		switch n := c.Node().(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			depth++
			if depth > sum.MaxLoopDepth {
				sum.MaxLoopDepth = depth
			}
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok {
				if id.Name == name {
					sum.Recursive = true
				}
				if id.Name == "make" || id.Name == "new" || id.Name == "append" {
					recordAlloc(&sum, depth)
				}
			}
		case *ast.CompositeLit:
			recordAlloc(&sum, depth)
		}
		return true
	}, func(c *astutil.Cursor) bool {
		switch c.Node().(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			depth--
		}
		return true
	})
	return sum
}

// recordAlloc notes an allocation at the current loop depth (only in-loop
// allocations affect the space estimate).
func recordAlloc(sum *complexity.Summary, depth int) {
	if depth >= 1 {
		sum.AllocInLoop = true
		if depth > sum.AllocDepth {
			sum.AllocDepth = depth
		}
	}
}
```

- [ ] **Step 4: Wire it into funcSymbol**

In `internal/extract/golang/symbols.go`, add the import `"goforge.dev/assayxport/internal/complexity"` and, in `funcSymbol`, replace the field
```go
		Complexity:      schema.DeferredComplexity(),
```
(the one inside `funcSymbol`, around line 270) with
```go
		Complexity:      complexity.Estimate(goSummary(fd)),
```
Leave every OTHER `schema.DeferredComplexity()` (types, fields, values, interface methods) unchanged.

- [ ] **Step 5: Run it and watch it pass**

Run: `GOTOOLCHAIN=local go test ./internal/extract/golang/ -run TestGoComplexity`
Expected: PASS.

- [ ] **Step 6: Regenerate the golden**

```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
GOTOOLCHAIN=local go test ./internal/extract/golang/ -run TestSampleGolden -update
GOTOOLCHAIN=local go test ./internal/extract/golang/ -run TestSampleGolden
```
(Use the actual golden test name from `golang_test.go` if it differs.) Inspect the golden diff: `complexity` objects on FUNCTION/METHOD symbols now carry `"time"`/`"space"`/`"method"` values; complexity on types/fields/consts/vars/interface-methods stays `{"time":null,"space":null,"method":"deferred"}`. Nothing else changes.

- [ ] **Step 7: Full package test + vet + commit**

Run: `GOTOOLCHAIN=local go test ./internal/extract/golang/ && GOTOOLCHAIN=local go vet ./...`
Expected: PASS; vet clean.

```bash
git add internal/complexity/ internal/extract/golang/
git commit -m "feat(complexity): Go walker + wire into extractor; regen golden"
```

---

### Task 3: Python walker + wiring + golden

**Files:**
- Create: `internal/extract/python/complexity.go`
- Modify: `internal/extract/python/symbols.go` (funcSymbol)
- Test: `internal/extract/python/complexity_test.go`
- Modify: `internal/extract/python/testdata/proj/**` fixture + golden (regenerate)

**Interfaces:**
- Consumes: `complexity.Summary`/`Estimate`; `internal/ts`.
- Produces: `pySummary(node ts.Node, src []byte, name string) complexity.Summary`.

**Node names:** confirm against the actual parser before trusting them (probe: parse a snippet with a for/while/comprehension/self-call/append, print `.Type()`s, then delete the probe). Canonical tree-sitter-python names: loops `for_statement`, `while_statement`; comprehensions `list_comprehension`, `set_comprehension`, `dictionary_comprehension`, `generator_expression`; a call is `call` with a `function` field; an attribute call's function is an `attribute` node with an `attribute` field naming the method; collection displays `list`, `dictionary`, `set`. Record any deviation in a comment.

- [ ] **Step 1: Write the failing test + fixture**

Add a module `internal/extract/python/testdata/proj/pkg/shapes.py`:

```python
def constant(x):
    return x + 1

def linear(xs):
    total = 0
    for x in xs:
        total += x
    return total

def quadratic(xs):
    n = 0
    for a in xs:
        for b in xs:
            n += 1
    return n

def collect(xs):
    out = []
    for x in xs:
        out.append(x * 2)
    return out

def recur(n):
    if n <= 0:
        return 0
    return recur(n - 1)
```

Create `internal/extract/python/complexity_test.go`:

```go
package python

import "testing"

func symComplexity(t *testing.T, moduleID, symID string) (time, space, method string) {
	t.Helper()
	pkgs, err := New().Extract("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkgs {
		if p.ID != moduleID {
			continue
		}
		for _, s := range p.Symbols {
			if s.ID == symID {
				tm, sp := "nil", "nil"
				if s.Complexity.Time != nil {
					tm = *s.Complexity.Time
				}
				if s.Complexity.Space != nil {
					sp = *s.Complexity.Space
				}
				return tm, sp, s.Complexity.Method
			}
		}
	}
	t.Fatalf("symbol %q in %q not found", symID, moduleID)
	return "", "", ""
}

func TestPyComplexity(t *testing.T) {
	cases := []struct{ sym, time, space, method string }{
		{"constant", "O(1)", "O(1)", "loop-nesting"},
		{"linear", "O(n)", "O(1)", "loop-nesting"},
		{"quadratic", "O(n^2)", "O(1)", "loop-nesting"},
		{"collect", "O(n)", "O(n)", "loop-nesting"},
		{"recur", "nil", "nil", "recursive"},
	}
	for _, c := range cases {
		tm, sp, m := symComplexity(t, "pkg.shapes", c.sym)
		if tm != c.time || sp != c.space || m != c.method {
			t.Errorf("%s: got {%s,%s,%s} want {%s,%s,%s}", c.sym, tm, sp, m, c.time, c.space, c.method)
		}
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `GOTOOLCHAIN=local go test ./internal/extract/python/ -run TestPyComplexity`
Expected: FAIL - complexity still `deferred`; `undefined: pySummary` once referenced.

- [ ] **Step 3: Implement the walker**

Create `internal/extract/python/complexity.go` implementing:

```go
package python

import (
	"strings"

	"goforge.dev/assayxport/internal/complexity"
	"goforge.dev/assayxport/internal/ts"
)

// pySummary walks a function_definition body producing a control-flow Summary.
// Loop nodes (for/while) and comprehensions increment depth; a bare-name call to
// the def's own name is recursion; list/dict/set displays and .append/.extend/
// .add/.update calls are allocations.
func pySummary(node ts.Node, src []byte, name string) complexity.Summary {
	var sum complexity.Summary
	body, ok := node.ChildByFieldName("body")
	if !ok {
		return sum
	}
	var walk func(n ts.Node, depth int)
	walk = func(n ts.Node, depth int) {
		for i := 0; i < n.NamedChildCount(); i++ {
			c := n.NamedChild(i)
			d := depth
			switch c.Type() {
			case "for_statement", "while_statement",
				"list_comprehension", "set_comprehension",
				"dictionary_comprehension", "generator_expression":
				d = depth + 1
				if d > sum.MaxLoopDepth {
					sum.MaxLoopDepth = d
				}
				if c.Type() != "for_statement" && c.Type() != "while_statement" {
					recordAlloc(&sum, depth) // a comprehension allocates at the enclosing depth
				}
			case "call":
				if pyIsSelfCall(c, src, name) {
					sum.Recursive = true
				}
				if pyIsAllocCall(c, src) {
					recordAlloc(&sum, depth)
				}
			case "list", "dictionary", "set":
				recordAlloc(&sum, depth)
			}
			walk(c, d)
		}
	}
	walk(body, 0)
	return sum
}

func recordAlloc(sum *complexity.Summary, depth int) {
	if depth >= 1 {
		sum.AllocInLoop = true
		if depth > sum.AllocDepth {
			sum.AllocDepth = depth
		}
	}
}

// pyIsSelfCall reports whether a call node invokes the bare name `name`.
func pyIsSelfCall(call ts.Node, src []byte, name string) bool {
	fn, ok := call.ChildByFieldName("function")
	if !ok {
		return false
	}
	return fn.Type() == "identifier" && fn.Content(src) == name
}

// pyIsAllocCall reports whether a call is x.append/extend/add/update(...).
func pyIsAllocCall(call ts.Node, src []byte) bool {
	fn, ok := call.ChildByFieldName("function")
	if !ok || fn.Type() != "attribute" {
		return false
	}
	attr, ok := fn.ChildByFieldName("attribute")
	if !ok {
		return false
	}
	switch strings.TrimSpace(attr.Content(src)) {
	case "append", "extend", "add", "update":
		return true
	}
	return false
}
```

Confirm `recordAlloc` is not already defined in the package; if the Go plan already added one it is in a DIFFERENT package (`golang`), so this `python`-package copy is fine. If a `recordAlloc` already exists in package `python`, reuse it instead of redefining.

- [ ] **Step 4: Wire it into funcSymbol**

In `internal/extract/python/symbols.go` `funcSymbol`, add the `complexity` import and replace its `Complexity: schema.DeferredComplexity()` with `Complexity: complexity.Estimate(pySummary(node, src, name))` (the `name` local is the def name already computed at the top of `funcSymbol`). Leave class/variable complexity as `DeferredComplexity()`.

- [ ] **Step 5: Run it and watch it pass**

Run: `GOTOOLCHAIN=local go test ./internal/extract/python/ -run TestPyComplexity`
Expected: PASS.

- [ ] **Step 6: Regenerate the golden**

```bash
GOTOOLCHAIN=local go test ./internal/extract/python/ -run TestGolden -update
GOTOOLCHAIN=local go test ./internal/extract/python/ -run TestGolden
```
Inspect: complexity on functions/methods/properties now computed; on classes/variables still `deferred`. Nothing else changes.

- [ ] **Step 7: Full package test + vet + commit**

Run: `GOTOOLCHAIN=local go test ./internal/extract/python/ && GOTOOLCHAIN=local go vet ./...`
Expected: PASS; vet clean.

```bash
git add internal/extract/python/
git commit -m "feat(complexity): Python walker + wire into extractor; regen golden"
```

---

### Task 4: Java walker + wiring + golden

**Files:**
- Create: `internal/extract/java/complexity.go`
- Modify: `internal/extract/java/symbols.go` (methodSymbol, constructorSymbol)
- Test: `internal/extract/java/complexity_test.go`
- Modify: `internal/extract/java/testdata/proj/**` fixture + golden (regenerate)

**Interfaces:**
- Consumes: `complexity.Summary`/`Estimate`; `internal/ts`.
- Produces: `javaSummary(node ts.Node, src []byte, name string) complexity.Summary`.

**Node names:** confirm against the actual gotreesitter parser (probe, then delete). Canonical tree-sitter-java: loops `for_statement`, `enhanced_for_statement`, `while_statement`, `do_statement`; a call is `method_invocation` with a `name` field (and an optional `object` field); allocations `object_creation_expression`, `array_creation_expression`; collection mutators are `method_invocation` whose name is `add`/`put`/`addAll`. Record deviations in a comment (SP3 already recorded several in `internal/ts/provenance.md`).

- [ ] **Step 1: Write the failing test + fixture**

Add `internal/extract/java/testdata/proj/com/foo/Shapes.java`:

```java
package com.foo;

import java.util.ArrayList;
import java.util.List;

public class Shapes {
    public int constant(int x) {
        return x + 1;
    }

    public int linear(int[] xs) {
        int total = 0;
        for (int x : xs) {
            total += x;
        }
        return total;
    }

    public int quadratic(int[] xs) {
        int n = 0;
        for (int a : xs) {
            for (int b : xs) {
                n++;
            }
        }
        return n;
    }

    public List<Integer> collect(int[] xs) {
        List<Integer> out = new ArrayList<>();
        for (int x : xs) {
            out.add(x * 2);
        }
        return out;
    }

    public int recur(int n) {
        if (n <= 0) {
            return 0;
        }
        return recur(n - 1);
    }
}
```

Create `internal/extract/java/complexity_test.go`:

```go
package java

import "testing"

func symComplexity(t *testing.T, symID string) (time, space, method string) {
	t.Helper()
	pkgs, err := New().Extract("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkgs {
		for _, s := range p.Symbols {
			if s.ID == symID {
				tm, sp := "nil", "nil"
				if s.Complexity.Time != nil {
					tm = *s.Complexity.Time
				}
				if s.Complexity.Space != nil {
					sp = *s.Complexity.Space
				}
				return tm, sp, s.Complexity.Method
			}
		}
	}
	t.Fatalf("symbol %q not found", symID)
	return "", "", ""
}

func TestJavaComplexity(t *testing.T) {
	cases := []struct{ sym, time, space, method string }{
		{"Shapes.constant", "O(1)", "O(1)", "loop-nesting"},
		{"Shapes.linear", "O(n)", "O(1)", "loop-nesting"},
		{"Shapes.quadratic", "O(n^2)", "O(1)", "loop-nesting"},
		{"Shapes.collect", "O(n)", "O(n)", "loop-nesting"},
		{"Shapes.recur", "nil", "nil", "recursive"},
	}
	for _, c := range cases {
		tm, sp, m := symComplexity(t, c.sym)
		if tm != c.time || sp != c.space || m != c.method {
			t.Errorf("%s: got {%s,%s,%s} want {%s,%s,%s}", c.sym, tm, sp, m, c.time, c.space, c.method)
		}
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `GOTOOLCHAIN=local go test ./internal/extract/java/ -run TestJavaComplexity`
Expected: FAIL - complexity still `deferred`; `undefined: javaSummary` once referenced.

- [ ] **Step 3: Implement the walker**

Create `internal/extract/java/complexity.go`:

```go
package java

import (
	"goforge.dev/assayxport/internal/complexity"
	"goforge.dev/assayxport/internal/ts"
)

// javaSummary walks a method/constructor body producing a control-flow Summary.
// for/enhanced-for/while/do increment depth; a self-named method_invocation with
// no scoping object (or `this`) is recursion; new/array-creation and collection
// add/put/addAll are allocations.
func javaSummary(node ts.Node, src []byte, name string) complexity.Summary {
	var sum complexity.Summary
	body, ok := node.ChildByFieldName("body")
	if !ok {
		return sum // abstract/interface method -> O(1)
	}
	var walk func(n ts.Node, depth int)
	walk = func(n ts.Node, depth int) {
		for i := 0; i < n.NamedChildCount(); i++ {
			c := n.NamedChild(i)
			d := depth
			switch c.Type() {
			case "for_statement", "enhanced_for_statement", "while_statement", "do_statement":
				d = depth + 1
				if d > sum.MaxLoopDepth {
					sum.MaxLoopDepth = d
				}
			case "method_invocation":
				if javaIsSelfCall(c, src, name) {
					sum.Recursive = true
				}
				if javaIsAllocCall(c, src) {
					recordAlloc(&sum, depth)
				}
			case "object_creation_expression", "array_creation_expression":
				recordAlloc(&sum, depth)
			}
			walk(c, d)
		}
	}
	walk(body, 0)
	return sum
}

func recordAlloc(sum *complexity.Summary, depth int) {
	if depth >= 1 {
		sum.AllocInLoop = true
		if depth > sum.AllocDepth {
			sum.AllocDepth = depth
		}
	}
}

// javaIsSelfCall reports whether a method_invocation calls `name` with no object
// or an implicit `this` receiver.
func javaIsSelfCall(call ts.Node, src []byte, name string) bool {
	nm, ok := call.ChildByFieldName("name")
	if !ok || nm.Content(src) != name {
		return false
	}
	obj, ok := call.ChildByFieldName("object")
	if !ok {
		return true // no receiver -> implicit this
	}
	return obj.Content(src) == "this"
}

// javaIsAllocCall reports whether a method_invocation is a collection mutator.
func javaIsAllocCall(call ts.Node, src []byte) bool {
	nm, ok := call.ChildByFieldName("name")
	if !ok {
		return false
	}
	switch nm.Content(src) {
	case "add", "put", "addAll":
		return true
	}
	return false
}
```

- [ ] **Step 4: Wire it into methodSymbol and constructorSymbol**

In `internal/extract/java/symbols.go`, add the `complexity` import. In `methodSymbol`, replace `Complexity: schema.DeferredComplexity()` with `Complexity: complexity.Estimate(javaSummary(node, src, name))` (the `name` local is the method name from `fieldText(node,"name",src)`). In `constructorSymbol`, replace it with `Complexity: complexity.Estimate(javaSummary(node, src, typeName))` (a constructor's "own name" is the type name; recursion via that name is fine). Leave field/enum-constant/type/annotation-element complexity as-is EXCEPT `annotationElementSymbol` (annotation elements are abstract - no body - so `javaSummary` returns the zero Summary -> `O(1)`; wiring it is optional. Keep annotation elements as `DeferredComplexity()` to avoid implying a body they do not have).

- [ ] **Step 5: Run it and watch it pass**

Run: `GOTOOLCHAIN=local go test ./internal/extract/java/ -run TestJavaComplexity`
Expected: PASS.

- [ ] **Step 6: Regenerate the golden**

```bash
GOTOOLCHAIN=local go test ./internal/extract/java/ -run TestGolden -update
GOTOOLCHAIN=local go test ./internal/extract/java/ -run TestGolden
```
Inspect: complexity on methods/constructors now computed; on types/fields/enum-constants/annotation-elements still `deferred`. Nothing else changes.

- [ ] **Step 7: Full suite + vet + commit**

Run: `GOTOOLCHAIN=local go test ./... && GOTOOLCHAIN=local go vet ./...`
Expected: all PASS; vet clean.

```bash
git add internal/extract/java/
git commit -m "feat(complexity): Java walker + wire into extractor; regen golden"
```

---

### Task 5: Document complexity

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: nothing code-level.
- Produces: README documents populated complexity, `method` values, and the honest limitations.

- [ ] **Step 1: Update the README**

Add a `Complexity` subsection under the output/languages docs stating: each function-like symbol carries a best-effort big-O estimate in `complexity` (`time`, `space`, `method`); `method` is `loop-nesting` (a bound derived from loop nesting), `recursive` (declined - `time`/`space` are null), or `deferred` (a symbol not analyzed, e.g. a type or field). State the limitations verbatim from the spec: intraprocedural (callee cost not propagated), constant-bound loops conservatively counted as `O(n)`, recursion never bounded, space is a weak allocation-based signal. Emphasize it is a triage signal, not a proof. No em/en-dashes.

- [ ] **Step 2: Confirm no dashes + final gate**

Run: `grep -rnP '[\x{2013}\x{2014}]' --include=*.go --include=*.md internal/ cmd/ NOTICE README.md` (expect no matches), then `GOTOOLCHAIN=local go test ./... && GOTOOLCHAIN=local go vet ./...` (expect all PASS, vet clean).

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document best-effort complexity estimates and their limits"
```

---

## Self-Review

**Spec coverage:**
- Shared engine (`Summary` + `Estimate`, no go/ast or ts import) -> Task 1. ✓
- Per-language walkers (Go astutil, Python/Java ts recursion) -> Tasks 2-4. ✓
- Estimate mapping (recursive -> nil/nil/"recursive"; else loop-nesting time + alloc-in-loop space) -> Task 1 engine + tests. ✓
- Wiring into func/method/constructor only; other kinds stay deferred -> Tasks 2-4 Step 4. ✓
- All three goldens regenerated, diff confined to complexity objects -> Tasks 2-4 Step 6. ✓
- No schema change, version "1" -> engine returns schema.Complexity; no schema edit in any task. ✓
- Determinism / double-run -> pure function; existing double-run tests unaffected (Python/Java have them; values deterministic). ✓
- Honest limitations documented -> Task 5. ✓
- Out of scope (interprocedural, constant-bound loops, recursion bound) -> not implemented; documented. ✓

**Placeholder scan:** No TBD/TODO. Engine + Go walker are complete verbatim code; Python/Java walkers are complete code with a probe-and-confirm note for node names (the only runtime-dependent part), matching SP2/SP3 discipline.

**Type/name consistency:** `complexity.Summary{MaxLoopDepth, Recursive, AllocInLoop, AllocDepth}` and `complexity.Estimate` (Task 1) are consumed unchanged in Tasks 2-4. `goSummary(*ast.FuncDecl)`, `pySummary(ts.Node,[]byte,string)`, `javaSummary(ts.Node,[]byte,string)` each return `complexity.Summary`. `recordAlloc` is defined once PER extractor package (golang, python, java) - three separate package-local copies, not a shared symbol (each package is distinct; no collision, no cross-package import needed). The wiring replaces exactly the `funcSymbol`/`methodSymbol`/`constructorSymbol` `DeferredComplexity()` calls named in the spec, leaving all other symbol kinds deferred.

**Cross-task note:** Task 1 must land before 2-4 (they import `internal/complexity`). Tasks 2, 3, 4 are otherwise independent (different packages + goldens) but must run after Task 1. Each regenerates only its own language golden. The Go golden test name, Python/Java `TestGolden` names, and fixture roots should be confirmed from each package's existing `*_test.go` before running `-update` (the plan names the common values; adjust if a package differs).
