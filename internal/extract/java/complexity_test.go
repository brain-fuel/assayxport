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

// TestJavaOverloadNotRecursive covers the overload-delegation false positive:
// delegate(int) calls delegate(int, int) (different arity), so no symbol named
// "delegate" may be flagged recursive.
func TestJavaOverloadNotRecursive(t *testing.T) {
	pkgs, err := New().Extract("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range pkgs {
		for _, s := range p.Symbols {
			if s.Name == "delegate" {
				found = true
				if s.Complexity.Method == "recursive" {
					t.Errorf("delegate overload %q flagged recursive (arity mismatch should prevent this)", s.ID)
				}
			}
		}
	}
	if !found {
		t.Fatal("no delegate symbol found")
	}
}

// TestEnumBodyMembers covers the enum-body extraction fix: methods and fields
// declared inside an enum body must be emitted (they were silently dropped).
func TestEnumBodyMembers(t *testing.T) {
	pkgs, err := New().Extract("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range pkgs {
		for _, s := range p.Symbols {
			seen[s.ID] = true
		}
	}
	for _, id := range []string{
		"Shapes.Kind.getCode", // enum_body_declarations method
		"Shapes.Kind.code",    // enum_body_declarations field
		"Shapes.Kind.A.label", // method inside a constant's class body
	} {
		if !seen[id] {
			t.Errorf("enum body member %q missing", id)
		}
	}
}

// TestNestedTypeModifiers covers the type-modifier fix: a static nested class
// must carry its static/final modifiers in signature.modifiers.
func TestNestedTypeModifiers(t *testing.T) {
	pkgs, err := New().Extract("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkgs {
		for _, s := range p.Symbols {
			if s.ID == "Shapes.Nested" {
				if s.Signature == nil || len(s.Signature.Modifiers) != 2 ||
					s.Signature.Modifiers[0] != "static" || s.Signature.Modifiers[1] != "final" {
					t.Fatalf("Shapes.Nested modifiers = %+v, want [static final]", s.Signature)
				}
				return
			}
		}
	}
	t.Fatal("Shapes.Nested not found")
}
