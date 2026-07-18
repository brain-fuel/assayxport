// Package extract defines the language-agnostic extractor contract.
package extract

import "goforge.dev/assayxport/internal/schema"

// Extractor turns a source tree into a stable list of packages.
type Extractor interface {
	// Language reports the language id this extractor handles, e.g. "go".
	Language() string
	// Extract loads every package under root and returns them sorted by ID.
	Extract(root string) ([]schema.Package, error)
}

// StreamExtractor is an optional extension for languages that can emit packages
// incrementally. When an extractor implements it, the pipeline streams each
// package to disk and releases it, so a huge tree's peak memory is the index
// metadata plus a few in-flight packages rather than every symbol at once.
//
// emit is called once per package, in an unspecified order and possibly
// concurrently (the extractor may parse across a worker pool); the caller's emit
// is responsible for its own synchronization. A non-nil error from emit, or any
// extraction error, aborts the stream. Order-independence is fine because the
// downstream writer sorts by package id.
type StreamExtractor interface {
	Extractor
	ExtractStream(root string, emit func(schema.Package) error) error
}

// SkeletonExtractor is an optional extension for languages that can enumerate
// their packages cheaply, without parsing symbols -- typically straight from the
// file tree. `ax serve` uses it to publish a structural skeleton (every package's
// id/path/name, no symbols) the instant the server starts, so the explorer renders
// the whole package tree in ~1s while the real symbol extraction streams in
// behind it.
//
// The returned packages must carry the SAME ids the extractor's Extract/
// ExtractStream will later produce (both should derive identity from the same file
// walk), so each streamed package's symbols map back onto its skeleton entry.
// Symbols is left nil; only ID, Language, Path, Name, and Level are set.
type SkeletonExtractor interface {
	Extractor
	Skeleton(root string) ([]schema.Package, error)
}
