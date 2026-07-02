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
