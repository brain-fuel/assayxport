package schema

import (
	"path"
	"strings"

	"goforge.dev/goplus/std/result"
)

// Opaque identifiers expose no fields; strings enter only through validation.
type PackageID struct { value string }
type SymbolID struct { value string }
type SymbolRef struct {
	pkg PackageID
	symbol SymbolID
}
type RelativeSourcePath struct { value string }
type ShardPath struct { value string }

type IdentityFailure enum { InvalidIdentity(kind string, value string) }

func NewPackageID(value string) result.Result[PackageID, IdentityFailure] {
	if value == "" || strings.Contains(value, "#") { return result.Err[PackageID, IdentityFailure]{Err: InvalidIdentity("package id", value)} }
	return result.Ok[PackageID, IdentityFailure]{Value: PackageID{value: value}}
}

func NewSymbolID(value string) result.Result[SymbolID, IdentityFailure] {
	if value == "" || strings.Contains(value, "#") { return result.Err[SymbolID, IdentityFailure]{Err: InvalidIdentity("symbol id", value)} }
	return result.Ok[SymbolID, IdentityFailure]{Value: SymbolID{value: value}}
}

func ParseSymbolRef(value string) result.Result[SymbolRef, IdentityFailure] {
	i := strings.LastIndex(value, "#")
	if i <= 0 || i == len(value)-1 { return result.Err[SymbolRef, IdentityFailure]{Err: InvalidIdentity("symbol ref", value)} }
	p, pf := result.Unpack(NewPackageID(value[:i]))
	if pf != nil { return result.Err[SymbolRef, IdentityFailure]{Err: pf} }
	s, sf := result.Unpack(NewSymbolID(value[i+1:]))
	if sf != nil { return result.Err[SymbolRef, IdentityFailure]{Err: sf} }
	return result.Ok[SymbolRef, IdentityFailure]{Value: SymbolRef{pkg: p, symbol: s}}
}

func NewRelativeSourcePath(value string) result.Result[RelativeSourcePath, IdentityFailure] {
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || path.Clean(value) != value || value == "." {
		return result.Err[RelativeSourcePath, IdentityFailure]{Err: InvalidIdentity("relative source path", value)}
	}
	return result.Ok[RelativeSourcePath, IdentityFailure]{Value: RelativeSourcePath{value: value}}
}

func NewShardPath(value string) result.Result[ShardPath, IdentityFailure] {
	if !strings.HasPrefix(value, ".assayxport/") || !strings.HasSuffix(value, ".json") || strings.Contains(value, "\\") || path.Clean(value) != value {
		return result.Err[ShardPath, IdentityFailure]{Err: InvalidIdentity("shard path", value)}
	}
	return result.Ok[ShardPath, IdentityFailure]{Value: ShardPath{value: value}}
}

func PackageIDString(v PackageID) string { return v.value }
func SymbolIDString(v SymbolID) string { return v.value }
func SymbolRefString(v SymbolRef) string { return v.pkg.value + "#" + v.symbol.value }
func RelativeSourcePathString(v RelativeSourcePath) string { return v.value }
func ShardPathString(v ShardPath) string { return v.value }

// MustPackageID is the authoring boundary for APIs where an invalid id is a
// programming error. Data/JSON boundaries should use NewPackageID instead.
func MustPackageID(value string) PackageID {
	id, failure := result.Unpack(NewPackageID(value))
	if failure != nil { panic("assayxport: invalid package id") }
	return id
}

func MustShardPath(value string) ShardPath {
	shard, failure := result.Unpack(NewShardPath(value))
	if failure != nil { panic("assayxport: invalid shard path") }
	return shard
}
