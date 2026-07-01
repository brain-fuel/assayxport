package ts

import "testing"

func TestParsePythonFunction(t *testing.T) {
	src := []byte("def add(a, b):\n    return a + b\n")
	tree, err := Parse(Python, src)
	if err != nil {
		t.Fatal(err)
	}
	root := tree.Root()
	if root.Type() != "module" {
		t.Fatalf("root type = %q, want module", root.Type())
	}
	if root.NamedChildCount() < 1 {
		t.Fatalf("expected at least one named child")
	}
	fn := root.NamedChild(0)
	if fn.Type() != "function_definition" {
		t.Fatalf("child type = %q, want function_definition", fn.Type())
	}
	name, ok := fn.ChildByFieldName("name")
	if !ok || name.Content(src) != "add" {
		t.Fatalf("function name = %q (ok=%v), want add", name.Content(src), ok)
	}
	if name.StartLine() != 1 {
		t.Fatalf("name start line = %d, want 1", name.StartLine())
	}
}

func TestParseJava(t *testing.T) {
	src := []byte("package com.foo;\npublic class Bar { public int x() { return 1; } }\n")
	tree, err := Parse(Java, src)
	if err != nil {
		t.Fatalf("Parse(Java): %v", err)
	}
	root := tree.Root()
	if root.IsNull() || root.NamedChildCount() == 0 {
		t.Fatalf("empty Java tree: type=%q count=%d", root.Type(), root.NamedChildCount())
	}
	if root.Type() != "program" {
		t.Fatalf("root type = %q, want program", root.Type())
	}
	// A class_declaration must be present so a future grammar swap can't silently
	// regress the node shape the Java extractor relies on.
	found := false
	for i := 0; i < root.NamedChildCount(); i++ {
		if root.NamedChild(i).Type() == "class_declaration" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no class_declaration among %d top-level nodes", root.NamedChildCount())
	}
}

func TestParseDeterministic(t *testing.T) {
	src := []byte("class C:\n    def m(self):\n        pass\n")
	a, err := Parse(Python, src)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse(Python, src)
	if err != nil {
		t.Fatal(err)
	}
	// Structural determinism: same root type + same named child count/types.
	if a.Root().Type() != b.Root().Type() || a.Root().NamedChildCount() != b.Root().NamedChildCount() {
		t.Fatalf("parse not structurally stable across runs")
	}
}
