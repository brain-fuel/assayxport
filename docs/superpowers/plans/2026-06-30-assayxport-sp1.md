# assayxport SP1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `assayxport` SP1 — a native-Go CLI that scans a Go codebase and writes a deterministic JSON manifest (root index + per-package shards) of its API, docs, structure, and entrypoints.

**Architecture:** A language-agnostic `Extractor` interface feeds a versioned `schema`; the Go extractor loads packages with `golang.org/x/tools/go/packages` and resolves every symbol's types via `go/types` (accurate, machine-stable type strings) while pulling doc text and positions from the AST; a deterministic emitter writes `assayxport.json` plus `.assayxport/<pkg-dir>.json` shards.

**Tech Stack:** Go 1.24, `golang.org/x/tools/go/packages`, stdlib `go/types` / `go/ast` / `go/token`, `encoding/json`.

## Global Constraints

- Go module path: `goforge.dev/assayxport`; `go 1.24` directive.
- License: **MIT**.
- `schema_version` value is the string `"1"` everywhere it appears.
- Determinism is mandatory: relative POSIX paths only (`filepath.ToSlash`); NO timestamps, absolute paths, usernames, or host/env data in output; `packages` sorted by `id`; `symbols` sorted by `(file, line, name)`; all arrays stable; type strings rendered with a fixed qualifier returning the package import path; JSON is 2-space indented, `\n` line endings, with a trailing newline; same inputs produce byte-identical output.
- Both exported AND unexported symbols are captured; `visibility` is a label (`exported`/`unexported` via `token.IsExported`), never a filter.
- `complexity` is always `{"time":null,"space":null,"method":"deferred"}` in SP1.
- SP1 is Go-only. No Python/Java/tree-sitter/wazero, no big-O values, no daemon mode, no partial manifests for a non-compiling build (load errors → non-zero exit).

---

### Task 1: Module scaffold + schema types

**Files:**
- Create: `go.mod`
- Create: `LICENSE`
- Create: `internal/schema/schema.go`
- Test: `internal/schema/schema_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: the JSON-tagged structs every later task fills and emits:
  `schema.Index`, `schema.PackageEntry`, `schema.Shard`, `schema.PackageInfo`,
  `schema.Symbol`, `schema.Signature`, `schema.Param`, `schema.TypeParam`,
  `schema.Location`, `schema.Doc`, `schema.Complexity`, `schema.Invocation`;
  and `schema.Version = "1"`, `schema.DeferredComplexity()`.

- [ ] **Step 1: Create the module and MIT license**

```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
go mod init goforge.dev/assayxport
```

Create `LICENSE` with the standard MIT text:

```text
MIT License

Copyright (c) 2026 brain-fuel

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

- [ ] **Step 2: Write the failing test**

Create `internal/schema/schema_test.go`:

```go
package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionConstant(t *testing.T) {
	if Version != "1" {
		t.Fatalf("Version = %q, want \"1\"", Version)
	}
}

func TestDeferredComplexity(t *testing.T) {
	c := DeferredComplexity()
	if c.Time != nil || c.Space != nil || c.Method != "deferred" {
		t.Fatalf("DeferredComplexity() = %+v, want nulls + deferred", c)
	}
}

func TestSymbolOmitsSignatureForNonCallable(t *testing.T) {
	s := Symbol{
		ID: "MaxInt", Name: "MaxInt", Kind: "const", Visibility: "exported",
		VisibilityIdiom: "capitalized",
		Location:        Location{File: "x.go", Line: 1, Col: 1, EndLine: 1},
		Doc:             Doc{Raw: "", Format: "godoc"},
		Complexity:      DeferredComplexity(),
		Type:            "int",
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "\"signature\"") {
		t.Fatalf("const symbol must omit signature, got %s", b)
	}
	if !strings.Contains(string(b), "\"type\":\"int\"") {
		t.Fatalf("const symbol must carry type, got %s", b)
	}
}

func TestSymbolIncludesSignatureForFunc(t *testing.T) {
	s := Symbol{
		ID: "Add", Name: "Add", Kind: "func", Visibility: "exported",
		VisibilityIdiom: "capitalized",
		Location:        Location{File: "x.go", Line: 2, Col: 1, EndLine: 4},
		Doc:             Doc{Raw: "Add adds.", Format: "godoc"},
		Complexity:      DeferredComplexity(),
		Signature: &Signature{
			Params:  []Param{{Name: "a", Type: "int"}, {Name: "b", Type: "int"}},
			Returns: []Param{{Name: "", Type: "int"}},
		},
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "\"signature\"") {
		t.Fatalf("func symbol must include signature, got %s", b)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/schema/`
Expected: FAIL — `undefined: Version` (package not yet written).

- [ ] **Step 4: Write the schema**

Create `internal/schema/schema.go`:

```go
// Package schema defines the assayxport manifest types and their stable JSON
// encoding. Version is the schema_version string emitted in every artifact.
package schema

// Version is the schema_version value written into every index and shard.
const Version = "1"

// Index is the root manifest written to assayxport.json.
type Index struct {
	SchemaVersion string         `json:"schema_version"`
	Tool          string         `json:"tool"`
	Languages     []string       `json:"languages"`
	Root          string         `json:"root"`
	Module        string         `json:"module,omitempty"`
	Packages      []PackageEntry `json:"packages"`
}

// PackageEntry is one package's summary in the index, pointing at its shard.
type PackageEntry struct {
	ID              string `json:"id"`
	Language        string `json:"language"`
	Path            string `json:"path"`
	Name            string `json:"name"`
	Doc             string `json:"doc"`
	SymbolCount     int    `json:"symbol_count"`
	EntrypointCount int    `json:"entrypoint_count"`
	Shard           string `json:"shard"`
}

// Shard is one package's full symbol listing, written to .assayxport/<dir>.json.
type Shard struct {
	SchemaVersion string      `json:"schema_version"`
	Package       PackageInfo `json:"package"`
	Symbols       []Symbol    `json:"symbols"`
}

// PackageInfo identifies a package inside its shard.
type PackageInfo struct {
	ID       string `json:"id"`
	Language string `json:"language"`
	Path     string `json:"path"`
	Name     string `json:"name"`
	Doc      string `json:"doc"`
}

// Package is the in-memory result an Extractor returns for one package.
// It carries everything needed to build both the index entry and the shard.
type Package struct {
	ID       string
	Language string
	Path     string
	Name     string
	Doc      string
	Symbols  []Symbol
}

// Symbol is one named declaration. Signature is set only for func/method;
// Type is set for const/var/field; TypeKind+Underlying are set for type.
type Symbol struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	Kind            string      `json:"kind"`
	Visibility      string      `json:"visibility"`
	VisibilityIdiom string      `json:"visibility_idiom"`
	Location        Location    `json:"location"`
	Owner           string      `json:"owner,omitempty"`
	Doc             Doc         `json:"doc"`
	IsEntrypoint    bool        `json:"is_entrypoint"`
	Invocation      *Invocation `json:"invocation,omitempty"`
	Complexity      Complexity  `json:"complexity"`

	Signature  *Signature `json:"signature,omitempty"`
	TypeKind   string     `json:"type_kind,omitempty"`
	Underlying string     `json:"underlying,omitempty"`
	Type       string     `json:"type,omitempty"`
}

// Signature describes a func or method.
type Signature struct {
	Params     []Param     `json:"params"`
	Returns    []Param     `json:"returns"`
	TypeParams []TypeParam `json:"type_params"`
	Receiver   *Param      `json:"receiver,omitempty"`
	Variadic   bool        `json:"variadic"`
}

// Param is one parameter, result, or receiver. Name may be empty.
type Param struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// TypeParam is one generic type parameter.
type TypeParam struct {
	Name       string `json:"name"`
	Constraint string `json:"constraint"`
}

// Location is a 1-based source position with a relative POSIX file path.
type Location struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	EndLine int    `json:"end_line"`
}

// Doc is a documentation comment plus its source idiom.
type Doc struct {
	Raw    string `json:"raw"`
	Format string `json:"format"`
}

// Invocation describes how to run an entrypoint symbol.
type Invocation struct {
	Kind string `json:"kind"`
	How  string `json:"how"`
}

// Complexity is the reserved big-O slot. SP1 always emits the deferred value.
type Complexity struct {
	Time   *string `json:"time"`
	Space  *string `json:"space"`
	Method string  `json:"method"`
}

// DeferredComplexity returns the SP1 placeholder complexity value.
func DeferredComplexity() Complexity {
	return Complexity{Time: nil, Space: nil, Method: "deferred"}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/schema/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
git add go.mod LICENSE internal/schema/
git commit -m "feat: module scaffold + versioned manifest schema (MIT)"
```

