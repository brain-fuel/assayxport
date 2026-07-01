package python

import (
	"strings"

	"goforge.dev/assayxport/internal/schema"
	"goforge.dev/assayxport/internal/ts"
)

// moduleSymbols parses one module's source and returns its top-level symbols
// (with nested methods/attrs owned), the module docstring (used by Task 4 for
// the module unit's Doc), the module's __all__ set (nil if none), and whether
// the module contains an `if __name__ == "__main__":` guard.
//
// Node-name note: the backing runtime (gotreesitter) emits module- and
// block-level docstrings as bare `string` nodes rather than wrapping them in
// an `expression_statement`, and there is no `expression_statement` node in
// the tree at all. The walk below therefore treats a leading `string` as the
// docstring statement directly.
func moduleSymbols(relFile string, src []byte) (syms []schema.Symbol, moduleDoc string, allSet map[string]bool, hasMain bool, err error) {
	tree, perr := ts.Parse(ts.Python, src)
	if perr != nil {
		return nil, "", nil, false, perr
	}
	root := tree.Root()

	n := root.NamedChildCount()
	for i := 0; i < n; i++ {
		child := root.NamedChild(i)
		switch child.Type() {
		case "string":
			// Module docstring: the first statement, if it is a string.
			if i == 0 && moduleDoc == "" {
				moduleDoc = stringText(child, src)
			}
		case "expression_statement":
			// Canonical grammar wraps a bare string here; support it too.
			if inner := firstNamedString(child); !inner.IsNull() {
				if i == 0 && moduleDoc == "" {
					moduleDoc = stringText(inner, src)
				}
			}
		case "assignment":
			if set, ok := parseAll(child, src); ok {
				allSet = set
				continue
			}
			if s, ok := assignmentVariable(child, "", src, relFile); ok {
				syms = append(syms, s)
			}
		case "function_definition", "async_function_definition":
			syms = append(syms, funcSymbol(child, "", src, relFile, nil, isAsyncDef(child, src)))
		case "class_definition":
			syms = append(syms, classSymbols(child, src, relFile, nil)...)
		case "decorated_definition":
			decorators, def := unwrapDecorated(child, src)
			if def.IsNull() {
				continue
			}
			switch {
			case isFuncDef(def.Type()):
				syms = append(syms, funcSymbol(def, "", src, relFile, decorators, isAsyncDef(def, src)))
			case def.Type() == "class_definition":
				syms = append(syms, classSymbols(def, src, relFile, decorators)...)
			}
		case "if_statement":
			if isMainGuard(child, src) {
				hasMain = true
			}
		}
	}

	// Stamp InAll on module-level symbols (owner == "") once __all__ is known.
	if allSet != nil {
		for i := range syms {
			if syms[i].Owner != "" {
				continue
			}
			v := allSet[syms[i].Name]
			b := v
			syms[i].InAll = &b
		}
	}

	return syms, moduleDoc, allSet, hasMain, nil
}

// funcSymbol builds a Symbol for a function_definition. owner is the enclosing
// class name (empty for module-level functions).
func funcSymbol(node ts.Node, owner string, src []byte, relFile string, decorators []string, isAsync bool) schema.Symbol {
	name := fieldText(node, "name", src)

	kind := "function"
	if owner != "" {
		kind = "method"
	}
	if hasDecorator(decorators, "property") {
		kind = "property"
	}

	id := name
	if owner != "" {
		id = owner + "." + name
	}

	sig := &schema.Signature{
		Params:  params(node, src),
		Returns: returns(node, src),
	}
	if isAsync {
		sig.Modifiers = append(sig.Modifiers, "async")
	}

	sym := schema.Symbol{
		ID:              id,
		Name:            name,
		Kind:            kind,
		Visibility:      pyVisibility(name),
		VisibilityIdiom: "underscore",
		Location:        locationOf(node, relFile),
		Owner:           owner,
		Doc:             bodyDoc(node, src),
		Complexity:      schema.DeferredComplexity(),
		Signature:       sig,
		Decorators:      decorators,
	}
	return sym
}

