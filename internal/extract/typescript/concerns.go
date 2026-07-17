package typescript

import (
	"strings"

	"goforge.dev/assayxport/internal/schema"
	"goforge.dev/assayxport/internal/ts"
)

// funcConcerns collects the type-honesty / off-standard flags a callable carries
// -- code that compiles and runs but forfeits its type contract. It combines the
// signature (untyped params/return, `any`) with an AST walk of the body for
// escape-hatch expressions (non-null `!`, `as any`, loose `==`) and a text scan
// for `@ts-ignore`-family suppressions.
func funcConcerns(n ts.Node, sig *schema.Signature, hadReturnType bool, src []byte, isTS bool) []string {
	seen := map[string]bool{}
	var out []string
	add := func(c string) {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}

	if !isTS {
		add("untyped") // a plain-JS function has no type contract to match up with
	}

	if isTS && sig != nil {
		for _, p := range sig.Params {
			// `this` and destructured params without annotations are common; still
			// flag a bare untyped parameter as an implicit any.
			if p.Type == "" && p.Name != "this" {
				add("untyped-param")
			}
			if hasAnyToken(p.Type) {
				add("any")
			}
		}
		for _, r := range sig.Returns {
			if hasAnyToken(r.Type) {
				add("any")
			}
		}
		if !hadReturnType && !isAccessorOrCtor(n, src) {
			add("untyped-return")
		}
	}

	// AST walk of the whole declaration for escape-hatch expressions.
	walkNode(n, func(c ts.Node) {
		switch c.Type() {
		case "non_null_expression":
			add("non-null-assertion")
		case "as_expression", "satisfies_expression":
			txt := c.Content(src)
			switch {
			case containsWord(txt, "any"):
				add("as-any")
			case strings.Contains(txt, "unknown"):
				add("as-unknown")
			default:
				add("type-assertion")
			}
		case "binary_expression":
			switch fieldText(c, "operator", src) {
			case "==", "!=":
				add("loose-equality")
			}
		}
	})

	// Suppression comments live in the declaration's source span.
	if txt := n.Content(src); strings.Contains(txt, "@ts-ignore") ||
		strings.Contains(txt, "@ts-expect-error") || strings.Contains(txt, "@ts-nocheck") {
		add("ts-ignore")
	}
	return out
}

func isAccessorOrCtor(n ts.Node, src []byte) bool {
	if fieldText(n, "name", src) == "constructor" {
		return true
	}
	k := methodKindWord(n, src)
	return k == "get" || k == "set"
}

// walkNode calls fn on n and every named descendant.
func walkNode(n ts.Node, fn func(ts.Node)) {
	if n.IsNull() {
		return
	}
	fn(n)
	for i := 0; i < n.NamedChildCount(); i++ {
		walkNode(n.NamedChild(i), fn)
	}
}

func containsWord(s, word string) bool {
	for _, f := range strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '$')
	}) {
		if f == word {
			return true
		}
	}
	return false
}
