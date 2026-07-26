// Scaffolded by goplus init. `go generate ./...` regenerates every
// *_gp.go in the module; plain `go build` works from there.
//
// The module has no root Go package, so this directive lives in the
// foundational schema package. It reaches back to the module root because go
// generate runs each directive from its own directory: ./... here would
// regenerate only this package, and goplus gen resolves patterns as
// directories, not module paths.

//go:generate go tool goplus gen ../../...

package schema
