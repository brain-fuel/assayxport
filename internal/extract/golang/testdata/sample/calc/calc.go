// Package calc does sample arithmetic for assayxport extractor tests.
package calc

// Add returns a + b.
func Add(a, b int) int { return a + b }

// sub returns a - b. Unexported on purpose.
func sub(a, b int) int { return a - b }