---

### Task 2: Extractor interface + Go package loading

**Files:**
- Create: `internal/extract/extract.go`
- Create: `internal/extract/golang/golang.go`
- Create: `internal/extract/golang/testdata/sample/go.mod`
- Create: `internal/extract/golang/testdata/sample/calc/calc.go`
- Create: `internal/extract/golang/testdata/sample/cmd/tool/main.go`
- Test: `internal/extract/golang/golang_test.go`

**Interfaces:**
- Consumes: `schema.Package` from Task 1.
- Produces: `extract.Extractor` interface; `golang.New() *golang.Extractor` with
  `Language() string` (returns `"go"`) and `Extract(root string) ([]schema.Package, error)`.
  In this task `Extract` returns packages with id/path/name/doc populated and
  `Symbols == nil`; later tasks fill `Symbols`.

- [ ] **Step 1: Add the go/packages dependency**

```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
go get golang.org/x/tools/go/packages@latest
```

- [ ] **Step 2: Create the test fixture module**

Create `internal/extract/golang/testdata/sample/go.mod`:

```text
module example.com/sample

go 1.24
```

Create `internal/extract/golang/testdata/sample/calc/calc.go`:

```go
// Package calc does sample arithmetic for assayxport extractor tests.
package calc

// Add returns a + b.
func Add(a, b int) int { return a + b }

// sub returns a - b. Unexported on purpose.
func sub(a, b int) int { return a - b }
```

Create `internal/extract/golang/testdata/sample/cmd/tool/main.go`:

```go
// Command tool is a sample entrypoint for assayxport extractor tests.
package main

func main() {}
```

- [ ] **Step 3: Write the failing test**

Create `internal/extract/golang/golang_test.go`:

```go
package golang

import "testing"

func TestExtractFindsPackagesSorted(t *testing.T) {
	pkgs, err := New().Extract("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range pkgs {
		ids = append(ids, p.ID)
	}
	want := []string{"example.com/sample/calc", "example.com/sample/cmd/tool"}
	if len(ids) != len(want) {
		t.Fatalf("got package ids %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("package ids %v not sorted/expected %v", ids, want)
		}
	}
}

func TestExtractPackageMetadata(t *testing.T) {
	pkgs, err := New().Extract("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	var calc *struct{ Name, Path, Doc string }
	for _, p := range pkgs {
		if p.ID == "example.com/sample/calc" {
			calc = &struct{ Name, Path, Doc string }{p.Name, p.Path, p.Doc}
		}
	}
	if calc == nil {
		t.Fatal("calc package not found")
	}
	if calc.Name != "calc" || calc.Path != "calc" {
		t.Fatalf("calc meta = %+v, want name=calc path=calc", calc)
	}
	if calc.Doc == "" {
		t.Fatalf("calc package doc should be non-empty")
	}
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `go test ./internal/extract/golang/`
Expected: FAIL — `undefined: New`.

- [ ] **Step 5: Write the interface and loader**

Create `internal/extract/extract.go`:

```go
// Package extract defines the language-agnostic extractor contract.
package extract

import "goforge.dev/assayxport/internal/schema"

// Extractor turns a source tree into a stable list of packages.
type Extractor interface {
	// Language reports the language id this extractor handles, e.g. "go".
	Language() string
	// Extract loads every package under root and returns them sorted by ID.
	Extract(root string) ([]schema.Package, error)
}
```

Create `internal/extract/golang/golang.go`:

```go
// Package golang extracts a Go source tree into assayxport's schema using
// go/packages for loading and go/types for accurate, machine-stable types.
package golang

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"goforge.dev/assayxport/internal/schema"
	"golang.org/x/tools/go/packages"
)

// Extractor is the Go language extractor.
type Extractor struct{}

// New returns a Go extractor.
func New() *Extractor { return &Extractor{} }

// Language reports the language id.
func (*Extractor) Language() string { return "go" }

const loadMode = packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
	packages.NeedTypes | packages.NeedTypesInfo | packages.NeedModule |
	packages.NeedImports | packages.NeedDeps

