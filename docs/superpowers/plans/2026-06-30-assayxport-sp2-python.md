# assayxport SP2 (Python) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Python support to assayxport via a pure-Go tree-sitter layer (wazero + embedded WASM grammar) and a Python extractor emitting the existing manifest schema, with polyglot dispatch so one `scan` covers Go and Python.

**Architecture:** A stable in-house wrapper `internal/ts` isolates the third-party tree-sitter-via-wazero library behind our own `Parse`/`Node` API. `internal/extract/python` walks the resulting syntax tree into `schema.Package` values (module + package units). A registry dispatches every selected language extractor over the scan root and merges results; the CLI gains a repeatable `--lang` filter. The schema grows additively.

**Tech Stack:** Go 1.24, `github.com/malivvan/tree-sitter` (wazero, no cgo), embedded `python.wasm` grammar, stdlib `go/*` (SP1), `encoding/json`.

## Global Constraints

- Module `goforge.dev/assayxport`; `go.mod` stays `go 1.24` with NO `toolchain` line; deps pinned (not `@latest`). Run every go command with `GOTOOLCHAIN=local`.
- MIT license. Vendored tree-sitter + grammar are MIT third-party → attributed in `NOTICE`.
- Determinism (mandatory, unchanged from SP1): relative POSIX paths; NO timestamps/absolute/host data; packages sorted by id; symbols by `(file, line, name)`; 2-space JSON + trailing newline + no HTML escaping; byte-identical on re-run.
- No external runtime (no CPython); grammar `.wasm` is vendored + `go:embed`'d. No cgo — if the library cannot work without cgo, escalate; do not add cgo.
- `schema_version` stays the string `"1"`; all new fields are `omitempty`. Both exported AND unexported symbols captured (visibility is a label).
- `complexity` is always `{time:null,space:null,method:"deferred"}`.
- SP2 = Python + shared `internal/ts` + polyglot dispatch. OUT: Java (SP3), big-O values (SP4), import resolution, inferred types, docstring-style parsing.

---

### Task 1: `internal/ts` — pure-Go tree-sitter layer + Python grammar (build probe)

**Files:**
- Create: `internal/ts/ts.go`
- Create: `internal/ts/grammars/` (vendored `python.wasm` if the library does not bundle Python)
- Create: `internal/ts/provenance.md` (grammar source + version + sha256, if vendored)
- Test: `internal/ts/ts_test.go`

**Interfaces:**
- Consumes: nothing (SP1 code untouched).
- Produces the STABLE in-house API every downstream Python task uses (so the third-party library is referenced ONLY here):

```go
package ts

// Language selects a grammar.
type Language int
const ( Python Language = iota )

// Tree is a parsed syntax tree; keep the source alongside for text slicing.
type Tree struct { /* holds root + src */ }

// Node is a lightweight handle into a Tree.
type Node struct { /* ... */ }

// Parse parses src under lang. Deterministic for equal input.
func Parse(lang Language, src []byte) (*Tree, error)

func (t *Tree) Root() Node

func (n Node) Type() string              // tree-sitter node type, e.g. "function_definition"
func (n Node) NamedChildCount() int
func (n Node) NamedChild(i int) Node
func (n Node) ChildByFieldName(f string) (Node, bool)  // e.g. "name","parameters","body","return_type"
func (n Node) StartLine() int            // 1-based
func (n Node) StartCol() int             // 1-based
func (n Node) EndLine() int              // 1-based
func (n Node) Content(src []byte) string // exact source slice for this node
func (n Node) IsNull() bool
```

- [ ] **Step 1: Add the dependency and probe the API**

```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
GOTOOLCHAIN=local go get github.com/malivvan/tree-sitter@latest
GOTOOLCHAIN=local go doc github.com/malivvan/tree-sitter 2>&1 | head -60
```
Read the godoc to learn the EXACT current signatures (`New(ctx)`, `NewParser`, `SetLanguage`, how to obtain the Python `Language`, and how to parse to a tree / walk nodes). Confirm `go.mod` still says `go 1.24` with no `toolchain` line afterward; if the `go get` bumped it, edit it back to `go 1.24` and pin the dependency to the resolved version. If the library forces go >1.24, STOP and report BLOCKED (do not bump the project).

- [ ] **Step 2: Obtain the Python grammar**

Determine from the godoc/source whether the library exposes a built-in Python language (a `LanguagePython`-style accessor) or requires a grammar `.wasm`.
- If built-in: use it; no vendoring needed.
- If not built-in: obtain a `tree-sitter-python` WASM grammar build, save it to `internal/ts/grammars/python.wasm`, and record its source URL, release/tag, and `sha256` in `internal/ts/provenance.md`. Load it via the library's grammar-from-bytes/wasm API, embedding with:
  ```go
  //go:embed grammars/python.wasm
  var pythonWASM []byte
  ```

- [ ] **Step 3: Write the failing test**

Create `internal/ts/ts_test.go`:

