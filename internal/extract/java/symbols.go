package java

import (
	"path/filepath"
	"strings"

	"goforge.dev/assayxport/internal/complexity"
	"goforge.dev/assayxport/internal/schema"
	"goforge.dev/assayxport/internal/ts"
)

// cuResult is the outcome of extracting one Java compilation unit. Symbol ids
// are type-relative (Bar, Bar.Inner, Bar.getCount); package qualification and
// the entrypoint FQCN are assembled later (Task 4).
type cuResult struct {
	PackageName   string          // dotted package from `package ...;`, "" if default
	IsPackageInfo bool            // file base name is package-info.java
	PackageDoc    string          // Javadoc of package-info.java (only when IsPackageInfo)
	Syms          []schema.Symbol // top-level types + nested members, dotted owners
	HasMain       bool
	MainType      string // simple name of the top-level type declaring main
}

// typeDeclTypes maps a type-declaration node type to its schema type_kind.
//
// Node names confirmed against the gotreesitter Java grammar (probe, 2026-07-01):
// class_declaration/interface_declaration/enum_declaration are emitted directly;
// record_declaration/annotation_type_declaration were not exercised by the
// fixture but use the canonical names.
var typeDeclTypes = map[string]string{
	"class_declaration":           "class",
	"interface_declaration":       "interface",
	"enum_declaration":            "enum",
	"record_declaration":          "record",
	"annotation_type_declaration": "annotation",
}

// compilationUnit parses one Java source file and returns its type-relative
// symbols plus package and entrypoint metadata.
func compilationUnit(relFile string, src []byte) (cuResult, error) {
	res := cuResult{
		IsPackageInfo: filepath.Base(relFile) == "package-info.java",
	}

	tree, err := ts.Parse(ts.Java, src)
	if err != nil {
		return cuResult{}, err
	}
	root := tree.Root()

	// pendingDoc holds a Javadoc block_comment that immediately precedes the
	// next declaration. Javadoc is a SIBLING of the declaration, not a child.
	var pendingDoc schema.Doc

	n := root.NamedChildCount()
	for i := 0; i < n; i++ {
		child := root.NamedChild(i)
		switch child.Type() {
		case "block_comment":
			if raw := javadocText(child, src); raw != "" {
				pendingDoc = schema.Doc{Raw: raw, Format: "javadoc"}
			}
		case "line_comment":
			// Header/line comments never carry Javadoc; leave pendingDoc as-is.
		case "package_declaration":
			res.PackageName = packageName(child, src)
			// In package-info.java the leading Javadoc documents the package.
			if res.IsPackageInfo && pendingDoc.Raw != "" {
				res.PackageDoc = pendingDoc.Raw
			}
			pendingDoc = schema.Doc{}
		default:
			if _, ok := typeDeclTypes[child.Type()]; ok {
				syms := typeSymbols(child, "", src, relFile, pendingDoc)
				res.Syms = append(res.Syms, syms...)

				// main detection is anchored to the top-level type only: a main
				// inside a nested type does not set the unit entrypoint in SP3.
				topName := typeName(child, src)
				for j := range syms {
					if syms[j].Owner == topName && isMainSymbol(syms[j]) {
						res.HasMain = true
						res.MainType = topName
					}
				}
			}
			// Any non-comment sibling (type declaration, import, etc.) ends the
			// scope of a pending Javadoc.
			pendingDoc = schema.Doc{}
		}
	}

	return res, nil
}

