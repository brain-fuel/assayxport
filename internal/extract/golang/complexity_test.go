package golang

import "testing"

func symComplexity(t *testing.T, id string) (time, space, method string) {
	t.Helper()
	pkgs, err := New().Extract("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkgs {
		for _, s := range p.Symbols {
			if s.ID == id {
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
	t.Fatalf("symbol %q not found", id)
	return "", "", ""
}

func TestGoComplexity(t *testing.T) {
	cases := []struct{ id, time, space, method string }{
		{"Constant", "O(1)", "O(1)", "loop-nesting"},
		{"Linear", "O(n)", "O(1)", "loop-nesting"},
		{"Quadratic", "O(n^2)", "O(1)", "loop-nesting"},
		{"Collect", "O(n)", "O(n)", "loop-nesting"},
		{"Recur", "nil", "nil", "recursive"},
		{"Closure", "O(1)", "O(1)", "loop-nesting"},
	}
	for _, c := range cases {
		tm, sp, m := symComplexity(t, c.id)
		if tm != c.time || sp != c.space || m != c.method {
			t.Errorf("%s: got {%s,%s,%s} want {%s,%s,%s}", c.id, tm, sp, m, c.time, c.space, c.method)
		}
	}
}
