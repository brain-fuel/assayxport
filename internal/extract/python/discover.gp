// Package python extracts a Python source tree into assayxport's schema.
package python

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type pyFile struct {
	Abs       string
	Rel       string
	ModuleID  string
	PackageID string
	IsInit    bool
}

func discover(root string) ([]pyFile, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var out []pyFile
	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// skip dot-dirs and common noise
			base := d.Name()
			if path != absRoot && (strings.HasPrefix(base, ".") || base == "__pycache__") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".py") {
			return nil
		}
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return err
		}
		mod, pkg, isInit := dottedPath(filepath.Dir(path), d.Name(), absRoot)
		out = append(out, pyFile{
			Abs:       path,
			Rel:       filepath.ToSlash(rel),
			ModuleID:  mod,
			PackageID: pkg,
			IsInit:    isInit,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, nil
}

// dottedPath climbs package dirs (those with __init__.py) to build the dotted
// module id, the containing package id, and whether the file is __init__.py.
// The climb is bounded by absRoot: the scan root and any ancestor above it are
// never folded into an id, so host directory names outside the scan cannot leak
// in (determinism / "no host data"). A scan root that is itself a package dir
// therefore does not contribute its basename to module ids.
func dottedPath(dir, filename, absRoot string) (moduleID, packageID string, isInit bool) {
	isInit = filename == "__init__.py"
	// Collect ancestor package names while each dir (strictly below absRoot)
	// has __init__.py.
	var names []string
	d := dir
	for d != absRoot && isPackageDir(d) {
		names = append(names, filepath.Base(d))
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	// names is bottom-up; reverse to top-down.
	for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
		names[i], names[j] = names[j], names[i]
	}
	pkgPath := strings.Join(names, ".")
	packageID = pkgPath
	if isInit {
		moduleID = pkgPath // __init__.py names the package itself
		return
	}
	stem := strings.TrimSuffix(filename, ".py")
	if pkgPath == "" {
		moduleID = stem // top-level module, no package
		return
	}
	moduleID = pkgPath + "." + stem
	return
}

func isPackageDir(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "__init__.py"))
	return err == nil && !info.IsDir()
}