```go
package ts

import "testing"

func TestParsePythonFunction(t *testing.T) {
	src := []byte("def add(a, b):\n    return a + b\n")
	tree, err := Parse(Python, src)
	if err != nil {
		t.Fatal(err)
	}
	root := tree.Root()
	if root.Type() != "module" {
		t.Fatalf("root type = %q, want module", root.Type())
	}
	if root.NamedChildCount() < 1 {
		t.Fatalf("expected at least one named child")
	}
	fn := root.NamedChild(0)
	if fn.Type() != "function_definition" {
		t.Fatalf("child type = %q, want function_definition", fn.Type())
	}
	name, ok := fn.ChildByFieldName("name")
	if !ok || name.Content(src) != "add" {
		t.Fatalf("function name = %q (ok=%v), want add", name.Content(src), ok)
	}
	if name.StartLine() != 1 {
		t.Fatalf("name start line = %d, want 1", name.StartLine())
	}
}

func TestParseDeterministic(t *testing.T) {
	src := []byte("class C:\n    def m(self):\n        pass\n")
	a, err := Parse(Python, src)
	if err != nil { t.Fatal(err) }
	b, err := Parse(Python, src)
	if err != nil { t.Fatal(err) }
	// Structural determinism: same root type + same named child count/types.
	if a.Root().Type() != b.Root().Type() || a.Root().NamedChildCount() != b.Root().NamedChildCount() {
		t.Fatalf("parse not structurally stable across runs")
	}
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `GOTOOLCHAIN=local go test ./internal/ts/`
Expected: FAIL — `undefined: Parse` (wrapper not written yet).

- [ ] **Step 5: Implement the wrapper**

Write `internal/ts/ts.go` implementing the in-house API above, backed by `github.com/malivvan/tree-sitter`. Use the exact signatures learned in Step 1. A single package-level `sync.Once` initializes the `TreeSitter` runtime + parser + Python language once (wazero instantiation is the one-time cost); `Parse` is safe for sequential use by the extractor. `Content` slices `src[startByte:endByte]` using the node's byte range. `StartLine`/`StartCol`/`EndLine` convert tree-sitter's 0-based points to 1-based. Keep ALL third-party imports confined to this file.

If, after honest effort, `malivvan/tree-sitter` cannot load a Python grammar deterministically, switch to the fallback `github.com/odvcencio/gotreesitter` (adjust only this file + the dep) and note the switch in `provenance.md`. If neither works without cgo, STOP and report BLOCKED.

- [ ] **Step 6: Run test to verify it passes**

Run: `GOTOOLCHAIN=local go test ./internal/ts/ && GOTOOLCHAIN=local go vet ./...`
Expected: both tests PASS; vet clean.

- [ ] **Step 7: Commit**

```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
git add go.mod go.sum internal/ts/
git commit -m "feat: pure-Go tree-sitter layer (wazero + Python grammar)"
```

---

### Task 2: Python file discovery + dotted-path derivation

**Files:**
- Create: `internal/extract/python/discover.go`
- Create: `internal/extract/python/testdata/proj/pkg/__init__.py`
- Create: `internal/extract/python/testdata/proj/pkg/mod.py`
- Create: `internal/extract/python/testdata/proj/pkg/sub/__init__.py`
- Create: `internal/extract/python/testdata/proj/pkg/sub/leaf.py`
- Create: `internal/extract/python/testdata/proj/script.py`
- Test: `internal/extract/python/discover_test.go`

**Interfaces:**
- Consumes: nothing (pure filesystem logic).
- Produces:
  ```go
  package python
  // pyFile describes one discovered .py file.
  type pyFile struct {
      Abs       string // absolute path
      Rel       string // POSIX path relative to scan root
      ModuleID  string // dotted module path, e.g. "pkg.mod"; for __init__.py = the package id
      PackageID string // dotted id of the containing package ("" if not in a package)
      IsInit    bool   // true for __init__.py
  }
  // discover walks root and returns all .py files sorted by Rel.
  func discover(root string) ([]pyFile, error)
  ```
  Rule: a directory is a package iff it contains `__init__.py`. A file's dotted path is built by climbing parents while each has `__init__.py`, collecting names top-down, then appending the file stem (omitted for `__init__.py`). A `.py` with no `__init__.py` ancestor is a top-level module (ModuleID = stem, PackageID = "").

- [ ] **Step 1: Create the fixture files**

`internal/extract/python/testdata/proj/pkg/__init__.py`:
```python
"""Package pkg."""
```
`internal/extract/python/testdata/proj/pkg/mod.py`:
```python
"""Module pkg.mod."""
```
`internal/extract/python/testdata/proj/pkg/sub/__init__.py`:
```python
"""Package pkg.sub."""
```
`internal/extract/python/testdata/proj/pkg/sub/leaf.py`:
```python
"""Module pkg.sub.leaf."""
```
`internal/extract/python/testdata/proj/script.py`:
```python
"""Top-level script."""
```

- [ ] **Step 2: Write the failing test**

Create `internal/extract/python/discover_test.go`:

```go
package python

import "testing"

func find(fs []pyFile, rel string) *pyFile {
	for i := range fs {
		if fs[i].Rel == rel {
			return &fs[i]
		}
	}
	return nil
}