// typeSymbols builds the type symbol for a type declaration plus symbols for its
// members and nested types. ownerPrefix is the enclosing type's fully-qualified
// id ("" at the top level); it makes nested types and members carry dotted ids.
func typeSymbols(node ts.Node, ownerPrefix string, src []byte, relFile string, doc schema.Doc) []schema.Symbol {
	name := typeName(node, src)
	id := name
	if ownerPrefix != "" {
		id = ownerPrefix + "." + name
	}

	mods := modifiersChild(node)

	// A type declaration gets a Signature when it is generic or carries
	// non-access modifiers (e.g. a `static` or `final` nested class); a bare
	// non-generic, unmodified type gets a nil Signature.
	var sig *schema.Signature
	tps := typeParams(node, src)
	tmods := javaModifiers(mods, src)
	if len(tps) > 0 || len(tmods) > 0 {
		sig = &schema.Signature{Params: []schema.Param{}, Returns: []schema.Param{}, TypeParams: tps, Modifiers: tmods}
	}

	typeSym := schema.Symbol{
		ID:              id,
		Name:            name,
		Kind:            "type",
		Visibility:      javaVisibility(mods, src),
		VisibilityIdiom: "access-modifier",
		Location:        locationOf(node, relFile),
		Owner:           ownerPrefix,
		Doc:             doc,
		Complexity:      schema.DeferredComplexity(),
		Signature:       sig,
		TypeKind:        typeDeclTypes[node.Type()],
		Annotations:     annotationNames(mods, src),
	}
	out := []schema.Symbol{typeSym}

	// A record keeps its components in a "parameters" (formal_parameters) child of
	// the record_declaration, not in the body. Each component is public API (an
	// implicit accessor plus a final field), so emit each as a public field member
	// in source order.
	if node.Type() == "record_declaration" {
		out = append(out, recordComponents(node, id, src, relFile)...)
	}

	body, ok := node.ChildByFieldName("body")
	if !ok {
		return out
	}

	// Members are owned by this type's fully-qualified id.
	out = append(out, memberSymbols(bodyMembers(body), id, name, src, relFile)...)
	return out
}

// bodyMembers returns the member nodes of a type body. An enum keeps its
// non-constant members (methods, fields, constructors, nested types) in an
// `enum_body_declarations` child after the constant list; that node is flattened
// so those members are surfaced rather than silently dropped.
func bodyMembers(body ts.Node) []ts.Node {
	var members []ts.Node
	for i := 0; i < body.NamedChildCount(); i++ {
		m := body.NamedChild(i)
		if m.Type() == "enum_body_declarations" {
			for j := 0; j < m.NamedChildCount(); j++ {
				members = append(members, m.NamedChild(j))
			}
			continue
		}
		members = append(members, m)
	}
	return members
}

// childOfType returns the first named child of node with the given type, or a
// null node if none exists.
func childOfType(node ts.Node, typ string) ts.Node {
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c.Type() == typ {
			return c
		}
	}
	return ts.Node{}
}

// memberSymbols builds symbols for a slice of type-body member nodes owned by
// ownerID. typeName is the declaring type's simple name (used to name
// constructors). It threads a pending Javadoc across members.
func memberSymbols(members []ts.Node, ownerID, typeName string, src []byte, relFile string) []schema.Symbol {
	var out []schema.Symbol
	var pendingDoc schema.Doc
	for _, member := range members {
		switch member.Type() {
		case "block_comment":
			if raw := javadocText(member, src); raw != "" {
				pendingDoc = schema.Doc{Raw: raw, Format: "javadoc"}
			}
			continue
		case "line_comment":
			// a line comment does not end a pending Javadoc
			continue
		case "method_declaration":
			out = append(out, methodSymbol(member, ownerID, src, relFile, pendingDoc))
		case "constructor_declaration":
			out = append(out, constructorSymbol(member, ownerID, typeName, src, relFile, pendingDoc))
		case "field_declaration":
			out = append(out, fieldSymbols(member, ownerID, src, relFile, pendingDoc)...)
		case "enum_constant":
			out = append(out, enumConstantSymbol(member, ownerID, src, relFile, pendingDoc))
			// A constant with a class body (e.g. DOUBLE { ... }) declares members
			// of an anonymous subclass; emit them owned by the constant.
			if cb := childOfType(member, "class_body"); !cb.IsNull() {
				constName := fieldText(member, "name", src)
				out = append(out, memberSymbols(bodyMembers(cb), ownerID+"."+constName, constName, src, relFile)...)
			}
		case "annotation_type_element_declaration":
			out = append(out, annotationElementSymbol(member, ownerID, src, relFile, pendingDoc))
		default:
			if _, ok := typeDeclTypes[member.Type()]; ok {
				out = append(out, typeSymbols(member, ownerID, src, relFile, pendingDoc)...)
			}
			// Any other member (static/instance initializer, etc.) ends the
			// scope of a pending Javadoc.
		}
		pendingDoc = schema.Doc{}
	}
	return out
}

