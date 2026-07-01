# assayxport SP3 (Java) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Java support to assayxport by reusing the SP2 `internal/ts` tree-sitter layer with gotreesitter's built-in Java grammar, plus a Java extractor that emits the existing deterministic manifest schema and a `java` entry in the polyglot registry.

**Architecture:** Add a `Java` value to the `ts.Language` enum and a lazy `java()` grammar loader (parallel to `python()`). A new `internal/extract/java` package discovers `.java` files, parses each compilation unit, and builds `schema.Package` units at two levels: one `module` per file and one `package` per Java `package` declaration (keyed by the declaration, not the directory). Register the Java extractor in the existing registry; the CLI's repeatable `--lang` is already generic.

**Tech Stack:** Go 1.24, `github.com/odvcencio/gotreesitter` v0.20.7 (pure-Go, cgo-free; behind `internal/ts`), the existing `internal/schema` + `internal/emit` + `internal/extract/registry`.

## Global Constraints

- License MIT. Module `goforge.dev/assayxport`, `go 1.24` (do NOT bump; do NOT add cgo). Run every go command as `GOTOOLCHAIN=local go ...`.
- No em-dashes or en-dashes anywhere in code, comments, or docs (strip, never add).
- All tree-sitter access goes through `internal/ts` ONLY. No third-party tree-sitter import outside `internal/ts/ts.go`.
- Schema changes are ADDITIVE and `omitempty`; `schema_version` stays `"1"`. SP3 adds exactly one field (`Symbol.Annotations []string`); everything else is new string values in existing free-string fields.
- Determinism is a hard requirement: relative POSIX paths, no timestamps / absolute paths / host data, packages sorted by id, symbols by `(file, line, name)`, `members` and `languages` sorted, byte-identical on re-run. No Go map-iteration order may reach output. The Java package `id` comes from the parsed `package` declaration so it never embeds host directory names.
- Java visibility is 4-way: `visibility` in `public | protected | private | package-private`, `visibility_idiom = "access-modifier"`. This deliberately diverges from the Go/Python binary `exported`/`unexported`; consumers key on `visibility_idiom`.
- gotreesitter's node/field names are NOT guaranteed to match canonical tree-sitter-java. Every task that names a node type or field MUST confirm it against the actual parser (probe) before trusting it, and adapt if it differs, recording the deviation.

**Reference interfaces (already in the tree, do not redefine):**
- `internal/ts`: `ts.Parse(lang ts.Language, src []byte) (*ts.Tree, error)`, `(*Tree).Root() ts.Node`; `ts.Node` methods `Type() string`, `NamedChildCount() int`, `NamedChild(i int) ts.Node`, `ChildByFieldName(f string) (ts.Node, bool)`, `StartLine()/StartCol()/EndLine() int` (1-based), `Content(src []byte) string`, `IsNull() bool`. Enum currently: `const ( Python Language = iota )`.
- `internal/schema`: `Package{ID, Language, Path, Name, Doc, Level, Members, IsEntrypoint, Invocation, Symbols}`, `Symbol{ID, Name, Kind, Visibility, VisibilityIdiom, Location, Owner, Doc, IsEntrypoint, Invocation, Complexity, Signature, TypeKind, Underlying, Type, InAll, Decorators}`, `Signature{Params, Returns, TypeParams, Receiver, Variadic, Modifiers}`, `Param{Name, Type}`, `TypeParam{Name, Constraint}`, `Location{File, Line, Col, EndLine}`, `Doc{Raw, Format}`, `Invocation{Kind, How}`, `DeferredComplexity() Complexity`.
- `internal/extract`: `Extractor interface { Language() string; Extract(root string) ([]schema.Package, error) }`.
- `internal/extract/registry`: `All() []extract.Extractor`, `Select(langs []string) ([]extract.Extractor, error)`, `Run(exts []extract.Extractor, root string) ([]schema.Package, []string, error)`.
- `internal/emit`: `Manifest(pkgs []schema.Package, module string, languages []string) (schema.Index, map[string]schema.Shard)`, `Combined(idx schema.Index, shards map[string]schema.Shard) ([]byte, error)`.

---

## File Structure

- `internal/ts/ts.go` (modify) - add `Java` to the enum + `java()` loader + `Parse` switch case.
- `internal/ts/ts_test.go` (modify) - add a Java parse test.
- `internal/ts/provenance.md` (modify) - record the Java grammar blob provenance.
- `internal/schema/schema.go` (modify) - add `Symbol.Annotations []string`.
- `internal/extract/java/discover.go` (create) - enumerate `.java` files.
- `internal/extract/java/discover_test.go` (create).
- `internal/extract/java/symbols.go` (create) - parse one compilation unit into symbols.
- `internal/extract/java/symbols_test.go` (create).
- `internal/extract/java/java.go` (create) - the `Extractor`: module + package units.
- `internal/extract/java/python_test.go` equivalent -> `internal/extract/java/java_test.go` (create) - unit + golden tests.
- `internal/extract/java/testdata/proj/**` (create) - fixture Java tree.
- `internal/extract/java/testdata/sample.golden.json` (create, generated).
- `internal/extract/registry/registry.go` (modify) - register `java.New()`.
- `internal/extract/registry/registry_test.go` (modify) - polyglot now 3 languages.
- `cmd/assayxport/testdata/mixed/` (modify) - add a `.java` file.
- `NOTICE` (modify) - add tree-sitter-java attribution.
- `README.md` (modify) - add Java.

---

### Task 1: `internal/ts` Java grammar support + parity probe

**Files:**
- Modify: `internal/ts/ts.go`
- Modify: `internal/ts/ts_test.go`
- Modify: `internal/ts/provenance.md`