// Extract loads ./... under root and returns packages sorted by import path.
func (e *Extractor) Extract(root string) ([]schema.Package, error) {
	cfg := &packages.Config{
		Mode:  loadMode,
		Dir:   root,
		Tests: false,
	}
	loaded, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("load packages: %w", err)
	}
	var loadErrs []string
	packages.Visit(loaded, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			loadErrs = append(loadErrs, e.Error())
		}
	})
	if len(loadErrs) > 0 {
		return nil, fmt.Errorf("package load errors:\n%s", strings.Join(loadErrs, "\n"))
	}

	moduleDir := root
	out := make([]schema.Package, 0, len(loaded))
	for _, p := range loaded {
		if p.Module != nil {
			moduleDir = p.Module.Dir
		}
		out = append(out, schema.Package{
			ID:       p.PkgPath,
			Language: "go",
			Name:     p.Name,
			Doc:      packageDoc(p),
			// Path filled below once moduleDir is known.
		})
	}
	// Compute each package's module-relative directory.
	for i, p := range loaded {
		dir := packageDir(p)
		rel, err := filepath.Rel(moduleDir, dir)
		if err != nil || strings.HasPrefix(rel, "..") {
			rel = filepath.Base(dir)
		}
		out[i].Path = filepath.ToSlash(rel)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// packageDir returns the directory holding a package's first Go file.
func packageDir(p *packages.Package) string {
	if len(p.GoFiles) > 0 {
		return filepath.Dir(p.GoFiles[0])
	}
	return ""
}

// packageDoc returns the package-level doc comment text, if any.
func packageDoc(p *packages.Package) string {
	for _, f := range p.Syntax {
		if f.Doc != nil {
			return strings.TrimSpace(f.Doc.Text())
		}
	}
	return ""
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/extract/golang/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
git add go.mod go.sum internal/extract/
git commit -m "feat: Extractor interface + Go package loader with metadata"
```

---

### Task 3: Extract funcs, methods, signatures, entrypoints

**Files:**
- Modify: `internal/extract/golang/golang.go`
- Create: `internal/extract/golang/symbols.go`
- Modify: `internal/extract/golang/testdata/sample/calc/calc.go` (add generic + variadic + method)
- Test: `internal/extract/golang/symbols_test.go`

**Interfaces:**
- Consumes: the loaded `*packages.Package` set from Task 2.
- Produces: `Symbols` populated for kinds `func` and `method`, each with
  `Signature` (params/returns/type_params/receiver/variadic), `Location`, `Doc`,
  `Visibility`, and `IsEntrypoint`+`Invocation` for `package main` `func main()`.
  Type strings come from `typeString(t, qual)` where `qual` returns the package
  import path. Symbols are appended in source order here; Task 5 sorts on emit.

- [ ] **Step 1: Extend the fixture**

Replace `internal/extract/golang/testdata/sample/calc/calc.go` with:

```go
// Package calc does sample arithmetic for assayxport extractor tests.
package calc

// Add returns a + b.
func Add(a, b int) int { return a + b }

// sub returns a - b. Unexported on purpose.
func sub(a, b int) int { return a - b }

// Sum returns the total of xs.
func Sum(xs ...int) int {
	t := 0
	for _, x := range xs {
		t += x
	}
	return t
}

// Max returns the larger of a and b for any ordered type.
func Max[T int | float64](a, b T) T {
	if a > b {
		return a
	}
	return b
}

// Accumulator sums pushed values.
type Accumulator struct{ total int }

// Push adds v to the accumulator and returns the new total.
func (a *Accumulator) Push(v int) int {
	a.total += v
	return a.total
}
```

- [ ] **Step 2: Write the failing test**

Create `internal/extract/golang/symbols_test.go`:

```go
package golang

import "testing"

func findSym(pkgs []symPkg, pkgID, id string) *sym {
	for _, p := range pkgs {
		if p.id == pkgID {
			for i := range p.syms {
				if p.syms[i].id == id {
					return &p.syms[i]
				}
			}
		}
	}
	return nil
}

// symPkg/sym are tiny local views to keep assertions readable.
type symPkg struct {
	id   string
	syms []sym
}
type sym struct {
	id, kind, vis, recv string
	variadic            bool
	params, returns     int
	typeParams          int
	entry               bool
	how                 string
}

func loadSyms(t *testing.T) []symPkg {
	t.Helper()
	pkgs, err := New().Extract("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	var out []symPkg
	for _, p := range pkgs {
		sp := symPkg{id: p.ID}
		for _, s := range p.Symbols {
			v := sym{id: s.ID, kind: s.Kind, vis: s.Visibility, entry: s.IsEntrypoint}
			if s.Signature != nil {
				v.params = len(s.Signature.Params)
				v.returns = len(s.Signature.Returns)
				v.typeParams = len(s.Signature.TypeParams)
				v.variadic = s.Signature.Variadic
				if s.Signature.Receiver != nil {
					v.recv = s.Signature.Receiver.Type
				}
			}
			if s.Invocation != nil {
				v.how = s.Invocation.How
			}
			sp.syms = append(sp.syms, v)
		}
		out = append(out, sp)
	}
	return out
}

func TestFuncAndUnexported(t *testing.T) {
	pkgs := loadSyms(t)
	add := findSym(pkgs, "example.com/sample/calc", "Add")
	if add == nil || add.kind != "func" || add.vis != "exported" || add.params != 2 || add.returns != 1 {
		t.Fatalf("Add = %+v", add)
	}
	sub := findSym(pkgs, "example.com/sample/calc", "sub")
	if sub == nil || sub.vis != "unexported" {
		t.Fatalf("sub = %+v (want unexported func captured)", sub)
	}
}

func TestVariadicAndGeneric(t *testing.T) {
	pkgs := loadSyms(t)
	sum := findSym(pkgs, "example.com/sample/calc", "Sum")
	if sum == nil || !sum.variadic {
		t.Fatalf("Sum = %+v (want variadic)", sum)
	}
	max := findSym(pkgs, "example.com/sample/calc", "Max")
	if max == nil || max.typeParams != 1 {
		t.Fatalf("Max = %+v (want 1 type param)", max)
	}
}

func TestMethodReceiver(t *testing.T) {
	pkgs := loadSyms(t)
	push := findSym(pkgs, "example.com/sample/calc", "Accumulator.Push")
	if push == nil || push.kind != "method" || push.recv != "*Accumulator" {
		t.Fatalf("Accumulator.Push = %+v (want method recv *Accumulator)", push)
	}
}

func TestEntrypoint(t *testing.T) {
	pkgs := loadSyms(t)
	main := findSym(pkgs, "example.com/sample/cmd/tool", "main")
	if main == nil || !main.entry || main.how != "go run ./cmd/tool" {
		t.Fatalf("main = %+v (want entrypoint how=go run ./cmd/tool)", main)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/extract/golang/ -run 'Func|Variadic|Method|Entry'`
Expected: FAIL — symbols slice empty, lookups return nil.

- [ ] **Step 4: Write the symbol extraction**

Create `internal/extract/golang/symbols.go`:

```go
package golang

import (
	"go/ast"
	"go/token"
	"go/types"
	"path"
	"strings"

	"goforge.dev/assayxport/internal/schema"
	"golang.org/x/tools/go/packages"
)

// typeQualifier renders package references as their import path for stable,
// machine-independent type strings.
func typeQualifier(p *types.Package) string { return p.Path() }

func typeString(t types.Type) string { return types.TypeString(t, typeQualifier) }

// extractSymbols walks one package and returns its callable symbols (func,
// method) in source order. Type/const/var are added in Task 4.
func extractSymbols(p *packages.Package, moduleDir string) []schema.Symbol {
	var syms []schema.Symbol
	for _, file := range p.Syntax {
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if s, ok := funcSymbol(p, fd, moduleDir); ok {
				syms = append(syms, s)
			}
		}
	}
	return syms
}

func funcSymbol(p *packages.Package, fd *ast.FuncDecl, moduleDir string) (schema.Symbol, bool) {
	obj := p.TypesInfo.Defs[fd.Name]
	fn, ok := obj.(*types.Func)
	if !ok || fn == nil {
		return schema.Symbol{}, false
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return schema.Symbol{}, false
	}

	name := fd.Name.Name
	kind := "func"
	id := name
	var recv *schema.Param
	if sig.Recv() != nil {
		kind = "method"
		recvType := typeString(sig.Recv().Type())
		recv = &schema.Param{Name: sig.Recv().Name(), Type: recvType}
		id = recvBase(recvType) + "." + name
	}

	s := schema.Symbol{
		ID:              id,
		Name:            name,
		Kind:            kind,
		Visibility:      visibility(name),
		VisibilityIdiom: "capitalized",
		Location:        locationOf(p.Fset, fd, moduleDir),
		Doc:             schema.Doc{Raw: strings.TrimSpace(fd.Doc.Text()), Format: "godoc"},
		Complexity:      schema.DeferredComplexity(),
		Signature: &schema.Signature{
			Params:     tupleParams(sig.Params()),
			Returns:    tupleParams(sig.Results()),
			TypeParams: typeParams(sig.TypeParams()),
			Receiver:   recv,
			Variadic:   sig.Variadic(),
		},
	}
	if recv != nil {
		s.Owner = recvBase(recv.Type)
	}

	if isEntrypoint(p, fd, sig) {
		s.IsEntrypoint = true
		s.Invocation = &schema.Invocation{Kind: "binary", How: entrypointHow(p, moduleDir)}
	}
	return s, true
}

// recvBase strips leading "*" so methods own the bare type name.
func recvBase(recvType string) string { return strings.TrimPrefix(recvType, "*") }

func tupleParams(t *types.Tuple) []schema.Param {
	out := make([]schema.Param, 0, t.Len())
	for i := 0; i < t.Len(); i++ {
		v := t.At(i)
		out = append(out, schema.Param{Name: v.Name(), Type: typeString(v.Type())})
	}
	return out
}

func typeParams(tp *types.TypeParamList) []schema.TypeParam {
	if tp == nil {
		return nil
	}
	out := make([]schema.TypeParam, 0, tp.Len())
	for i := 0; i < tp.Len(); i++ {
		p := tp.At(i)
		out = append(out, schema.TypeParam{Name: p.Obj().Name(), Constraint: typeString(p.Constraint())})
	}
	return out
}

func visibility(name string) string {
	if token.IsExported(name) {
		return "exported"
	}
	return "unexported"
}

func locationOf(fset *token.FileSet, node ast.Node, moduleDir string) schema.Location {
	start := fset.Position(node.Pos())
	end := fset.Position(node.End())
	return schema.Location{
		File:    relFile(start.Filename, moduleDir),
		Line:    start.Line,
		Col:     start.Column,
		EndLine: end.Line,
	}
}

// isEntrypoint reports a package-main func main() with no recv/params/results.
func isEntrypoint(p *packages.Package, fd *ast.FuncDecl, sig *types.Signature) bool {
	return p.Name == "main" && fd.Name.Name == "main" &&
		sig.Recv() == nil && sig.Params().Len() == 0 && sig.Results().Len() == 0
}
```

Append these helpers to `internal/extract/golang/golang.go` (they depend on `moduleDir`, computed during load):

```go
// relFile returns the module-relative POSIX path of an absolute file path.
func relFile(absFile, moduleDir string) string {
	rel, err := filepathRel(moduleDir, absFile)
	if err != nil {
		return filepath.ToSlash(absFile)
	}
	return filepath.ToSlash(rel)
}

// entrypointHow renders the `go run` invocation for a main package, using its
// module-relative directory (e.g. "go run ./cmd/tool").
func entrypointHow(p *packages.Package, moduleDir string) string {
	dir := packageDir(p)
	rel, err := filepathRel(moduleDir, dir)
	if err != nil {
		return "go run " + filepath.ToSlash(dir)
	}
	return "go run ./" + filepath.ToSlash(rel)
}
```

Add `filepathRel` as a thin wrapper so both files share one impl (place in `golang.go`):

```go
// filepathRel is filepath.Rel, isolated so symbol code can reuse it.
func filepathRel(base, target string) (string, error) { return filepath.Rel(base, target) }
```

Now wire symbol extraction into `Extract`. In `golang.go`, after `moduleDir` is determined and before sorting, fill symbols. Replace the per-package metadata loop body so each `out[i]` also gets symbols, and pass `moduleDir`:

```go
	// (inside Extract, replacing the second loop that set out[i].Path)
	for i, p := range loaded {
		dir := packageDir(p)
		rel, err := filepath.Rel(moduleDir, dir)
		if err != nil || strings.HasPrefix(rel, "..") {
			rel = filepath.Base(dir)
		}
		out[i].Path = filepath.ToSlash(rel)
		out[i].Symbols = extractSymbols(p, moduleDir)
	}
```

Add `"path"` usage note: `symbols.go` imports `path` only if needed; if `go vet` flags it unused, remove the import. (It is listed for forward use in Task 4’s field IDs; remove now if unused.)

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/extract/golang/ -run 'Func|Variadic|Method|Entry'`
Expected: PASS. Then `go vet ./...` — fix any unused import it reports (drop `"path"` from `symbols.go` if unused).

- [ ] **Step 6: Run the full package tests**

Run: `go test ./internal/extract/golang/`
Expected: PASS (Task 2 metadata tests still green).

- [ ] **Step 7: Commit**

```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
git add internal/extract/golang/
git commit -m "feat: extract Go funcs, methods, signatures, entrypoints"
```

---

### Task 4: Extract types, consts, vars, fields, interface methods

**Files:**
- Modify: `internal/extract/golang/symbols.go`
- Modify: `internal/extract/golang/testdata/sample/calc/calc.go` (add type/const/var/interface)
- Test: `internal/extract/golang/symbols_test.go` (add cases)

**Interfaces:**
- Consumes: the `extractSymbols` walk from Task 3.
- Produces: symbols for kinds `type` (with `TypeKind` ∈ struct|interface|alias|defined
  and `Underlying`), `const`, `var` (with `Type`), `field` (owned by its struct),
  and interface `method` (owned by its interface). Const/var/field carry `Type`,
  no `Signature`.

- [ ] **Step 1: Extend the fixture**

Append to `internal/extract/golang/testdata/sample/calc/calc.go`:

```go
// MaxInt is the largest representable sample value.
const MaxInt = 1<<31 - 1

// Default is the zero accumulator.
var Default = Accumulator{}

// Point is a 2D point.
type Point struct {
	X int // X coordinate.
	Y int // Y coordinate.
}

// Adder is anything that can add.
type Adder interface {
	// Add returns the sum.
	Add(a, b int) int
}

// Celsius is a defined numeric type.
type Celsius float64
```

- [ ] **Step 2: Write the failing test**

Append to `internal/extract/golang/symbols_test.go`:

```go
func findFull(t *testing.T, pkgID, id string) (kind, typeKind, underlying, typ, owner string, found bool) {
	t.Helper()
	pkgs, err := New().Extract("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkgs {
		if p.ID != pkgID {
			continue
		}
		for _, s := range p.Symbols {
			if s.ID == id {
				return s.Kind, s.TypeKind, s.Underlying, s.Type, s.Owner, true
			}
		}
	}
	return "", "", "", "", "", false
}

func TestConstAndVar(t *testing.T) {
	if k, _, _, typ, _, ok := findFull(t, "example.com/sample/calc", "MaxInt"); !ok || k != "const" || typ == "" {
		t.Fatalf("MaxInt kind=%q type=%q ok=%v", k, typ, ok)
	}
	if k, _, _, _, _, ok := findFull(t, "example.com/sample/calc", "Default"); !ok || k != "var" {
		t.Fatalf("Default kind=%q ok=%v", k, ok)
	}
}

func TestStructAndField(t *testing.T) {
	if k, tk, _, _, _, ok := findFull(t, "example.com/sample/calc", "Point"); !ok || k != "type" || tk != "struct" {
		t.Fatalf("Point kind=%q typeKind=%q ok=%v", k, tk, ok)
	}
	if k, _, _, typ, owner, ok := findFull(t, "example.com/sample/calc", "Point.X"); !ok || k != "field" || typ != "int" || owner != "Point" {
		t.Fatalf("Point.X kind=%q type=%q owner=%q ok=%v", k, typ, owner, ok)
	}
}

func TestInterfaceAndMethod(t *testing.T) {
	if k, tk, _, _, _, ok := findFull(t, "example.com/sample/calc", "Adder"); !ok || k != "type" || tk != "interface" {
		t.Fatalf("Adder kind=%q typeKind=%q ok=%v", k, tk, ok)
	}
	if k, _, _, _, owner, ok := findFull(t, "example.com/sample/calc", "Adder.Add"); !ok || k != "method" || owner != "Adder" {
		t.Fatalf("Adder.Add kind=%q owner=%q ok=%v", k, owner, ok)
	}
}

func TestDefinedType(t *testing.T) {
	if k, tk, under, _, _, ok := findFull(t, "example.com/sample/calc", "Celsius"); !ok || k != "type" || tk != "defined" || under != "float64" {
		t.Fatalf("Celsius kind=%q typeKind=%q underlying=%q ok=%v", k, tk, under, ok)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/extract/golang/ -run 'Const|Struct|Interface|Defined'`
Expected: FAIL — these symbols not yet produced.

- [ ] **Step 4: Extend extraction to GenDecl**

In `internal/extract/golang/symbols.go`, extend `extractSymbols` to also walk `*ast.GenDecl`, and add the helpers below. Replace the `extractSymbols` body with:

```go
func extractSymbols(p *packages.Package, moduleDir string) []schema.Symbol {
	var syms []schema.Symbol
	for _, file := range p.Syntax {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if s, ok := funcSymbol(p, d, moduleDir); ok {
					syms = append(syms, s)
				}
			case *ast.GenDecl:
				syms = append(syms, genSymbols(p, d, moduleDir)...)
			}
		}
	}
	return syms
}

// genSymbols handles type/const/var declarations and their owned members.
func genSymbols(p *packages.Package, gd *ast.GenDecl, moduleDir string) []schema.Symbol {
	var out []schema.Symbol
	for _, spec := range gd.Specs {
		switch sp := spec.(type) {
		case *ast.TypeSpec:
			out = append(out, typeSymbols(p, gd, sp, moduleDir)...)
		case *ast.ValueSpec:
			out = append(out, valueSymbols(p, gd, sp, moduleDir)...)
		}
	}
	return out
}

func typeSymbols(p *packages.Package, gd *ast.GenDecl, ts *ast.TypeSpec, moduleDir string) []schema.Symbol {
	obj, _ := p.TypesInfo.Defs[ts.Name].(*types.TypeName)
	if obj == nil {
		return nil
	}
	name := ts.Name.Name
	underlying := obj.Type().Underlying()
	tk := "defined"
	if ts.Assign.IsValid() {
		tk = "alias"
	}
	switch underlying.(type) {
	case *types.Struct:
		tk = "struct"
	case *types.Interface:
		tk = "interface"
	}

	sym := schema.Symbol{
		ID:              name,
		Name:            name,
		Kind:            "type",
		Visibility:      visibility(name),
		VisibilityIdiom: "capitalized",
		Location:        locationOf(p.Fset, ts, moduleDir),
		Doc:             schema.Doc{Raw: docText(ts.Doc, gd.Doc), Format: "godoc"},
		Complexity:      schema.DeferredComplexity(),
		TypeKind:        tk,
		Underlying:      typeString(underlying),
	}
	out := []schema.Symbol{sym}

	switch u := underlying.(type) {
	case *types.Struct:
		out = append(out, structFields(p, ts, u, name, moduleDir)...)
	case *types.Interface:
		out = append(out, interfaceMethods(p, ts, u, name, moduleDir)...)
	}
	return out
}

func structFields(p *packages.Package, ts *ast.TypeSpec, st *types.Struct, owner, moduleDir string) []schema.Symbol {
	var out []schema.Symbol
	stype, ok := ts.Type.(*ast.StructType)
	if !ok {
		return out
	}
	idx := 0
	for _, field := range stype.Fields.List {
		names := field.Names
		if len(names) == 0 { // embedded
			if idx < st.NumFields() {
				f := st.Field(idx)
				out = append(out, fieldSymbol(p, field, f.Name(), typeString(f.Type()), owner, moduleDir))
				idx++
			}
			continue
		}
		for range names {
			if idx >= st.NumFields() {
				break
			}
			f := st.Field(idx)
			out = append(out, fieldSymbol(p, field, f.Name(), typeString(f.Type()), owner, moduleDir))
			idx++
		}
	}
	return out
}

func fieldSymbol(p *packages.Package, node ast.Node, name, typ, owner, moduleDir string) schema.Symbol {
	return schema.Symbol{
		ID:              owner + "." + name,
		Name:            name,
		Kind:            "field",
		Visibility:      visibility(name),
		VisibilityIdiom: "capitalized",
		Location:        locationOf(p.Fset, node, moduleDir),
		Owner:           owner,
		Doc:             schema.Doc{Raw: fieldDoc(node), Format: "godoc"},
		Complexity:      schema.DeferredComplexity(),
		Type:            typ,
	}
}

func interfaceMethods(p *packages.Package, ts *ast.TypeSpec, it *types.Interface, owner, moduleDir string) []schema.Symbol {
	var out []schema.Symbol
	for i := 0; i < it.NumExplicitMethods(); i++ {
		m := it.ExplicitMethod(i)
		sig, _ := m.Type().(*types.Signature)
		s := schema.Symbol{
			ID:              owner + "." + m.Name(),
			Name:            m.Name(),
			Kind:            "method",
			Visibility:      visibility(m.Name()),
			VisibilityIdiom: "capitalized",
			Location:        locationOf(p.Fset, ts, moduleDir),
			Owner:           owner,
			Doc:             schema.Doc{Raw: "", Format: "godoc"},
			Complexity:      schema.DeferredComplexity(),
		}
		if sig != nil {
			s.Signature = &schema.Signature{
				Params:     tupleParams(sig.Params()),
				Returns:    tupleParams(sig.Results()),
				TypeParams: typeParams(sig.TypeParams()),
				Variadic:   sig.Variadic(),
			}
		}
		out = append(out, s)
	}
	return out
}

func valueSymbols(p *packages.Package, gd *ast.GenDecl, vs *ast.ValueSpec, moduleDir string) []schema.Symbol {
	kind := "var"
	if gd.Tok == token.CONST {
		kind = "const"
	}
	var out []schema.Symbol
	for _, ident := range vs.Names {
		if ident.Name == "_" {
			continue
		}
		obj := p.TypesInfo.Defs[ident]
		if obj == nil {
			continue
		}
		out = append(out, schema.Symbol{
			ID:              ident.Name,
			Name:            ident.Name,
			Kind:            kind,
			Visibility:      visibility(ident.Name),
			VisibilityIdiom: "capitalized",
			Location:        locationOf(p.Fset, ident, moduleDir),
			Doc:             schema.Doc{Raw: docText(vs.Doc, gd.Doc), Format: "godoc"},
			Complexity:      schema.DeferredComplexity(),
			Type:            typeString(obj.Type()),
		})
	}
	return out
}

// docText prefers the spec's own doc, falling back to the GenDecl's doc.
func docText(specDoc, genDoc *ast.CommentGroup) string {
	if specDoc != nil && strings.TrimSpace(specDoc.Text()) != "" {
		return strings.TrimSpace(specDoc.Text())
	}
	if genDoc != nil {
		return strings.TrimSpace(genDoc.Text())
	}
	return ""
}

// fieldDoc returns a struct field's doc or line comment, if any.
func fieldDoc(node ast.Node) string {
	f, ok := node.(*ast.Field)
	if !ok {
		return ""
	}
	if f.Doc != nil && strings.TrimSpace(f.Doc.Text()) != "" {
		return strings.TrimSpace(f.Doc.Text())
	}
	if f.Comment != nil {
		return strings.TrimSpace(f.Comment.Text())
	}
	return ""
}
```

If the `"path"` import added in Task 3 is still unused, remove it now.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/extract/golang/ -run 'Const|Struct|Interface|Defined'`
Expected: PASS.

- [ ] **Step 6: Full package + vet**

Run: `go test ./internal/extract/golang/ && go vet ./...`
Expected: all PASS, vet clean.

- [ ] **Step 7: Commit**

```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
git add internal/extract/golang/
git commit -m "feat: extract Go types, consts, vars, fields, interface methods"
```

---

### Task 5: Deterministic emitter (index + shards)

**Files:**
- Create: `internal/emit/emit.go`
- Test: `internal/emit/emit_test.go`

**Interfaces:**
- Consumes: `[]schema.Package`, plus `module string` and `languages []string`.
- Produces:
  `emit.Manifest(pkgs []schema.Package, module string, languages []string) (schema.Index, map[string]schema.Shard)`
  — pure builder, sorts deterministically, computes shard paths and counts;
  `emit.WriteDir(outDir string, idx schema.Index, shards map[string]schema.Shard) error`
  — writes `assayxport.json` + `.assayxport/...` with stable JSON;
  `emit.Combined(idx schema.Index, shards map[string]schema.Shard) ([]byte, error)`
  — one JSON blob `{"index":...,"shards":...}` for `--stdout`.
  Shard map keys are shard relative paths.

- [ ] **Step 1: Write the failing test**

Create `internal/emit/emit_test.go`:

```go
package emit

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"goforge.dev/assayxport/internal/schema"
)

func samplePkgs() []schema.Package {
	return []schema.Package{
		{
			ID: "example.com/s/b", Language: "go", Path: "b", Name: "b", Doc: "Package b.",
			Symbols: []schema.Symbol{
				{ID: "Z", Name: "Z", Kind: "func", Visibility: "exported", VisibilityIdiom: "capitalized",
					Location: schema.Location{File: "b/b.go", Line: 9, Col: 1, EndLine: 9}, Complexity: schema.DeferredComplexity(),
					Doc: schema.Doc{Format: "godoc"}, Signature: &schema.Signature{Params: []schema.Param{}, Returns: []schema.Param{}}},
				{ID: "A", Name: "A", Kind: "func", Visibility: "exported", VisibilityIdiom: "capitalized",
					Location: schema.Location{File: "b/b.go", Line: 3, Col: 1, EndLine: 3}, Complexity: schema.DeferredComplexity(),
					Doc: schema.Doc{Format: "godoc"}, Signature: &schema.Signature{Params: []schema.Param{}, Returns: []schema.Param{}}},
			},
		},
		{
			ID: "example.com/s/a", Language: "go", Path: "a", Name: "a", Doc: "Package a.",
			Symbols: []schema.Symbol{{ID: "Main", Name: "main", Kind: "func", Visibility: "unexported",
				VisibilityIdiom: "capitalized", Location: schema.Location{File: "a/a.go", Line: 1, Col: 1, EndLine: 1},
				Complexity: schema.DeferredComplexity(), Doc: schema.Doc{Format: "godoc"}, IsEntrypoint: true}},
		},
	}
}

func TestManifestSortsAndCounts(t *testing.T) {
	idx, shards := Manifest(samplePkgs(), "example.com/s", []string{"go"})
	if idx.SchemaVersion != "1" || idx.Tool != "assayxport" {
		t.Fatalf("index header = %+v", idx)
	}
	if len(idx.Packages) != 2 || idx.Packages[0].ID != "example.com/s/a" {
		t.Fatalf("packages not sorted by id: %+v", idx.Packages)
	}
	b := idx.Packages[1]
	if b.ID != "example.com/s/b" || b.SymbolCount != 2 || b.Shard != ".assayxport/b.json" {
		t.Fatalf("pkg b entry = %+v", b)
	}
	if idx.Packages[0].EntrypointCount != 1 {
		t.Fatalf("pkg a entrypoint_count = %d, want 1", idx.Packages[0].EntrypointCount)
	}
	// symbols sorted by (file,line,name): A(line3) before Z(line9)
	sh := shards[".assayxport/b.json"]
	if sh.Symbols[0].ID != "A" || sh.Symbols[1].ID != "Z" {
		t.Fatalf("shard b symbols not sorted: %+v", sh.Symbols)
	}
}

func TestWriteDirDeterministic(t *testing.T) {
	dir := t.TempDir()
	idx, shards := Manifest(samplePkgs(), "example.com/s", []string{"go"})
	if err := WriteDir(dir, idx, shards); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(dir, "assayxport.json"))
	if err != nil {
		t.Fatal(err)
	}
	if first[len(first)-1] != '\n' {
		t.Fatalf("index must end with newline")
	}
	if !bytes.Contains(first, []byte("\"schema_version\": \"1\"")) {
		t.Fatalf("index not 2-space indented JSON: %s", first)
	}
	// Re-emit; bytes identical.
	dir2 := t.TempDir()
	if err := WriteDir(dir2, idx, shards); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(dir2, "assayxport.json"))
	if !bytes.Equal(first, second) {
		t.Fatalf("index not deterministic across runs")
	}
	if _, err := os.Stat(filepath.Join(dir, ".assayxport", "b.json")); err != nil {
		t.Fatalf("shard b.json not written: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/emit/`
Expected: FAIL — `undefined: Manifest`.

- [ ] **Step 3: Write the emitter**

Create `internal/emit/emit.go`:

```go
// Package emit builds and writes assayxport's deterministic manifest: a root
// index plus one shard per package. Output is byte-stable for equal inputs.
package emit

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"goforge.dev/assayxport/internal/schema"
)

const shardDir = ".assayxport"

// shardPath returns the POSIX shard path for a package's relative dir.
func shardPath(pkgDir string) string {
	if pkgDir == "" || pkgDir == "." {
		return shardDir + "/_root.json"
	}
	return shardDir + "/" + filepath.ToSlash(pkgDir) + ".json"
}

// Manifest builds the index and shard set from packages, sorting
// deterministically and computing shard paths and counts.
func Manifest(pkgs []schema.Package, module string, languages []string) (schema.Index, map[string]schema.Shard) {
	sorted := append([]schema.Package(nil), pkgs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	idx := schema.Index{
		SchemaVersion: schema.Version,
		Tool:          "assayxport",
		Languages:     languages,
		Root:          ".",
		Module:        module,
	}
	shards := make(map[string]schema.Shard, len(sorted))

	for _, p := range sorted {
		syms := append([]schema.Symbol(nil), p.Symbols...)
		sort.SliceStable(syms, func(i, j int) bool {
			a, b := syms[i].Location, syms[j].Location
			if a.File != b.File {
				return a.File < b.File
			}
			if a.Line != b.Line {
				return a.Line < b.Line
			}
			return syms[i].Name < syms[j].Name
		})
		entrypoints := 0
		for _, s := range syms {
			if s.IsEntrypoint {
				entrypoints++
			}
		}
		sp := shardPath(p.Path)
		idx.Packages = append(idx.Packages, schema.PackageEntry{
			ID:              p.ID,
			Language:        p.Language,
			Path:            p.Path,
			Name:            p.Name,
			Doc:             p.Doc,
			SymbolCount:     len(syms),
			EntrypointCount: entrypoints,
			Shard:           sp,
		})
		shards[sp] = schema.Shard{
			SchemaVersion: schema.Version,
			Package:       schema.PackageInfo{ID: p.ID, Language: p.Language, Path: p.Path, Name: p.Name, Doc: p.Doc},
			Symbols:       syms,
		}
	}
	return idx, shards
}

// marshal renders v as 2-space-indented JSON with a trailing newline and no
// HTML escaping, for stable diffable output.
func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil { // Encode appends a trailing newline
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteDir writes the index and all shards under outDir.
func WriteDir(outDir string, idx schema.Index, shards map[string]schema.Shard) error {
	idxBytes, err := marshal(idx)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "assayxport.json"), idxBytes, 0o644); err != nil {
		return err
	}
	paths := make([]string, 0, len(shards))
	for p := range shards {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		full := filepath.Join(outDir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		b, err := marshal(shards[p])
		if err != nil {
			return err
		}
		if err := os.WriteFile(full, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Combined renders the whole manifest as one JSON blob for --stdout.
func Combined(idx schema.Index, shards map[string]schema.Shard) ([]byte, error) {
	type combined struct {
		Index  schema.Index            `json:"index"`
		Shards map[string]schema.Shard `json:"shards"`
	}
	return marshal(combined{Index: idx, Shards: shards})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/emit/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
git add internal/emit/
git commit -m "feat: deterministic manifest emitter (index + shards)"
```

---

### Task 6: CLI wiring + end-to-end golden + dogfood

**Files:**
- Create: `cmd/assayxport/main.go`
- Create: `internal/extract/golang/golden_test.go`
- Create: `internal/extract/golang/testdata/sample.golden.json`
- Create: `README.md`
- Test: end-to-end via the golden test + a CLI smoke run.

**Interfaces:**
- Consumes: `golang.New()`, `emit.Manifest`, `emit.WriteDir`, `emit.Combined`.
- Produces: the `assayxport` binary with `scan [path]` and flags
  `--out`, `--stdout`, `--quiet`; exit 0 on success, non-zero on load error.

- [ ] **Step 1: Write the golden end-to-end test**

Create `internal/extract/golang/golden_test.go`:

```go
package golang

import (
	"flag"
	"os"
	"testing"

	"goforge.dev/assayxport/internal/emit"
)

var update = flag.Bool("update", false, "update golden file")

func TestSampleGolden(t *testing.T) {
	pkgs, err := New().Extract("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	idx, shards := emit.Manifest(pkgs, "example.com/sample", []string{"go"})
	got, err := emit.Combined(idx, shards)
	if err != nil {
		t.Fatal(err)
	}
	const golden = "testdata/sample.golden.json"
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update first): %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("golden mismatch; re-run with -update if intended.\n--- got ---\n%s", got)
	}
}
```

- [ ] **Step 2: Generate the golden, then verify it is stable**

Run: `go test ./internal/extract/golang/ -run TestSampleGolden -update`
Then: `go test ./internal/extract/golang/ -run TestSampleGolden`
Expected: first generates `testdata/sample.golden.json`; second PASSES against it. Open the golden and sanity-check it contains `"tool": "assayxport"`, package ids for `calc` and `cmd/tool`, `Accumulator.Push` as a method, and `"is_entrypoint": true` for `main`.

- [ ] **Step 3: Surface the module path from the extractor**

The CLI needs the module path for the index. Add an accessor on the extractor. In
`internal/extract/golang/golang.go`, give `Extractor` a `module` field and an
accessor:

```go
// Extractor is the Go language extractor.
type Extractor struct{ module string }

// Module returns the module path discovered by the most recent Extract call.
func (e *Extractor) Module() string { return e.module }
```

Inside `Extract`, in the loop where `p.Module` is read, record the path
(guard for nil):

```go
		if p.Module != nil {
			moduleDir = p.Module.Dir
			e.module = p.Module.Path
		}
```

(Replace the existing `if p.Module != nil { moduleDir = p.Module.Dir }` block
from Task 2's loader with the version above.)

- [ ] **Step 4: Write the CLI**

Create `cmd/assayxport/main.go`:

```go
// Command assayxport scans a Go codebase and writes a deterministic JSON
// manifest (assayxport.json + .assayxport/ shards) of its API and docs.
package main

import (
	"flag"
	"fmt"
	"os"

	"goforge.dev/assayxport/internal/emit"
	"goforge.dev/assayxport/internal/extract/golang"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "assayxport:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("assayxport", flag.ContinueOnError)
	out := fs.String("out", "", "output directory (default: scan path)")
	stdout := fs.Bool("stdout", false, "print combined JSON to stdout; write no files")
	quiet := fs.Bool("quiet", false, "suppress progress on stderr")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: assayxport scan [path] [flags]")
		fs.PrintDefaults()
	}
	if len(args) == 0 || args[0] != "scan" {
		fs.Usage()
		return fmt.Errorf("expected subcommand \"scan\"")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	path := "."
	if fs.NArg() > 0 {
		path = fs.Arg(0)
	}

	ex := golang.New()
	pkgs, err := ex.Extract(path)
	if err != nil {
		return err
	}
	idx, shards := emit.Manifest(pkgs, ex.Module(), []string{"go"})

	if *stdout {
		b, err := emit.Combined(idx, shards)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(b)
		return err
	}

	outDir := *out
	if outDir == "" {
		outDir = path
	}
	if err := emit.WriteDir(outDir, idx, shards); err != nil {
		return err
	}
	if !*quiet {
		fmt.Fprintf(os.Stderr, "assayxport: wrote %d packages to %s\n", len(idx.Packages), outDir)
	}
	return nil
}
```

- [ ] **Step 5: Build and smoke-test the CLI on the fixture**

```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
go build ./...
go run ./cmd/assayxport scan internal/extract/golang/testdata/sample --out /tmp/axp-smoke
test -f /tmp/axp-smoke/assayxport.json && echo INDEX_OK
go run ./cmd/assayxport scan internal/extract/golang/testdata/sample --stdout | head -c 60; echo
```
Expected: `INDEX_OK`; `--stdout` prints JSON starting with `{` and `"index"`.

- [ ] **Step 6: Dogfood on the repo itself**

```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
go run ./cmd/assayxport scan . --out /tmp/axp-self
test -f /tmp/axp-self/assayxport.json && echo SELF_OK
grep -q '"tool": "assayxport"' /tmp/axp-self/assayxport.json && echo SCHEMA_OK
```
Expected: `SELF_OK` and `SCHEMA_OK`. (assayxport's own packages — schema, extract, emit, cmd — appear in the manifest.)

- [ ] **Step 7: Write the README**

Create `README.md`:

```markdown
# assayxport

> Assaying analyzes a metal to report its exact composition. `assayxport`
> analyzes a codebase to report its exact API composition - where each symbol
> lives, what package it belongs to, its signature, its docs, and whether it is
> a runnable entrypoint - as a deterministic JSON manifest at the project root,
> so an LLM, docgen, or tool reads one map instead of reparsing everything.

Part of the [goforge](https://goforge.dev) suite. SP1 supports Go; Python and
Java follow.

## Install

```bash
go install goforge.dev/assayxport/cmd/assayxport@latest
```

## Use

```bash
assayxport scan .            # writes assayxport.json + .assayxport/ shards
assayxport scan ./pkg --stdout   # print combined JSON, write nothing
```

## Output

- `assayxport.json` - root index: project metadata + one entry per package.
- `.assayxport/<package-dir>.json` - per-package shard with the full symbol list.

Output is deterministic: relative paths, no timestamps, stable ordering. Equal
inputs produce byte-identical files.

## License

MIT.
```

- [ ] **Step 8: Full suite + vet + commit**

```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
go vet ./... && go test ./...
git add cmd/ internal/extract/golang/golden_test.go internal/extract/golang/testdata/sample.golden.json README.md internal/extract/golang/golang.go
git commit -m "feat: assayxport CLI (scan) + end-to-end golden + README"
```

Expected: `go vet` clean, all tests PASS.

---

## Self-Review

**Spec coverage:**
- Single Go binary, `goforge.dev/assayxport`, MIT → Task 1 (module, LICENSE), Task 6 (CLI). ✓
- `Extractor` interface seam → Task 2. ✓
- Go extraction via go/packages + go/types → Tasks 2–4. ✓
- All symbol kinds (func/method/type/const/var/field/interface-method) with visibility label, signature/type_kind/underlying/type per kind → Tasks 3–4; schema shape → Task 1. ✓
- Entrypoint detection (`package main` `func main()`) + invocation → Task 3. ✓
- Doc comments (godoc, cleaned via `CommentGroup.Text()`) → Tasks 2–4. ✓
- Output: root `assayxport.json` index + `.assayxport/<dir>.json` shards, `_root.json` sentinel → Task 5. ✓
- Determinism (relative POSIX paths, no timestamps/host data, sorted pkgs/symbols, fixed type qualifier, 2-space JSON + trailing newline, byte-identical re-run) → Task 5 (emit + determinism test), Task 3 (`typeQualifier`), Task 6 (golden). ✓
- `complexity` always deferred → Task 1 (`DeferredComplexity`), used throughout. ✓
- Load errors → non-zero exit, no partial manifest → Task 2 (`loadErrs`), Task 6 (CLI propagates error → exit 1). ✓
- Testing: golden + determinism + unit + dogfood → Tasks 1,3,4,5,6. ✓
- Out of scope (Python/Java/tree-sitter/wazero/big-O/daemon) → not present. ✓

**Placeholder scan:** No TBD/TODO. Every code step shows full code. The one initially-wrong `moduleOf` stub in Task 6 Step 3 is explicitly corrected in Step 4 (delete it; use `ex.Module()`); flagged inline so the implementer cannot ship the broken signature. Fixture/golden content is concrete.

**Type/name consistency:** `schema.Package` (in-memory) vs `schema.PackageEntry`/`PackageInfo` (JSON) used consistently. `New() *Extractor`, `Extract(root) ([]schema.Package, error)`, `Module() string`, `Language() string` consistent across Tasks 2–6. `emit.Manifest`/`WriteDir`/`Combined` signatures match their callers in Task 6. `typeString`/`typeQualifier` defined once (Task 3) and reused (Task 4). Symbol field names (`TypeKind`, `Underlying`, `Type`, `Signature`, `Owner`, `Invocation`) match the Task 1 schema. Shard path `_root.json` sentinel consistent between spec and Task 5.

**Known follow-ups (not SP1 gaps):** module path is recorded from whichever loaded package carries `*packages.Module` (single-module repos — the SP1 target); multi-module trees are an SP-later concern.