func TestDiscover(t *testing.T) {
	fs, err := discover("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}
	// sorted by Rel
	for i := 1; i < len(fs); i++ {
		if fs[i-1].Rel > fs[i].Rel {
			t.Fatalf("not sorted: %q > %q", fs[i-1].Rel, fs[i].Rel)
		}
	}
	cases := []struct{ rel, mod, pkg string; isInit bool }{
		{"pkg/__init__.py", "pkg", "pkg", true},
		{"pkg/mod.py", "pkg.mod", "pkg", false},
		{"pkg/sub/__init__.py", "pkg.sub", "pkg.sub", true},
		{"pkg/sub/leaf.py", "pkg.sub.leaf", "pkg.sub", false},
		{"script.py", "script", "", false},
	}
	for _, c := range cases {
		f := find(fs, c.rel)
		if f == nil {
			t.Fatalf("missing %s", c.rel)
		}
		if f.ModuleID != c.mod || f.PackageID != c.pkg || f.IsInit != c.isInit {
			t.Fatalf("%s => mod=%q pkg=%q init=%v; want mod=%q pkg=%q init=%v",
				c.rel, f.ModuleID, f.PackageID, f.IsInit, c.mod, c.pkg, c.isInit)
		}
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `GOTOOLCHAIN=local go test ./internal/extract/python/ -run TestDiscover`
Expected: FAIL — `undefined: discover`.

- [ ] **Step 4: Implement discovery**

Create `internal/extract/python/discover.go`:

```go
// Package python extracts a Python source tree into assayxport's schema.
package python

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

type pyFile struct {
	Abs       string
	Rel       string
	ModuleID  string
	PackageID string
	IsInit    bool
}

func discover(root string) ([]pyFile, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var out []pyFile
	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// skip dot-dirs and common noise
			base := d.Name()
			if path != absRoot && (strings.HasPrefix(base, ".") || base == "__pycache__") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".py") {
			return nil
		}
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return err
		}
		mod, pkg, isInit := dottedPath(filepath.Dir(path), d.Name())
		out = append(out, pyFile{
			Abs:       path,
			Rel:       filepath.ToSlash(rel),
			ModuleID:  mod,
			PackageID: pkg,
			IsInit:    isInit,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, nil
}

// dottedPath climbs package dirs (those with __init__.py) to build the dotted
// module id, the containing package id, and whether the file is __init__.py.
func dottedPath(dir, filename string) (moduleID, packageID string, isInit bool) {
	isInit = filename == "__init__.py"
	// Collect ancestor package names while each dir has __init__.py.
	var names []string
	d := dir
	for isPackageDir(d) {
		names = append(names, filepath.Base(d))
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	// names is bottom-up; reverse to top-down.
	for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
		names[i], names[j] = names[j], names[i]
	}
	pkgPath := strings.Join(names, ".")
	packageID = pkgPath
	if isInit {
		moduleID = pkgPath // __init__.py names the package itself
		return
	}
	stem := strings.TrimSuffix(filename, ".py")
	if pkgPath == "" {
		moduleID = stem // top-level module, no package
		return
	}
	moduleID = pkgPath + "." + stem
	return
}

func isPackageDir(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "__init__.py"))
	return err == nil && !info.IsDir()
}
```

Add the `os` import (used by `isPackageDir`). Run `GOTOOLCHAIN=local go vet ./internal/extract/python/` and add any missing import it flags.

- [ ] **Step 5: Run test to verify it passes**

Run: `GOTOOLCHAIN=local go test ./internal/extract/python/ -run TestDiscover`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
git add internal/extract/python/
git commit -m "feat: Python file discovery + dotted-path derivation"
```

---

### Task 3: Python symbol extraction (functions, classes, members, docstrings, decorators, visibility, __all__)

**Files:**
- Create: `internal/extract/python/symbols.go`
- Create: `internal/extract/python/testdata/sample.py`
- Test: `internal/extract/python/symbols_test.go`

**Interfaces:**
- Consumes: `ts.Parse` + `ts.Node` (Task 1); `schema.Symbol` (SP1).
- Produces:
  ```go
  // moduleSymbols parses one module's source and returns its top-level symbols
  // (with nested methods/attrs owned), the module docstring (used by Task 4 for
  // the module unit's Doc), the module's __all__ set (nil if none), and whether
  // the module contains an `if __name__ == "__main__":` guard.
  func moduleSymbols(relFile string, src []byte) (syms []schema.Symbol, moduleDoc string, allSet map[string]bool, hasMain bool, err error)
  ```
  tree-sitter-python node types used (stable): `module`, `function_definition` (fields `name`,`parameters`,`return_type`,`body`), `class_definition` (fields `name`,`body`), `decorated_definition` (fields `decorator`* + `definition`), `decorator`, `expression_statement`, `string`, `assignment` (fields `left`,`right`), `identifier`, `if_statement` (field `condition`), `parameters`. Docstring = first `expression_statement`>`string` in a body. Visibility: leading `_` non-dunder → unexported. `async` prefix → modifier. `@property`-decorated def → kind `property`. Type/return annotations captured as-written via node text; absent → empty.

- [ ] **Step 1: Create the fixture**

`internal/extract/python/testdata/sample.py`:
```python
"""Sample module for extractor tests."""

__all__ = ["Widget", "make"]


def make(name: str, size: int = 1) -> "Widget":
    """Build a Widget."""
    return Widget(name)


def _helper(x):
    return x


async def fetch(url):
    """Fetch a url."""
    return url


class Widget:
    """A widget."""

    count = 0

    def __init__(self, name: str):
        """Init."""
        self.name = name

    @property
    def label(self) -> str:
        return self.name

    def _private(self):
        return None


if __name__ == "__main__":
    make("x")
```

- [ ] **Step 2: Write the failing test**

Create `internal/extract/python/symbols_test.go`:

```go
package python

import "os"
import "testing"

func load(t *testing.T) ([]symView, map[string]bool, bool) {
	t.Helper()
	src, err := os.ReadFile("testdata/sample.py")
	if err != nil { t.Fatal(err) }
	syms, _, all, hasMain, err := moduleSymbols("sample.py", src)
	if err != nil { t.Fatal(err) }
	var vs []symView
	for _, s := range syms {
		v := symView{s.ID, s.Kind, s.Visibility, s.Owner, s.Doc.Raw}
		if s.InAll != nil { v.inAll = *s.InAll; v.hasInAll = true }
		v.decorators = append(v.decorators, s.Decorators...)
		if s.Signature != nil { v.mods = append(v.mods, s.Signature.Modifiers...) }
		vs = append(vs, v)
	}
	return vs, all, hasMain
}

type symView struct {
	id, kind, vis, owner, doc string
	inAll, hasInAll           bool
	decorators, mods          []string
}

func get(vs []symView, id string) *symView {
	for i := range vs { if vs[i].id == id { return &vs[i] } }
	return nil
}

func TestModuleSymbols(t *testing.T) {
	vs, all, hasMain := load(t)
	if !hasMain { t.Fatal("expected __main__ guard detected") }
	if !all["Widget"] || !all["make"] || len(all) != 2 {
		t.Fatalf("__all__ = %v, want {Widget, make}", all)
	}
	if m := get(vs, "make"); m == nil || m.kind != "function" || m.vis != "exported" || !m.hasInAll || !m.inAll || m.doc == "" {
		t.Fatalf("make = %+v", m)
	}
	if h := get(vs, "_helper"); h == nil || h.vis != "unexported" || !h.hasInAll || h.inAll {
		t.Fatalf("_helper = %+v (want unexported, in_all=false)", h)
	}
	if f := get(vs, "fetch"); f == nil || len(f.mods) == 0 || f.mods[0] != "async" {
		t.Fatalf("fetch = %+v (want async modifier)", f)
	}
	if w := get(vs, "Widget"); w == nil || w.kind != "class" || w.doc == "" {
		t.Fatalf("Widget = %+v", w)
	}
	if lbl := get(vs, "Widget.label"); lbl == nil || lbl.kind != "property" || lbl.owner != "Widget" {
		t.Fatalf("Widget.label = %+v (want property owner Widget)", lbl)
	}
	if ini := get(vs, "Widget.__init__"); ini == nil || ini.vis != "exported" {
		t.Fatalf("Widget.__init__ = %+v (dunder must be exported)", ini)
	}
	if pv := get(vs, "Widget._private"); pv == nil || pv.vis != "unexported" {
		t.Fatalf("Widget._private = %+v (want unexported)", pv)
	}
	if c := get(vs, "Widget.count"); c == nil || c.kind != "variable" || c.owner != "Widget" {
		t.Fatalf("Widget.count = %+v (want class attribute variable)", c)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `GOTOOLCHAIN=local go test ./internal/extract/python/ -run TestModuleSymbols`
Expected: FAIL — `undefined: moduleSymbols`.

- [ ] **Step 4: Implement extraction**

Create `internal/extract/python/symbols.go` with a tree walk over the module's named children. Implement:
- `moduleSymbols`: parse via `ts.Parse(ts.Python, src)`; iterate root named children; dispatch `function_definition`, `class_definition`, `decorated_definition` (unwrap to its inner def, collecting decorator names), `expression_statement` (module docstring = first one holding a `string`; `__all__` assignment), `assignment` (module-level `variable`). Detect `if_statement` whose condition text is `__name__ == "__main__"` → `hasMain = true`.
- `funcSymbol(node, owner, src, relFile, decorators, isAsync)`: kind `method` if `owner != ""` else `function`; kind `property` if `"property"` is among decorators; id = `owner + "." + name` when owned else `name`; visibility via `pyVisibility(name)`; doc = first docstring in body; `Signature` with params (from `parameters` node, each param's text split into name/annotation/default as-written), return annotation from `return_type` field, `Modifiers` includes `"async"` when async; `Decorators` set; `Complexity` deferred; `Location` from node start/end.
- `classSymbol(node, src, relFile, decorators)`: kind `class`; then walk its `body` for nested `function_definition`/`decorated_definition` (→ methods/properties owned by the class) and `assignment` (→ class attribute `variable` owned by the class).
- `pyVisibility(name)`: `unexported` if it starts with `_` and is NOT a dunder (`__x__`); else `exported`. idiom `"underscore"`.
- `parseAll(assignmentNode, src)`: if the assignment's `left` is identifier `__all__` and `right` is a list/tuple of strings, return the set of string values.
- Set `Symbol.InAll` for module-level symbols only, after `__all__` is known: `true` if in the set, `false` if the set is non-nil and the name is absent, left nil if no `__all__`. (Do this in Task 4 where the module is assembled, OR return raw symbols here and let Task 4 stamp `InAll`; per the interface above, `moduleSymbols` returns `allSet` and Task 4 stamps `InAll`. For THIS task's test, stamp `InAll` on top-level symbols inside `moduleSymbols` using the discovered `allSet` so the test passes.)

Write concrete Go implementing the above against the `ts.Node` API from Task 1 and `schema.Symbol` (add fields `InAll *bool`, `Decorators []string` are introduced in Task 5 — for Task 3 to compile, they must exist; so this task depends on Task 5's schema fields. To avoid a cycle, ADD the two `Symbol` fields `InAll *bool json:"in_all,omitempty"` and `Decorators []string json:"decorators,omitempty"` as the FIRST step of THIS task if they are not present yet).

Correction to ordering: add the two Symbol fields here if absent:
```go
// in internal/schema/schema.go, Symbol struct:
InAll      *bool    `json:"in_all,omitempty"`
Decorators []string `json:"decorators,omitempty"`
```

- [ ] **Step 5: Run test to verify it passes**

Run: `GOTOOLCHAIN=local go test ./internal/extract/python/ -run TestModuleSymbols && GOTOOLCHAIN=local go vet ./...`
Expected: PASS; vet clean. (The `schema` package still builds; new fields are additive.)

- [ ] **Step 6: Commit**

```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
git add internal/extract/python/ internal/schema/schema.go
git commit -m "feat: Python symbol extraction (defs, classes, members, __all__, decorators)"
```

---

### Task 4: Python units (module + package) + entrypoint + Extractor

**Files:**
- Create: `internal/extract/python/python.go`
- Create: `internal/extract/python/testdata/sample.golden.json`
- Test: `internal/extract/python/python_test.go`

**Interfaces:**
- Consumes: `discover` (Task 2), `moduleSymbols` (Task 3), `schema` (with new `level`/`members`/unit-entrypoint fields added in Task 5 — add them here if absent, see Step 4), `emit.Manifest`/`emit.Combined` (SP1) for the golden.
- Produces:
  ```go
  package python
  type Extractor struct{}
  func New() *Extractor
  func (*Extractor) Language() string           // "python"
  func (*Extractor) Extract(root string) ([]schema.Package, error)
  ```
  Builds one `schema.Package` per module (`level:"module"`) and one per package dir (`level:"package"`, `members` = ids of direct child modules + sub-packages, symbols = the `__init__.py`'s own top-level symbols). A module with a `__main__` guard sets unit-level `IsEntrypoint`+`Invocation{Kind:"module", How:"python -m <moduleID>"}`; a top-level `def main` also gets symbol-level entrypoint.

- [ ] **Step 1: Add the failing test**

Create `internal/extract/python/python_test.go`:

```go
package python

import "testing"

func pkgByID(ps []schema.Package, id string) *schema.Package {
	for i := range ps { if ps[i].ID == id { return &ps[i] } }
	return nil
}

func TestExtractUnits(t *testing.T) {
	ps, err := New().Extract("testdata/proj")
	if err != nil { t.Fatal(err) }
	// pkg (package unit) exists with members including pkg.mod and pkg.sub
	pkg := pkgByID(ps, "pkg")
	if pkg == nil || pkg.Level != "package" {
		t.Fatalf("pkg unit = %+v", pkg)
	}
	hasMod, hasSub := false, false
	for _, m := range pkg.Members {
		if m == "pkg.mod" { hasMod = true }
		if m == "pkg.sub" { hasSub = true }
	}
	if !hasMod || !hasSub {
		t.Fatalf("pkg.Members = %v, want pkg.mod + pkg.sub", pkg.Members)
	}
	// module unit for pkg.mod
	mod := pkgByID(ps, "pkg.mod")
	if mod == nil || mod.Level != "module" {
		t.Fatalf("pkg.mod unit = %+v", mod)
	}
}
```

Add `import "goforge.dev/assayxport/internal/schema"` to the test file.

- [ ] **Step 2: Run to verify it fails**

Run: `GOTOOLCHAIN=local go test ./internal/extract/python/ -run TestExtractUnits`
Expected: FAIL — `undefined: New` / missing `Level`/`Members`.

- [ ] **Step 3: Add schema unit-level fields (if not already present)**

In `internal/schema/schema.go`, ensure `Package`, `PackageEntry`, and `PackageInfo` carry (add if absent):
```go
Level        string      `json:"level,omitempty"`
Members      []string    `json:"members,omitempty"`
IsEntrypoint bool        `json:"is_entrypoint,omitempty"`
Invocation   *Invocation `json:"invocation,omitempty"`
```
(`schema.Package` is the in-memory type the extractor returns; `PackageEntry`/`PackageInfo` are the emitted forms — Task 5 wires emit to copy these through. For this task, `schema.Package` needs the fields so `Extract` can set them.)

- [ ] **Step 4: Implement the extractor**

Create `internal/extract/python/python.go`:
- `Extract(root)`: `discover(root)`; for each `pyFile`, read + `moduleSymbols`; build a module `schema.Package{ID:f.ModuleID, Language:"python", Path:f.Rel, Name:<stem or pkg name>, Level:"module", Doc:<module docstring>, Symbols:<syms>}`. If `hasMain`, set `IsEntrypoint=true`, `Invocation=&schema.Invocation{Kind:"module", How:"python -m "+f.ModuleID}`; if a top-level symbol named `main` of kind `function` exists, set its `IsEntrypoint` + same invocation.
- Build package units: for each `pyFile` with `IsInit`, create a `schema.Package{ID:f.PackageID, Language:"python", Path:<dir>, Name:<last dotted segment>, Level:"package", Doc:<__init__ docstring>, Symbols:<__init__ top-level syms>, Members:<sorted ids of direct child modules + sub-packages>}`. Compute members by scanning discovered files whose `PackageID` equals this package id (direct children only: their module/package id has exactly one more dotted segment).
- Return all units; sorting happens in emit, but sort here too for stable intermediate output.
- Module docstring: derive from the module's leading docstring; expose it from `moduleSymbols` (extend its return or re-read the first symbol) — simplest: add a returned `moduleDoc string` to `moduleSymbols` in Task 3 OR recompute here by parsing. To avoid changing Task 3's signature retroactively, recompute the module docstring here via a small `firstDocstring(src)` helper using `ts` (or extend `moduleSymbols` return in Task 3). Choose extending `moduleSymbols` to also return `moduleDoc string`; update its Task 3 callers/tests accordingly if you implement tasks in order.

- [ ] **Step 5: Generate + verify the Python golden**

```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
# Add a golden test mirroring SP1's pattern, then:
GOTOOLCHAIN=local go test ./internal/extract/python/ -run TestGolden -update
GOTOOLCHAIN=local go test ./internal/extract/python/ -run TestGolden
```
Add a `TestGolden` in `python_test.go` that runs `Extract("testdata/proj")` (or a richer fixture that includes `sample.py` placed inside the package), feeds it through `emit.Manifest`+`emit.Combined`, and compares to `testdata/sample.golden.json` with a `-update` flag (same shape as SP1's golden test). Spot-check the golden shows `level` values, a `members` list, and a `python -m` invocation.

- [ ] **Step 6: Full package test + vet + commit**

Run: `GOTOOLCHAIN=local go test ./internal/extract/python/ && GOTOOLCHAIN=local go vet ./...`
Expected: PASS; vet clean.

```bash
git add internal/extract/python/ internal/schema/schema.go
git commit -m "feat: Python module+package units, entrypoints, extractor + golden"
```

---

### Task 5: Emit the new fields + update Go extractor + regenerate Go golden

**Files:**
- Modify: `internal/emit/emit.go`
- Modify: `internal/extract/golang/golang.go` and/or `internal/extract/golang/symbols.go`
- Modify: `internal/extract/golang/testdata/sample.golden.json` (regenerated)
- Test: `internal/emit/emit_test.go` (extend)

**Interfaces:**
- Consumes: schema fields from Tasks 3–4 (`Level`, `Members`, `IsEntrypoint`, `Invocation` on `schema.Package`; `InAll`, `Decorators` on `Symbol`).
- Produces: `emit.Manifest` copies `Level`/`Members`/`IsEntrypoint`/`Invocation` from each `schema.Package` into its `PackageEntry` and `PackageInfo`; the Go extractor sets `Level:"package"` on every package and unit-level `IsEntrypoint`+`Invocation{Kind:"binary", How:"go run ./<dir>"}` on `package main`.

- [ ] **Step 1: Extend the emit test**

In `internal/emit/emit_test.go`, extend `samplePkgs()` so one package has `Level:"package"`, `Members:[]string{"x.y"}`, `IsEntrypoint:true`, `Invocation:&schema.Invocation{Kind:"binary",How:"go run ./a"}`, and assert `Manifest` copies all four into the corresponding `PackageEntry` and that the shard's `PackageInfo` carries `Level`/`Members`. Run it and watch it fail.

- [ ] **Step 2: Wire emit to copy the fields**

In `internal/emit/emit.go` `Manifest`, when building each `PackageEntry` and `Shard.Package` (`PackageInfo`), copy `Level`, `Members`, `IsEntrypoint`, `Invocation` from the source `schema.Package`. Keep all existing sorting/determinism.

- [ ] **Step 3: Update the Go extractor**

In the Go extractor, set `Level: "package"` on every emitted `schema.Package`. For a `package main`, set the package's `IsEntrypoint = true` and `Invocation = &schema.Invocation{Kind:"binary", How: entrypointHow(p, moduleDir)}` (reuse the existing `entrypointHow`). Leave the symbol-level `func main` entrypoint as-is.

- [ ] **Step 4: Regenerate the Go golden**

```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
GOTOOLCHAIN=local go test ./internal/extract/golang/ -run TestSampleGolden -update
GOTOOLCHAIN=local go test ./internal/extract/golang/ -run TestSampleGolden
```
Inspect the diff of `sample.golden.json`: every package now has `"level": "package"`; `cmd/tool` additionally has package-level `"is_entrypoint": true` + `"invocation"`. NOTHING ELSE should change (symbol content identical). If anything else changed, investigate.

- [ ] **Step 5: Full suite + vet + commit**

Run: `GOTOOLCHAIN=local go test ./... && GOTOOLCHAIN=local go vet ./...`
Expected: all PASS; vet clean.

```bash
git add internal/emit/ internal/extract/golang/
git commit -m "feat: emit level/members/unit-entrypoint; Go extractor sets them; regen golden"
```

---

### Task 6: Extractor registry + polyglot dispatch + CLI `--lang`

**Files:**
- Create: `internal/extract/registry/registry.go`
- Modify: `cmd/assayxport/main.go`
- Create: `cmd/assayxport/testdata/mixed/` (a Go package + a Python module)
- Test: `internal/extract/registry/registry_test.go`

**Interfaces:**
- Consumes: `golang.New()`, `python.New()`, the `extract.Extractor` interface.
- Produces:
  ```go
  package registry
  // All returns every registered extractor (go, python), in a stable order.
  func All() []extract.Extractor
  // Select returns the extractors whose Language() is in langs; error if any
  // requested language is not registered (message lists available languages).
  // Empty langs => All().
  func Select(langs []string) ([]extract.Extractor, error)
  // Run executes each extractor over root and returns the merged packages
  // plus the sorted set of languages that produced at least one package.
  func Run(exts []extract.Extractor, root string) (pkgs []schema.Package, languages []string, err error)
  ```

- [ ] **Step 1: Write the failing test**

Create `internal/extract/registry/registry_test.go`:

```go
package registry

import "testing"

func TestSelectUnknownLang(t *testing.T) {
	_, err := Select([]string{"java"})
	if err == nil {
		t.Fatal("expected error for unregistered language java")
	}
}

func TestSelectSubset(t *testing.T) {
	exts, err := Select([]string{"python"})
	if err != nil { t.Fatal(err) }
	if len(exts) != 1 || exts[0].Language() != "python" {
		t.Fatalf("Select(python) = %v", exts)
	}
}

func TestAllRegistered(t *testing.T) {
	langs := map[string]bool{}
	for _, e := range All() { langs[e.Language()] = true }
	if !langs["go"] || !langs["python"] {
		t.Fatalf("registry missing go/python: %v", langs)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `GOTOOLCHAIN=local go test ./internal/extract/registry/`
Expected: FAIL — `undefined: Select`.

- [ ] **Step 3: Implement the registry**

Create `internal/extract/registry/registry.go`:

```go
// Package registry holds the language extractors and dispatches them.
package registry

import (
	"fmt"
	"sort"

	"goforge.dev/assayxport/internal/extract"
	"goforge.dev/assayxport/internal/extract/golang"
	"goforge.dev/assayxport/internal/extract/python"
	"goforge.dev/assayxport/internal/schema"
)

func All() []extract.Extractor {
	// Stable order by Language().
	return []extract.Extractor{golang.New(), python.New()}
}

func Select(langs []string) ([]extract.Extractor, error) {
	if len(langs) == 0 {
		return All(), nil
	}
	byLang := map[string]extract.Extractor{}
	var available []string
	for _, e := range All() {
		byLang[e.Language()] = e
		available = append(available, e.Language())
	}
	sort.Strings(available)
	var out []extract.Extractor
	for _, l := range langs {
		e, ok := byLang[l]
		if !ok {
			return nil, fmt.Errorf("unknown language %q; available: %v", l, available)
		}
		out = append(out, e)
	}
	return out, nil
}

func Run(exts []extract.Extractor, root string) ([]schema.Package, []string, error) {
	var pkgs []schema.Package
	langSet := map[string]bool{}
	for _, e := range exts {
		got, err := e.Extract(root)
		if err != nil {
			return nil, nil, err
		}
		if len(got) > 0 {
			langSet[e.Language()] = true
		}
		pkgs = append(pkgs, got...)
	}
	var languages []string
	for l := range langSet {
		languages = append(languages, l)
	}
	sort.Strings(languages)
	return pkgs, languages, nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `GOTOOLCHAIN=local go test ./internal/extract/registry/`
Expected: PASS.

- [ ] **Step 5: Wire the CLI to the registry with `--lang`**

In `cmd/assayxport/main.go`: replace the direct `golang.New()` call with the registry. Add a repeatable `--lang` flag (a custom `flag.Value` collecting a string slice). Build:
```go
	exts, err := registry.Select(langs) // langs = []string from --lang; empty => all
	if err != nil {
		return err
	}
	pkgs, languages, err := registry.Run(exts, path)
	if err != nil {
		return err
	}
	idx, shards := emit.Manifest(pkgs, moduleHint(pkgs), languages)
```
Pass `languages` (from `Run`) into `emit.Manifest` as the `languages` arg instead of the hardcoded `[]string{"go"}`. For the `module` arg, keep the Go module path when present: since `Extract` no longer runs only Go, derive the module string from the Go extractor if used, else "". Implement `moduleHint` to return the Go extractor's `Module()` when Go is among the selected extractors (call `golang.New().Module()` is stale — instead have the CLI keep a reference to the Go extractor from `Select`/`All` and read `Module()` after `Run`). Simplest: after `Run`, find the `*golang.Extractor` in `exts` and call its `Module()`.

- [ ] **Step 6: Mixed-fixture end-to-end + polyglot flag behavior**

Create `cmd/assayxport/testdata/mixed/` with a minimal Go module (a `go.mod`, one package with an exported func) and a Python module (`thing.py` with a class). Then verify by running the binary:
```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
GOTOOLCHAIN=local go run ./cmd/assayxport scan cmd/assayxport/testdata/mixed --stdout | grep -q '"languages"' && echo LANGS_OK
GOTOOLCHAIN=local go run ./cmd/assayxport scan cmd/assayxport/testdata/mixed --lang python --stdout | grep -q '"language": "python"' && echo PY_ONLY_OK
GOTOOLCHAIN=local go run ./cmd/assayxport scan cmd/assayxport/testdata/mixed --lang java --stdout; echo "exit=$?"   # expect non-zero + error listing available
```
Expected: `LANGS_OK`; `PY_ONLY_OK`; the `--lang java` run exits non-zero with an "unknown language" error. (If the Go fixture module inside testdata interferes with the outer module build, keep it minimal and self-contained; loader errors there should surface as a clean non-zero exit, which is acceptable to observe — adjust the fixture so `--lang python` still succeeds independently.)

- [ ] **Step 7: Full suite + vet + commit**

Run: `GOTOOLCHAIN=local go test ./... && GOTOOLCHAIN=local go vet ./...`
Expected: PASS; vet clean.

```bash
git add internal/extract/registry/ cmd/assayxport/
git commit -m "feat: extractor registry + polyglot dispatch + --lang override"
```

---

### Task 7: Third-party attribution + README

**Files:**
- Modify: `NOTICE`
- Modify: `README.md`

**Interfaces:**
- Consumes: nothing code-level.
- Produces: MIT attribution for the vendored tree-sitter runtime + grammar; README documents Python + `--lang`.

- [ ] **Step 1: Attribute the third-party components**

Append to `NOTICE` the MIT attribution for `github.com/malivvan/tree-sitter` (and the underlying `tree-sitter`/`tree-sitter-python` if a grammar `.wasm` was vendored — copyright holders + MIT reference + the grammar's source/version from `internal/ts/provenance.md`). Do NOT use em/en-dashes.

- [ ] **Step 2: Update the README**

In `README.md`, add Python to the supported-languages line, document `--lang` (repeatable; default all), and note the deterministic polyglot manifest covers every supported language found. No em/en-dashes; run `grep -nP '[\x{2013}\x{2014}]' README.md NOTICE` and expect no matches.

- [ ] **Step 3: Final gate + commit**

Run: `GOTOOLCHAIN=local go test ./... && GOTOOLCHAIN=local go vet ./...`
Expected: all PASS; vet clean.

```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
git add NOTICE README.md
git commit -m "docs: attribute tree-sitter deps; document Python + --lang"
```

---

## Self-Review

**Spec coverage:**
- Pure-Go tree-sitter layer (wazero + embedded WASM grammar), isolated behind in-house API → Task 1. ✓
- Python extractor emitting the schema → Tasks 2–4. ✓
- Both module + package units, members rollup → Task 4. ✓
- Visibility (underscore) + separate `in_all`; decorators; async; docstrings; kinds (function/method/class/property/variable) → Task 3. ✓
- Entrypoint = `__main__` guard → unit `is_entrypoint` + `python -m`, plus `def main` symbol → Task 4. ✓
- Schema additions (level/members/unit-entrypoint/in_all/decorators), additive, version "1" → Tasks 3,4 (schema fields), Task 5 (emit wiring). ✓
- Go extractor gains level + unit-entrypoint; SP1 Go golden regenerated → Task 5. ✓
- Registry + polyglot dispatch; CLI repeatable `--lang`; unknown lang errors → Task 6. ✓
- Determinism (sorted, POSIX, no host data, double-run) → Tasks 2 (sorted discover), 4 (Python golden), 5 (Go golden), plus emit unchanged. ✓
- Licensing/NOTICE for third-party MIT deps → Task 7. ✓
- Out of scope (Java/big-O/import resolution/docstring styles) → not present. ✓

**Placeholder scan:** No TBD/TODO. Task 1 is a deliberate integration spike, but it defines the exact in-house API it must expose and a concrete acceptance test; its only "consult the live godoc" instruction is confined to gluing the third-party calls, which cannot be pinned verbatim pre-probe — every downstream task codes against the fixed `ts` API, not the library. All other steps carry full code + commands + expected output.

**Type/name consistency:** `ts.Parse`/`ts.Node` methods (`Type`,`NamedChild`,`ChildByFieldName`,`StartLine`,`Content`) are defined in Task 1 and used unchanged in Task 3. `pyFile` fields (Task 2) consumed in Task 4. `moduleSymbols` signature (Task 3) — NOTE it returns `(syms, allSet, hasMain, err)` in the interface but Task 4 also needs the module docstring; Task 3 Step 4 and Task 4 Step 4 both call for extending it to also return `moduleDoc string`. **Resolved:** `moduleSymbols` final signature is `(syms []schema.Symbol, moduleDoc string, allSet map[string]bool, hasMain bool, err error)` — implement it this way in Task 3 (update the Task 3 test's call site to match: it ignores `moduleDoc` with `_`). Registry `Select`/`Run`/`All` (Task 6) match their CLI callers. Schema fields `Level`/`Members`/`IsEntrypoint`/`Invocation`/`InAll`/`Decorators` are added once (Tasks 3–4) and copied by emit (Task 5) consistently.

**Cross-task note:** Tasks 3 and 4 both touch `internal/schema/schema.go` to add fields; whichever runs first adds them, the later one finds them present (its "add if absent" guard handles ordering). The Task 3 `moduleSymbols` signature is `(syms, moduleDoc, allSet, hasMain, err)` — the Task 3 test must call it as `syms, _, all, hasMain, err := moduleSymbols(...)`.
