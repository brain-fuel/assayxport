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
