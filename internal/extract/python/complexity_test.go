package python

import "testing"

func symComplexity(t *testing.T, moduleID, symID string) (time, space, method string) {
	t.Helper()
	pkgs, err := New().Extract("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkgs {
		if p.ID != moduleID {
			continue
		}
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
	t.Fatalf("symbol %q in %q not found", symID, moduleID)
	return "", "", ""
}

func TestPyComplexity(t *testing.T) {
	cases := []struct{ sym, time, space, method string }{
		{"constant", "O(1)", "O(1)", "loop-nesting"},
		{"linear", "O(n)", "O(1)", "loop-nesting"},
		{"quadratic", "O(n^2)", "O(1)", "loop-nesting"},
		{"collect", "O(n)", "O(n)", "loop-nesting"},
		{"recur", "nil", "nil", "recursive"},
		{"closure", "O(1)", "O(1)", "loop-nesting"},
		{"nested_class", "O(1)", "O(1)", "loop-nesting"},
		// Method self-call self.walk() is genuine recursion.
		{"Evt.walk", "nil", "nil", "recursive"},
		// Bare-name call from a method matching the method's own name resolves
		// to the module-level free function, NOT self -> not recursion.
		{"Evt.get_header_value", "O(1)", "O(1)", "loop-nesting"},
		// Dict comprehension: O(n) time and O(n) space.
		{"dict_build", "O(n)", "O(n)", "loop-nesting"},
		// Subscript assignment inside a loop grows a dict: O(n) space.
		{"dict_loop", "O(n)", "O(n)", "loop-nesting"},
	}
	for _, c := range cases {
		tm, sp, m := symComplexity(t, "pkg.shapes", c.sym)
		if tm != c.time || sp != c.space || m != c.method {
			t.Errorf("%s: got {%s,%s,%s} want {%s,%s,%s}", c.sym, tm, sp, m, c.time, c.space, c.method)
		}
	}
}
