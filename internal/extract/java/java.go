// Package java implements the assayxport Extractor for Java source trees. It
// produces one schema.Package per .java compilation unit (level "module") and
// one per Java package declaration (level "package"). Packages are keyed by the
// `package` declaration, not by directory, so ids never embed host paths.
package java

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

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

	for _, f := range files {
		src, rerr := os.ReadFile(f.Abs)
		if rerr != nil {
			return nil, rerr
		}
		res, cerr := compilationUnit(f.Rel, src)
		if cerr != nil {
			return nil, cerr
		}

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

	// Assign package Paths and Members.
	memberLists := make(map[string][]string, len(packageUnits))
	for pkgID := range packageUnits {
		var members []string
		for modID := range moduleUnits {
			if isDirectChild(pkgID, modID) {
				members = append(members, modID)
			}
		}
		for subID := range packageUnits {
			if subID != pkgID && isDirectChild(pkgID, subID) {
				members = append(members, subID)
			}
		}
		sort.Strings(members)
		memberLists[pkgID] = members
	}
	for pkgID, members := range memberLists {
		pkg := packageUnits[pkgID]
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

// isDirectChild reports whether childID is a direct child of parentID in dotted
// terms: parentID is a proper prefix and exactly one more segment follows.
func isDirectChild(parentID, childID string) bool {
	if parentID == "" {
		return childID != "" && !strings.Contains(childID, ".")
	}
	prefix := parentID + "."
	if !strings.HasPrefix(childID, prefix) {
		return false
	}
	rest := childID[len(prefix):]
	return rest != "" && !strings.Contains(rest, ".")
}
