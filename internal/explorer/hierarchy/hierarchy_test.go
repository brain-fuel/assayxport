package hierarchy

import (
	"fmt"
	"reflect"
	"strings"
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

// ---- the summary monoid and the structures built on it ----

func TestSummaryMonoidLaws(t *testing.T) {
	vals := []Summary{
		{}, {Symbols: 1}, {Packages: 1, HasEntrypoint: true},
		{Symbols: 3, Packages: 2}, {Symbols: 5, Packages: 1, HasEntrypoint: true},
	}
	var zero Summary
	for _, a := range vals {
		if a.Plus(zero) != a || zero.Plus(a) != a {
			t.Fatalf("identity law broken for %+v", a)
		}
		for _, b := range vals {
			if a.Plus(b) != b.Plus(a) {
				t.Fatalf("commutativity broken for %+v, %+v", a, b)
			}
			for _, c := range vals {
				if a.Plus(b).Plus(c) != a.Plus(b.Plus(c)) {
					t.Fatalf("associativity broken for %+v, %+v, %+v", a, b, c)
				}
			}
		}
	}
}

// genIndex builds a deterministic n-package index with shared path prefixes,
// nesting up to several levels, group-packages (a package whose path prefixes
// another's), and a sprinkling of entrypoints.
func genIndex(n int) schema.Index {
	seen := map[string]bool{}
	var idx schema.Index
	for i := range n {
		segs := []string{fmt.Sprintf("top%d", i%9)}
		for x := i / 9; x > 0; x /= 5 {
			segs = append(segs, fmt.Sprintf("d%d", x%5))
		}
		p := strings.Join(segs, "/")
		if seen[p] {
			p += fmt.Sprintf("/leaf%d", i)
		}
		seen[p] = true
		idx.Packages = append(idx.Packages, schema.PackageEntry{
			ID: fmt.Sprintf("m/p%d", i), Path: p,
			Shard:       fmt.Sprintf(".assayxport/p%d.json", i),
			SymbolCount: (i * 7) % 23, IsEntrypoint: i%17 == 0,
		})
	}
	return idx
}

// levelsMatch asserts got and want serve identical levels for every path,
// ignoring versions (equivalence of structure and aggregates, not of history).
func levelsMatch(t *testing.T, got, want *Tree) {
	t.Helper()
	if len(got.byPath) != len(want.byPath) {
		t.Fatalf("node count = %d, want %d", len(got.byPath), len(want.byPath))
	}
	if got.pkgs != want.pkgs {
		t.Fatalf("package count = %d, want %d", got.pkgs, want.pkgs)
	}
	for p := range want.byPath {
		g, gok := got.Level(p)
		w, wok := want.Level(p)
		if !gok || !wok {
			t.Fatalf("level %q: ok = %v/%v", p, gok, wok)
		}
		g.Version, w.Version = 0, 0
		if !reflect.DeepEqual(g, w) {
			t.Fatalf("level %q:\n got %+v\nwant %+v", p, g, w)
		}
	}
}

func TestParallelBuildMatchesSequential(t *testing.T) {
	idx := genIndex(2000)
	seq := buildPartial(idx.Packages)
	for _, workers := range []int{2, 3, 8} {
		levelsMatch(t, buildParallel(idx.Packages, workers), seq)
	}
}

func TestApplyProgressiveFillMatchesFreshBuild(t *testing.T) {
	full := genIndex(600)
	// Skeleton: the full package set, nothing parsed yet.
	skel := schema.Index{Packages: make([]schema.PackageEntry, len(full.Packages))}
	copy(skel.Packages, full.Packages)
	for i := range skel.Packages {
		skel.Packages[i].SymbolCount = 0
		skel.Packages[i].Shard = ""
		skel.Packages[i].IsEntrypoint = false
	}
	tr := Build(skel)
	levelsMatch(t, tr, Build(skel))
	// Parse in waves; after each grown snapshot the incrementally-applied tree
	// must be indistinguishable from a fresh build of that snapshot.
	for cut := 150; ; cut += 150 {
		if cut > len(full.Packages) {
			cut = len(full.Packages)
		}
		snap := schema.Index{Packages: make([]schema.PackageEntry, len(full.Packages))}
		copy(snap.Packages, skel.Packages)
		copy(snap.Packages[:cut], full.Packages[:cut])
		tr.Apply(snap)
		levelsMatch(t, tr, Build(snap))
		if cut == len(full.Packages) {
			break
		}
	}
}

func TestApplyVersionsChangeOnlyTouchedLevels(t *testing.T) {
	tr := Build(fixture())
	root0, _ := tr.Level("")
	a0, _ := tr.Level("a")
	// d gains symbols; nothing under a changes.
	idx := fixture()
	for i := range idx.Packages {
		if idx.Packages[i].Path == "d" {
			idx.Packages[i].SymbolCount = 9
		}
	}
	if n := tr.Apply(idx); n != 1 {
		t.Fatalf("changed = %d, want 1", n)
	}
	root1, _ := tr.Level("")
	a1, _ := tr.Level("a")
	if root1.Version == root0.Version {
		t.Fatal("root version should bump: d changed under it")
	}
	if a1.Version != a0.Version {
		t.Fatalf("a's version bumped (%d -> %d) though nothing under a changed", a0.Version, a1.Version)
	}
	for _, c := range root1.Children {
		if c.Name == "d" && c.Symbols != 9 {
			t.Fatalf("d.Symbols = %d after apply, want 9", c.Symbols)
		}
	}
}

func TestApplyIdenticalIndexIsNoop(t *testing.T) {
	tr := Build(fixture())
	v0, _ := tr.Level("")
	if n := tr.Apply(fixture()); n != 0 {
		t.Fatalf("changed = %d, want 0", n)
	}
	v1, _ := tr.Level("")
	if v1.Version != v0.Version {
		t.Fatal("no-op apply must not bump any level version")
	}
}

func TestApplyShardFillBumpsVersionWithoutCountChange(t *testing.T) {
	idx := fixture()
	idx.Packages[3].Shard = "" // d still pending
	tr := Build(idx)
	v0, _ := tr.Level("")
	if n := tr.Apply(fixture()); n != 1 { // shard path fills in, counts identical
		t.Fatalf("changed = %d, want 1", n)
	}
	v1, _ := tr.Level("")
	if v1.Version == v0.Version {
		t.Fatal("shard fill must bump the level version even with all counts unchanged")
	}
	for _, c := range v1.Children {
		if c.Name == "d" && c.Shard != ".assayxport/d.json" {
			t.Fatalf("d.Shard = %q after apply, want filled", c.Shard)
		}
	}
}

func TestApplyNewPackageOnNewPath(t *testing.T) {
	tr := Build(fixture())
	idx := fixture()
	idx.Packages = append(idx.Packages, schema.PackageEntry{
		ID: "m/a/e/f", Path: "a/e/f", Shard: ".assayxport/a/e/f.json", SymbolCount: 4,
	})
	if n := tr.Apply(idx); n != 1 {
		t.Fatalf("changed = %d, want 1", n)
	}
	levelsMatch(t, tr, Build(idx))
	lv, ok := tr.Level("a/e")
	if !ok || len(lv.Children) != 1 || lv.Children[0].Kind != "package" {
		t.Fatalf("a/e after apply = %+v ok=%v, want one package child", lv, ok)
	}
}

func TestApplyGroupBecomingPackage(t *testing.T) {
	idx := fixture()
	skel := schema.Index{Packages: idx.Packages[1:]} // no "a" entry: a is a bare group
	tr := Build(skel)
	if n := tr.Apply(idx); n != 1 {
		t.Fatalf("changed = %d, want 1", n)
	}
	levelsMatch(t, tr, Build(idx))
}

func TestApplyRemovalFallsBackToRebuild(t *testing.T) {
	tr := Build(fixture())
	idx := fixture()
	idx.Packages = idx.Packages[:len(idx.Packages)-1] // d vanishes: not a grow
	tr.Apply(idx)
	levelsMatch(t, tr, Build(idx))
	if _, ok := tr.Level("d"); ok {
		t.Fatal("d still present after a snapshot without it")
	}
}

func TestApplyEntrypointRetractionFallsBack(t *testing.T) {
	tr := Build(fixture())
	idx := fixture()
	for i := range idx.Packages {
		idx.Packages[i].IsEntrypoint = false
	}
	tr.Apply(idx)
	levelsMatch(t, tr, Build(idx))
	lv, _ := tr.Level("")
	for _, c := range lv.Children {
		if c.HasEntrypoint {
			t.Fatalf("%s still reports an entrypoint after retraction", c.Path)
		}
	}
}

func TestPathKeyMatchesFullClean(t *testing.T) {
	// pathKey's no-alloc fast path must agree with the real clean on every
	// shape splitPath tolerates.
	cases := []string{
		"", ".", "./a/b", "a/./b", "a//b", "/a", "a/b/", "a/b/.", `a\b`,
		"a", "a/b", "top1/d2/leaf3",
	}
	for _, p := range cases {
		want := strings.Join(splitPath(p), "/")
		if got := pathKey(p); got != want {
			t.Fatalf("pathKey(%q) = %q, want %q", p, got, want)
		}
	}
}

func BenchmarkBuild5000(b *testing.B) {
	idx := genIndex(5000)
	b.ResetTimer()
	for b.Loop() {
		Build(idx)
	}
}

func BenchmarkApplySmallDelta5000(b *testing.B) {
	idx := genIndex(5000)
	tr := Build(idx)
	b.ResetTimer()
	for b.Loop() {
		// 8 packages change per poll; the rest of the snapshot is untouched.
		for j := 0; j < 8; j++ {
			idx.Packages[j*613].SymbolCount ^= 1
		}
		tr.Apply(idx)
	}
}
