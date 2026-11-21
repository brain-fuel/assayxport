// Package haskell implements the assayxport Extractor for Haskell source
// trees. It produces one schema.Package per .hs module (level "module").
//
// Its logic is written in LawSpec (sources: law/spec/laws, units
// assayxport.haskell.policy and assayxport.haskell.extract) and generated as
// typed Go into package law. This file is only what LawSpec cannot do: walk
// the tree, read the files, parse them with tree-sitter's Haskell grammar,
// hand the parse to the generated extraction as its Syntax, and convert the
// result to schema.Package.
package haskell

import (
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"

	law "goforge.dev/assayxport/internal/extract/haskell/law"
	"goforge.dev/assayxport/internal/schema"
	"goforge.dev/assayxport/internal/ts"
)

// When holds what `ax assay` was given for `--when`: FileLines are the lines
// of the discovered decisions file and of every `--when-file` in order,
// FlagLines every `--when` value in order (each is one line of the file's
// syntax), and whether `--when-complete` was given. The names a decisions
// file lists as undecided come from FileLines only. The zero value is the
// empty environment.
type When struct {
	FileLines []string
	FlagLines []string
	Complete  bool
}

// Lines is every binding line in precedence order: files, then flags.
func (w When) Lines() []string {
	return append(append([]string{}, w.FileLines...), w.FlagLines...)
}

// Extractor implements extract.Extractor for Haskell.
type Extractor struct {
	when     When
	warnings []string
	paste    []string
	filtered law.Filtered
}

// New returns a new Haskell Extractor.
func New() *Extractor { return &Extractor{} }

// SetWhen sets the names the manifest is filtered by: every CPP condition
// that they decide is dropped or cleared (design.md, section 6).
func (e *Extractor) SetWhen(w When) { e.when = w }

// Warnings returns what the last Extract found undecided: one line, without
// the `ax: warning: ` start, for the names still missing and one per
// condition that cannot be evaluated.
func (e *Extractor) Warnings() []string { return e.warnings }

// Paste returns what the last Extract found to put into a decisions file:
// a block for every name still missing that no decisions file lists, read off
// the manifest as extracted. They are written as they are, with no `ax: `.
func (e *Extractor) Paste() []string { return e.paste }

// InitWarnings returns the warning lines `ax when init` prints (without the
// `ax: warning: ` start): the filter's, except the three about missing names
// (law WHP-14).
func (e *Extractor) InitWarnings() []string {
	return law.LawSpecDefinitions.WhenInitWarnings(map[string]*law.LawSpecSymbol{}, e.filtered)
}

// InitNothingText returns `ax when init`'s last line, without `ax: `, where
// it finds no name to list under root and writes nothing (law WHP-14).
func (e *Extractor) InitNothingText(root string) string {
	return law.LawSpecDefinitions.WhenInitNothing(map[string]*law.LawSpecSymbol{}, root, e.filtered)
}

// Header returns the lines that start a decisions file.
func Header() []string {
	return law.LawSpecDefinitions.WhenHeader(map[string]*law.LawSpecSymbol{}, law.LawSpecUnit{})
}

// Listed returns the names that a decisions file's lines list as undecided.
func Listed(lines []string) []string {
	return law.LawSpecDefinitions.WhenListed(map[string]*law.LawSpecSymbol{}, lines)
}

// CheckWhen reports the first line of one origin (a `--when-file`'s path, or
// "--when" for the flags' values, numbered from 1) that is not a binding
// line, as the usage error text `when: <origin>:<n>: cannot read "<line>"`.
func CheckWhen(origin string, lines []string) error {
	symbols := map[string]*law.LawSpecSymbol{}
	if text, ok := law.LawSpecDefinitions.WhenEnv(symbols, origin, lines).Left(); ok {
		return fmt.Errorf("%s", text)
	}
	return nil
}

func envOf(w When) law.Env {
	symbols := map[string]*law.LawSpecSymbol{}
	env, ok := law.LawSpecDefinitions.WhenEnv(symbols, "", w.Lines()).Right()
	if !ok {
		panic("haskell: --when lines were not checked before use")
	}
	env.Complete = w.Complete
	return env
}

// SkippedPath says whether the extractor's walk does not read the file at
// rel, a slash path relative to the root: a dot-directory, dist-newstyle/ or
// dist/ above it. `ax watch` and `ax serve` follow a changed .hs file exactly
// when this is false (law HSX-45).
func SkippedPath(rel string) bool {
	return law.LawSpecDefinitions.SkippedPath(map[string]*law.LawSpecSymbol{}, rel)
}

