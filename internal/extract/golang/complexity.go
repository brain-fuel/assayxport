package golang

import (
	"go/ast"

	"golang.org/x/tools/go/ast/astutil"

	"goforge.dev/assayxport/internal/complexity"
)

// goSummary walks a function body and produces a control-flow Summary. It uses
// astutil.Apply's pre/post callbacks to track loop-nesting depth. Recursion is
// detected by a bare-name self-call (selector self-calls like p.foo() are not
// detected, which conservatively yields a loop-nesting bound rather than nil).
func goSummary(fd *ast.FuncDecl) complexity.Summary {
	var sum complexity.Summary
	if fd == nil || fd.Body == nil {
		return sum // bodiless (external/asm) or nil -> O(1)
	}
	name := fd.Name.Name
	depth := 0
	astutil.Apply(fd.Body, func(c *astutil.Cursor) bool {
		switch n := c.Node().(type) {
		case *ast.FuncLit:
			// A nested closure is its own scope; its loops and calls belong to
			// it, not to fd. Skip the subtree so a loop inside a closure passed
			// to a library function does not inflate fd's depth.
			return false
		case *ast.ForStmt, *ast.RangeStmt:
			depth++
			if depth > sum.MaxLoopDepth {
				sum.MaxLoopDepth = depth
			}
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok {
				if id.Name == name {
					sum.Recursive = true
				}
				if id.Name == "make" || id.Name == "new" || id.Name == "append" {
					recordAlloc(&sum, depth)
				}
			}
		case *ast.CompositeLit:
			recordAlloc(&sum, depth)
		}
		return true
	}, func(c *astutil.Cursor) bool {
		switch c.Node().(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			depth--
		}
		return true
	})
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
