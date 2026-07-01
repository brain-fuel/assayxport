// Package registry holds the language extractors and dispatches them.
package registry

import (
	"fmt"
	"sort"

	"goforge.dev/assayxport/internal/extract"
	"goforge.dev/assayxport/internal/extract/golang"
	"goforge.dev/assayxport/internal/extract/python"
	"goforge.dev/assayxport/internal/schema"
)

// All returns every registered extractor (go, python), in a stable order.
func All() []extract.Extractor {
	return []extract.Extractor{golang.New(), python.New()}
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
	for _, l := range langs {
		e, ok := byLang[l]
		if !ok {
			return nil, fmt.Errorf("unknown language %q; available: %v", l, available)
		}
		out = append(out, e)
	}
	return out, nil
}

// Run executes each extractor over root and returns the merged packages plus
// the sorted set of languages that produced at least one package.
func Run(exts []extract.Extractor, root string) ([]schema.Package, []string, error) {
	var pkgs []schema.Package
	langSet := map[string]bool{}
	for _, e := range exts {
		got, err := e.Extract(root)
		if err != nil {
			return nil, nil, err
		}
		if len(got) > 0 {
			langSet[e.Language()] = true
		}
		pkgs = append(pkgs, got...)
	}
	var languages []string
	for l := range langSet {
		languages = append(languages, l)
	}
	sort.Strings(languages)
	return pkgs, languages, nil
}
