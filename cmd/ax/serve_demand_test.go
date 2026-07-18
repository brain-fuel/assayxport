package main

import (
	"sort"
	"testing"
	"time"
)

func TestUnderFocus(t *testing.T) {
	cases := []struct {
		pkgPath string
		focus   []string
		want    bool
	}{
		{"core/math/vec.ts", []string{"core/math"}, true}, // below the focused dir
		{"core/geometry.ts", []string{"core"}, true},      // a file directly under it
		{"web/app.tsx", []string{"core"}, false},          // a different subtree
		{"core.ts", []string{"core"}, false},              // sibling file, not under core/
		{"anything/x.ts", []string{""}, true},             // root focus matches all
		{"a/b.ts", []string{"z", "a"}, true},              // union: matches the second path
		{"a/b.ts", nil, false},                            // no focus at all
		{"core/math", []string{"core/math"}, true},        // exact path
	}
	for _, c := range cases {
		if got := underFocus(c.pkgPath, c.focus); got != c.want {
			t.Errorf("underFocus(%q, %v) = %v, want %v", c.pkgPath, c.focus, got, c.want)
		}
	}
}

// TestFocusRegistryUnionAndTTL covers the per-client merge: two clients' paths
// union, a dropped client leaves, and a client older than focusTTL expires.
func TestFocusRegistryUnionAndTTL(t *testing.T) {
	clock := time.Unix(1000, 0)
	fr := newFocusRegistry()
	fr.now = func() time.Time { return clock }

	fr.set("a", "core")
	fr.set("b", "web")
	if paths := sortedPaths(fr); !equal(paths, []string{"core", "web"}) {
		t.Fatalf("union = %v, want [core web]", paths)
	}

	// Same path from two clients dedups to one.
	fr.set("c", "core")
	if paths := sortedPaths(fr); !equal(paths, []string{"core", "web"}) {
		t.Fatalf("after dup = %v, want [core web]", paths)
	}

	// Dropping b removes web (a and c still hold core).
	fr.drop("b")
	if paths := sortedPaths(fr); !equal(paths, []string{"core"}) {
		t.Fatalf("after drop b = %v, want [core]", paths)
	}

	// Advancing past focusTTL without a refresh expires a and c.
	clock = clock.Add(focusTTL + time.Second)
	if paths := sortedPaths(fr); len(paths) != 0 {
		t.Fatalf("after TTL = %v, want empty", paths)
	}
}

func sortedPaths(fr *focusRegistry) []string {
	p := fr.livePaths()
	sort.Strings(p)
	return p
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
