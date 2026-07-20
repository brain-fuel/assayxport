package emit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"goforge.dev/assayxport/internal/schema"
)

// sortSymbols orders a package's symbols deterministically by location then
// name -- the stable order every shard is written in.
func sortSymbols(syms []schema.Symbol) {
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
}

// shardOf builds a package's shard (symbols copied and sorted) and returns the
// count of entrypoint symbols. Shared by Manifest and Writer so buffered and
// streamed output are byte-identical.
func shardOf(p schema.Package) (schema.Shard, int) {
	syms := append([]schema.Symbol(nil), p.Symbols...)
	sortSymbols(syms)
	entrypoints := 0
	for _, s := range syms {
		if s.IsEntrypoint {
			entrypoints++
		}
	}
	return schema.Shard{
		SchemaVersion: schema.Version,
		Package: schema.PackageInfo{
			ID:           p.ID,
			Language:     p.Language,
			Path:         p.Path,
			Name:         p.Name,
			Doc:          p.Doc,
			Level:        p.Level,
			Members:      p.Members,
			IsEntrypoint: p.IsEntrypoint,
			Invocation:   p.Invocation,
		},
		Symbols: syms,
	}, entrypoints
}

// entryOf builds a package's index entry from its shard counts and path.
func entryOf(p schema.Package, symbolCount, entrypoints int, shard string) schema.PackageEntry {
	return schema.PackageEntry{
		ID:              p.ID,
		Language:        p.Language,
		Path:            p.Path,
		Name:            p.Name,
		Doc:             p.Doc,
		Level:           p.Level,
		Members:         p.Members,
		IsEntrypoint:    p.IsEntrypoint,
		Invocation:      p.Invocation,
		SymbolCount:     symbolCount,
		EntrypointCount: entrypoints,
		Shard:           shard,
	}
}

// assignShard returns the shard path for pkgPath, disambiguating against used
// (which it mutates) exactly as Manifest does, so streamed and buffered runs
// choose the same paths. See Manifest for why collisions can occur.
func assignShard(pkgPath, pkgID string, used map[string]bool) string {
	sp := shardPath(pkgPath)
	if used[sp] {
		base := sp[:len(sp)-len(".json")]
		sp = base + "__" + sanitizeID(pkgID) + ".json"
		for i := 2; used[sp]; i++ {
			sp = fmt.Sprintf("%s__%s_%d.json", base, sanitizeID(pkgID), i)
		}
	}
	used[sp] = true
	return sp
}

// pruneShards deletes any *.json under <outDir>/.assayxport/ that is not in the
// written set (keyed by POSIX shard path relative to outDir). Empty
// subdirectories are left in place. Shared by WriteDir and Writer.Finalize.
func pruneShards(outDir string, written map[string]bool) error {
	shardRoot := filepath.Join(outDir, shardDir)
	if _, err := os.Stat(shardRoot); err != nil {
		return nil // nothing written yet
	}
	return filepath.Walk(shardRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		rel, err := filepath.Rel(outDir, path)
		if err != nil {
			return err
		}
		if !written[filepath.ToSlash(rel)] {
			return os.Remove(path)
		}
		return nil
	})
}

// staged is one Add: the package's index entry (its Shard path is filled in at
// Finalize) and the staging file its shard JSON was written to.
type staged struct {
	entry schema.PackageEntry
	stage string
}

// Writer streams a manifest to disk one shard at a time. Add writes each
// package's shard to a staging file and keeps only its lightweight index entry,
// so peak memory is the index metadata plus the in-flight shards -- not the
// whole symbol graph. Finalize sorts the entries, assigns deterministic shard
// paths (moving the staged files into place), writes the index, and prunes
// stale shards. Output is byte-identical to Manifest + WriteDir for equal input.
//
// Add is safe for concurrent use (a Java parse streams from a worker pool);
// Finalize is called exactly once, after every Add has returned.
type Writer struct {
	dir   string
	stage string
	mu    sync.Mutex
	seq   int
	items []staged
}

// NewWriter prepares dir for streaming (creating its shard staging area).
func NewWriter(dir string) (*Writer, error) {
	stage := filepath.Join(dir, shardDir, ".stage")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return nil, err
	}
	return &Writer{dir: dir, stage: stage}, nil
}

// Add writes p's shard to a staging file and records its index entry. The
// package's symbols are needed only for the marshal here; the caller may release
// them once Add returns.
func (w *Writer) Add(p schema.Package) error {
	shard, entrypoints := shardOf(p)
	b, err := marshal(shard)
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.seq++
	stageFile := filepath.Join(w.stage, fmt.Sprintf("%d.json", w.seq))
	w.items = append(w.items, staged{
		entry: entryOf(p, len(shard.Symbols), entrypoints, ""),
		stage: stageFile,
	})
	w.mu.Unlock()
	return os.WriteFile(stageFile, b, 0o644)
}

// Finalize sorts the accumulated entries by id, assigns shard paths (moving each
// staged file into place), writes the index, and prunes stale shards. It returns
// the built index (the same value Manifest would have produced).
func (w *Writer) Finalize(module string, languages []string) (schema.Index, error) {
	sort.Slice(w.items, func(i, j int) bool {
		a, b := w.items[i].entry, w.items[j].entry
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Path < b.Path // total order so streamed == buffered when FQCNs collide
	})

	idx := schema.Index{
		SchemaVersion: schema.Version,
		Tool:          "assayxport",
		Languages:     languages,
		Root:          ".",
		Module:        module,
	}
	used := make(map[string]bool, len(w.items))
	written := make(map[string]bool, len(w.items))
	for i := range w.items {
		it := &w.items[i]
		sp := assignShard(it.entry.Path, it.entry.ID, used)
		it.entry.Shard = sp
		full := filepath.Join(w.dir, filepath.FromSlash(sp))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return schema.Index{}, err
		}
		if err := os.Rename(it.stage, full); err != nil {
			return schema.Index{}, err
		}
		written[sp] = true
		idx.Packages = append(idx.Packages, it.entry)
	}

	idxBytes, err := marshal(idx)
	if err != nil {
		return schema.Index{}, err
	}
	if err := os.WriteFile(filepath.Join(w.dir, "assayxport.json"), idxBytes, 0o644); err != nil {
		return schema.Index{}, err
	}
	_ = os.RemoveAll(w.stage)
	if err := pruneShards(w.dir, written); err != nil {
		return schema.Index{}, err
	}
	return idx, nil
}
