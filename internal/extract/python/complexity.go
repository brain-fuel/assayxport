package python

import (
	"strings"

	"goforge.dev/assayxport/internal/complexity"
	"goforge.dev/assayxport/internal/ts"
)

// pySummary walks a function_definition body producing a control-flow Summary.
// Loop nodes (for/while) and comprehensions increment depth; a bare-name call
// to the def's own name is recursion; list/dict/set displays and
// .append/.extend/.add/.update calls are allocations.
//
// Probe-verified node/field names (gotreesitter grammars.PythonLanguage):
//   - loops:          for_statement, while_statement
//   - comprehensions: list_comprehension, set_comprehension,
//                     dictionary_comprehension, generator_expression
//   - calls:          "call" node with "function" field (identifier or attribute)
//   - attr call:      function field is "attribute" node; method name via "attribute" field
//   - literals:       list, dictionary, set
//
// No deviations from the brief's canonical names were found.
// Limitation: recursion is detected only for bare-name self-calls; method
// self-calls (self.foo()) are not detected.
func pySummary(node ts.Node, src []byte, name string) complexity.Summary {
	var sum complexity.Summary
	body, ok := node.ChildByFieldName("body")
	if !ok {
		return sum
	}
	var walk func(n ts.Node, depth int)
	walk = func(n ts.Node, depth int) {
		for i := 0; i < n.NamedChildCount(); i++ {
			c := n.NamedChild(i)
			d := depth
			switch c.Type() {
			case "for_statement", "while_statement",
				"list_comprehension", "set_comprehension",
				"dictionary_comprehension", "generator_expression":
				d = depth + 1
				if d > sum.MaxLoopDepth {
					sum.MaxLoopDepth = d
				}
				if c.Type() != "for_statement" && c.Type() != "while_statement" {
					// a comprehension allocates at the enclosing depth
					recordAlloc(&sum, depth)
				}
			case "call":
				if pyIsSelfCall(c, src, name) {
					sum.Recursive = true
				}
				if pyIsAllocCall(c, src) {
					recordAlloc(&sum, depth)
				}
			case "list", "dictionary", "set":
				recordAlloc(&sum, depth)
			}
			walk(c, d)
		}
	}
	walk(body, 0)
	return sum
}

// recordAlloc notes an allocation at the current loop depth (only in-loop
// allocations affect the space estimate).
func recordAlloc(sum *complexity.Summary, depth int) {
	if depth >= 1 {
		sum.AllocInLoop = true
		if depth > sum.AllocDepth {
			sum.AllocDepth = depth
		}
	}
}

// pyIsSelfCall reports whether a call node invokes the bare name `name`.
func pyIsSelfCall(call ts.Node, src []byte, name string) bool {
	fn, ok := call.ChildByFieldName("function")
	if !ok {
		return false
	}
	return fn.Type() == "identifier" && fn.Content(src) == name
}

// pyIsAllocCall reports whether a call is x.append/extend/add/update(...).
func pyIsAllocCall(call ts.Node, src []byte) bool {
	fn, ok := call.ChildByFieldName("function")
	if !ok || fn.Type() != "attribute" {
		return false
	}
	attr, ok := fn.ChildByFieldName("attribute")
	if !ok {
		return false
	}
	switch strings.TrimSpace(attr.Content(src)) {
	case "append", "extend", "add", "update":
		return true
	}
	return false
}
