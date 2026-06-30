package golang

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"goforge.dev/assayxport/internal/schema"
	"golang.org/x/tools/go/packages"
)

// typeQualifier renders package references as their import path for stable,
// machine-independent type strings.
func typeQualifier(p *types.Package) string { return p.Path() }

func typeString(t types.Type) string { return types.TypeString(t, typeQualifier) }

// typeStringLocal renders a type without qualifying names from currentPkg, so
// receiver types appear as "*Accumulator" rather than "*example.com/pkg.Accumulator".
func typeStringLocal(t types.Type, currentPkg *types.Package) string {
	return types.TypeString(t, func(p *types.Package) string {
		if p == currentPkg {
			return ""
		}
		return p.Path()
	})
}

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