// methodSymbol builds a method Symbol. owner is the declaring type's id.
func methodSymbol(node ts.Node, owner string, src []byte, relFile string, doc schema.Doc) schema.Symbol {
	name := fieldText(node, "name", src)
	mods := modifiersChild(node)
	sig := &schema.Signature{
		Params:     paramList(node, src),
		Returns:    returnType(node, src),
		TypeParams: typeParams(node, src),
		Modifiers:  javaModifiers(mods, src),
	}
	return schema.Symbol{
		ID:              owner + "." + name,
		Name:            name,
		Kind:            "method",
		Visibility:      javaVisibility(mods, src),
		VisibilityIdiom: "access-modifier",
		Location:        locationOf(node, relFile),
		Owner:           owner,
		Doc:             doc,
		Complexity:      complexity.Estimate(javaSummary(node, src, name)),
		Signature:       sig,
		Annotations:     annotationNames(mods, src),
	}
}

// constructorSymbol builds a constructor Symbol. Its id is Owner.SimpleTypeName
// and its kind is "constructor". typeName is the declaring type's simple name.
func constructorSymbol(node ts.Node, owner, typeName string, src []byte, relFile string, doc schema.Doc) schema.Symbol {
	mods := modifiersChild(node)
	sig := &schema.Signature{
		Params:     paramList(node, src),
		Returns:    []schema.Param{},
		TypeParams: typeParams(node, src),
		Modifiers:  javaModifiers(mods, src),
	}
	return schema.Symbol{
		ID:              owner + "." + typeName,
		Name:            typeName,
		Kind:            "constructor",
		Visibility:      javaVisibility(mods, src),
		VisibilityIdiom: "access-modifier",
		Location:        locationOf(node, relFile),
		Owner:           owner,
		Doc:             doc,
		Complexity:      complexity.Estimate(javaSummary(node, src, typeName)),
		Signature:       sig,
		Annotations:     annotationNames(mods, src),
	}
}

// fieldSymbols builds one field Symbol per variable_declarator in a
// field_declaration (a single declaration can declare several names).
func fieldSymbols(node ts.Node, owner string, src []byte, relFile string, doc schema.Doc) []schema.Symbol {
	mods := modifiersChild(node)
	vis := javaVisibility(mods, src)
	annos := annotationNames(mods, src)
	typ := fieldText(node, "type", src)

	var out []schema.Symbol
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c.Type() != "variable_declarator" {
			continue
		}
		name := fieldText(c, "name", src)
		out = append(out, schema.Symbol{
			ID:              owner + "." + name,
			Name:            name,
			Kind:            "field",
			Visibility:      vis,
			VisibilityIdiom: "access-modifier",
			Location:        locationOf(c, relFile),
			Owner:           owner,
			Doc:             doc,
			Complexity:      schema.DeferredComplexity(),
			Type:            typ,
			Annotations:     annos,
		})
	}
	return out
}

// enumConstantSymbol builds an enum-constant Symbol.
func enumConstantSymbol(node ts.Node, owner string, src []byte, relFile string, doc schema.Doc) schema.Symbol {
	name := fieldText(node, "name", src)
	mods := modifiersChild(node)
	return schema.Symbol{
		ID:              owner + "." + name,
		Name:            name,
		Kind:            "enum-constant",
		Visibility:      "public", // enum constants are implicitly public.
		VisibilityIdiom: "access-modifier",
		Location:        locationOf(node, relFile),
		Owner:           owner,
		Doc:             doc,
		Complexity:      schema.DeferredComplexity(),
		Annotations:     annotationNames(mods, src),
	}
}

// recordComponents builds a public field Symbol for each component in a
// record_declaration's "parameters" (formal_parameters) child, in source order.
func recordComponents(node ts.Node, owner string, src []byte, relFile string) []schema.Symbol {
	params, ok := node.ChildByFieldName("parameters")
	if !ok {
		return nil
	}
	var out []schema.Symbol
	for i := 0; i < params.NamedChildCount(); i++ {
		c := params.NamedChild(i)
		if c.Type() != "formal_parameter" {
			continue
		}
		name := fieldText(c, "name", src)
		out = append(out, schema.Symbol{
			ID:              owner + "." + name,
			Name:            name,
			Kind:            "field",
			Visibility:      "public", // record components are public API.
			VisibilityIdiom: "access-modifier",
			Location:        locationOf(c, relFile),
			Owner:           owner,
			Complexity:      schema.DeferredComplexity(),
			Type:            fieldText(c, "type", src),
		})
	}
	return out
}

