// Package schema defines the assayxport manifest types and their stable JSON
// encoding. Version is the schema_version string emitted in every artifact.
package schema

// Version is the schema_version value written into every index and shard.
const Version = "1"

// Index is the root manifest written to assayxport.json.
type Index struct {
	SchemaVersion string         `json:"schema_version"`
	Tool          string         `json:"tool"`
	Languages     []string       `json:"languages"`
	Root          string         `json:"root"`
	Module        string         `json:"module,omitempty"`
	Packages      []PackageEntry `json:"packages"`
}

// PackageEntry is one package's summary in the index, pointing at its shard.
type PackageEntry struct {
	ID              string `json:"id"`
	Language        string `json:"language"`
	Path            string `json:"path"`
	Name            string `json:"name"`
	Doc             string `json:"doc"`
	SymbolCount     int    `json:"symbol_count"`
	EntrypointCount int    `json:"entrypoint_count"`
	Shard           string `json:"shard"`
}

// Shard is one package's full symbol listing, written to .assayxport/<dir>.json.
type Shard struct {
	SchemaVersion string      `json:"schema_version"`
	Package       PackageInfo `json:"package"`
	Symbols       []Symbol    `json:"symbols"`
}

// PackageInfo identifies a package inside its shard.
type PackageInfo struct {
	ID       string `json:"id"`
	Language string `json:"language"`
	Path     string `json:"path"`
	Name     string `json:"name"`
	Doc      string `json:"doc"`
}

// Package is the in-memory result an Extractor returns for one package.
// It carries everything needed to build both the index entry and the shard.
type Package struct {
	ID       string
	Language string
	Path     string
	Name     string
	Doc      string
	Symbols  []Symbol
}

// Symbol is one named declaration. Signature is set only for func/method;
// Type is set for const/var/field; TypeKind+Underlying are set for type.
type Symbol struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	Kind            string      `json:"kind"`
	Visibility      string      `json:"visibility"`
	VisibilityIdiom string      `json:"visibility_idiom"`
	Location        Location    `json:"location"`
	Owner           string      `json:"owner,omitempty"`
	Doc             Doc         `json:"doc"`
	IsEntrypoint    bool        `json:"is_entrypoint"`
	Invocation      *Invocation `json:"invocation,omitempty"`
	Complexity      Complexity  `json:"complexity"`

	Signature  *Signature `json:"signature,omitempty"`
	TypeKind   string     `json:"type_kind,omitempty"`
	Underlying string     `json:"underlying,omitempty"`
	Type       string     `json:"type,omitempty"`
}

// Signature describes a func or method.
type Signature struct {
	Params     []Param     `json:"params"`
	Returns    []Param     `json:"returns"`
	TypeParams []TypeParam `json:"type_params"`
	Receiver   *Param      `json:"receiver,omitempty"`
	Variadic   bool        `json:"variadic"`
}

// Param is one parameter, result, or receiver. Name may be empty.
type Param struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// TypeParam is one generic type parameter.
type TypeParam struct {
	Name       string `json:"name"`
	Constraint string `json:"constraint"`
}

// Location is a 1-based source position with a relative POSIX file path.
type Location struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	EndLine int    `json:"end_line"`
}

// Doc is a documentation comment plus its source idiom.
type Doc struct {
	Raw    string `json:"raw"`
	Format string `json:"format"`
}

// Invocation describes how to run an entrypoint symbol.
type Invocation struct {
	Kind string `json:"kind"`
	How  string `json:"how"`
}

// Complexity is the reserved big-O slot. SP1 always emits the deferred value.
type Complexity struct {
	Time   *string `json:"time"`
	Space  *string `json:"space"`
	Method string  `json:"method"`
}

// DeferredComplexity returns the SP1 placeholder complexity value.
func DeferredComplexity() Complexity {
	return Complexity{Time: nil, Space: nil, Method: "deferred"}
}
