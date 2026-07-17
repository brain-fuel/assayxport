// Package registry holds the language extractors and dispatches them.
package registry

import (
	"errors"
	"fmt"
	"sort"
	"sync/atomic"

	"goforge.dev/assayxport/internal/extract"
	"goforge.dev/assayxport/internal/extract/golang"
	"goforge.dev/assayxport/internal/extract/java"
	"goforge.dev/assayxport/internal/extract/python"
	"goforge.dev/assayxport/internal/schema"
)

// All returns every registered extractor (go, java, python), in a stable order.
func All() []extract.Extractor {
	return []extract.Extractor{golang.New(), java.New(), python.New()}
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
		if len(got) > 0 {
			langSet[e.Language()] = true
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
	var errs []error
	produced := false
	for _, e := range exts {
		// A StreamExtractor may call emit concurrently from a worker pool, so the
		// produced-count is atomic.
		var got atomic.Int64
		count := func(p schema.Package) error {
			got.Add(1)
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
			langSet[e.Language()] = true
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