// Language returns "haskell".
func (*Extractor) Language() string { return "haskell" }

// Extract discovers every .hs file under root and returns one package per
// module, sorted by ID. It skips dot-directories (as the Python extractor
// does) and the build directories dist-newstyle/ and dist/; .lhs and
// .hs-boot are out of scope. A file or directory that cannot be read fails
// the extraction, as the Python extractor's does.
func (e *Extractor) Extract(root string) ([]schema.Package, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	// A root that is a symbolic link to a directory is read as that directory
	// (law HSX-46): the walk does not follow a root that is a link. Only the
	// root; links inside the tree are not followed.
	if resolved, rerr := filepath.EvalSymlinks(absRoot); rerr == nil {
		absRoot = resolved
	}
	var rels []string
	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != absRoot && (strings.HasPrefix(d.Name(), ".") || d.Name() == "dist-newstyle" || d.Name() == "dist") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".hs") {
			return nil
		}
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return err
		}
		rels = append(rels, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(rels)
	symbols := map[string]*law.LawSpecSymbol{}
	var all []law.Package
	for _, rel := range rels {
		src, err := os.ReadFile(filepath.Join(absRoot, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		// CRLF as LF: under CRLF the grammar splits a multi-line `-- |` into a
		// haddock and a comment node (law HSE-7). Lines and columns are unchanged.
		text := strings.ReplaceAll(string(src), "\r\n", "\n")
		// Every branch of the CPP conditionals is read, each in a copy of the
		// source with its other branches blanked and no line moved (laws
		// HSC-1 to HSC-7); a file with no conditional is its own one copy.
		plan := law.LawSpecDefinitions.CppPlan(symbols, text)
		pks := make([][]law.Package, 0, len(plan.Copies))
		for _, copyText := range plan.Copies {
			tree, err := ts.Parse(ts.Haskell, []byte(copyText))
			if err != nil {
				return nil, err
			}
			// None when the parse lost the header (law HSX-8): read as headerless,
			// the module would be `Main` with every symbol unexported.
			pks = append(pks, law.LawSpecDefinitions.ModuleOf(symbols, rel, rootSyntax(tree.Root(), copyText)))
		}
		all = append(all, law.LawSpecDefinitions.PackagesOfCopies(symbols, plan, pks)...)
	}
	// The manifest is always filtered by the names given (none: the empty
	// environment, which decides a condition that needs no name).
	env := envOf(e.when)
	listed := Listed(e.when.FileLines)
	filtered := law.LawSpecDefinitions.AssayxportConditionsApplyWhen(symbols, env, all)
	e.filtered = filtered
	e.warnings = law.LawSpecDefinitions.WhenWarningsWith(symbols, filtered, listed)
	e.paste = law.LawSpecDefinitions.WhenPaste(symbols, env, listed, all)
	out := make([]schema.Package, 0, len(filtered.Packages))
	for _, p := range filtered.Packages {
		out = append(out, toSchema(p))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// rootSyntax is the parse as the extraction reads it: named nodes with
// their field names, 1-based positions, source text and error flags. The
// root is always a `haskell` node (law HSP-1) and its text is the whole
// file (law HSP-3). Every node's text is a slice of the one source string,
// not a copy.
func rootSyntax(n ts.Node, src string) law.Syntax {
	s := syntaxOf(n, "", src)
	if s.Kind != "haskell" {
		s = law.Syntax{Kind: "haskell", From: s.From, To: s.To, Children: []law.Syntax{s}}
	}
	s.Text = src
	return s
}

func syntaxOf(n ts.Node, field string, src string) law.Syntax {
	kids, fields := n.NamedChildrenWithFields()
	children := make([]law.Syntax, 0, len(kids))
	for i, k := range kids {
		children = append(children, syntaxOf(k, fields[i], src))
	}
	text := ""
	if a, b := n.StartByte(), n.EndByte(); a >= 0 && b <= len(src) && a <= b {
		text = src[a:b]
	}
	return law.Syntax{
		Kind:     n.Type(),
		Field:    field,
		Text:     text,
		From:     point(n.StartLine(), n.StartCol()),
		To:       point(n.EndLine(), n.EndCol()),
		IsError:  n.IsError(),
		Children: children,
	}
}

func point(line, col int) law.Point {
	return law.Point{Line: big.NewInt(int64(line)), Col: big.NewInt(int64(col))}
}

// toSchema converts the generated package to ax's schema.
func toSchema(p law.Package) schema.Package {
	inv, hasInv := p.Invocation.Value()
	out := schema.Package{
		ID: p.Id, Language: p.Language, Path: p.Path, Name: p.Name, Doc: p.Doc, Level: p.Level,
		Members: p.Members, IsEntrypoint: p.IsEntrypoint,
	}
	if hasInv {
		out.Invocation = &schema.Invocation{Kind: inv.Kind, How: inv.How}
	}
	for _, d := range p.Symbols {
		out.Symbols = append(out.Symbols, symbolOf(d))
	}
	return out
}

func symbolOf(d law.Decl) schema.Symbol {
	s := schema.Symbol{
		ID: d.Id, Name: d.Name, Kind: kindOf(d.Kind), Visibility: visibilityOf(d.Visibility),
		VisibilityIdiom: idiomOf(d.Idiom), Owner: d.Owner,
		Location: schema.Location{File: d.Location.File, Line: int(d.Location.Line.Int64()), Col: int(d.Location.Col.Int64()), EndLine: int(d.Location.EndLine.Int64())},
		Doc:          schema.Doc{Raw: d.Doc.Raw, Format: docFormatOf(d.Doc.Format)},
		IsEntrypoint: d.Entry.IsEntrypoint,
		Complexity:   schema.DeferredComplexity(),
		TypeKind:     d.Detail.TypeKind, Underlying: d.Detail.Underlying, Type: d.Detail.TypeText,
		Extends:      d.Detail.Binary.Extends,
		Implements:   d.Detail.Binary.Implements,
	}
	s.When = d.Condition
	if inv, ok := d.Entry.Invocation.Value(); ok {
		s.Invocation = &schema.Invocation{Kind: inv.Kind, How: inv.How}
	}
	if sig, ok := d.Detail.Signature.Value(); ok {
		s.Signature = &schema.Signature{Params: paramsOf(sig.Params), Returns: paramsOf(sig.Returns), TypeParams: []schema.TypeParam{}}
	}
	var calls []schema.Call
	for _, c := range d.Calls {
		call := schema.Call{Target: c.Target, Kind: callKindOf(c.Kind), Count: int(c.Count.Int64()), When: c.Condition}
		if ref, ok := c.Ref.Value(); ok {
			call.Ref = ref
		}
		calls = append(calls, call)
	}
	if len(calls) > 0 {
		// Already merged by the extraction in ax's order; DedupeCalls is the
		// schema's own canonical form, kept as the other extractors do.
		s.Calls = schema.DedupeCalls(calls)
	}
	return s
}

func paramsOf(ps []law.Param) []schema.Param {
	out := make([]schema.Param, 0, len(ps))
	for _, p := range ps {
		out = append(out, schema.Param{Name: p.Name, Type: p.TypeText})
	}
	return out
}

func kindOf(k law.SymbolKind) string {
	switch k.(type) {
	case law.SymbolKindKindType:
		return "type"
	case law.SymbolKindKindFunction:
		return "function"
	case law.SymbolKindKindMethod:
		return "method"
	case law.SymbolKindKindConstructor:
		return "constructor"
	case law.SymbolKindKindField:
		return "field"
	case law.SymbolKindKindConst:
		return "const"
	case law.SymbolKindKindClass:
		return "class"
	case law.SymbolKindKindInstance:
		return "instance"
	}
	panic("haskell: the extraction produced a kind outside its mapping")
}

func visibilityOf(v law.Visibility) string {
	if _, ok := v.(law.VisibilityExported); ok {
		return "exported"
	}
	return "unexported"
}

func idiomOf(law.VisibilityIdiom) string { return "export" }

func docFormatOf(f law.DocFormat) string {
	if _, ok := f.(law.DocFormatHaddock); ok {
		return "haddock"
	}
	return ""
}

func callKindOf(k law.CallKind) string {
	switch k.(type) {
	case law.CallKindInternal:
		return "internal"
	case law.CallKindExternal:
		return "external"
	case law.CallKindBuiltin:
		return "builtin"
	case law.CallKindDynamic:
		return "dynamic"
	}
	return "unresolved"
}
