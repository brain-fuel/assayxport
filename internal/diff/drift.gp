package diff

import (
	"sort"
	"strings"

	"goforge.dev/assayxport/internal/schema"
)

// DriftOptions configures drift matching.
type DriftOptions struct {
	// IgnoreParamNames compares signatures by types only, so a parameter
	// rename is not signature drift.
	IgnoreParamNames bool
}

// ComputeDrift compares two package sets by identity: packages by
// (language, id) with a (language, path) fallback for renamed modules,
// symbols by manifest id. It is exact and deterministic; the result's slices
// are always non-nil so the document shape is stable.
//
// The summary's symbol counts cover matched packages only: a package added
// or removed whole is counted once as a package, and its symbols stay in the
// manifests rather than being re-listed here.
func ComputeDrift(a, b []schema.Package, opt DriftOptions) *Drift {
	d := &Drift{Packages: DriftPackages{
		Added:   []PackageStub{},
		Removed: []PackageStub{},
		Changed: []PackageDrift{},
	}}

	type pkgPair struct {
		a, b        schema.Package
		renamedFrom string
	}
	var pairs []pkgPair
	var leftA, leftB []schema.Package

	// Group by (language, id); a colliding id (possible across shard
	// disambiguation) pairs positionally in path order.
	aByID := groupPackages(a, func(p schema.Package) string { return p.Language + "\x00" + p.ID })
	bByID := groupPackages(b, func(p schema.Package) string { return p.Language + "\x00" + p.ID })
	for _, key := range sortedKeys(aByID) {
		ag := aByID[key]
		bg := bByID[key]
		n := len(ag)
		if len(bg) < n {
			n = len(bg)
		}
		for i := 0; i < n; i++ {
			pairs = append(pairs, pkgPair{a: ag[i], b: bg[i]})
		}
		leftA = append(leftA, ag[n:]...)
		if len(bg) > n {
			leftB = append(leftB, bg[n:]...)
		}
		delete(bByID, key)
	}
	for _, key := range sortedKeys(bByID) {
		leftB = append(leftB, bByID[key]...)
	}

	// Fallback: match remaining packages by (language, path), but only when
	// the path is unique on both sides -- anything weaker would fabricate
	// renames.
	pathKey := func(p schema.Package) string { return p.Language + "\x00" + p.Path }
	leftAByPath := groupPackages(leftA, pathKey)
	leftBByPath := groupPackages(leftB, pathKey)
	leftA, leftB = nil, nil
	for _, key := range sortedKeys(leftAByPath) {
		ag := leftAByPath[key]
		bg := leftBByPath[key]
		if len(ag) == 1 && len(bg) == 1 {
			pairs = append(pairs, pkgPair{a: ag[0], b: bg[0], renamedFrom: ag[0].ID})
			delete(leftBByPath, key)
			continue
		}
		leftA = append(leftA, ag...)
	}
	for _, key := range sortedKeys(leftBByPath) {
		leftB = append(leftB, leftBByPath[key]...)
	}

	for _, p := range leftA {
		d.Packages.Removed = append(d.Packages.Removed, PackageStub{ID: p.ID, SymbolCount: len(p.Symbols)})
	}
	for _, p := range leftB {
		d.Packages.Added = append(d.Packages.Added, PackageStub{ID: p.ID, SymbolCount: len(p.Symbols)})
	}
	sortStubs(d.Packages.Added)
	sortStubs(d.Packages.Removed)
	d.Summary.PackagesAdded = len(d.Packages.Added)
	d.Summary.PackagesRemoved = len(d.Packages.Removed)

	for _, pr := range pairs {
		sym, any := diffSymbols(pr.a.Symbols, pr.b.Symbols, opt)
		if !any {
			continue
		}
		d.Packages.Changed = append(d.Packages.Changed, PackageDrift{
			ID:            pr.b.ID,
			RenamedFromID: pr.renamedFrom,
			Symbols:       sym,
		})
		d.Summary.SymbolsAdded += len(sym.Added)
		d.Summary.SymbolsRemoved += len(sym.Removed)
		d.Summary.SymbolsChanged += len(sym.Changed)
	}
	sort.Slice(d.Packages.Changed, func(i, j int) bool {
		return d.Packages.Changed[i].ID < d.Packages.Changed[j].ID
	})
	return d
}