// annotationElementSymbol builds a method Symbol for an
// annotation_type_element_declaration (an element of an @interface). The node
// exposes "name" and "type" fields (probe, 2026-07-01); elements take no
// parameters and are implicitly public.
func annotationElementSymbol(node ts.Node, owner string, src []byte, relFile string, doc schema.Doc) schema.Symbol {
	name := fieldText(node, "name", src)
	mods := modifiersChild(node)
	sig := &schema.Signature{
		Params:     []schema.Param{},
		Returns:    returnType(node, src),
		TypeParams: nil,
		Modifiers:  javaModifiers(mods, src),
	}
	return schema.Symbol{
		ID:              owner + "." + name,
		Name:            name,
		Kind:            "method",
		Visibility:      "public", // annotation elements are implicitly public.
		VisibilityIdiom: "access-modifier",
		Location:        locationOf(node, relFile),
		Owner:           owner,
		Doc:             doc,
		Complexity:      schema.DeferredComplexity(),
		Signature:       sig,
		Annotations:     annotationNames(mods, src),
	}
}

// typeName returns a type declaration's simple name via its "name" field.
func typeName(node ts.Node, src []byte) string {
	return fieldText(node, "name", src)
}

// modifiersChild returns the "modifiers" named child of a declaration, or a null
// Node when there is none (e.g. package-private interface methods).
func modifiersChild(node ts.Node) ts.Node {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c.Type() == "modifiers" {
			return c
		}
	}
	return ts.Node{}
}

// javaVisibility returns the 4-way visibility. Access keywords appear as bare
// words in the modifiers text; word matching prevents annotation text (e.g.
// @PublicApi) from false-matching.
func javaVisibility(mods ts.Node, src []byte) string {
	if mods.IsNull() {
		return "package-private"
	}
	for _, w := range strings.Fields(mods.Content(src)) {
		switch w {
		case "public":
			return "public"
		case "protected":
			return "protected"
		case "private":
			return "private"
		}
	}
	return "package-private"
}

// javaModifierOrder is the fixed, deterministic ordering of recognized
// non-access modifiers.
var javaModifierOrder = []string{"static", "final", "abstract", "default", "synchronized", "native"}

// javaModifiers returns the recognized non-access modifiers present, in a fixed
// order for determinism.
func javaModifiers(mods ts.Node, src []byte) []string {
	if mods.IsNull() {
		return nil
	}
	present := map[string]bool{}
	for _, w := range strings.Fields(mods.Content(src)) {
		present[w] = true
	}
	var out []string
	for _, m := range javaModifierOrder {
		if present[m] {
			out = append(out, m)
		}
	}
	return out
}

// simpleName returns the last dotted segment of a name, or the whole string.
func simpleName(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

// annotationNames returns bare annotation names (arguments dropped) from the
// modifiers node, in source order.
func annotationNames(mods ts.Node, src []byte) []string {
	if mods.IsNull() {
		return nil
	}
	var out []string
	for i := 0; i < mods.NamedChildCount(); i++ {
		c := mods.NamedChild(i)
		if c.Type() == "annotation" || c.Type() == "marker_annotation" {
			// A fully-qualified annotation (@java.lang.Override) reduces to its
			// simple name.
			out = append(out, simpleName(fieldText(c, "name", src)))
		}
	}
	return out
}

// javadocText normalizes a Javadoc block_comment: it strips the leading /**,
// trailing */, and per-line leading * plus surrounding whitespace. It returns ""
// for a block_comment that is not Javadoc (does not start with /**).
func javadocText(comment ts.Node, src []byte) string {
	raw := comment.Content(src)
	if !strings.HasPrefix(raw, "/**") {
		return ""
	}
	raw = strings.TrimPrefix(raw, "/**")
	raw = strings.TrimSuffix(raw, "*/")
	lines := strings.Split(raw, "\n")
	var cleaned []string
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		ln = strings.TrimPrefix(ln, "*")
		ln = strings.TrimSpace(ln)
		cleaned = append(cleaned, ln)
	}
	// Drop leading and trailing empty lines produced by the delimiters.
	for len(cleaned) > 0 && cleaned[0] == "" {
		cleaned = cleaned[1:]
	}
	for len(cleaned) > 0 && cleaned[len(cleaned)-1] == "" {
		cleaned = cleaned[:len(cleaned)-1]
	}
	return strings.Join(cleaned, "\n")
}

