package hierarchy

import (
	"testing"

	"goforge.dev/assayxport/internal/schema"
)

func fixture() schema.Index {
	return schema.Index{
		Packages: []schema.PackageEntry{
			{ID: "m/a", Path: "a", Shard: ".assayxport/a.json", SymbolCount: 3},
			{ID: "m/a/b", Path: "a/b", Shard: ".assayxport/a/b.json", SymbolCount: 5, IsEntrypoint: true},
			{ID: "m/a/c", Path: "a/c", Shard: ".assayxport/a/c.json", SymbolCount: 2},
			{ID: "m/d", Path: "d", Shard: ".assayxport/d.json", SymbolCount: 7},
		},
	}
}

func TestRootLevelIsBreadthNotAllPackages(t *testing.T) {
	tr := Build(fixture())
	root, ok := tr.Level("")
	if !ok {
		t.Fatal("root level missing")
	}
	// Top level is {a, d}, not all four packages.
	if len(root.Children) != 2 {
		t.Fatalf("root children = %d, want 2 (a, d)", len(root.Children))
	}
	got := map[string]LevelEntry{}
	for _, c := range root.Children {
		got[c.Name] = c
	}
	// "a" is a group (has sub-packages) whose subtree aggregates roll up.
	a := got["a"]
	if a.Kind != "group" {
		t.Fatalf("a.Kind = %q, want group", a.Kind)
	}
	if a.Symbols != 3+5+2 {
		t.Fatalf("a.Symbols = %d, want 10 (own 3 + b 5 + c 2)", a.Symbols)
	}
	if a.Packages != 3 {
		t.Fatalf("a.Packages = %d, want 3", a.Packages)
	}
	if !a.HasEntrypoint {
		t.Fatal("a should report an entrypoint in its subtree (b)")
	}
	// "d" is a leaf package.
	d := got["d"]
	if d.Kind != "package" || d.Shard == "" {
		t.Fatalf("d should be a leaf package with a shard, got %+v", d)
	}
}

func TestDescendOneLevel(t *testing.T) {
	tr := Build(fixture())
	lv, ok := tr.Level("a")
	if !ok {
		t.Fatal("level a missing")
	}
	if len(lv.Children) != 2 {
		t.Fatalf("a's children = %d, want 2 (b, c)", len(lv.Children))
	}
	for _, c := range lv.Children {
		if c.Kind != "package" {
			t.Fatalf("child %s of a should be a leaf package, got %q", c.Name, c.Kind)
		}
	}
	// a is itself a package; the level reports that so the client can open a's
	// own symbols even while descending into its children.
	if !lv.IsPkg {
		t.Fatal("level a should report IsPkg (a is an assayed package too)")
	}
}

func TestGroupThatIsAlsoPackageKeepsShard(t *testing.T) {
	tr := Build(fixture())
	root, _ := tr.Level("")
	for _, c := range root.Children {
		if c.Name == "a" {
			if c.Shard == "" || c.PkgID != "m/a" {
				t.Fatalf("group-package a should keep its shard/id, got %+v", c)
			}
		}
	}
}

func TestUnknownNode(t *testing.T) {
	tr := Build(fixture())
	if _, ok := tr.Level("nope"); ok {
		t.Fatal("expected miss for unknown node")
	}
}

func TestDeterministicOrder(t *testing.T) {
	tr := Build(fixture())
	lv, _ := tr.Level("a")
	if lv.Children[0].Name != "b" || lv.Children[1].Name != "c" {
		t.Fatalf("children not sorted: %s, %s", lv.Children[0].Name, lv.Children[1].Name)
	}
}
