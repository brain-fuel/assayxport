package python

import (
	"strings"

	"goforge.dev/assayxport/internal/schema"
	"goforge.dev/assayxport/internal/ts"
)

// Call extraction is syntactic, like everything else in this extractor, and
// resolves exactly as far as module-local knowledge reaches:
//
//   - a bare-name call to a module-level def or class -> internal (a class
//     call is its constructor);
//   - recv.name(...) on the method's own receiver to a sibling method of the
//     same class -> internal;
//   - a name bound by a top-level import -> external, with the local alias
//     expanded to the imported dotted path (np.array -> numpy.array);
//   - a bare name in the builtin table (and not shadowed by any of the
//     above) -> builtin;
//   - any other identifier or attribute callee -> unresolved, as written;
//   - a callee that is not a name at all (fs[0](), f()()) -> dynamic.
//
// Nested defs, lambdas, and comprehensions inside a function body are walked:
// they are not symbols of their own, so their calls are attributed to the
// enclosing function (mirroring the Go extractor's closure rule).
//
// Probe-verified node/field names (gotreesitter grammars.PythonLanguage):
//   - import_statement:      children dotted_name | aliased_import
//   - aliased_import:        dotted_name + alias identifier (last child)
//   - import_from_statement: first child dotted_name|relative_import (module),
//     remaining children dotted_name | aliased_import (imported names)
//   - call:                  "function" field (identifier | attribute | other)
//   - attribute:             "object" and "attribute" fields
//
// No deviations from the canonical grammar names were found.

// pyModuleCtx is one module's name environment for classifying calls.
type pyModuleCtx struct {
	unitID  string                     // manifest unit id, for internal refs
	funcs   map[string]bool            // module-level def names
	classes map[string]bool            // module-level class names
	methods map[string]map[string]bool // class ownerID -> its def names
	imports map[string]string          // local name -> imported dotted path
}

// buildModuleCtx makes one pre-pass over the module's top level, collecting
// the def/class/import environment that pyCalls resolves against. Imports
// inside functions or conditionals are deliberately ignored: only the
// module's top level is a stable, honest source of name bindings.
func buildModuleCtx(root ts.Node, src []byte, unitID string) *pyModuleCtx {
	ctx := &pyModuleCtx{
		unitID:  unitID,
		funcs:   map[string]bool{},
		classes: map[string]bool{},
		methods: map[string]map[string]bool{},
		imports: map[string]string{},
	}
	for i := 0; i < root.NamedChildCount(); i++ {
		child := root.NamedChild(i)
		switch child.Type() {
		case "function_definition", "async_function_definition":
			ctx.funcs[fieldText(child, "name", src)] = true
		case "class_definition":
			ctx.addClass(child, src, "")
		case "decorated_definition":
			_, def := unwrapDecorated(child, src)
			if def.IsNull() {
				continue
			}
			switch {
			case isFuncDef(def.Type()):
				ctx.funcs[fieldText(def, "name", src)] = true
			case def.Type() == "class_definition":
				ctx.addClass(def, src, "")
			}
		case "import_statement":
			for j := 0; j < child.NamedChildCount(); j++ {
				ctx.addImport(child.NamedChild(j), src, "")
			}
		case "import_from_statement":
			if child.NamedChildCount() == 0 {
				continue
			}
			module := child.NamedChild(0).Content(src)
			for j := 1; j < child.NamedChildCount(); j++ {
				ctx.addImport(child.NamedChild(j), src, module)
			}
		}
	}
	return ctx
}

// addClass records a class and its method names, recursing into nested
// classes with dotted owner ids (Outer.Inner), mirroring classSymbols.
func (ctx *pyModuleCtx) addClass(node ts.Node, src []byte, ownerPrefix string) {
	name := fieldText(node, "name", src)
	id := name
	if ownerPrefix != "" {
		id = ownerPrefix + "." + name
	} else {
		ctx.classes[name] = true
	}
	methods := map[string]bool{}
	ctx.methods[id] = methods

	body, ok := node.ChildByFieldName("body")
	if !ok {
		return
	}
	for i := 0; i < body.NamedChildCount(); i++ {
		member := body.NamedChild(i)
		switch member.Type() {
		case "function_definition", "async_function_definition":
			methods[fieldText(member, "name", src)] = true
		case "class_definition":
			ctx.addClass(member, src, id)
		case "decorated_definition":
			_, def := unwrapDecorated(member, src)
			if def.IsNull() {
				continue
			}
			switch {
			case isFuncDef(def.Type()):
				methods[fieldText(def, "name", src)] = true
			case def.Type() == "class_definition":
				ctx.addClass(def, src, id)
			}
		}
	}
}

// addImport records one imported name. module is "" for `import x` forms and
// the from-module for `from m import x` forms.
func (ctx *pyModuleCtx) addImport(node ts.Node, src []byte, module string) {
	switch node.Type() {
	case "dotted_name":
		text := node.Content(src)
		if module != "" {
			// from m import f -> f resolves to m.f
			ctx.imports[text] = joinDotted(module, text)
			return
		}
		// import os.path binds the FIRST segment (os) in the module namespace.
		base := text
		if i := strings.IndexByte(text, '.'); i >= 0 {
			base = text[:i]
		}
		ctx.imports[base] = base
	case "aliased_import":
		// dotted_name as identifier; prefer the grammar's name/alias fields,
		// falling back to first/last named child positions.
		if node.NamedChildCount() < 2 {
			return
		}
		orig, ok := node.ChildByFieldName("name")
		if !ok {
			orig = node.NamedChild(0)
		}
		alias, ok := node.ChildByFieldName("alias")
		if !ok {
			alias = node.NamedChild(node.NamedChildCount() - 1)
		}
		origin := orig.Content(src)
		if module != "" {
			origin = joinDotted(module, origin)
		}
		ctx.imports[alias.Content(src)] = origin
	}
}

