// Package calc does sample arithmetic for assayxport extractor tests.
package calc

// Add returns a + b.
func Add(a, b int) int { return a + b }

// sub returns a - b. Unexported on purpose.
func sub(a, b int) int { return a - b }

// Sum returns the total of xs.
func Sum(xs ...int) int {
	t := 0
	for _, x := range xs {
		t += x
	}
	return t
}

// Max returns the larger of a and b for any ordered type.
func Max[T int | float64](a, b T) T {
	if a > b {
		return a
	}
	return b
}

// Accumulator sums pushed values.
type Accumulator struct{ total int }

// Push adds v to the accumulator and returns the new total.
func (a *Accumulator) Push(v int) int {
	a.total += v
	return a.total
}

// Clone returns a copy of a.
func Clone(a *Accumulator) *Accumulator { c := *a; return &c }

// MaxInt is the largest representable sample value.
const MaxInt = 1<<31 - 1

// Default is the zero accumulator.
var Default = Accumulator{}

// Point is a 2D point.
type Point struct {
	X int // X coordinate.
	Y int // Y coordinate.
}

// Adder is anything that can add.
type Adder interface {
	// Add returns the sum.
	Add(a, b int) int
}

// Celsius is a defined numeric type.
type Celsius float64
