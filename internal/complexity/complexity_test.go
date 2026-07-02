package complexity

import "testing"

func str(p *string) string {
	if p == nil {
		return "nil"
	}
	return *p
}

func TestEstimate(t *testing.T) {
	cases := []struct {
		name              string
		in                Summary
		time, space, meth string
	}{
		{"constant", Summary{}, "O(1)", "O(1)", "loop-nesting"},
		{"linear", Summary{MaxLoopDepth: 1}, "O(n)", "O(1)", "loop-nesting"},
		{"quadratic", Summary{MaxLoopDepth: 2}, "O(n^2)", "O(1)", "loop-nesting"},
		{"cubic", Summary{MaxLoopDepth: 3}, "O(n^3)", "O(1)", "loop-nesting"},
		{"alloc in loop", Summary{MaxLoopDepth: 1, AllocInLoop: true, AllocDepth: 1}, "O(n)", "O(n)", "loop-nesting"},
		{"recursive", Summary{Recursive: true}, "nil", "nil", "recursive"},
		{"recursive beats loops", Summary{MaxLoopDepth: 2, Recursive: true}, "nil", "nil", "recursive"},
	}
	for _, c := range cases {
		got := Estimate(c.in)
		if str(got.Time) != c.time || str(got.Space) != c.space || got.Method != c.meth {
			t.Errorf("%s: got {%s, %s, %s}, want {%s, %s, %s}",
				c.name, str(got.Time), str(got.Space), got.Method, c.time, c.space, c.meth)
		}
	}
}
