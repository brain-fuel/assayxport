// Package golang extracts a Go source tree into assayxport's schema using
// go/packages for loading and go/types for accurate, machine-stable types.
package golang

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"goforge.dev/assayxport/internal/extract"
	"goforge.dev/assayxport/internal/schema"
	"golang.org/x/tools/go/packages"
)

// Compile-time assertion: *Extractor must satisfy extract.Extractor.
var _ extract.Extractor = (*Extractor)(nil)

// Extractor is the Go language extractor.
type Extractor struct{ module string }

// New returns a Go extractor.
func New() *Extractor { return &Extractor{} }

// Module returns the module path discovered by the most recent Extract call.
func (e *Extractor) Module() string { return e.module }

// Language reports the language id.
func (*Extractor) Language() string { return "go" }

const loadMode = packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
	packages.NeedTypes | packages.NeedTypesInfo | packages.NeedModule |
	packages.NeedImports | packages.NeedDeps

// Extract loads ./... under root and returns packages sorted by import path.
func (e *Extractor) Extract(root string) ([]schema.Package, error) {
	// Abs-resolve so module-relative paths are always correct.
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
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
			e.module = p.Module.Path
		}
		out = append(out, schema.Package{
			ID:       p.PkgPath,
			Language: "go",
			Name:     p.Name,
			Doc:      packageDoc(p),
			// Path and Symbols filled below once moduleDir is known.
		})
	}
	// Compute each package's module-relative directory and extract symbols.
	for i, p := range loaded {
		dir := packageDir(p)
		rel, err := filepath.Rel(moduleDir, dir)
		if err != nil || strings.HasPrefix(rel, "..") {
			rel = filepath.Base(dir)
		}
		out[i].Path = filepath.ToSlash(rel)
		out[i].Symbols = extractSymbols(p, moduleDir)
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

// filepathRel is filepath.Rel, isolated so both files in this package share one impl.
func filepathRel(base, target string) (string, error) { return filepath.Rel(base, target) }

// relFile returns the module-relative POSIX path of an absolute file path.
func relFile(absFile, moduleDir string) string {
	rel, err := filepathRel(moduleDir, absFile)
	if err != nil {
		return filepath.ToSlash(absFile)
	}
	return filepath.ToSlash(rel)
}

// entrypointHow renders the `go run` invocation for a main package, using its
// module-relative directory (e.g. "go run ./cmd/tool").
func entrypointHow(p *packages.Package, moduleDir string) string {
	dir := packageDir(p)
	rel, err := filepathRel(moduleDir, dir)
	if err != nil {
		return "go run " + filepath.ToSlash(dir)
	}
	return "go run ./" + filepath.ToSlash(rel)
}
