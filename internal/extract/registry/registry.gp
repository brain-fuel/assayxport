// Package registry holds the language extractors and dispatches them.
package registry

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"goforge.dev/assayxport/internal/extract"
	"goforge.dev/assayxport/internal/extract/golang"
	"goforge.dev/assayxport/internal/extract/java"
	"goforge.dev/assayxport/internal/extract/python"
	"goforge.dev/assayxport/internal/extract/typescript"
	"goforge.dev/assayxport/internal/schema"
)

// All returns every registered extractor (go, java, python, typescript), in a
// stable order.
func All() []extract.Extractor {
	return []extract.Extractor{golang.New(), java.New(), python.New(), typescript.New()}
}

// Select returns the extractors whose Language() is in langs; error if any
// requested language is not registered (message lists available languages).
// Empty langs => All().
func Select(langs []string) ([]extract.Extractor, error) {
	if len(langs) == 0 {
		return All(), nil
	}
	byLang := map[string]extract.Extractor{}
	var available []string
	for _, e := range All() {
		byLang[e.Language()] = e
		available = append(available, e.Language())
	}
	sort.Strings(available)
	var out []extract.Extractor
	seen := map[string]bool{}
	for _, l := range langs {
		e, ok := byLang[l]
		if !ok {
			return nil, fmt.Errorf("unknown language %q; available: %v", l, available)
		}
		if seen[l] {
			continue // ignore repeated --lang so output is not duplicated
		}
		seen[l] = true
		out = append(out, e)
	}
	return out, nil
}

// Skeleton returns a fast, symbol-free package enumeration for every extractor
// that implements extract.SkeletonExtractor -- the structural tree `ax serve`
// publishes immediately, before any parsing. Extractors that cannot enumerate
// cheaply (e.g. Go, whose package set comes from a full type-load) are skipped;
// their packages simply appear via streamed deltas once extraction reaches them.
// The returned languages are those that contributed a skeleton, folded from the
// per-package Language (an extractor may emit more than one).
func Skeleton(exts []extract.Extractor, root string) (pkgs []schema.Package, languages []string, err error) {
	langSet := map[string]bool{}
	for _, e := range exts {
		se, ok := e.(extract.SkeletonExtractor)
		if !ok {
			continue
		}
		got, serr := se.Skeleton(root)
		if serr != nil {
			return nil, nil, fmt.Errorf("%s skeleton: %w", e.Language(), serr)
		}
		for _, p := range got {
			langSet[p.Language] = true
		}
		pkgs = append(pkgs, got...)
	}
	for l := range langSet {
		languages = append(languages, l)
	}
	sort.Strings(languages)
	return pkgs, languages, nil
}

// Run executes each extractor over root and returns the merged packages, the
// sorted set of languages that produced at least one package, and any
// per-extractor errors that were tolerated (see below).
//
// A single extractor's failure does not abort the scan: in a polyglot tree, a
// Go loader error (e.g. a directory that is not a Go module) must not suppress
// the Python and Java results. When at least one extractor produced packages,
// the failures of the others are returned as `warnings` (a non-fatal nil
// error) so the caller can surface them rather than presenting a silent
// partial manifest. Only when NO extractor produced any package are the errors
// joined into the fatal return, so a genuinely broken single-language scan
// still fails cleanly.
func Run(exts []extract.Extractor, root string) (pkgs []schema.Package, languages []string, warnings []error, err error) {
	langSet := map[string]bool{}
	var errs []error
	for _, e := range exts {
		got, extErr := e.Extract(root)
		if extErr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.Language(), extErr))
			continue
		}
		// Fold in each produced package's own language, not just the extractor's
		// nominal one: a single extractor may emit more than one (the TS extractor
		// tags plain-JS files "javascript" and typed files "typescript").
		for _, p := range got {
			langSet[p.Language] = true
		}
		pkgs = append(pkgs, got...)
	}
	if len(pkgs) == 0 && len(errs) > 0 {
		return nil, nil, nil, errors.Join(errs...)
	}
	for l := range langSet {
		languages = append(languages, l)
	}
	sort.Strings(languages)
	return pkgs, languages, errs, nil
}

// RunStream is the streaming counterpart of Run: each extractor's packages are
// handed to emit one at a time (via ExtractStream where an extractor supports
// it, else by replaying a buffered Extract) rather than accumulated into one
// slice, so a large tree's peak memory stays bounded. The tolerated-failure and
// language-set semantics match Run; emit is called possibly concurrently for
// StreamExtractors, so it must synchronize itself.
func RunStream(exts []extract.Extractor, root string, emit func(schema.Package) error) (languages []string, warnings []error, err error) {
	langSet := map[string]bool{}
	var langMu sync.Mutex
	var errs []error
	produced := false
	for _, e := range exts {
		// A StreamExtractor may call emit concurrently from a worker pool, so the
		// produced-count is atomic and the language-set write is mutex-guarded.
		var got atomic.Int64
		count := func(p schema.Package) error {
			got.Add(1)
			// Fold in the package's own language (an extractor may emit more than
			// one -- e.g. TS tags plain-JS files "javascript").
			langMu.Lock()
			langSet[p.Language] = true
			langMu.Unlock()
			return emit(p)
		}
		var extErr error
		if se, ok := e.(extract.StreamExtractor); ok {
			extErr = se.ExtractStream(root, count)
		} else {
			var pkgs []schema.Package
			pkgs, extErr = e.Extract(root)
			for _, p := range pkgs {
				if err := count(p); err != nil {
					return nil, nil, err
				}
			}
		}
		if extErr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.Language(), extErr))
			continue
		}
		if got.Load() > 0 {
			produced = true
		}
	}
	if !produced && len(errs) > 0 {
		return nil, nil, errors.Join(errs...)
	}
	for l := range langSet {
		languages = append(languages, l)
	}
	sort.Strings(languages)
	return languages, errs, nil
}