**Interfaces:**
- Consumes: `github.com/odvcencio/gotreesitter/grammars.JavaLanguage()` (confirmed present in v0.20.7; embedded blob `grammars/grammar_blobs/java.bin`, ~46 KB, ABI 15).
- Produces: `ts.Java ts.Language`; `ts.Parse(ts.Java, src)` returns a parsed `*ts.Tree`.

- [ ] **Step 1: Add the failing test**

In `internal/ts/ts_test.go`, add:

```go
func TestParseJava(t *testing.T) {
	src := []byte("package com.foo;\npublic class Bar { public int x() { return 1; } }\n")
	tree, err := Parse(Java, src)
	if err != nil {
		t.Fatalf("Parse(Java): %v", err)
	}
	root := tree.Root()
	if root.IsNull() || root.NamedChildCount() == 0 {
		t.Fatalf("empty Java tree: type=%q count=%d", root.Type(), root.NamedChildCount())
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `GOTOOLCHAIN=local go test ./internal/ts/ -run TestParseJava`
Expected: FAIL - `undefined: Java` (compile error).

- [ ] **Step 3: Add Java to the enum and loader**

In `internal/ts/ts.go`, extend the imports are unchanged. Change the const block and add the loader:

```go
const (
	// Python selects the tree-sitter Python grammar.
	Python Language = iota
	// Java selects the tree-sitter Java grammar.
	Java
)
```

Add, next to the python loader:

```go
// javaLang holds the lazily-initialized Java grammar, decoded once under
// sync.Once (mirrors python()).
var (
	javaOnce sync.Once
	javaLang *gts.Language
)

func java() *gts.Language {
	javaOnce.Do(func() {
		javaLang = grammars.JavaLanguage()
	})
	return javaLang
}
```

In `Parse`, add the case to the switch:

```go
	case Python:
		g = python()
	case Java:
		g = java()
```

- [ ] **Step 4: Run it and watch it pass**

Run: `GOTOOLCHAIN=local go test ./internal/ts/ -run TestParseJava`
Expected: PASS.

- [ ] **Step 5: Probe the Java grammar and record provenance**

Write a TEMPORARY probe test (delete it before commit) that dumps the tree so later tasks code against real node/field names. Add to `internal/ts/ts_test.go`:

```go
func TestProbeJavaShape(t *testing.T) {
	src := []byte("package com.foo;\n" +
		"/** Doc. */\n" +
		"@Deprecated public class Bar<T> extends Base implements I {\n" +
		"  private int n;\n" +
		"  public Bar(int n) { this.n = n; }\n" +
		"  @Override public static void main(String[] args) {}\n" +
		"  interface Inner {}\n" +
		"}\n" +
		"enum E { A, B }\n")
	tree, _ := Parse(Java, src)
	var walk func(n Node, d int)
	walk = func(n Node, d int) {
		for i := 0; i < n.NamedChildCount(); i++ {
			c := n.NamedChild(i)
			t.Logf("%*s%s", d*2, "", c.Type())
			walk(c, d+1)
		}
	}
	walk(tree.Root(), 0)
	t.Fail() // force -v output; DELETE this test before committing
}
```

Run: `GOTOOLCHAIN=local go test ./internal/ts/ -run TestProbeJavaShape -v`
Record in `internal/ts/provenance.md` (new "## Java grammar" section): the confirmed node types for package declaration, each type declaration (class/interface/enum/record/annotation type), method/constructor/field/enum-constant, the `modifiers` node, `annotation`/`marker_annotation`, `type_parameters`, and how Javadoc appears (expected: a `block_comment` node preceding the declaration as a sibling). Also record the Java blob path (`grammars/grammar_blobs/java.bin` inside the module), and note the ABI (15) is shared with Python. Then DELETE `TestProbeJavaShape`.

Note any deviation from these canonical names so Task 3 uses the real ones:
`program`, `package_declaration`, `class_declaration`, `interface_declaration`, `enum_declaration`, `record_declaration`, `annotation_type_declaration`, `class_body`/`enum_body`/`interface_body`/`annotation_type_body`, `method_declaration`, `constructor_declaration`, `field_declaration`, `enum_constant`, `formal_parameters`, `formal_parameter`, `spread_parameter`, `modifiers`, `annotation`, `marker_annotation`, `type_parameters`, `type_parameter`, `variable_declarator`, `block_comment`, `line_comment`, `scoped_identifier`, `identifier`; fields `name`, `parameters`, `type`, `body`, `superclass`, `interfaces`, `type_parameters`, `dimensions`, `value`.

- [ ] **Step 6: Vet + commit**

Run: `GOTOOLCHAIN=local go test ./internal/ts/ && GOTOOLCHAIN=local go vet ./...`
Expected: PASS; vet clean; no `TestProbeJavaShape` remaining.

```bash
git add internal/ts/
git commit -m "feat(ts): add built-in Java grammar to the tree-sitter layer"
```

---

### Task 2: Java file discovery

**Files:**
- Create: `internal/extract/java/discover.go`
- Test: `internal/extract/java/discover_test.go`

**Interfaces:**
- Produces:
  ```go
  package java
  type javaFile struct{ Abs, Rel string }
  func discover(root string) ([]javaFile, error) // sorted by Rel, POSIX Rel
  ```
  Unlike Python, package identity is NOT derived here (it comes from the parsed `package` declaration in Task 3). Discovery only enumerates `.java` files.

- [ ] **Step 1: Write the failing test**

Create `internal/extract/java/discover_test.go`:

```go
package java

import "testing"

