// Package typescript implements the assayxport Extractor for TypeScript and
// JavaScript source trees. It produces one schema.Package per source file (level
// "module"), keyed by the file's slash path without extension so ids never embed
// host paths above the scan root. Plain JavaScript files are extracted the same
// way but flagged as untyped: assayxport's TS support is oriented at the newest
// standards, so a symbol that "works" yet forfeits its type contract (an `any`,
// an `as any`, a non-null `!`, a `@ts-ignore`, an untyped parameter, loose
// equality) carries a Concern the explorer surfaces.
package typescript

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"goforge.dev/assayxport/internal/schema"
	"goforge.dev/assayxport/internal/ts"
)

// grammar selects which tree-sitter grammar parses a file (see langFor).
const (
	grammarTS = iota
	grammarTSX
	grammarJS
)

func tsGrammar(g int) ts.Language {
	switch g {
	case grammarTSX:
		return ts.TSX
	case grammarJS:
		return ts.JavaScript
	default:
		return ts.TypeScript
	}
}

// Extractor implements extract.Extractor for TypeScript/JavaScript.
type Extractor struct{}

// New returns a new TypeScript/JavaScript Extractor.
func New() *Extractor { return &Extractor{} }

// Language returns "typescript". The extractor also handles JavaScript; each
// module records its own Language ("typescript" or "javascript").
func (*Extractor) Language() string { return "typescript" }

// moduleID is the file's slash path without its extension -- the manifest id and
// the target of internal call refs. e.g. "core/geometry.ts" -> "core/geometry".
func moduleID(rel string) string {
	rel = filepath.ToSlash(rel)
	for _, ext := range []string{".d.ts", ".tsx", ".ts", ".mts", ".cts", ".jsx", ".js", ".mjs", ".cjs"} {
		if strings.HasSuffix(rel, ext) {
			return strings.TrimSuffix(rel, ext)
		}
	}
	return rel
}

// baseStem is the file's name without directory or extension.
func baseStem(rel string) string {
	return moduleID(filepath.Base(rel))
}

// Extract discovers all TS/JS files under root and returns one module package
// per file, sorted by id. Files are parsed in parallel (the tree-sitter backend
// is cgo-free with a fresh parser per call); results are folded deterministically.
func (*Extractor) Extract(root string) ([]schema.Package, error) {
	files, err := discover(root)
	if err != nil {
		return nil, err
	}

	type result struct {
		pkg schema.Package
		err error
	}
	results := make([]result, len(files))

	workers := runtime.NumCPU() - 1
	if workers < 1 {
		workers = 1
	}
	var wg sync.WaitGroup
	jobs := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				f := files[i]
				src, rerr := os.ReadFile(f.Abs)
				if rerr != nil {
					results[i] = result{err: rerr}
					continue
				}
				g, isTS, _ := langFor(filepath.Base(f.Rel))
				id := moduleID(f.Rel)
				syms, moduleDoc, cerr := moduleSymbols(tsGrammar(g), f.Rel, id, src, isTS)
				if cerr != nil {
					results[i] = result{err: cerr}
					continue
				}
				lang := "typescript"
				if !isTS {
					lang = "javascript"
				}
				results[i] = result{pkg: schema.Package{
					ID:       id,
					Language: lang,
					Path:     f.Rel,
					Name:     baseStem(f.Rel),
					Level:    "module",
					Doc:      moduleDoc,
					Symbols:  syms,
				}}
			}
		}()
	}
	for i := range files {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	out := make([]schema.Package, 0, len(files))
	for i := range results {
		if results[i].err != nil {
			return nil, results[i].err
		}
		out = append(out, results[i].pkg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
