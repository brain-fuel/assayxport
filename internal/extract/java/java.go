// Package java implements the assayxport Extractor for Java source trees. It
// produces one schema.Package per .java compilation unit (level "module") and
// one per Java package declaration (level "package"). Packages are keyed by the
// `package` declaration, not by directory, so ids never embed host paths.
package java

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"goforge.dev/assayxport/internal/schema"
)

// Extractor implements extract.Extractor for Java.
type Extractor struct{}

// New returns a new Java Extractor.
func New() *Extractor { return &Extractor{} }

// Language returns "java".
func (*Extractor) Language() string { return "java" }

// Extract discovers all .java files under root and returns one module unit per
// file and one package unit per package declaration, sorted by ID.
func (*Extractor) Extract(root string) ([]schema.Package, error) {
	files, err := discover(root)
	if err != nil {
		return nil, err
	}

	moduleUnits := make(map[string]schema.Package)  // moduleID -> Package
	packageUnits := make(map[string]schema.Package) // packageID -> Package
	// pkgDir records a representative directory per package for its Path, and
	// pkgHasInfo marks packages whose Path came from package-info.java (which
	// wins over a plain compilation unit's directory).
	pkgDir := make(map[string]string)
	pkgHasInfo := make(map[string]bool)

	// Parse every file in parallel. compilationUnit is pure per file (its result
	// depends only on that file's bytes) and the tree-sitter backend is cgo-free
	// with a fresh parser per call, so N workers parse N files with no shared
	// mutable state. The order-dependent bookkeeping below then runs serially in
	// the original sorted file order, so output stays byte-identical to a serial
	// parse.
	type parsedCU struct {
		res cuResult
		err error
	}
	parsed := make([]parsedCU, len(files))
	// Leave one core unused so a caller sharing this process -- notably
	// `ax serve`, which parses in a background goroutine while its HTTP server
	// answers "analyzing" on the main one -- stays responsive during the parse.
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
				src, rerr := os.ReadFile(files[i].Abs)
				if rerr != nil {
					parsed[i] = parsedCU{err: rerr}
					continue
				}
				res, cerr := compilationUnit(files[i].Rel, src)
				parsed[i] = parsedCU{res: res, err: cerr}
			}
		}()
	}
	for i := range files {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	// Fold per-file results in sorted file order so the "first file wins"
	// package-directory choice (pkgDir below) stays deterministic.
	for i := range files {
		f := files[i]
		if parsed[i].err != nil {
			return nil, parsed[i].err
		}
		res := parsed[i].res

		stem := strings.TrimSuffix(filepath.Base(f.Rel), ".java")
		dir := filepath.ToSlash(filepath.Dir(f.Rel))
		if dir == "." {
			dir = ""
		}

		// package-info.java carries no module symbols; it supplies the package
		// doc and a preferred package Path.
		if res.IsPackageInfo {
			if res.PackageName != "" {
				pkg := packageUnits[res.PackageName]
				pkg.ID = res.PackageName
				pkg.Language = "java"
				pkg.Level = "package"
				pkg.Name = simpleName(res.PackageName)
				pkg.Doc = res.PackageDoc
				packageUnits[res.PackageName] = pkg
				pkgDir[res.PackageName] = dir
				pkgHasInfo[res.PackageName] = true
			}
			continue
		}

		// Module id and entrypoint FQCN.
		moduleID := stem
		if res.PackageName != "" {
			moduleID = res.PackageName + "." + stem
		}
		mod := schema.Package{
			ID:       moduleID,
			Language: "java",
			Path:     f.Rel,
			Name:     stem,
			Level:    "module",
			Symbols:  res.Syms,
		}
		if res.HasMain {
			fqcn := res.MainType
			if res.PackageName != "" {
				fqcn = res.PackageName + "." + res.MainType
			}
			how := "java " + fqcn
			mod.IsEntrypoint = true
			// The module unit and the stamped main symbol each get their own
			// Invocation value (same fields) to avoid sharing one pointer.
			mod.Invocation = &schema.Invocation{Kind: "class", How: how}
			for i := range mod.Symbols {
				s := &mod.Symbols[i]
				if s.Kind == "method" && s.Name == "main" && s.Owner == res.MainType {
					s.IsEntrypoint = true
					s.Invocation = &schema.Invocation{Kind: "class", How: how}
				}
			}
		}
		moduleUnits[moduleID] = mod

		// Ensure a package unit exists for this file's package (unless default
		// package). package-info.java Path wins; otherwise first file's dir.
		if res.PackageName != "" {
			if _, ok := packageUnits[res.PackageName]; !ok {
				packageUnits[res.PackageName] = schema.Package{
					ID:       res.PackageName,
					Language: "java",
					Level:    "package",
					Name:     simpleName(res.PackageName),
				}
			}
			if !pkgHasInfo[res.PackageName] {
				if _, ok := pkgDir[res.PackageName]; !ok {
					pkgDir[res.PackageName] = dir // files are sorted, so this is the first
				}
			}
		}
	}

	// Assign package Paths and Members. A unit's direct parent is its id minus
	// the last dotted segment, so bucketing every module and package unit under
	// its parent is one O(M+P) pass -- versus scanning all units for every
	// package (O(P*(M+P))), which on a large tree is tens of millions of prefix
	// comparisons.
	memberLists := make(map[string][]string, len(packageUnits))
	addMember := func(childID string) {
		parent := parentID(childID)
		if _, ok := packageUnits[parent]; ok {
			memberLists[parent] = append(memberLists[parent], childID)
		}
	}
	for modID := range moduleUnits {
		addMember(modID)
	}
	for subID := range packageUnits {
		addMember(subID)
	}
	for pkgID := range packageUnits {
		pkg := packageUnits[pkgID]
		members := memberLists[pkgID]
		sort.Strings(members)
		pkg.Members = members
		pkg.Path = pkgDir[pkgID]
		packageUnits[pkgID] = pkg
	}

	out := make([]schema.Package, 0, len(moduleUnits)+len(packageUnits))
	for _, p := range moduleUnits {
		out = append(out, p)
	}
	for _, p := range packageUnits {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