func TestDiscoverFindsJavaFilesSorted(t *testing.T) {
	files, err := discover("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no .java files discovered")
	}
	for i := 1; i < len(files); i++ {
		if files[i-1].Rel >= files[i].Rel {
			t.Fatalf("not sorted: %q >= %q", files[i-1].Rel, files[i].Rel)
		}
	}
	for _, f := range files {
		if f.Rel == "" || f.Abs == "" {
			t.Fatalf("empty path in %+v", f)
		}
	}
}

func TestDiscoverSkipsBuildDirs(t *testing.T) {
	files, err := discover("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Rel == "target/Generated.java" {
			t.Fatalf("build dir not skipped: %q", f.Rel)
		}
	}
}
```

Create the minimal fixture needed for these two tests (Task 4 enriches it): `internal/extract/java/testdata/proj/com/foo/Bar.java` containing `package com.foo;\npublic class Bar {}\n`, and a file that MUST be skipped: `internal/extract/java/testdata/proj/target/Generated.java` containing `package com.foo;\nclass Generated {}\n`.

- [ ] **Step 2: Run it and watch it fail**

Run: `GOTOOLCHAIN=local go test ./internal/extract/java/ -run TestDiscover`
Expected: FAIL - `undefined: discover`.

- [ ] **Step 3: Implement discovery**

Create `internal/extract/java/discover.go`:

```go
// Package java extracts a Java source tree into assayxport's schema.
package java

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

type javaFile struct {
	Abs string
	Rel string
}

// buildDirs are directory names that hold generated or compiled output and are
// never part of the source API surface.
var buildDirs = map[string]bool{
	"target": true, // Maven
	"build":  true, // Gradle
	"out":    true, // IntelliJ
	"bin":    true, // Eclipse
}

