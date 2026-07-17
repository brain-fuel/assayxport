package typescript

import (
	"strings"

	"goforge.dev/assayxport/internal/complexity"
	"goforge.dev/assayxport/internal/schema"
	"goforge.dev/assayxport/internal/ts"
)

// moduleSymbols parses one TS/JS file and returns its top-level (and nested
// class-member) symbols. isTS is false for plain .js/.jsx, whose symbols are
// flagged untyped. moduleDoc is left empty (JSDoc lives in tree-sitter "extra"
// comment nodes the wrapper does not expose as named children).
func moduleSymbols(lang ts.Language, relFile, unitID string, src []byte, isTS bool) ([]schema.Symbol, string, error) {
	tree, err := ts.Parse(lang, src)
	if err != nil {
		return nil, "", err
	}
	root := tree.Root()
	ctx := buildFileCtx(root, src, unitID)

	var syms []schema.Symbol
	for i := 0; i < root.NamedChildCount(); i++ {
		decl, exported := unwrapExport(root.NamedChild(i))
		if decl.IsNull() {
			continue
		}
		syms = append(syms, declSymbols(decl, "", exported, relFile, unitID, src, isTS, ctx)...)
	}
	return syms, "", nil
}

// unwrapExport peels an `export ...` statement to the declaration it exports,
// reporting exported=true. `export { a }` / `export * from` re-exports carry no
// new declaration and return a null node.
func unwrapExport(n ts.Node) (ts.Node, bool) {
	if n.Type() != "export_statement" {
		return n, false
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if isDeclType(c.Type()) {
			return c, true
		}
	}
	return ts.Node{}, false
}

func isDeclType(t string) bool {
	switch t {
	case "function_declaration", "generator_function_declaration",
		"class_declaration", "abstract_class_declaration",
		"interface_declaration", "type_alias_declaration", "enum_declaration",
		"lexical_declaration", "variable_declaration", "internal_module", "module":
		return true
	}
	return false
}

// declSymbols turns one top-level declaration into symbols (a class yields the
// class plus its members). ownerPrefix is the dotted enclosing scope ("" at
// module level).
func declSymbols(decl ts.Node, ownerPrefix string, exported bool, relFile, unitID string, src []byte, isTS bool, ctx *tsFileCtx) []schema.Symbol {
	switch decl.Type() {
	case "function_declaration", "generator_function_declaration":
		return []schema.Symbol{funcSymbol(decl, ownerPrefix, exported, "function", relFile, unitID, src, isTS, ctx)}
	case "class_declaration", "abstract_class_declaration":
		return classSymbols(decl, ownerPrefix, exported, relFile, unitID, src, isTS, ctx)
	case "interface_declaration":
		return []schema.Symbol{typeSymbol(decl, ownerPrefix, exported, "interface", relFile, src, isTS)}
	case "type_alias_declaration":
		return []schema.Symbol{typeSymbol(decl, ownerPrefix, exported, "alias", relFile, src, isTS)}
	case "enum_declaration":
		return []schema.Symbol{typeSymbol(decl, ownerPrefix, exported, "enum", relFile, src, isTS)}
	case "lexical_declaration", "variable_declaration":
		return varSymbols(decl, ownerPrefix, exported, relFile, unitID, src, isTS, ctx)
	}
	return nil
}

// funcSymbol builds a function/method symbol: signature, complexity, calls, and
// the type-honesty concerns it carries.
func funcSymbol(n ts.Node, ownerPrefix string, exported bool, kind, relFile, unitID string, src []byte, isTS bool, ctx *tsFileCtx) schema.Symbol {
	name := fieldText(n, "name", src)
	sig, hadReturnType := signatureOf(n, src)
	s := schema.Symbol{
		ID:              qualify(ownerPrefix, name),
		Name:            name,
		Kind:            kind,
		Visibility:      visOf(exported, ownerPrefix),
		VisibilityIdiom: idiomOf(ownerPrefix),
		Owner:           ownerPrefix,
		Location:        locationOf(n, relFile),
		Doc:             schema.Doc{},
		Signature:       sig,
		Complexity:      complexity.Estimate(tsSummary(n, name, src)),
		Calls:           tsCalls(n, unitID, src, ctx),
		Concerns:        funcConcerns(n, sig, hadReturnType, src, isTS),
	}
	if decs := decoratorsOf(n, src); len(decs) > 0 {
		s.Decorators = decs
	}
	return s
}

