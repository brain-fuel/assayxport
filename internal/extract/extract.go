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