// discover enumerates .java files under root, sorted by relative POSIX path.
// Dot-directories and common build-output directories are skipped.
func discover(root string) ([]javaFile, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var out []javaFile
	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if path != absRoot && (strings.HasPrefix(base, ".") || buildDirs[base]) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".java") {
			return nil
		}
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return err
		}
		out = append(out, javaFile{Abs: path, Rel: filepath.ToSlash(rel)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, nil
}
```

- [ ] **Step 4: Run it and watch it pass**

Run: `GOTOOLCHAIN=local go test ./internal/extract/java/ -run TestDiscover`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/extract/java/
git commit -m "feat(java): discover .java files, skipping build dirs"
```

---

### Task 3: Java compilation-unit symbol extraction (the crux)

**Files:**
- Modify: `internal/schema/schema.go` (add `Symbol.Annotations`)
- Create: `internal/extract/java/symbols.go`
- Test: `internal/extract/java/symbols_test.go`
- Modify: `internal/extract/java/testdata/proj/com/foo/Bar.java` (enrich the fixture)

**Interfaces:**
- Consumes: `internal/ts` (`ts.Parse(ts.Java, src)`), `internal/schema`.
- Produces:
  ```go
  package java
  type cuResult struct {
      PackageName   string          // dotted package from `package ...;`, "" if default
      IsPackageInfo bool            // file base name is package-info.java
      PackageDoc    string          // Javadoc of package-info.java (only when IsPackageInfo)
      Syms          []schema.Symbol // top-level types + nested members, dotted owners
      HasMain       bool
      MainType      string          // simple name of the top-level type declaring main
  }
  func compilationUnit(relFile string, src []byte) (cuResult, error)
  ```
  Symbol ids are type-relative (`Bar`, `Bar.Inner`, `Bar.method`) exactly as Python class ids are module-relative. Package qualification and the entrypoint FQCN are assembled in Task 4.

**Node names:** use the canonical tree-sitter-java names listed in Task 1 Step 5, but VALIDATE each against the probe output from Task 1 and adapt if gotreesitter differs (record any deviation in a comment, as SP2 did for the missing `expression_statement`). In particular confirm: how Javadoc attaches (expected: a `block_comment` sibling node immediately preceding the declaration, NOT a child), the `modifiers` node holding access keywords as anonymous tokens plus `annotation`/`marker_annotation` named children, and the `field` names `name`/`type`/`parameters`/`body`/`type_parameters`.

- [ ] **Step 1: Add the schema field**

In `internal/schema/schema.go`, add to `Symbol` after `Decorators`:

```go
	Annotations []string `json:"annotations,omitempty"`
```

- [ ] **Step 2: Write the failing test + enrich the fixture**

Replace `internal/extract/java/testdata/proj/com/foo/Bar.java` with:

```java
// Copyright placeholder header comment.
package com.foo;

/** Bar is the primary type. */
@Deprecated
public class Bar<T> {
    private int count;
    protected String name;

    /** Builds a Bar. */
    public Bar(int count) {
        this.count = count;
    }

    /** Returns the count. */
    @Override
    public int getCount() {
        return count;
    }

    static final double RATIO = 1.5;

    interface Inner {
        void ping();
    }

    public static void main(String[] args) {
        System.out.println("hi");
    }
}

enum Color { RED, GREEN }
```

Create `internal/extract/java/symbols_test.go`:

```go
package java

import (
	"os"
	"testing"

	"goforge.dev/assayxport/internal/schema"
)

func loadCU(t *testing.T, rel string) cuResult {
	t.Helper()
	src, err := os.ReadFile("testdata/proj/" + rel)
	if err != nil {
		t.Fatal(err)
	}
	res, err := compilationUnit(rel, src)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func symByID(syms []schema.Symbol, id string) *schema.Symbol {
	for i := range syms {
		if syms[i].ID == id {
			return &syms[i]
		}
	}
	return nil
}

func TestCompilationUnitPackage(t *testing.T) {
	res := loadCU(t, "com/foo/Bar.java")
	if res.PackageName != "com.foo" {
		t.Fatalf("PackageName = %q, want com.foo", res.PackageName)
	}
}

func TestCompilationUnitTypesAndMembers(t *testing.T) {
	res := loadCU(t, "com/foo/Bar.java")

	bar := symByID(res.Syms, "Bar")
	if bar == nil || bar.Kind != "type" || bar.TypeKind != "class" {
		t.Fatalf("Bar = %+v", bar)
	}
	if bar.Visibility != "public" || bar.VisibilityIdiom != "access-modifier" {
		t.Fatalf("Bar visibility = %q/%q", bar.Visibility, bar.VisibilityIdiom)
	}
	if len(bar.Annotations) != 1 || bar.Annotations[0] != "Deprecated" {
		t.Fatalf("Bar annotations = %v", bar.Annotations)
	}
	if bar.Signature == nil || len(bar.Signature.TypeParams) != 1 || bar.Signature.TypeParams[0].Name != "T" {
		t.Fatalf("Bar type params = %+v", bar.Signature)
	}
	if bar.Doc.Raw == "" || bar.Doc.Format != "javadoc" {
		t.Fatalf("Bar doc = %+v", bar.Doc)
	}

	ctor := symByID(res.Syms, "Bar.Bar")
	if ctor == nil || ctor.Kind != "constructor" {
		t.Fatalf("constructor = %+v", ctor)
	}

	get := symByID(res.Syms, "Bar.getCount")
	if get == nil || get.Kind != "method" || get.Visibility != "public" {
		t.Fatalf("getCount = %+v", get)
	}
	if len(get.Annotations) != 1 || get.Annotations[0] != "Override" {
		t.Fatalf("getCount annotations = %v", get.Annotations)
	}

	count := symByID(res.Syms, "Bar.count")
	if count == nil || count.Kind != "field" || count.Visibility != "private" || count.Type != "int" {
		t.Fatalf("count = %+v", count)
	}

	ratio := symByID(res.Syms, "Bar.RATIO")
	if ratio == nil || ratio.Visibility != "package-private" {
		t.Fatalf("RATIO = %+v (want package-private)", ratio)
	}

	inner := symByID(res.Syms, "Bar.Inner")
	if inner == nil || inner.TypeKind != "interface" || inner.Owner != "Bar" {
		t.Fatalf("Inner = %+v", inner)
	}
	ping := symByID(res.Syms, "Bar.Inner.ping")
	if ping == nil || ping.Kind != "method" || ping.Owner != "Bar.Inner" {
		t.Fatalf("ping = %+v", ping)
	}

	color := symByID(res.Syms, "Color")
	if color == nil || color.TypeKind != "enum" {
		t.Fatalf("Color = %+v", color)
	}
	red := symByID(res.Syms, "Color.RED")
	if red == nil || red.Kind != "enum-constant" {
		t.Fatalf("RED = %+v", red)
	}
}

func TestCompilationUnitMain(t *testing.T) {
	res := loadCU(t, "com/foo/Bar.java")
	if !res.HasMain || res.MainType != "Bar" {
		t.Fatalf("HasMain=%v MainType=%q", res.HasMain, res.MainType)
	}
	m := symByID(res.Syms, "Bar.main")
	if m == nil || m.Kind != "method" {
		t.Fatalf("main symbol = %+v", m)
	}
	static := false
	if m != nil && m.Signature != nil {
		for _, mod := range m.Signature.Modifiers {
			if mod == "static" {
				static = true
			}
		}
	}
	if !static {
		t.Fatalf("main missing static modifier: %+v", m.Signature)
	}
}
```

- [ ] **Step 3: Run it and watch it fail**

Run: `GOTOOLCHAIN=local go test ./internal/extract/java/ -run TestCompilationUnit`
Expected: FAIL - `undefined: compilationUnit`.

- [ ] **Step 4: Implement `symbols.go`**

Create `internal/extract/java/symbols.go`. Implement the following, validating node/field names against the Task 1 probe and adapting where gotreesitter deviates. The design:

- `compilationUnit(relFile, src)` parses with `ts.Parse(ts.Java, src)`, walks the `program` root's named children: a `package_declaration` sets `PackageName` (its scoped identifier text, dropping the trailing `;`); a `block_comment` starting with `/**` becomes the pending Javadoc for the next declaration; each type declaration (`class_declaration`/`interface_declaration`/`enum_declaration`/`record_declaration`/`annotation_type_declaration`) produces a type symbol plus its members via `typeSymbols(node, ownerPrefix, ...)`. `IsPackageInfo` = `filepath.Base(relFile) == "package-info.java"`; when true, the leading Javadoc becomes `PackageDoc`.
- `typeSymbols(node, ownerPrefix, src, relFile, pendingDoc)` builds the type symbol (`kind:"type"`, `type_kind` from the node type via `typeKind()`, dotted id from `ownerPrefix`, `owner=ownerPrefix`, visibility + modifiers + annotations from the `modifiers` child, `doc` from `pendingDoc`, generic `type_params` from the `type_parameters` child if present), then walks the type's `body` child. Body walking keeps its own `pendingDoc` (a preceding `block_comment`); on each member it dispatches: `method_declaration` -> method symbol; `constructor_declaration` -> constructor symbol (id `Owner.SimpleTypeName`, kind `constructor`); `field_declaration` -> one field symbol per `variable_declarator` (kind `field`, `Type` from the `type` field text); `enum_constant` -> enum-constant symbol; nested type declarations -> recurse with the new dotted owner. Members are owned by the type's fully qualified id.
- Helpers:
  - `typeKind(nodeType string) string`: maps `class_declaration`->`class`, `interface_declaration`->`interface`, `enum_declaration`->`enum`, `record_declaration`->`record`, `annotation_type_declaration`->`annotation`.
  - `modifiersChild(node) ts.Node`: returns the `modifiers` named child (search children by type), or a null Node.
  - `javaVisibility(mods ts.Node, src) string`: `public`/`protected`/`private` if the corresponding keyword word is present in the modifiers text, else `package-private`. Use word matching on `strings.Fields(mods.Content(src))` so annotations in the modifiers text do not false-match.
  - `javaModifiers(mods, src) []string`: the subset of `{static, final, abstract, default, synchronized, native}` present, in that fixed order (deterministic).
  - `annotationNames(mods, src) []string`: iterate the modifiers node's named children of type `annotation` or `marker_annotation`; for each, take the `name` field text (drop arguments), in source order.
  - `javadocText(comment ts.Node, src) string`: strip a leading `/**`, trailing `*/`, and per-line leading `*` and surrounding whitespace; return "" if the comment does not start with `/**`.
  - `paramList(node, src) []schema.Param`: from the `parameters` (`formal_parameters`) child, one `Param` per `formal_parameter`/`spread_parameter` using its `type` field text and `name` field text.
  - `returnType(node, src) []schema.Param`: from the method's `type` field text; empty slice for `void` and for constructors.
  - `typeParams(node, src) []schema.TypeParam`: from the `type_parameters` child, one entry per `type_parameter` (Name from its identifier, Constraint from any bound text as written; empty if none).
  - `isMainMethod(name string, mods []string, params []schema.Param, ret []schema.Param) bool`: `name=="main"`, `static` in mods, `public` visibility (checked by caller), exactly one param whose type text is `String[]` or `String...`, and `void` return (empty ret). Set `HasMain` + `MainType` at the top-level-type layer only (main inside a nested type does not set the unit entrypoint in SP3; note this assumption in a comment).
  - `firstLineLocation` etc. reuse `ts.Node` positions; build `schema.Location{File: relFile, Line: node.StartLine(), Col: node.StartCol(), EndLine: node.EndLine()}`.
- Every symbol gets `Complexity: schema.DeferredComplexity()`, `VisibilityIdiom: "access-modifier"`. Generic type declarations get a `Signature{TypeParams: ...}` with empty `Params`/`Returns`; non-generic types get a nil `Signature`.

Write the code in full following this design. Keep functions small and node-name changes localized so a future grammar bump is a one-line edit.

- [ ] **Step 5: Run it and watch it pass**

Run: `GOTOOLCHAIN=local go test ./internal/extract/java/ -run TestCompilationUnit`
Expected: PASS. Then `GOTOOLCHAIN=local go test ./internal/schema/` to confirm the schema field addition compiles.

- [ ] **Step 6: Vet + commit**

Run: `GOTOOLCHAIN=local go test ./internal/extract/java/ ./internal/schema/ && GOTOOLCHAIN=local go vet ./...`
Expected: PASS; vet clean.

```bash
git add internal/schema/schema.go internal/extract/java/
git commit -m "feat(java): compilation-unit symbol extraction (types, members, nesting, annotations, javadoc, main)"
```

---

### Task 4: Java units (module + package) + Extractor + golden

**Files:**
- Create: `internal/extract/java/java.go`
- Create: `internal/extract/java/java_test.go`
- Create: `internal/extract/java/testdata/sample.golden.json` (generated)
- Modify: `internal/extract/java/testdata/proj/**` (add a sub-package + package-info.java)

**Interfaces:**
- Consumes: `discover` (Task 2), `compilationUnit` (Task 3), `internal/schema`, `internal/emit` (for the golden).
- Produces:
  ```go
  package java
  type Extractor struct{}
  func New() *Extractor
  func (*Extractor) Language() string          // "java"
  func (*Extractor) Extract(root string) ([]schema.Package, error)
  ```
  One `module` unit per `.java` (id = `<pkg>.<fileStem>`, or `<fileStem>` in the default package); one `package` unit per distinct `package` declaration (id = the dotted package name), with `members` = ids of direct child modules + direct child sub-packages (one extra dotted segment), sorted. A module whose top-level type declares `public static void main` gets unit-level `IsEntrypoint` + `Invocation{Kind:"class", How:"java <FQCN>"}` where `<FQCN>` = `<pkg>.<MainType>` (just `<MainType>` in the default package); the `main` method symbol also gets symbol-level entrypoint with the same invocation.

- [ ] **Step 1: Add the failing test**

Add a sub-package and a `package-info.java` to the fixture:
- `internal/extract/java/testdata/proj/com/foo/package-info.java`:
  ```java
  /** The com.foo package. */
  package com.foo;
  ```
- `internal/extract/java/testdata/proj/com/foo/sub/Leaf.java`:
  ```java
  package com.foo.sub;
  public class Leaf {}
  ```

Create `internal/extract/java/java_test.go`:

```go
package java

import (
	"flag"
	"os"
	"testing"

	"goforge.dev/assayxport/internal/emit"
	"goforge.dev/assayxport/internal/schema"
)

var update = flag.Bool("update", false, "regenerate golden files")

func pkgByID(ps []schema.Package, id string) *schema.Package {
	for i := range ps {
		if ps[i].ID == id {
			return &ps[i]
		}
	}
	return nil
}

func TestExtractUnits(t *testing.T) {
	ps, err := New().Extract("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}

	pkg := pkgByID(ps, "com.foo")
	if pkg == nil || pkg.Level != "package" {
		t.Fatalf("com.foo unit = %+v", pkg)
	}
	if pkg.Doc != "The com.foo package." {
		t.Fatalf("com.foo doc = %q", pkg.Doc)
	}
	hasMod, hasSub := false, false
	for _, m := range pkg.Members {
		if m == "com.foo.Bar" {
			hasMod = true
		}
		if m == "com.foo.sub" {
			hasSub = true
		}
	}
	if !hasMod || !hasSub {
		t.Fatalf("com.foo members = %v, want com.foo.Bar + com.foo.sub", pkg.Members)
	}

	mod := pkgByID(ps, "com.foo.Bar")
	if mod == nil || mod.Level != "module" {
		t.Fatalf("com.foo.Bar unit = %+v", mod)
	}
	if !mod.IsEntrypoint || mod.Invocation == nil || mod.Invocation.How != "java com.foo.Bar" {
		t.Fatalf("com.foo.Bar entrypoint = %v / %+v", mod.IsEntrypoint, mod.Invocation)
	}
}

func TestGolden(t *testing.T) {
	ps, err := New().Extract("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}
	idx, shards := emit.Manifest(ps, "", []string{"java"})
	got, err := emit.Combined(idx, shards)
	if err != nil {
		t.Fatal(err)
	}
	const golden = "testdata/sample.golden.json"
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("golden mismatch; run with -update")
	}
}

func TestDoubleRunByteIdentical(t *testing.T) {
	run := func() []byte {
		ps, err := New().Extract("testdata/proj")
		if err != nil {
			t.Fatal(err)
		}
		idx, shards := emit.Manifest(ps, "", []string{"java"})
		b, err := emit.Combined(idx, shards)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if string(run()) != string(run()) {
		t.Fatal("extract+emit is not byte-identical across runs")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `GOTOOLCHAIN=local go test ./internal/extract/java/ -run TestExtractUnits`
Expected: FAIL - `undefined: New`.

- [ ] **Step 3: Implement `java.go`**

Create `internal/extract/java/java.go`:

```go
// Package java implements the assayxport Extractor for Java source trees. It
// produces one schema.Package per .java compilation unit (level "module") and
// one per Java package declaration (level "package"). Packages are keyed by the
// `package` declaration, not by directory, so ids never embed host paths.
package java

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"goforge.dev/assayxport/internal/schema"
)

