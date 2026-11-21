package haskell

import (
	"os"
	"path/filepath"
	"testing"

	"goforge.dev/assayxport/internal/schema"
)

// The extractor's behaviour is specified and tested by LawSpec laws (the
// policy, extraction and conformance units); these tests are upstream's
// smoke check that the wiring works inside ax's own module.

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func find(ps []schema.Package, id string) *schema.Package {
	for i := range ps {
		if ps[i].ID == id {
			return &ps[i]
		}
	}
	return nil
}

func sym(p *schema.Package, id string) *schema.Symbol {
	for i := range p.Symbols {
		if p.Symbols[i].ID == id {
			return &p.Symbols[i]
		}
	}
	return nil
}

func TestExtractModules(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Lib/Shape.hs", "-- | Shapes.\nmodule Lib.Shape (Shape(..), area) where\n\n-- | A shape.\ndata Shape = Circle Int | Square Int\n\narea :: Shape -> Int\narea s = helper s\n\nhelper :: Shape -> Int\nhelper _ = length []\n")
	write(t, root, "app.hs", "main :: IO ()\nmain = mempty\n")
	write(t, root, "dist-newstyle/src/Dep.hs", "module Dep where\ndep = 0\n")
	write(t, root, ".hidden/Ghost.hs", "module Ghost where\nghost = 0\n")
	ps, err := New().Extract(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("got %d packages, want 2 (build and dot-directories skipped)", len(ps))
	}
	shape := find(ps, "Lib.Shape")
	if shape == nil || shape.Name != "Shape" || shape.Doc != "Shapes." || shape.Language != "haskell" {
		t.Fatalf("Lib.Shape package = %+v", shape)
	}
	if c := sym(shape, "Shape.Circle"); c == nil || c.Kind != "constructor" || c.Visibility != "exported" {
		t.Errorf("Shape.Circle = %+v", c)
	}
	if h := sym(shape, "helper"); h == nil || h.Visibility != "unexported" || len(h.Calls) != 1 || h.Calls[0].Kind != "builtin" {
		t.Errorf("helper = %+v", h)
	}
	if a := sym(shape, "area"); a == nil || len(a.Calls) != 1 || a.Calls[0].Kind != "internal" || a.Calls[0].Ref != "Lib.Shape#helper" {
		t.Errorf("area = %+v", a)
	}
	main := find(ps, "Main")
	if main == nil || !main.IsEntrypoint || main.Invocation == nil || main.Invocation.How != "runghc app.hs" {
		t.Errorf("Main package = %+v", main)
	}
}