// classSymbols builds the class symbol plus its methods, fields, getters/setters.
func classSymbols(n ts.Node, ownerPrefix string, exported bool, relFile, unitID string, src []byte, isTS bool, ctx *tsFileCtx) []schema.Symbol {
	name := fieldText(n, "name", src)
	id := qualify(ownerPrefix, name)
	cls := schema.Symbol{
		ID: id, Name: name, Kind: "class",
		Visibility: visOf(exported, ownerPrefix), VisibilityIdiom: idiomOf(ownerPrefix),
		Owner: ownerPrefix, Location: locationOf(n, relFile), TypeKind: "class",
		Complexity: schema.DeferredComplexity(),
	}
	if strings.HasPrefix(strings.TrimSpace(nodeHead(n, src)), "abstract") {
		cls.TypeKind = "abstract-class"
	}
	if tp := typeParamsOf(n, src); len(tp) > 0 {
		cls.Signature = &schema.Signature{TypeParams: tp}
	}
	if decs := decoratorsOf(n, src); len(decs) > 0 {
		cls.Decorators = decs
	}
	out := []schema.Symbol{cls}

	body, ok := n.ChildByFieldName("body")
	if !ok {
		return out
	}
	for i := 0; i < body.NamedChildCount(); i++ {
		m := body.NamedChild(i)
		memberExported := true // class members default public; visibility set from modifiers
		switch m.Type() {
		case "method_definition":
			mk := "method"
			switch methodKindWord(m, src) {
			case "get":
				mk = "getter"
			case "set":
				mk = "setter"
			}
			out = append(out, funcSymbol(m, id, memberExported, mk, relFile, unitID, src, isTS, ctx))
		case "public_field_definition", "field_definition":
			out = append(out, fieldSymbol(m, id, relFile, src, isTS))
		}
	}
	return out
}

// typeSymbol builds an interface / type-alias / enum symbol.
func typeSymbol(n ts.Node, ownerPrefix string, exported bool, typeKind, relFile string, src []byte, isTS bool) schema.Symbol {
	name := fieldText(n, "name", src)
	s := schema.Symbol{
		ID: qualify(ownerPrefix, name), Name: name, Kind: "type",
		Visibility: visOf(exported, ownerPrefix), VisibilityIdiom: idiomOf(ownerPrefix),
		Owner: ownerPrefix, Location: locationOf(n, relFile), TypeKind: typeKind,
		Complexity: schema.DeferredComplexity(),
	}
	if typeKind == "alias" {
		if v, ok := n.ChildByFieldName("value"); ok {
			s.Underlying = collapseWS(v.Content(src))
		}
	}
	if tp := typeParamsOf(n, src); len(tp) > 0 {
		s.Signature = &schema.Signature{TypeParams: tp}
	}
	// an alias to `any` is itself a leak.
	if hasAnyToken(s.Underlying) {
		s.Concerns = []string{"any"}
	}
	return s
}

// fieldSymbol builds a class field / property symbol.
func fieldSymbol(n ts.Node, ownerPrefix, relFile string, src []byte, isTS bool) schema.Symbol {
	name := fieldText(n, "name", src)
	s := schema.Symbol{
		ID: qualify(ownerPrefix, name), Name: name, Kind: "property",
		Visibility: memberVisibility(n, src), VisibilityIdiom: "access-modifier",
		Owner: ownerPrefix, Location: locationOf(n, relFile),
		Complexity: schema.DeferredComplexity(),
	}
	if ty, ok := n.ChildByFieldName("type"); ok {
		s.Type = collapseWS(strings.TrimPrefix(strings.TrimSpace(ty.Content(src)), ":"))
	} else if isTS {
		s.Concerns = append(s.Concerns, "untyped-field")
	}
	if hasAnyToken(s.Type) {
		s.Concerns = append(s.Concerns, "any")
	}
	return s
}