// Extractor implements extract.Extractor for Java.
type Extractor struct{}

// New returns a new Java Extractor.
func New() *Extractor { return &Extractor{} }

// Language returns "java".
func (*Extractor) Language() string { return "java" }

// Extract discovers all .java files under root and returns one module unit per
// file and one package unit per package declaration, sorted by ID.
func (*Extractor) Extract(root string) ([]schema.Package, error) {
	files, err := discover(root)
	if err != nil {
		return nil, err
	}

	moduleUnits := make(map[string]schema.Package)  // moduleID -> Package
	packageUnits := make(map[string]schema.Package) // packageID -> Package
	// pkgDir records a representative directory per package for its Path, and
	// pkgHasInfo marks packages whose Path came from package-info.java (which
	// wins over a plain compilation unit's directory).
	pkgDir := make(map[string]string)
	pkgHasInfo := make(map[string]bool)

	for _, f := range files {
		src, rerr := os.ReadFile(f.Abs)
		if rerr != nil {
			return nil, rerr
		}
		res, cerr := compilationUnit(f.Rel, src)
		if cerr != nil {
			return nil, cerr
		}

		stem := strings.TrimSuffix(filepath.Base(f.Rel), ".java")
		dir := filepath.ToSlash(filepath.Dir(f.Rel))
		if dir == "." {
			dir = ""
		}

		// package-info.java carries no module symbols; it supplies the package
		// doc and a preferred package Path.
		if res.IsPackageInfo {
			if res.PackageName != "" {
				pkg := packageUnits[res.PackageName]
				pkg.ID = res.PackageName
				pkg.Language = "java"
				pkg.Level = "package"
				pkg.Name = lastSegment(res.PackageName)
				pkg.Doc = res.PackageDoc
				packageUnits[res.PackageName] = pkg
				pkgDir[res.PackageName] = dir
				pkgHasInfo[res.PackageName] = true
			}
			continue
		}

		// Module id and entrypoint FQCN.
		moduleID := stem
		if res.PackageName != "" {
			moduleID = res.PackageName + "." + stem
		}
		mod := schema.Package{
			ID:       moduleID,
			Language: "java",
			Path:     f.Rel,
			Name:     stem,
			Level:    "module",
			Symbols:  res.Syms,
		}
		if res.HasMain {
			fqcn := res.MainType
			if res.PackageName != "" {
				fqcn = res.PackageName + "." + res.MainType
			}
			inv := &schema.Invocation{Kind: "class", How: "java " + fqcn}
			mod.IsEntrypoint = true
			mod.Invocation = inv
			for i := range mod.Symbols {
				s := &mod.Symbols[i]
				if s.Kind == "method" && s.Name == "main" && s.Owner == res.MainType {
					s.IsEntrypoint = true
					s.Invocation = inv
				}
			}
		}
		moduleUnits[moduleID] = mod

		// Ensure a package unit exists for this file's package (unless default
		// package). package-info.java Path wins; otherwise first file's dir.
		if res.PackageName != "" {
			if _, ok := packageUnits[res.PackageName]; !ok {
				packageUnits[res.PackageName] = schema.Package{
					ID:       res.PackageName,
					Language: "java",
					Level:    "package",
					Name:     lastSegment(res.PackageName),
				}
			}
			if !pkgHasInfo[res.PackageName] {
				if _, ok := pkgDir[res.PackageName]; !ok {
					pkgDir[res.PackageName] = dir // files are sorted, so this is the first
				}
			}
		}
	}

	// Assign package Paths and Members.
	memberLists := make(map[string][]string, len(packageUnits))
	for pkgID := range packageUnits {
		var members []string
		for modID := range moduleUnits {
			if isDirectChild(pkgID, modID) {
				members = append(members, modID)
			}
		}
		for subID := range packageUnits {
			if subID != pkgID && isDirectChild(pkgID, subID) {
				members = append(members, subID)
			}
		}
		sort.Strings(members)
		memberLists[pkgID] = members
	}
	for pkgID, members := range memberLists {
		pkg := packageUnits[pkgID]
		pkg.Members = members
		pkg.Path = pkgDir[pkgID]
		packageUnits[pkgID] = pkg
	}

	out := make([]schema.Package, 0, len(moduleUnits)+len(packageUnits))
	for _, p := range moduleUnits {
		out = append(out, p)
	}
	for _, p := range packageUnits {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// isDirectChild reports whether childID is a direct child of parentID in dotted
// terms: parentID is a proper prefix and exactly one more segment follows.
func isDirectChild(parentID, childID string) bool {
	if parentID == "" {
		return childID != "" && !strings.Contains(childID, ".")
	}
	prefix := parentID + "."
	if !strings.HasPrefix(childID, prefix) {
		return false
	}
	rest := childID[len(prefix):]
	return rest != "" && !strings.Contains(rest, ".")
}

// lastSegment returns the last dotted segment, or the whole string if none.
func lastSegment(dotted string) string {
	if i := strings.LastIndex(dotted, "."); i >= 0 {
		return dotted[i+1:]
	}
	return dotted
}
```

Note the member-rollup uses the same "collect into `memberLists` then apply" pattern SP2 adopted so the code never writes `packageUnits` while ranging it.

- [ ] **Step 4: Generate and verify the golden**

```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
GOTOOLCHAIN=local go test ./internal/extract/java/ -run TestGolden -update
GOTOOLCHAIN=local go test ./internal/extract/java/ -run TestGolden
```
Inspect `testdata/sample.golden.json`: confirm `level` values (`module`/`package`), the `com.foo` package `members` listing `com.foo.Bar` + `com.foo.sub`, the `com.foo.Bar` module `is_entrypoint` + `"how": "java com.foo.Bar"`, 4-way `visibility` values, `annotations`, `type_kind` values (`class`/`interface`/`enum`), and a `constructor`/`enum-constant` kind.

- [ ] **Step 5: Full package test + vet + commit**

Run: `GOTOOLCHAIN=local go test ./internal/extract/java/ && GOTOOLCHAIN=local go vet ./...`
Expected: PASS; vet clean.

```bash
git add internal/extract/java/
git commit -m "feat(java): module+package units, package-by-declaration, entrypoints, golden"
```

---

### Task 5: Register Java in the polyglot registry

**Files:**
- Modify: `internal/extract/registry/registry.go`
- Modify: `internal/extract/registry/registry_test.go`
- Modify: `cmd/assayxport/testdata/mixed/` (add a `.java` file)

**Interfaces:**
- Consumes: `java.New()` (Task 4).
- Produces: `registry.All()` returns `[go, java, python]`; `Select(["java"])` works; `Run(All(), mixed)` reports `["go","java","python"]`.

- [ ] **Step 1: Extend the failing test + fixture**

Add `cmd/assayxport/testdata/mixed/Widget.java`:

```java
package mixed;
public class Widget {
    public int size() { return 0; }
}
```

In `internal/extract/registry/registry_test.go`, update `TestRunPolyglotMerge` to expect three languages (replace the existing two-language assertion):

```go
func TestRunPolyglotMerge(t *testing.T) {
	pkgs, langs, err := Run(All(), mixedFixture)
	if err != nil {
		t.Fatalf("Run(All(), mixed): %v", err)
	}
	if len(langs) != 3 || langs[0] != "go" || langs[1] != "java" || langs[2] != "python" {
		t.Fatalf("languages = %v, want [go java python]", langs)
	}
	seen := map[string]bool{}
	for _, p := range pkgs {
		seen[p.Language] = true
	}
	if !seen["go"] || !seen["java"] || !seen["python"] {
		t.Fatalf("merged units missing a language: %v", seen)
	}
}
```

Add a Java-subset test:

```go
func TestRunSelectJavaSubset(t *testing.T) {
	exts, err := Select([]string{"java"})
	if err != nil {
		t.Fatal(err)
	}
	pkgs, langs, err := Run(exts, mixedFixture)
	if err != nil {
		t.Fatalf("Run(java, mixed): %v", err)
	}
	if len(langs) != 1 || langs[0] != "java" {
		t.Fatalf("languages = %v, want [java]", langs)
	}
	for _, p := range pkgs {
		if p.Language != "java" {
			t.Fatalf("subset produced non-java unit %q (%s)", p.ID, p.Language)
		}
	}
	if len(pkgs) == 0 {
		t.Fatal("expected at least one java unit from mixed fixture")
	}
}
```

Update `TestAllRegistered` to also require `java`:

```go
	if !langs["go"] || !langs["python"] || !langs["java"] {
		t.Fatalf("registry missing go/python/java: %v", langs)
	}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `GOTOOLCHAIN=local go test ./internal/extract/registry/ -run 'TestRun|TestAll'`
Expected: FAIL - languages list is `[go python]`, missing `java` (and the Java subset test errors on the unregistered language).

- [ ] **Step 3: Register the Java extractor**

In `internal/extract/registry/registry.go`, add the import and extend `All()`:

```go
	"goforge.dev/assayxport/internal/extract/golang"
	"goforge.dev/assayxport/internal/extract/java"
	"goforge.dev/assayxport/internal/extract/python"
```

```go
// All returns every registered extractor (go, java, python), in a stable order.
func All() []extract.Extractor {
	return []extract.Extractor{golang.New(), java.New(), python.New()}
}
```

Update the doc comment on `All` to say `go, java, python`.

- [ ] **Step 4: Run it and watch it pass**

Run: `GOTOOLCHAIN=local go test ./internal/extract/registry/`
Expected: PASS.

- [ ] **Step 5: End-to-end smoke via the binary**

```bash
cd /home/brainfuel/matt/goforge.dev/assayxport
GOTOOLCHAIN=local go run ./cmd/assayxport scan cmd/assayxport/testdata/mixed --lang java --stdout | grep -q '"language": "java"' && echo JAVA_OK
GOTOOLCHAIN=local go run ./cmd/assayxport scan cmd/assayxport/testdata/mixed --stdout | grep -q '"java"' && echo LANGS_OK
```
Expected: `JAVA_OK` and `LANGS_OK`.

- [ ] **Step 6: Full suite + vet + commit**

Run: `GOTOOLCHAIN=local go test ./... && GOTOOLCHAIN=local go vet ./...`
Expected: all PASS; vet clean.

```bash
git add internal/extract/registry/ cmd/assayxport/testdata/mixed/
git commit -m "feat(java): register java extractor in polyglot dispatch"
```

---

### Task 6: Third-party attribution + README

**Files:**
- Modify: `NOTICE`
- Modify: `README.md`

**Interfaces:**
- Consumes: nothing code-level.
- Produces: MIT attribution for the Java grammar; README documents Java support.

- [ ] **Step 1: Attribute tree-sitter-java in NOTICE**

Append a `tree-sitter-java (grammar)` section to `NOTICE`, parallel to the existing `tree-sitter-python (grammar)` block: MIT, copyright the tree-sitter authors (Max Brunsfeld and contributors), noting the grammar is embedded in the already-attributed `gotreesitter` module (blob `grammars/grammar_blobs/java.bin`, not vendored in this repo), decoded at runtime, ABI 15. Reproduce the MIT permission + warranty text as the other grammar blocks do. Do NOT use em/en-dashes.

- [ ] **Step 2: Update the README**

In `README.md`, add Java to the supported-languages line and the Languages section (Go native; Python + Java syntactic via tree-sitter). Note `--lang java` is now valid and the polyglot manifest covers Go, Python, and Java together.

- [ ] **Step 3: Confirm no dashes**

Run: `grep -rnP '[\x{2013}\x{2014}]' --include=*.go --include=*.md internal/ NOTICE README.md`
Expected: no matches.

- [ ] **Step 4: Final gate + commit**

Run: `GOTOOLCHAIN=local go test ./... && GOTOOLCHAIN=local go vet ./...`
Expected: all PASS; vet clean.

```bash
git add NOTICE README.md
git commit -m "docs: attribute tree-sitter-java; document Java support"
```

---

## Self-Review

**Spec coverage:**
- `internal/ts` Java grammar + probe -> Task 1. ✓
- Java extractor (discover/symbols/units) -> Tasks 2-4. ✓
- Module + package units, package-by-declaration, members rollup -> Task 4. ✓
- Type kinds (class/interface/enum/record/annotation), members (method/constructor/field/enum-constant), nested types dotted owners -> Task 3. ✓
- 4-way visibility + `visibility_idiom="access-modifier"`; annotations; Javadoc; generics `type_params`; modifiers -> Task 3. ✓
- Entrypoint = `public static void main` -> module `is_entrypoint` + `java <FQCN>` + `main` symbol -> Tasks 3 (detection) + 4 (FQCN + stamping). ✓
- Schema addition `annotations` (additive, version "1") -> Task 3. ✓
- Registry + polyglot dispatch; `--lang java` valid -> Task 5. ✓
- Determinism (sorted units/members/languages, no host data via declaration-keyed ids, double-run) -> Tasks 2 (sorted discover), 4 (golden + double-run). ✓
- Licensing/NOTICE for the Java grammar -> Task 6. ✓
- Out of scope (throws, annotation args, big-O, Javadoc tags, other JVM langs) -> not present. ✓

**Placeholder scan:** No TBD/TODO. Task 3 gives a full design + node-name list + probe-and-adapt instruction (the only part not verbatim-coded is the exact node-name gluing, which cannot be pinned pre-probe); every other code step carries complete code + commands + expected output.

**Type/name consistency:** `ts.Java` (Task 1) used by `compilationUnit` (Task 3). `javaFile{Abs,Rel}` (Task 2) consumed in `java.go` (Task 4). `cuResult{PackageName, IsPackageInfo, PackageDoc, Syms, HasMain, MainType}` (Task 3) consumed by `Extract` (Task 4). `New()`/`Language()`/`Extract` match `extract.Extractor` and the registry call in Task 5. `Symbol.Annotations` added once (Task 3) and populated by the Java extractor only. `isDirectChild`/`lastSegment` are defined in `java.go` (Task 4) and used only there; they intentionally mirror the Python extractor's helpers but are package-local (no cross-package import), consistent with SP2 keeping per-language helpers private.

**Cross-task note:** Task 3 adds `Symbol.Annotations` to `internal/schema/schema.go`; it is the only schema edit in SP3. The main-method entrypoint is detected in Task 3 (`HasMain`/`MainType`) but the FQCN and symbol stamping are assembled in Task 4 where the package name is known; the `main` symbol is matched by `Kind=="method" && Name=="main" && Owner==MainType`, so `MainType` must be the top-level type's simple name (documented in Task 3).
