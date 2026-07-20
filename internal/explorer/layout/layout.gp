// Package layout places one navigation level's nodes deterministically. It
// replaces the old explorer's O(passes*n^2) force relaxation over every package
// at once -- which froze the browser for thousands of packages -- with an O(n)
// phyllotaxis placement over just the current level's breadth (a handful to a
// few hundred nodes). Equal input yields byte-identical positions, so a level
// looks the same every time it is visited.
//
// It has no dependency on syscall/js and is pure arithmetic, so it builds and
// unit-tests on every GOOS; the browser only renders the positions it returns.
package layout

import (
	"math"
	"sort"
)

// goldenAngle is the phyllotaxis increment; successive points at i*goldenAngle
// on a sqrt(i) spiral tile the plane evenly with no two points aligned.
var goldenAngle = math.Pi * (3 - math.Sqrt(5))

// Item is one node to place: a stable ID (drives deterministic ordering and
// any tie-break jitter) and a Radius (its drawn size, so bigger nodes get more
// room).
type Item struct {
	ID     string
	Radius float64
}

// Pos is Item ID's placed center.
type Pos struct {
	ID   string
	X, Y float64
}

// relaxCap bounds the optional overlap-relaxation to levels small enough that
// its O(n^2) passes stay cheap; above it, the phyllotaxis placement alone
// (O(n)) is used, which never freezes regardless of breadth.
const relaxCap = 400

// Place lays out items on a radius-scaled phyllotaxis spiral, largest first,
// then (for small levels) runs a few deterministic relaxation passes to push
// apart any residual overlaps. The result is centered on the origin. O(n) for
// large levels, O(n^2) with a tiny constant only up to relaxCap nodes.
func Place(items []Item) []Pos {
	n := len(items)
	if n == 0 {
		return nil
	}
	// Deterministic order: largest radius first (big islands anchor the
	// center), ties broken by ID so the layout is stable across visits.
	ord := make([]Item, n)
	copy(ord, items)
	sort.Slice(ord, func(i, j int) bool {
		if ord[i].Radius != ord[j].Radius {
			return ord[i].Radius > ord[j].Radius
		}
		return ord[i].ID < ord[j].ID
	})

	maxR := 0.0
	sumR := 0.0
	for _, it := range ord {
		if it.Radius > maxR {
			maxR = it.Radius
		}
		sumR += it.Radius
	}
	avgR := sumR / float64(n)
	// Spiral spacing: wide enough that neighbors on the spiral clear each
	// other. Tie it to the average radius, with the max radius as a floor so a
	// single huge node doesn't overlap its neighbors.
	c := math.Max(avgR*2.1, maxR*1.15)

	xs := make([]float64, n)
	ys := make([]float64, n)
	for i := range ord {
		ang := float64(i) * goldenAngle
		rad := c * math.Sqrt(float64(i))
		xs[i] = math.Cos(ang) * rad
		ys[i] = math.Sin(ang) * rad
	}

	if n <= relaxCap {
		relax(ord, xs, ys)
	}

	// Recenter on the centroid so the level frames symmetrically.
	var cx, cy float64
	for i := range ord {
		cx += xs[i]
		cy += ys[i]
	}
	cx /= float64(n)
	cy /= float64(n)

	out := make([]Pos, n)
	for i, it := range ord {
		out[i] = Pos{ID: it.ID, X: xs[i] - cx, Y: ys[i] - cy}
	}
	// Return in the caller's original item order for stable consumption.
	pos := make(map[string]Pos, n)
	for _, p := range out {
		pos[p.ID] = p
	}
	final := make([]Pos, n)
	for i, it := range items {
		final[i] = pos[it.ID]
	}
	return final
}

// relax runs a fixed number of deterministic separation passes: any two nodes
// closer than their radii plus a margin are pushed apart along their axis, with
// a hashed jitter when exactly coincident so the result stays deterministic
// without depending on floating-point equality.
func relax(items []Item, xs, ys []float64) {
	const passes = 60
	const margin = 12.0
	n := len(items)
	for p := 0; p < passes; p++ {
		moved := false
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				dx := xs[j] - xs[i]
				dy := ys[j] - ys[i]
				d := math.Hypot(dx, dy)
				min := items[i].Radius + items[j].Radius + margin
				if d >= min {
					continue
				}
				if d < 0.01 {
					h := hash(items[i].ID+items[j].ID) * 2 * math.Pi
					dx, dy, d = math.Cos(h), math.Sin(h), 1
				}
				push := (min - d) / 2
				ux, uy := dx/d, dy/d
				xs[i] -= ux * push
				ys[i] -= uy * push
				xs[j] += ux * push
				ys[j] += uy * push
				moved = true
			}
		}
		if !moved {
			break
		}
	}
}

// RadiusFor maps a symbol count to an island radius, matching the explorer's
// sealed-island estimate (the packed sunflower of n symbols grows ~as sqrt(n)),
// so a group island reads at the same scale as the package islands it stands in
// for. Zero symbols still gets a minimum footprint so an empty node is visible.
func RadiusFor(symbols int) float64 {
	if symbols <= 0 {
		return 20
	}
	return 8.9*math.Sqrt(float64(symbols)) + 8
}

// hash is FNV-1a folded to [0,1), matching the JS hashN so Go-side and any
// JS-side placement agree on jitter for coincident nodes.
func hash(s string) float64 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return float64(h) / 4294967296.0
}
