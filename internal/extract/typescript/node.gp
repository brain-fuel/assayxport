package typescript

// Node ecosystem semantics: package.json manifests give a scan its runnable
// entrypoints (bin), its public entry modules (main/module/exports), and doc
// attribution for entry modules; a node shebang marks a script. All of it is
// best-effort -- a missing or malformed package.json never fails a scan.

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"goforge.dev/assayxport/internal/schema"
)

// nodeContext resolves Node semantics for one scan root.
type nodeContext struct {
	binByRel map[string]string // file rel path -> bin name (npm `bin` field)
	entryDoc map[string]string // module id -> owning package.json description
}

// rawManifest is the slice of package.json assayxport reads.
type rawManifest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Bin         json.RawMessage `json:"bin"`
	Main        string          `json:"main"`
	Module      string          `json:"module"`
	Exports     json.RawMessage `json:"exports"`
}

// loadNodeContext walks root for package.json files (same skip rules as source
// discovery) and folds them into one lookup context. Errors reading or parsing
// any single manifest are ignored: Node semantics degrade, the scan proceeds.
func loadNodeContext(root string) *nodeContext {
	nc := &nodeContext{binByRel: map[string]string{}, entryDoc: map[string]string{}}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nc
	}
	_ = filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != absRoot {
				base := d.Name()
				if strings.HasPrefix(base, ".") || skipDirs[base] {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if d.Name() != "package.json" {
			return nil
		}
		rel, rerr := filepath.Rel(absRoot, p)
		if rerr != nil {
			return nil
		}
		nc.addManifest(filepath.ToSlash(filepath.Dir(rel)), p)
		return nil
	})
	return nc
}

// addManifest parses one package.json and records its bin targets and entry
// modules. relDir is the manifest's directory relative to the scan root
// ("." for the root manifest).
func (nc *nodeContext) addManifest(relDir, absPath string) {
	src, err := os.ReadFile(absPath)
	if err != nil {
		return
	}
	var m rawManifest
	if json.Unmarshal(src, &m) != nil {
		return
	}
	for name, target := range binEntries(m) {
		nc.binByRel[joinRel(relDir, target)] = name
	}
	for _, target := range entryTargets(m) {
		id := moduleID(joinRel(relDir, target))
		if _, taken := nc.entryDoc[id]; !taken {
			nc.entryDoc[id] = m.Description
		}
	}
}

// binEntries normalizes the `bin` field: a bare string names one bin after the
// package (scope stripped, npm's rule), an object maps bin names to files.
func binEntries(m rawManifest) map[string]string {
	out := map[string]string{}
	if len(m.Bin) == 0 {
		return out
	}
	var one string
	if json.Unmarshal(m.Bin, &one) == nil {
		name := m.Name
		if i := strings.LastIndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		}
		if name != "" && one != "" {
			out[name] = one
		}
		return out
	}
	var many map[string]string
	if json.Unmarshal(m.Bin, &many) == nil {
		for k, v := range many {
			if k != "" && v != "" {
				out[k] = v
			}
		}
	}
	return out
}

// entryTargets collects the file paths package.json names as entry modules:
// main, module, and every string leaf of the exports map (conditions and
// subpaths alike), excluding ambient declaration files.
func entryTargets(m rawManifest) []string {
	var out []string
	add := func(p string) {
		if p != "" && !strings.HasSuffix(p, ".d.ts") && !strings.HasSuffix(p, ".json") {
			out = append(out, p)
		}
	}
	add(m.Main)
	add(m.Module)
	if len(m.Exports) > 0 {
		var v interface{}
		if json.Unmarshal(m.Exports, &v) == nil {
			collectStringLeaves(v, add)
		}
	}
	return out
}

func collectStringLeaves(v interface{}, add func(string)) {
	switch t := v.(type) {
	case string:
		add(t)
	case map[string]interface{}:
		for _, sub := range t {
			collectStringLeaves(sub, add)
		}
	case []interface{}:
		for _, sub := range t {
			collectStringLeaves(sub, add)
		}
	}
}

// joinRel resolves a manifest-relative file reference ("./src/cli.ts") to a
// slash path relative to the scan root.
func joinRel(relDir, target string) string {
	target = strings.TrimPrefix(target, "./")
	if relDir == "." || relDir == "" {
		return path.Clean(target)
	}
	return path.Clean(path.Join(relDir, target))
}

// isNodeShebang reports whether src opens with a `#!` line that runs node.
func isNodeShebang(src []byte) bool {
	if !bytes.HasPrefix(src, []byte("#!")) {
		return false
	}
	line := src
	if i := bytes.IndexByte(src, '\n'); i >= 0 {
		line = src[:i]
	}
	return bytes.Contains(line, []byte("node"))
}

// applyNode marks a freshly extracted module package with its Node semantics:
// npm bin targets and node-shebang scripts become entrypoints (mirrored onto a
// top-level `main` function, as the Python extractor does), and a package entry
// module with no doc of its own inherits its package.json description.
func applyNode(pkg *schema.Package, rel string, src []byte, nc *nodeContext) {
	if nc == nil {
		return
	}
	var inv *schema.Invocation
	if name, ok := nc.binByRel[rel]; ok {
		inv = &schema.Invocation{Kind: "bin", How: "npx " + name}
	} else if isNodeShebang(src) {
		inv = &schema.Invocation{Kind: "script", How: "node " + rel}
	}
	if inv != nil {
		pkg.IsEntrypoint = true
		pkg.Invocation = inv
		for i := range pkg.Symbols {
			s := &pkg.Symbols[i]
			if s.Name == "main" && s.Kind == "function" && s.Owner == "" {
				s.IsEntrypoint = true
				s.Invocation = inv
			}
		}
	}
	if pkg.Doc == "" {
		if desc, ok := nc.entryDoc[pkg.ID]; ok {
			pkg.Doc = desc
		}
	}
}
