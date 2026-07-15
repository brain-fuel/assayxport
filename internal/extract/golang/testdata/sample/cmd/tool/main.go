// Command tool is a sample entrypoint for assayxport extractor tests.
package main

import (
	"fmt"

	"example.com/sample/calc"
)

func main() {
	acc := &calc.Accumulator{}
	report("total", acc.Push(calc.Add(1, 2))) // internal: method, func, same-pkg func
	report("len", len("xs"))                  // builtin: len
}

// report prints a labeled value; its edge to fmt.Println is external.
func report(label string, v int) {
	fmt.Println(label, v)
}

// dispatch calls through an interface: a dynamic edge that still names (and
// links) the interface method, since the concrete callee is chosen at run time.
func dispatch(a calc.Adder) int { return a.Add(1, 2) }

// apply calls a func-typed parameter: a dynamic edge with no static name.
func apply(f func(int) int, v int) int { return f(v) }

// convert uses call syntax that is a type conversion, not a call: no edge.
func convert(f float64) calc.Celsius { return calc.Celsius(f) }

// deferred calls fmt.Println only from inside a closure; a closure is not a
// symbol of its own, so the edge is attributed to deferred.
func deferred() {
	defer func() { fmt.Println("done") }()
}
