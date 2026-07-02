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

// Run executes each extractor over root and returns the merged packages plus
// the sorted set of languages that produced at least one package.
//
// A single extractor's failure does not abort the scan: in a polyglot tree, a
// Go loader error (e.g. a directory that is not a Go module) must not suppress
// the Python and Java results. Per-extractor errors are collected and returned
// ONLY when no extractor produced any package, so a genuinely broken
// single-language scan still fails cleanly while a mixed tree yields what
// succeeded.
func Run(exts []extract.Extractor, root string) ([]schema.Package, []string, error) {
	var pkgs []schema.Package
	langSet := map[string]bool{}
	var errs []error
	for _, e := range exts {
		got, err := e.Extract(root)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.Language(), err))
			continue
		}
		if len(got) > 0 {
			langSet[e.Language()] = true
		}
		pkgs = append(pkgs, got...)
	}
	if len(pkgs) == 0 && len(errs) > 0 {
		return nil, nil, errors.Join(errs...)
	}
	var languages []string
	for l := range langSet {
		languages = append(languages, l)
	}
	sort.Strings(languages)
	return pkgs, languages, nil
}
