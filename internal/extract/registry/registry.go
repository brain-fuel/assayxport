// Package registry holds the language extractors and dispatches them.
package registry

import (
	"errors"
	"fmt"
	"sort"

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
