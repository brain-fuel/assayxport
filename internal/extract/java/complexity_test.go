package java

import "testing"

func symComplexity(t *testing.T, symID string) (time, space, method string) {
	t.Helper()
	pkgs, err := New().Extract("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkgs {
		for _, s := range p.Symbols {
			if s.ID == symID {
				tm, sp := "nil", "nil"
				if s.Complexity.Time != nil {
					tm = *s.Complexity.Time
				}
				if s.Complexity.Space != nil {
					sp = *s.Complexity.Space
				}
				return tm, sp, s.Complexity.Method
			}
		}
	}
	t.Fatalf("symbol %q not found", symID)
	return "", "", ""
}

func TestJavaComplexity(t *testing.T) {
	cases := []struct{ sym, time, space, method string }{
		{"Shapes.constant", "O(1)", "O(1)", "loop-nesting"},
		{"Shapes.linear", "O(n)", "O(1)", "loop-nesting"},
		{"Shapes.quadratic", "O(n^2)", "O(1)", "loop-nesting"},
		{"Shapes.collect", "O(n)", "O(n)", "loop-nesting"},
		{"Shapes.recur", "nil", "nil", "recursive"},
		// Regression: only loop is inside a lambda body; enclosing method must be O(1).
		{"Shapes.noLoopHere", "O(1)", "O(1)", "loop-nesting"},
	}
	for _, c := range cases {
		tm, sp, m := symComplexity(t, c.sym)
		if tm != c.time || sp != c.space || m != c.method {
			t.Errorf("%s: got {%s,%s,%s} want {%s,%s,%s}", c.sym, tm, sp, m, c.time, c.space, c.method)
		}
	}
}
