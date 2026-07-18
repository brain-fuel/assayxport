package emit

import "goforge.dev/assayxport/internal/schema"

// This file exposes the per-package shard/entry primitives `ax serve` needs to
// fill an index progressively -- publishing a symbol-free skeleton immediately,
// then writing each package's shard and index entry as extraction streams it in,
// rather than sorting and writing everything at once in Writer.Finalize. Serve
// does not need the byte-identical, sorted output `ax assay` produces, so it
// assigns shard paths in arrival order (via AssignShard against a shared used
// set) instead of Finalize's sorted pass.

// PreparedShard is a marshaled shard plus the counts its index entry needs.
type PreparedShard struct {
	Body        []byte // the shard JSON, ready to write to disk
	SymbolCount int
	Entrypoints int
}

// PrepareShard marshals p's shard (sorted symbols, HTML-safe indented JSON, the
// same bytes Writer.Add would stage). The heavy work -- marshaling -- is pure, so
// a caller can run it off the lock that guards shard-path assignment.
func PrepareShard(p schema.Package) (PreparedShard, error) {
	shard, entrypoints := shardOf(p)
	b, err := marshal(shard)
	if err != nil {
		return PreparedShard{}, err
	}
	return PreparedShard{Body: b, SymbolCount: len(shard.Symbols), Entrypoints: entrypoints}, nil
}

// AssignShard returns a collision-free shard path for a package, recording it in
// used. The caller must serialize calls that share a used map.
func AssignShard(pkgPath, pkgID string, used map[string]bool) string {
	return assignShard(pkgPath, pkgID, used)
}

// EntryOf builds a package's index entry from its shard counts and path.
func EntryOf(p schema.Package, symbolCount, entrypoints int, shard string) schema.PackageEntry {
	return entryOf(p, symbolCount, entrypoints, shard)
}

// SkeletonEntry is the index entry for a not-yet-parsed package: identity and
// level only, no symbol counts and no shard (the shard path and counts arrive
// when the package is actually extracted). It is what `ax serve` publishes for
// the whole tree up front so the explorer can render structure immediately.
func SkeletonEntry(p schema.Package) schema.PackageEntry {
	return schema.PackageEntry{
		ID:       p.ID,
		Language: p.Language,
		Path:     p.Path,
		Name:     p.Name,
		Level:    p.Level,
		Members:  p.Members,
	}
}
