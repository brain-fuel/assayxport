// Package emit builds and writes assayxport's deterministic manifest: a root
// index plus one shard per package. Output is byte-stable for equal inputs.
package emit

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"goforge.dev/assayxport/internal/schema"
)

const shardDir = ".assayxport"

// shardPath returns the POSIX shard path for a package's relative dir.
func shardPath(pkgDir string) string {
	if pkgDir == "" || pkgDir == "." {
		return shardDir + "/_root.json"
	}
	return shardDir + "/" + filepath.ToSlash(pkgDir) + ".json"
}

// Manifest builds the index and shard set from packages, sorting
// deterministically and computing shard paths and counts.
func Manifest(pkgs []schema.Package, module string, languages []string) (schema.Index, map[string]schema.Shard) {
	sorted := append([]schema.Package(nil), pkgs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	idx := schema.Index{
		SchemaVersion: schema.Version,
		Tool:          "assayxport",
		Languages:     languages,
		Root:          ".",
		Module:        module,
	}
	shards := make(map[string]schema.Shard, len(sorted))

	for _, p := range sorted {
		syms := append([]schema.Symbol(nil), p.Symbols...)
		sort.SliceStable(syms, func(i, j int) bool {
			a, b := syms[i].Location, syms[j].Location
			if a.File != b.File {
				return a.File < b.File
			}
			if a.Line != b.Line {
				return a.Line < b.Line
			}
			return syms[i].Name < syms[j].Name
		})
		entrypoints := 0
		for _, s := range syms {
			if s.IsEntrypoint {
				entrypoints++
			}
		}
		sp := shardPath(p.Path)
		idx.Packages = append(idx.Packages, schema.PackageEntry{
			ID:              p.ID,
			Language:        p.Language,
			Path:            p.Path,
			Name:            p.Name,
			Doc:             p.Doc,
			SymbolCount:     len(syms),
			EntrypointCount: entrypoints,
			Shard:           sp,
		})
		shards[sp] = schema.Shard{
			SchemaVersion: schema.Version,
			Package:       schema.PackageInfo{ID: p.ID, Language: p.Language, Path: p.Path, Name: p.Name, Doc: p.Doc},
			Symbols:       syms,
		}
	}
	return idx, shards
}

// marshal renders v as 2-space-indented JSON with a trailing newline and no
// HTML escaping, for stable diffable output.
func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil { // Encode appends a trailing newline
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteDir writes the index and all shards under outDir.
func WriteDir(outDir string, idx schema.Index, shards map[string]schema.Shard) error {
	idxBytes, err := marshal(idx)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "assayxport.json"), idxBytes, 0o644); err != nil {
		return err
	}
	paths := make([]string, 0, len(shards))
	for p := range shards {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		full := filepath.Join(outDir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		b, err := marshal(shards[p])
		if err != nil {
			return err
		}
		if err := os.WriteFile(full, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Combined renders the whole manifest as one JSON blob for --stdout.
func Combined(idx schema.Index, shards map[string]schema.Shard) ([]byte, error) {
	type combined struct {
		Index  schema.Index            `json:"index"`
		Shards map[string]schema.Shard `json:"shards"`
	}
	return marshal(combined{Index: idx, Shards: shards})
}
