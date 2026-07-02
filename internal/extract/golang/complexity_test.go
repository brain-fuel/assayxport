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
		// Method calling the package-level func of the same name: NOT recursion.
		{"Node.MarkThing", "O(1)", "O(1)", "loop-nesting"},
		// Method selector self-call n.parent.Root(): IS recursion.
		{"Node.Root", "nil", "nil", "recursive"},
	}
	for _, c := range cases {
		tm, sp, m := symComplexity(t, c.id)
		if tm != c.time || sp != c.space || m != c.method {
			t.Errorf("%s: got {%s,%s,%s} want {%s,%s,%s}", c.id, tm, sp, m, c.time, c.space, c.method)
		}
	}
}

// TestExtractNoGoFilesEmpty confirms a tree with no .go files yields an empty
// result (not a go/packages "no main module" error), so a default polyglot
// scan of a Python- or Java-only repo is not aborted by the Go extractor.
func TestExtractNoGoFilesEmpty(t *testing.T) {
	pkgs, err := New().Extract("testdata/nogo")
	if err != nil {
		t.Fatalf("Extract(no-go dir) = error %v, want nil", err)
	}
	if len(pkgs) != 0 {
		t.Fatalf("Extract(no-go dir) = %d packages, want 0", len(pkgs))
	}
}

// TestGenericTypeParams confirms a generic type declaration exposes its type
// parameters in signature.type_params (Box[T any]).
func TestGenericTypeParams(t *testing.T) {
	pkgs, err := New().Extract("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkgs {
		for _, s := range p.Symbols {
			if s.ID == "Box" {
				if s.Signature == nil || len(s.Signature.TypeParams) != 1 || s.Signature.TypeParams[0].Name != "T" {
					t.Fatalf("Box type_params = %+v, want one param named T", s.Signature)
				}
				return
			}
		}
	}
	t.Fatal("type Box not found")
}