// classSymbols builds the class Symbol plus symbols for its members (methods,
// properties, and class-attribute variables), all owned by the class.
func classSymbols(node ts.Node, src []byte, relFile string, decorators []string) []schema.Symbol {
	name := fieldText(node, "name", src)
	class := schema.Symbol{
		ID:              name,
		Name:            name,
		Kind:            "class",
		Visibility:      pyVisibility(name),
		VisibilityIdiom: "underscore",
		Location:        locationOf(node, relFile),
		Doc:             bodyDoc(node, src),
		Complexity:      schema.DeferredComplexity(),
		Decorators:      decorators,
	}
	out := []schema.Symbol{class}

	body, ok := node.ChildByFieldName("body")
	if !ok {
		return out
	}
	for i := 0; i < body.NamedChildCount(); i++ {
		member := body.NamedChild(i)
		switch member.Type() {
		case "function_definition", "async_function_definition":
			out = append(out, funcSymbol(member, name, src, relFile, nil, isAsyncDef(member, src)))
		case "decorated_definition":
			decos, def := unwrapDecorated(member, src)
			if isFuncDef(def.Type()) {
				out = append(out, funcSymbol(def, name, src, relFile, decos, isAsyncDef(def, src)))
			}
		case "assignment":
			if s, ok := assignmentVariable(member, name, src, relFile); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// assignmentVariable builds a variable Symbol for an assignment whose left side
// is a plain identifier. owner is the enclosing class (empty at module level).
func assignmentVariable(node ts.Node, owner string, src []byte, relFile string) (schema.Symbol, bool) {
	left, ok := node.ChildByFieldName("left")
	if !ok || left.Type() != "identifier" {
		return schema.Symbol{}, false
	}
	name := left.Content(src)
	id := name
	if owner != "" {
		id = owner + "." + name
	}
	typ := ""
	if t, ok := node.ChildByFieldName("type"); ok {
		typ = t.Content(src)
	}
	return schema.Symbol{
		ID:              id,
		Name:            name,
		Kind:            "variable",
		Visibility:      pyVisibility(name),
		VisibilityIdiom: "underscore",
		Location:        locationOf(node, relFile),
		Owner:           owner,
		Doc:             schema.Doc{},
		Complexity:      schema.DeferredComplexity(),
		Type:            typ,
	}, true
}

// parseAll returns the set of string values when the assignment defines
// __all__ = [...] / (...).
func parseAll(node ts.Node, src []byte) (map[string]bool, bool) {
	left, ok := node.ChildByFieldName("left")
	if !ok || left.Type() != "identifier" || left.Content(src) != "__all__" {
		return nil, false
	}
	right, ok := node.ChildByFieldName("right")
	if !ok {
		return nil, false
	}
	if right.Type() != "list" && right.Type() != "tuple" && right.Type() != "set" {
		return nil, false
	}
	set := map[string]bool{}
	for i := 0; i < right.NamedChildCount(); i++ {
		elem := right.NamedChild(i)
		if elem.Type() == "string" {
			set[stringText(elem, src)] = true
		}
	}
	return set, true
}

// unwrapDecorated returns the decorator names and inner definition of a
// decorated_definition node.
func unwrapDecorated(node ts.Node, src []byte) (decorators []string, def ts.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c.Type() == "decorator" {
			decorators = append(decorators, decoratorName(c, src))
		}
	}
	def, _ = node.ChildByFieldName("definition")
	return decorators, def
}

// decoratorName returns a decorator's dotted/called name without the leading @.
func decoratorName(node ts.Node, src []byte) string {
	txt := strings.TrimSpace(node.Content(src))
	txt = strings.TrimPrefix(txt, "@")
	return strings.TrimSpace(txt)
}

func hasDecorator(decorators []string, name string) bool {
	for _, d := range decorators {
		if d == name {
			return true
		}
	}
	return false
}

// isFuncDef reports whether a node is a function definition. The pinned
// gotreesitter grammar emits "function_definition" for both sync and async
// defs, but the canonical tree-sitter-python grammar uses a dedicated
// "async_function_definition" type in some versions; accept both so a future
// grammar bump cannot silently drop async functions.
func isFuncDef(t string) bool {
	return t == "function_definition" || t == "async_function_definition"
}

// isAsyncDef reports whether a function definition is declared async.
func isAsyncDef(node ts.Node, src []byte) bool {
	if node.Type() == "async_function_definition" {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(node.Content(src)), "async")
}

// isMainGuard reports whether an if_statement is `if __name__ == "__main__":`.
func isMainGuard(node ts.Node, src []byte) bool {
	cond, ok := node.ChildByFieldName("condition")
	if !ok {
		return false
	}
	norm := strings.Join(strings.Fields(cond.Content(src)), "")
	return norm == `__name__=="__main__"` || norm == `"__main__"==__name__`
}

// params extracts the parameter list from a function_definition.
func params(node ts.Node, src []byte) []schema.Param {
	pnode, ok := node.ChildByFieldName("parameters")
	if !ok {
		return nil
	}
	var out []schema.Param
	for i := 0; i < pnode.NamedChildCount(); i++ {
		c := pnode.NamedChild(i)
		out = append(out, paramFrom(c, src))
	}
	return out
}

// paramFrom parses a single parameter node's text into name/type as-written.
func paramFrom(node ts.Node, src []byte) schema.Param {
	if node.Type() == "identifier" {
		return schema.Param{Name: node.Content(src)}
	}
	txt := node.Content(src)
	// Drop any default value.
	if idx := strings.Index(txt, "="); idx >= 0 {
		txt = txt[:idx]
	}
	name, typ := txt, ""
	if idx := strings.Index(txt, ":"); idx >= 0 {
		name = txt[:idx]
		typ = strings.TrimSpace(txt[idx+1:])
	}
	return schema.Param{Name: strings.TrimSpace(name), Type: typ}
}

// returns extracts the return annotation as a single as-written result.
func returns(node ts.Node, src []byte) []schema.Param {
	rt, ok := node.ChildByFieldName("return_type")
	if !ok {
		return nil
	}
	return []schema.Param{{Type: rt.Content(src)}}
}

// bodyDoc returns the docstring (first string statement) of a definition body.
func bodyDoc(node ts.Node, src []byte) schema.Doc {
	body, ok := node.ChildByFieldName("body")
	if !ok || body.NamedChildCount() == 0 {
		return schema.Doc{}
	}
	first := body.NamedChild(0)
	if first.Type() == "string" {
		return schema.Doc{Raw: stringText(first, src), Format: "docstring"}
	}
	if first.Type() == "expression_statement" {
		if s := firstNamedString(first); !s.IsNull() {
			return schema.Doc{Raw: stringText(s, src), Format: "docstring"}
		}
	}
	return schema.Doc{}
}

// firstNamedString returns the first named child of type string, or a null node.
func firstNamedString(node ts.Node) ts.Node {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c.Type() == "string" {
			return c
		}
	}
	return ts.Node{}
}

// stringText returns the textual content of a string node, preferring its
// string_content child and otherwise trimming surrounding quotes.
func stringText(node ts.Node, src []byte) string {
	var parts []string
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c.Type() == "string_content" {
			parts = append(parts, c.Content(src))
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "")
	}
	return trimQuotes(node.Content(src))
}

func trimQuotes(s string) string {
	s = strings.TrimSpace(s)
	for _, q := range []string{`"""`, `'''`, `"`, `'`} {
		if strings.HasPrefix(s, q) && strings.HasSuffix(s, q) && len(s) >= 2*len(q) {
			return s[len(q) : len(s)-len(q)]
		}
	}
	return s
}

// pyVisibility applies the underscore rule: a leading underscore means
// unexported, except for dunder names (__x__) which are exported.
func pyVisibility(name string) string {
	if !strings.HasPrefix(name, "_") {
		return "exported"
	}
	if isDunder(name) {
		return "exported"
	}
	return "unexported"
}

func isDunder(name string) bool {
	return len(name) > 4 && strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__")
}

// fieldText returns the text of a node's field child, or "" if absent.
func fieldText(node ts.Node, field string, src []byte) string {
	if c, ok := node.ChildByFieldName(field); ok {
		return c.Content(src)
	}
	return ""
}

func locationOf(node ts.Node, relFile string) schema.Location {
	return schema.Location{
		File:    relFile,
		Line:    node.StartLine(),
		Col:     node.StartCol(),
		EndLine: node.EndLine(),
	}
}