// varSymbols builds symbols for a const/let/var declaration. An arrow function or
// function expression bound to a name is treated as a function; other values are
// variables.
func varSymbols(decl ts.Node, ownerPrefix string, exported bool, relFile, unitID string, src []byte, isTS bool, ctx *tsFileCtx) []schema.Symbol {
	isVar := strings.HasPrefix(strings.TrimSpace(decl.Content(src)), "var")
	var out []schema.Symbol
	for i := 0; i < decl.NamedChildCount(); i++ {
		d := decl.NamedChild(i)
		if d.Type() != "variable_declarator" {
			continue
		}
		name := fieldText(d, "name", src)
		if name == "" {
			continue
		}
		val, _ := d.ChildByFieldName("value")
		if !val.IsNull() && (val.Type() == "arrow_function" || val.Type() == "function_expression") {
			s := funcSymbol(val, ownerPrefix, exported, "function", relFile, unitID, src, isTS, ctx)
			s.ID = qualify(ownerPrefix, name)
			s.Name = name
			s.Location = locationOf(d, relFile)
			if isVar {
				s.Concerns = append(s.Concerns, "var")
			}
			out = append(out, s)
			continue
		}
		v := schema.Symbol{
			ID: qualify(ownerPrefix, name), Name: name, Kind: "const",
			Visibility: visOf(exported, ownerPrefix), VisibilityIdiom: idiomOf(ownerPrefix),
			Owner: ownerPrefix, Location: locationOf(d, relFile),
			Complexity: schema.DeferredComplexity(),
		}
		if ty, ok := d.ChildByFieldName("type"); ok {
			v.Type = collapseWS(strings.TrimPrefix(strings.TrimSpace(ty.Content(src)), ":"))
		} else if isTS && exported {
			v.Concerns = append(v.Concerns, "untyped-export")
		}
		if hasAnyToken(v.Type) {
			v.Concerns = append(v.Concerns, "any")
		}
		if isVar {
			v.Concerns = append(v.Concerns, "var")
		}
		out = append(out, v)
	}
	return out
}

// ---- signatures -----------------------------------------------------------

// signatureOf builds a callable's signature and reports whether an explicit
// return type was present (absence is an untyped-return concern in TS).
func signatureOf(n ts.Node, src []byte) (*schema.Signature, bool) {
	sig := &schema.Signature{Params: paramList(n, src)}
	hadReturn := false
	if rt, ok := n.ChildByFieldName("return_type"); ok {
		sig.Returns = []schema.Param{{Type: collapseWS(strings.TrimPrefix(strings.TrimSpace(rt.Content(src)), ":"))}}
		hadReturn = true
	}
	sig.TypeParams = typeParamsOf(n, src)
	if head := nodeHead(n, src); strings.Contains(head, "async") {
		sig.Modifiers = append(sig.Modifiers, "async")
	}
	if strings.HasPrefix(strings.TrimSpace(nodeHead(n, src)), "static") {
		sig.Modifiers = append(sig.Modifiers, "static")
	}
	if len(sig.Params) == 0 && len(sig.Returns) == 0 && len(sig.TypeParams) == 0 && len(sig.Modifiers) == 0 {
		return sig, hadReturn
	}
	return sig, hadReturn
}

// paramList reads the "parameters" field into schema.Params (name + type as
// written). Object/array destructuring patterns keep their raw text as the name.
func paramList(n ts.Node, src []byte) []schema.Param {
	ps, ok := n.ChildByFieldName("parameters")
	if !ok {
		return nil
	}
	var out []schema.Param
	for i := 0; i < ps.NamedChildCount(); i++ {
		p := ps.NamedChild(i)
		var param schema.Param
		if pat, ok := p.ChildByFieldName("pattern"); ok {
			param.Name = collapseWS(pat.Content(src))
		} else {
			param.Name = collapseWS(firstLine(p.Content(src)))
		}
		if ty, ok := p.ChildByFieldName("type"); ok {
			param.Type = collapseWS(strings.TrimPrefix(strings.TrimSpace(ty.Content(src)), ":"))
		}
		out = append(out, param)
	}
	return out
}

