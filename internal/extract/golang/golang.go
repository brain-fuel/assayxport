// Package golang extracts a Go source tree into assayxport's schema using
// go/packages for loading and go/types for accurate, machine-stable types.
package golang

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"goforge.dev/assayxport/internal/schema"
	"golang.org/x/tools/go/packages"
)

// Extractor is the Go language extractor.
type Extractor struct{}

// New returns a Go extractor.
func New() *Extractor { return &Extractor{} }

// Language reports the language id.
func (*Extractor) Language() string { return "go" }

const loadMode = packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
	packages.NeedTypes | packages.NeedTypesInfo | packages.NeedModule |
	packages.NeedImports | packages.NeedDeps

// Extract loads ./... under root and returns packages sorted by import path.
func (e *Extractor) Extract(root string) ([]schema.Package, error) {
	cfg := &packages.Config{
		Mode:  loadMode,
		Dir:   root,
		Tests: false,
	}
	loaded, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("load packages: %w", err)
	}
	var loadErrs []string
	packages.Visit(loaded, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			loadErrs = append(loadErrs, e.Error())
		}
	})
	if len(loadErrs) > 0 {
		return nil, fmt.Errorf("package load errors:\n%s", strings.Join(loadErrs, "\n"))
	}

	moduleDir := root
	out := make([]schema.Package, 0, len(loaded))
	for _, p := range loaded {
		if p.Module != nil {
			moduleDir = p.Module.Dir
		}
		out = append(out, schema.Package{
			ID:       p.PkgPath,
			Language: "go",
			Name:     p.Name,
			Doc:      packageDoc(p),
			// Path filled below once moduleDir is known.
		})
	}
	// Compute each package's module-relative directory.
	for i, p := range loaded {
		dir := packageDir(p)
		rel, err := filepath.Rel(moduleDir, dir)
		if err != nil || strings.HasPrefix(rel, "..") {
			rel = filepath.Base(dir)
		}
		out[i].Path = filepath.ToSlash(rel)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// packageDir returns the directory holding a package's first Go file.
func packageDir(p *packages.Package) string {
	if len(p.GoFiles) > 0 {
		return filepath.Dir(p.GoFiles[0])
	}
	return ""
}

// packageDoc returns the package-level doc comment text, if any.
func packageDoc(p *packages.Package) string {
	for _, f := range p.Syntax {
		if f.Doc != nil {
			return strings.TrimSpace(f.Doc.Text())
		}
	}
	return ""
}
