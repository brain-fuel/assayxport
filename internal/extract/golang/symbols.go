package golang

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"goforge.dev/assayxport/internal/complexity"
	"goforge.dev/assayxport/internal/schema"
	"golang.org/x/tools/go/packages"
)

// typeStringLocal renders a type without qualifying names from currentPkg, so
// same-package types appear as "*Accumulator" rather than
// "*example.com/pkg.Accumulator", while cross-package types are fully
// import-path-qualified for stable, machine-independent strings.
func typeStringLocal(t types.Type, currentPkg *types.Package) string {
	return types.TypeString(t, func(p *types.Package) string {
		if p == currentPkg {
			return ""
		}
		return p.Path()
	})
}

// extractSymbols walks one package and returns all symbols in source order.
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
	} else {
		switch underlying.(type) {
		case *types.Struct:
			tk = "struct"
		case *types.Interface:
			tk = "interface"
		}
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
		Underlying:      typeStringLocal(underlying, p.Types),
	}
	// Capture type parameters of a generic type declaration
	// (e.g. type Set[T comparable] ...), mirroring generic funcs/methods.
	if named, ok := obj.Type().(*types.Named); ok {
		if tps := typeParams(named.TypeParams(), p.Types); len(tps) > 0 {
			sym.Signature = &schema.Signature{TypeParams: tps}
		}
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
				out = append(out, fieldSymbol(p, field, f.Name(), typeStringLocal(f.Type(), p.Types), owner, moduleDir))
				idx++
			}
			continue
		}
		for range names {
			if idx >= st.NumFields() {
				break
			}
			f := st.Field(idx)
			out = append(out, fieldSymbol(p, field, f.Name(), typeStringLocal(f.Type(), p.Types), owner, moduleDir))
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
	iface, _ := ts.Type.(*ast.InterfaceType)
	for i := 0; i < it.NumExplicitMethods(); i++ {
		m := it.ExplicitMethod(i)
		sig, _ := m.Type().(*types.Signature)
		// Use the method's own AST field position; fall back to the TypeSpec
		// for embedded/promoted methods that have no direct field entry.
		loc := locationOf(p.Fset, ts, moduleDir)
		if iface != nil {
			for _, field := range iface.Methods.List {
				if len(field.Names) > 0 && field.Names[0].Name == m.Name() {
					loc = locationOf(p.Fset, field, moduleDir)
					break
				}
			}
		}
		s := schema.Symbol{
			ID:              owner + "." + m.Name(),
			Name:            m.Name(),
			Kind:            "method",
			Visibility:      visibility(m.Name()),
			VisibilityIdiom: "capitalized",
			Location:        loc,
			Owner:           owner,
			Doc:             schema.Doc{Raw: "", Format: "godoc"},
			Complexity:      schema.DeferredComplexity(),
		}
		if sig != nil {
			s.Signature = &schema.Signature{
				Params:     tupleParams(sig.Params(), p.Types),
				Returns:    tupleParams(sig.Results(), p.Types),
				TypeParams: typeParams(sig.TypeParams(), p.Types),
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
			Type:            typeStringLocal(obj.Type(), p.Types),
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
		recvType := typeStringLocal(sig.Recv().Type(), p.Types)
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
		Complexity:      complexity.Estimate(goSummary(fd)),
		Signature: &schema.Signature{
			Params:     tupleParams(sig.Params(), p.Types),
			Returns:    tupleParams(sig.Results(), p.Types),
			TypeParams: typeParams(sig.TypeParams(), p.Types),
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

func tupleParams(t *types.Tuple, pkg *types.Package) []schema.Param {
	out := make([]schema.Param, 0, t.Len())
	for i := 0; i < t.Len(); i++ {
		v := t.At(i)
		out = append(out, schema.Param{Name: v.Name(), Type: typeStringLocal(v.Type(), pkg)})
	}
	return out
}

func typeParams(tp *types.TypeParamList, pkg *types.Package) []schema.TypeParam {
	if tp == nil {
		return nil
	}
	out := make([]schema.TypeParam, 0, tp.Len())
	for i := 0; i < tp.Len(); i++ {
		p := tp.At(i)
		out = append(out, schema.TypeParam{Name: p.Obj().Name(), Constraint: typeStringLocal(p.Constraint(), pkg)})
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