func groupPackages(pkgs []schema.Package, key func(schema.Package) string) map[string][]schema.Package {
	m := map[string][]schema.Package{}
	for _, p := range pkgs {
		m[key(p)] = append(m[key(p)], p)
	}
	for _, g := range m {
		sort.Slice(g, func(i, j int) bool { return g[i].Path < g[j].Path })
	}
	return m
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortStubs(s []PackageStub) {
	sort.Slice(s, func(i, j int) bool { return s[i].ID < s[j].ID })
}

// diffSymbols matches symbols by id. Java overloads share one id, so an id
// maps to a group: exact-shape members pair first, the leftovers pair
// positionally in shape order (reading as signature changes), and surplus
// members report the id as added or removed.
func diffSymbols(a, b []schema.Symbol, opt DriftOptions) (SymbolDrifts, bool) {
	out := SymbolDrifts{Added: []string{}, Removed: []string{}, Changed: []SymbolDrift{}}
	aByID := groupSymbols(a)
	bByID := groupSymbols(b)
	ids := sortedKeys(aByID)
	for _, id := range sortedKeys(bByID) {
		if _, ok := aByID[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		ag, bg := aByID[id], bByID[id]
		switch {
		case len(ag) == 0:
			out.Added = append(out.Added, id)
		case len(bg) == 0:
			out.Removed = append(out.Removed, id)
		default:
			diffSymbolGroup(id, ag, bg, opt, &out)
		}
	}
	sort.Strings(out.Added)
	sort.Strings(out.Removed)
	sort.Slice(out.Changed, func(i, j int) bool {
		ci, cj := out.Changed[i], out.Changed[j]
		if ci.ID != cj.ID {
			return ci.ID < cj.ID
		}
		si, sj := "", ""
		if ci.Signature != nil {
			si = ci.Signature.A + "\x00" + ci.Signature.B
		}
		if cj.Signature != nil {
			sj = cj.Signature.A + "\x00" + cj.Signature.B
		}
		return si < sj
	})
	any := len(out.Added) > 0 || len(out.Removed) > 0 || len(out.Changed) > 0
	return out, any
}

func groupSymbols(syms []schema.Symbol) map[string][]schema.Symbol {
	m := map[string][]schema.Symbol{}
	for _, s := range syms {
		m[s.ID] = append(m[s.ID], s)
	}
	return m
}

func diffSymbolGroup(id string, ag, bg []schema.Symbol, opt DriftOptions, out *SymbolDrifts) {
	shape := func(s schema.Symbol) string { return symbolShape(s, !opt.IgnoreParamNames) }
	// Exact-shape pairing first.
	used := make([]bool, len(bg))
	var restA []schema.Symbol
	for _, as := range ag {
		matched := false
		for i, bs := range bg {
			if !used[i] && shape(as) == shape(bs) {
				used[i] = true
				matched = true
				if sd := compareSymbols(as, bs, opt); sd != nil {
					out.Changed = append(out.Changed, *sd)
				}
				break
			}
		}
		if !matched {
			restA = append(restA, as)
		}
	}
	var restB []schema.Symbol
	for i, bs := range bg {
		if !used[i] {
			restB = append(restB, bs)
		}
	}
	sort.Slice(restA, func(i, j int) bool { return shape(restA[i]) < shape(restA[j]) })
	sort.Slice(restB, func(i, j int) bool { return shape(restB[i]) < shape(restB[j]) })
	n := len(restA)
	if len(restB) < n {
		n = len(restB)
	}
	for i := 0; i < n; i++ {
		if sd := compareSymbols(restA[i], restB[i], opt); sd != nil {
			out.Changed = append(out.Changed, *sd)
		}
	}
	// Surplus overloads: the id gained or lost declarations.
	if len(restA) > n {
		out.Removed = append(out.Removed, id)
	}
	if len(restB) > n {
		out.Added = append(out.Added, id)
	}
}

// compareSymbols reports what changed between two declarations of one id,
// or nil when nothing did. Change kinds appear in canonical order: kind,
// signature, visibility, entrypoint, complexity, calls.
func compareSymbols(a, b schema.Symbol, opt DriftOptions) *SymbolDrift {
	sd := SymbolDrift{ID: a.ID}
	var changes []string
	if a.Kind != b.Kind {
		changes = append(changes, "kind")
		sd.Kind = &AB{A: a.Kind, B: b.Kind}
	}
	shapeA := symbolShape(a, !opt.IgnoreParamNames)
	shapeB := symbolShape(b, !opt.IgnoreParamNames)
	if shapeA != shapeB {
		changes = append(changes, "signature")
		sd.Signature = &AB{A: shapeA, B: shapeB}
	}
	if a.Visibility != b.Visibility || a.VisibilityIdiom != b.VisibilityIdiom {
		av, bv := a.Visibility, b.Visibility
		if av == bv { // idiom-only change
			av, bv = a.VisibilityIdiom, b.VisibilityIdiom
		}
		changes = append(changes, "visibility")
		sd.Visibility = &AB{A: av, B: bv}
	}
	if a.IsEntrypoint != b.IsEntrypoint {
		changes = append(changes, "entrypoint")
		sd.Entrypoint = &ABBool{A: a.IsEntrypoint, B: b.IsEntrypoint}
	}
	if !complexityEqual(a.Complexity, b.Complexity) {
		changes = append(changes, "complexity")
		sd.Complexity = &ABComplexity{A: a.Complexity, B: b.Complexity}
	}
	if cd := diffCalls(a.Calls, b.Calls); cd != nil {
		changes = append(changes, "calls")
		sd.Calls = cd
	}
	if len(changes) == 0 {
		return nil
	}
	sd.Changes = changes
	return &sd
}

// symbolShape renders a symbol's declaration identity as one stable string:
// the signature for function-likes, the type structure for types, the
// declared type for everything else.
func symbolShape(s schema.Symbol, withNames bool) string {
	if s.Signature != nil {
		return renderSignature(s.Signature, withNames)
	}
	if s.Kind == "type" || s.TypeKind != "" {
		return strings.TrimSpace(s.TypeKind + " " + s.Underlying)
	}
	return s.Type
}

// renderSignature is the canonical one-line form used in drift output and
// signature comparison. It is deterministic and language-neutral: modifiers,
// receiver, type params, params (variadic marked on the last), returns,
// declared throws.
func renderSignature(sig *schema.Signature, withNames bool) string {
	var b strings.Builder
	if len(sig.Modifiers) > 0 {
		b.WriteString(strings.Join(sig.Modifiers, " "))
		b.WriteString(" ")
	}
	if sig.Receiver != nil {
		b.WriteString("(")
		b.WriteString(renderParam(*sig.Receiver, withNames))
		b.WriteString(") ")
	}
	if len(sig.TypeParams) > 0 {
		b.WriteString("[")
		for i, tp := range sig.TypeParams {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(strings.TrimSpace(tp.Name + " " + tp.Constraint))
		}
		b.WriteString("]")
	}
	b.WriteString("(")
	for i, p := range sig.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		if sig.Variadic && i == len(sig.Params)-1 {
			b.WriteString("...")
		}
		b.WriteString(renderParam(p, withNames))
	}
	b.WriteString(")")
	if len(sig.Returns) == 1 {
		b.WriteString(" ")
		b.WriteString(renderParam(sig.Returns[0], withNames))
	} else if len(sig.Returns) > 1 {
		b.WriteString(" (")
		for i, r := range sig.Returns {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(renderParam(r, withNames))
		}
		b.WriteString(")")
	}
	if len(sig.Throws) > 0 {
		b.WriteString(" throws ")
		b.WriteString(strings.Join(sig.Throws, ", "))
	}
	return b.String()
}

func renderParam(p schema.Param, withNames bool) string {
	if !withNames || p.Name == "" || p.NameSynthetic {
		if p.Type == "" {
			return p.Name
		}
		return p.Type
	}
	return strings.TrimSpace(p.Name + " " + p.Type)
}

func complexityEqual(a, b schema.Complexity) bool {
	return a.Method == b.Method && strPtrEqual(a.Time, b.Time) && strPtrEqual(a.Space, b.Space)
}

func strPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// diffCalls reports edges present on one side only, keyed by (target, kind).
// Arity/evidence variations of one target collapse into the key: edge
// existence, not call-site detail, is what drift tracks.
func diffCalls(a, b []schema.Call) *CallsDrift {
	aSet := callSet(a)
	bSet := callSet(b)
	cd := &CallsDrift{Added: []CallStub{}, Removed: []CallStub{}}
	for _, k := range sortedKeys(bSet) {
		if _, ok := aSet[k]; !ok {
			cd.Added = append(cd.Added, bSet[k])
		}
	}
	for _, k := range sortedKeys(aSet) {
		if _, ok := bSet[k]; !ok {
			cd.Removed = append(cd.Removed, aSet[k])
		}
	}
	if len(cd.Added) == 0 && len(cd.Removed) == 0 {
		return nil
	}
	return cd
}

func callSet(calls []schema.Call) map[string]CallStub {
	m := map[string]CallStub{}
	for _, c := range calls {
		m[c.Target+"\x00"+c.Kind] = CallStub{Target: c.Target, Kind: c.Kind}
	}
	return m
}