// typeParamsOf reads a `<T extends U>` clause into TypeParams.
func typeParamsOf(n ts.Node, src []byte) []schema.TypeParam {
	tp, ok := n.ChildByFieldName("type_parameters")
	if !ok {
		return nil
	}
	var out []schema.TypeParam
	for i := 0; i < tp.NamedChildCount(); i++ {
		t := tp.NamedChild(i)
		if t.Type() != "type_parameter" {
			continue
		}
		name := collapseWS(firstLine(t.Content(src)))
		var constraint string
		if c := childOfType(t, "constraint"); !c.IsNull() {
			constraint = collapseWS(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(c.Content(src)), "extends")))
			name = collapseWS(fieldText(t, "name", src))
			if name == "" {
				name = collapseWS(firstLine(t.Content(src)))
			}
		}
		out = append(out, schema.TypeParam{Name: name, Constraint: constraint})
	}
	return out
}

// ---- helpers --------------------------------------------------------------

func qualify(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func visOf(exported bool, ownerPrefix string) string {
	if ownerPrefix != "" {
		return "public" // class-member default; overridden by memberVisibility on fields
	}
	if exported {
		return "exported"
	}
	return "unexported"
}

func idiomOf(ownerPrefix string) string {
	if ownerPrefix != "" {
		return "access-modifier"
	}
	return "export"
}

func memberVisibility(n ts.Node, src []byte) string {
	head := strings.TrimSpace(nodeHead(n, src))
	switch {
	case strings.HasPrefix(head, "private") || strings.Contains(head, "#"):
		return "private"
	case strings.HasPrefix(head, "protected"):
		return "protected"
	default:
		return "public"
	}
}

func locationOf(n ts.Node, relFile string) schema.Location {
	return schema.Location{File: relFile, Line: n.StartLine(), Col: n.StartCol(), EndLine: n.EndLine()}
}

func fieldText(n ts.Node, field string, src []byte) string {
	if c, ok := n.ChildByFieldName(field); ok {
		return c.Content(src)
	}
	return ""
}

func childOfType(n ts.Node, typ string) ts.Node {
	for i := 0; i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); c.Type() == typ {
			return c
		}
	}
	return ts.Node{}
}

// decoratorsOf collects `@Decorator` names attached to a declaration.
func decoratorsOf(n ts.Node, src []byte) []string {
	var out []string
	for i := 0; i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); c.Type() == "decorator" {
			out = append(out, strings.TrimPrefix(collapseWS(firstLine(c.Content(src))), "@"))
		}
	}
	return out
}

// methodKindWord returns "get"/"set"/"" for a method_definition (accessor).
func methodKindWord(n ts.Node, src []byte) string {
	head := strings.TrimSpace(nodeHead(n, src))
	head = strings.TrimPrefix(head, "static ")
	if strings.HasPrefix(head, "get ") {
		return "get"
	}
	if strings.HasPrefix(head, "set ") {
		return "set"
	}
	return ""
}

// nodeHead is the node's source up to its body (or the first newline) -- enough
// to read leading modifier keywords without pulling in the whole body.
func nodeHead(n ts.Node, src []byte) string {
	if b, ok := n.ChildByFieldName("body"); ok {
		full := n.Content(src)
		bodyTxt := b.Content(src)
		if i := strings.Index(full, bodyTxt); i > 0 {
			return full[:i]
		}
	}
	return firstLine(n.Content(src))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// hasAnyToken reports whether a type string uses the `any` type as a whole word.
func hasAnyToken(t string) bool {
	for _, f := range strings.FieldsFunc(t, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '$')
	}) {
		if f == "any" {
			return true
		}
	}
	return false
}
