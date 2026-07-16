package layout

import (
	"math"
	"testing"
)

func items(n int) []Item {
	out := make([]Item, n)
	for i := range out {
		out[i] = Item{ID: string(rune('a'+i%26)) + string(rune('0'+i/26)), Radius: 20 + float64(i%7)*5}
	}
	return out
}

func TestDeterministic(t *testing.T) {
	a := Place(items(50))
	b := Place(items(50))
	if len(a) != 50 {
		t.Fatalf("got %d positions", len(a))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("non-deterministic at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestOrderPreserved(t *testing.T) {
	in := items(10)
	out := Place(in)
	for i := range in {
		if out[i].ID != in[i].ID {
			t.Fatalf("position %d is %q, want input order %q", i, out[i].ID, in[i].ID)
		}
	}
}

func TestSmallLevelNoOverlap(t *testing.T) {
	// After relaxation, a small level should have no badly overlapping discs.
	in := items(40)
	out := Place(in)
	byID := map[string]Pos{}
	rad := map[string]float64{}
	for _, p := range out {
		byID[p.ID] = p
	}
	for _, it := range in {
		rad[it.ID] = it.Radius
	}
	overlaps := 0
	for i := 0; i < len(in); i++ {
		for j := i + 1; j < len(in); j++ {
			a, b := out[i], out[j]
			d := math.Hypot(a.X-b.X, a.Y-b.Y)
			// Allow a small tolerance below radius sum (margin not fully met is ok).
			if d < (rad[a.ID]+rad[b.ID])*0.9 {
				overlaps++
			}
		}
	}
	if overlaps > 0 {
		t.Fatalf("%d overlapping pairs after relax in a 40-node level", overlaps)
	}
}

func TestLargeLevelIsFastAndFinite(t *testing.T) {
	// Above relaxCap we skip relaxation; this must still return finite, unique-ish
	// spiral positions quickly (the whole point: no O(n^2) freeze at scale).
	in := items(5000)
	out := Place(in)
	if len(out) != 5000 {
		t.Fatalf("got %d", len(out))
	}
	for _, p := range out {
		if math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) {
			t.Fatalf("non-finite position %+v", p)
		}
	}
}

func TestCenteredOnOrigin(t *testing.T) {
	out := Place(items(100))
	var cx, cy float64
	for _, p := range out {
		cx += p.X
		cy += p.Y
	}
	cx /= float64(len(out))
	cy /= float64(len(out))
	if math.Abs(cx) > 1e-6 || math.Abs(cy) > 1e-6 {
		t.Fatalf("centroid not at origin: (%g,%g)", cx, cy)
	}
}

func TestRadiusForMonotonic(t *testing.T) {
	if RadiusFor(0) <= 0 {
		t.Fatal("empty node must still have a footprint")
	}
	if RadiusFor(100) <= RadiusFor(10) {
		t.Fatal("radius should grow with symbol count")
	}
}

func TestEmpty(t *testing.T) {
	if Place(nil) != nil {
		t.Fatal("nil in, nil out")
	}
}
