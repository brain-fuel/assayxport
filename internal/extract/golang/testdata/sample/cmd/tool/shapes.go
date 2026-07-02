package main

// Constant does no looping.
func Constant(x int) int { return x + 1 }

// Linear loops once.
func Linear(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	return total
}

// Quadratic nests two loops.
func Quadratic(xs []int) int {
	n := 0
	for range xs {
		for range xs {
			n++
		}
	}
	return n
}

// Collect allocates inside a loop (space O(n)).
func Collect(xs []int) []int {
	out := make([]int, 0)
	for _, x := range xs {
		out = append(out, x*2)
	}
	return out
}

// Recur calls itself.
func Recur(n int) int {
	if n <= 0 {
		return 0
	}
	return Recur(n - 1)
}

// Closure has no loop of its own; the loop lives inside a nested closure and
// must NOT count toward Closure's complexity (stays O(1)).
func Closure(xs []int) int {
	f := func() int {
		s := 0
		for _, x := range xs {
			s += x
		}
		return s
	}
	return f()
}

// MarkThing is a package-level function that a method of the same name wraps.
func MarkThing(n *Node) int { return n.depth }

// Node is a tree node used to exercise method recursion detection.
type Node struct {
	depth  int
	parent *Node
}

// MarkThing is a method that calls the package-level MarkThing of the same
// name. That bare-name call is NOT a self-call, so MarkThing must be O(1)
// loop-nesting, never "recursive".
func (n *Node) MarkThing() int { return MarkThing(n) }

// Root walks to the tree root by calling itself on the parent. The selector
// self-call n.parent.Root() must be detected as recursion (method + selector).
func (n *Node) Root() *Node {
	if n.parent == nil {
		return n
	}
	return n.parent.Root()
}

// Box is a generic type; its type parameter must appear in the symbol's
// signature.type_params.
type Box[T any] struct {
	v T
}