// joinDotted joins a from-module and an imported name, tolerating a relative
// module ("." or ".sub") whose text already ends without a separator.
func joinDotted(module, name string) string {
	if strings.HasSuffix(module, ".") {
		return module + name
	}
	return module + "." + name
}

// pyCalls walks a function body and returns its call edges, deduplicated and
// sorted per schema.DedupeCalls. ownerID is the enclosing class id ("" for a
// module-level function); recv is the method's receiver name ("self"/"cls",
// "" when there is none).
func pyCalls(node ts.Node, src []byte, ctx *pyModuleCtx, ownerID, recv string) []schema.Call {
	body, ok := node.ChildByFieldName("body")
	if !ok || ctx == nil {
		return nil
	}
	var raw []schema.Call
	var walk func(n ts.Node)
	walk = func(n ts.Node) {
		for i := 0; i < n.NamedChildCount(); i++ {
			c := n.NamedChild(i)
			if c.Type() == "call" {
				raw = append(raw, pyCallEdge(c, src, ctx, ownerID, recv))
			}
			walk(c)
		}
	}
	walk(body)
	return schema.DedupeCalls(raw)
}

// pyCallEdge classifies one call node per the resolution ladder documented at
// the top of this file.
func pyCallEdge(call ts.Node, src []byte, ctx *pyModuleCtx, ownerID, recv string) schema.Call {
	fn, ok := call.ChildByFieldName("function")
	if !ok {
		return schema.Call{Target: "", Kind: "dynamic"}
	}
	switch fn.Type() {
	case "identifier":
		name := fn.Content(src)
		switch {
		case ctx.funcs[name] || ctx.classes[name]:
			return schema.Call{Target: name, Kind: "internal", Ref: ctx.unitID + "#" + name}
		case ctx.imports[name] != "":
			return schema.Call{Target: ctx.imports[name], Kind: "external"}
		case pyBuiltins[name]:
			return schema.Call{Target: name, Kind: "builtin"}
		}
		return schema.Call{Target: name, Kind: "unresolved"}
	case "attribute":
		obj, okObj := fn.ChildByFieldName("object")
		attr, okAttr := fn.ChildByFieldName("attribute")
		if !okObj || !okAttr {
			return schema.Call{Target: fn.Content(src), Kind: "unresolved"}
		}
		name := attr.Content(src)
		// recv.method(...) on the method's own class.
		if recv != "" && obj.Type() == "identifier" && obj.Content(src) == recv &&
			ownerID != "" && ctx.methods[ownerID][name] {
			return schema.Call{
				Target: ownerID + "." + name,
				Kind:   "internal",
				Ref:    ctx.unitID + "#" + ownerID + "." + name,
			}
		}
		// alias.attr... where alias is a top-level import: expand the alias.
		if base, rest, ok := attributeBase(fn, src); ok {
			if origin := ctx.imports[base]; origin != "" {
				return schema.Call{Target: origin + rest, Kind: "external"}
			}
		}
		return schema.Call{Target: fn.Content(src), Kind: "unresolved"}
	}
	// The callee is not a name at all: fs[0](), f()(), (lambda x: x)(1).
	return schema.Call{Target: fn.Content(src), Kind: "dynamic"}
}

// attributeBase decomposes a pure attribute chain a.b.c into its base
// identifier "a" and the remainder ".b.c". ok is false when the chain's base
// is not a plain identifier (e.g. a subscript or call result).
func attributeBase(fn ts.Node, src []byte) (base, rest string, ok bool) {
	cur := fn
	for cur.Type() == "attribute" {
		attr, okA := cur.ChildByFieldName("attribute")
		obj, okO := cur.ChildByFieldName("object")
		if !okA || !okO {
			return "", "", false
		}
		rest = "." + attr.Content(src) + rest
		cur = obj
	}
	if cur.Type() != "identifier" {
		return "", "", false
	}
	return cur.Content(src), rest, true
}

// pyBuiltins is the resolution floor: bare-name calls landing here (and not
// shadowed by a module def, class, or import) are language primitives.
var pyBuiltins = map[string]bool{
	"abs": true, "aiter": true, "all": true, "anext": true, "any": true,
	"ascii": true, "bin": true, "bool": true, "breakpoint": true,
	"bytearray": true, "bytes": true, "callable": true, "chr": true,
	"classmethod": true, "compile": true, "complex": true, "delattr": true,
	"dict": true, "dir": true, "divmod": true, "enumerate": true, "eval": true,
	"exec": true, "filter": true, "float": true, "format": true,
	"frozenset": true, "getattr": true, "globals": true, "hasattr": true,
	"hash": true, "help": true, "hex": true, "id": true, "input": true,
	"int": true, "isinstance": true, "issubclass": true, "iter": true,
	"len": true, "list": true, "locals": true, "map": true, "max": true,
	"memoryview": true, "min": true, "next": true, "object": true, "oct": true,
	"open": true, "ord": true, "pow": true, "print": true, "property": true,
	"range": true, "repr": true, "reversed": true, "round": true, "set": true,
	"setattr": true, "slice": true, "sorted": true, "staticmethod": true,
	"str": true, "sum": true, "super": true, "tuple": true, "type": true,
	"vars": true, "zip": true,
}