// paramList extracts parameters from the "parameters" (formal_parameters) child.
// A formal_parameter carries "type" and "name" fields directly; a spread_parameter
// (varargs) does not (see spreadParam).
func paramList(node ts.Node, src []byte) []schema.Param {
	pnode, ok := node.ChildByFieldName("parameters")
	if !ok {
		return nil
	}
	var out []schema.Param
	for i := 0; i < pnode.NamedChildCount(); i++ {
		c := pnode.NamedChild(i)
		switch c.Type() {
		case "formal_parameter":
			out = append(out, schema.Param{Name: fieldText(c, "name", src), Type: fieldText(c, "type", src)})
		case "spread_parameter":
			out = append(out, spreadParam(c, src))
		}
	}
	return out
}

// spreadParam builds the Param for a varargs spread_parameter. The node has no
// "type" or "name" field (probe, 2026-07-01): the name lives in its
// variable_declarator child's "name" field, and the base type is the named child
// that is neither "modifiers" nor "variable_declarator". The rendered type is the
// base type followed by "..." (e.g. String... -> {name:"args", type:"String..."}).
func spreadParam(node ts.Node, src []byte) schema.Param {
	var name, base string
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		switch c.Type() {
		case "modifiers":
			// final/annotations on a varargs parameter carry no type or name.
		case "variable_declarator":
			name = fieldText(c, "name", src)
		default:
			if base == "" {
				base = c.Content(src)
			}
		}
	}
	return schema.Param{Name: name, Type: base + "..."}
}

// returnType returns the method's return type as a single result Param. It is an
// empty slice for a void return and for constructors (no "type" field). The void
// return arrives as a void_type node whose text is "void".
func returnType(node ts.Node, src []byte) []schema.Param {
	t, ok := node.ChildByFieldName("type")
	if !ok {
		return []schema.Param{}
	}
	txt := t.Content(src)
	if txt == "void" {
		return []schema.Param{}
	}
	return []schema.Param{{Type: txt}}
}

// typeParams extracts generic type parameters from the "type_parameters" child.
// The gotreesitter grammar does not expose a "name" field on type_parameter, so
// the name is taken from its type_identifier child and any remaining text is the
// constraint (bound) as written.
func typeParams(node ts.Node, src []byte) []schema.TypeParam {
	tp, ok := node.ChildByFieldName("type_parameters")
	if !ok {
		return nil
	}
	var out []schema.TypeParam
	for i := 0; i < tp.NamedChildCount(); i++ {
		c := tp.NamedChild(i)
		if c.Type() != "type_parameter" {
			continue
		}
		var name, constraint string
		for j := 0; j < c.NamedChildCount(); j++ {
			g := c.NamedChild(j)
			switch {
			case g.Type() == "type_identifier" && name == "":
				name = g.Content(src)
			default:
				// Bounds (type_bound) and annotations become the constraint text.
				if t := strings.TrimSpace(g.Content(src)); t != "" {
					constraint = t
				}
			}
		}
		out = append(out, schema.TypeParam{Name: name, Constraint: constraint})
	}
	return out
}

// isMainSymbol reports whether a method Symbol is a Java entrypoint:
// public static void main(String[] args) (or String... varargs).
func isMainSymbol(s schema.Symbol) bool {
	if s.Kind != "method" || s.Name != "main" || s.Visibility != "public" || s.Signature == nil {
		return false
	}
	if len(s.Signature.Returns) != 0 { // void return
		return false
	}
	hasStatic := false
	for _, m := range s.Signature.Modifiers {
		if m == "static" {
			hasStatic = true
		}
	}
	if !hasStatic {
		return false
	}
	if len(s.Signature.Params) != 1 {
		return false
	}
	pt := s.Signature.Params[0].Type
	return pt == "String[]" || pt == "String..."
}

// fieldText returns the text of a node's field child, or "" if absent.
func fieldText(node ts.Node, field string, src []byte) string {
	if c, ok := node.ChildByFieldName(field); ok {
		return c.Content(src)
	}
	return ""
}

// packageName returns the dotted package name from a package_declaration,
// dropping the trailing semicolon.
func packageName(node ts.Node, src []byte) string {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c.Type() == "scoped_identifier" || c.Type() == "identifier" {
			return c.Content(src)
		}
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
