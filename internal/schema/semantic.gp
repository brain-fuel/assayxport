package schema

import (
	"reflect"
	"strconv"
	"strings"

	"goforge.dev/goplus/std/canonical"
	"goforge.dev/goplus/std/result"
)

// CallResolution makes the resolution boundary exhaustive. Only an internal
// edge can carry a SymbolRef.
type CallResolution enum {
	CallInternal(ref SymbolRef)
	CallExternal
	CallBuiltin
	CallDynamic
	CallUnresolved
}

// TypeEvidence distinguishes absent evidence from an explicitly stated type.
type TypeEvidence enum {
	EvidenceUnknown
	EvidenceStated(typeName string)
}

// OverloadResolution records the three honest results of joining call-site
// evidence with declarations.
type OverloadResolution enum {
	ResolutionExact(target SymbolRef)
	ResolutionAmbiguous(candidates []SymbolRef)
	ResolutionImpossible
}

// Bound is the closed complexity vocabulary AssayXport currently emits.
type Bound enum {
	ConstantBound
	LinearBound
	PolynomialBound(degree int)
}

// ComplexityEstimate prevents a recursive/deferred estimate from carrying
// fabricated bounds.
type ComplexityEstimate enum {
	ComplexityDeferred
	ComplexityLoopNesting(time Bound, space Bound)
	ComplexityRecursive
}

type Language enum {
	GoLanguage
	PythonLanguage
	JavaLanguage
	TypeScriptLanguage
	JavaScriptLanguage
}
type SymbolKind enum {
	TypeSymbol; FunctionSymbol; FuncSymbol; MethodSymbol; ConstructorSymbol; FieldSymbol
	VariableSymbol; VarSymbol; ConstantSymbol; PropertySymbol; ClassSymbol; EnumConstantSymbol
}
type Visibility enum { ExportedVisibility; UnexportedVisibility; PublicVisibility; ProtectedVisibility; PrivateVisibility; PackageVisibility }
type VisibilityIdiom enum { CapitalizedVisibility; UnderscoreVisibility; AccessModifierVisibility; ExportVisibility }
type DocumentationFormat enum { NoDocumentation; GoDoc; Docstring; JavaDoc; TSDoc }
type InvocationKind enum { BinaryInvocation; ModuleInvocation; ClassInvocation }
type HierarchyNodeKind enum { GroupNode; PackageNode }
type ConcernKind enum {
	AnyConcern; AsAnyConcern; NonNullAssertionConcern; TSIgnoreConcern
	UntypedParamConcern; UntypedReturnConcern; LooseEqualityConcern
}

type InvocationMeaning enum { NotInvokable; Invokable(kind InvocationKind, how string) }

type SemanticCall struct {
	Target string
	Resolution CallResolution
	Arity *Arity
	Evidence []TypeEvidence
	Count CallCount
}

type SemanticSymbol struct {
	ID SymbolID
	Kind SymbolKind
	Visibility Visibility
	VisibilityIdiom VisibilityIdiom
	File RelativeSourcePath
	Line SourceLine
	Column SourceColumn
	Documentation DocumentationFormat
	Invocation InvocationMeaning
	Complexity ComplexityEstimate
	Concerns []ConcernKind
	Calls []SemanticCall
}

type SemanticPackage struct {
	ID PackageID
	Language Language
	Symbols []SemanticSymbol
}

type SemanticFailure enum { InvalidSemantic(field string, value string) }

func CallResolutionOf(c Call) result.Result[CallResolution, SemanticFailure] {
	switch c.Kind {
	case "internal":
		ref := ParseSymbolRef(c.Ref)
		return result.MapError(ref, func(_ IdentityFailure) SemanticFailure { return InvalidSemantic("call ref", c.Ref) }).Map(func(r SymbolRef) CallResolution { return CallInternal(r) })
	case "external": if c.Ref != "" { return result.Err[CallResolution, SemanticFailure]{Err: InvalidSemantic("external call ref", c.Ref)} }; return result.Ok[CallResolution, SemanticFailure]{Value: CallExternal()}
	case "builtin": if c.Ref != "" { return result.Err[CallResolution, SemanticFailure]{Err: InvalidSemantic("builtin call ref", c.Ref)} }; return result.Ok[CallResolution, SemanticFailure]{Value: CallBuiltin()}
	case "dynamic": if c.Ref != "" { return result.Err[CallResolution, SemanticFailure]{Err: InvalidSemantic("dynamic call ref", c.Ref)} }; return result.Ok[CallResolution, SemanticFailure]{Value: CallDynamic()}
	case "unresolved": if c.Ref != "" { return result.Err[CallResolution, SemanticFailure]{Err: InvalidSemantic("unresolved call ref", c.Ref)} }; return result.Ok[CallResolution, SemanticFailure]{Value: CallUnresolved()}
	default: return result.Err[CallResolution, SemanticFailure]{Err: InvalidSemantic("call kind", c.Kind)}
	}
}

// InternalCallRef is the exhaustive projection used by graph consumers.
func InternalCallRef(c Call) (string, bool) {
	resolution, failure := result.Unpack(CallResolutionOf(c))
	if failure != nil { return "", false }
	match resolution {
	case CallInternal(ref):
		return SymbolRefString(ref), true
	case CallExternal, CallBuiltin, CallDynamic, CallUnresolved:
		return "", false
	}
}

func EvidenceOf(raw []*string) []TypeEvidence {
	out := make([]TypeEvidence, len(raw))
	for i, value := range raw {
		if value == nil { out[i] = EvidenceUnknown() } else { out[i] = EvidenceStated(*value) }
	}
	return out
}

// CallSetCanonical makes deterministic call normalization a law-tested
// standard-library Canonical instance rather than a sorting convention.
instance CallSetCanonical canonical.Canonical[[]Call] {
	Normalize(value []Call) []Call { return DedupeCalls(value) }
	Equivalent(a, b []Call) bool { return reflect.DeepEqual(DedupeCalls(a), DedupeCalls(b)) }
}

type OverloadCandidate struct {
	Target SymbolRef
	Params []string
	Variadic bool
}

func ResolveOverload(candidates []OverloadCandidate, arity Arity, evidence []TypeEvidence) OverloadResolution {
	var matches []SymbolRef
	for _, candidate := range candidates {
		n := int(arity)
		if (!candidate.Variadic && len(candidate.Params) != n) || (candidate.Variadic && n < len(candidate.Params)-1) { continue }
		compatible := true
		for i, ev := range evidence {
			paramIndex := i
			if paramIndex >= len(candidate.Params) {
				if candidate.Variadic && len(candidate.Params) > 0 { paramIndex = len(candidate.Params)-1 } else { compatible = false; break }
			}
			match ev {
			case EvidenceUnknown:
			case EvidenceStated(stated):
				if stated != candidate.Params[paramIndex] { compatible = false }
			}
			if !compatible { break }
		}
		if compatible { matches = append(matches, candidate.Target) }
	}
	if len(matches) == 0 { return ResolutionImpossible() }
	if len(matches) == 1 { return ResolutionExact(matches[0]) }
	return ResolutionAmbiguous(matches)
}

func LanguageOf(value string) result.Result[Language, SemanticFailure] {
	switch value {
	case "go": return result.Ok[Language, SemanticFailure]{Value: GoLanguage()}
	case "python": return result.Ok[Language, SemanticFailure]{Value: PythonLanguage()}
	case "java": return result.Ok[Language, SemanticFailure]{Value: JavaLanguage()}
	case "typescript": return result.Ok[Language, SemanticFailure]{Value: TypeScriptLanguage()}
	case "javascript": return result.Ok[Language, SemanticFailure]{Value: JavaScriptLanguage()}
	default: return result.Err[Language, SemanticFailure]{Err: InvalidSemantic("language", value)}
	}
}

func SymbolKindOf(value string) result.Result[SymbolKind, SemanticFailure] {
	switch value {
	case "type": return result.Ok[SymbolKind, SemanticFailure]{Value: TypeSymbol()}
	case "function": return result.Ok[SymbolKind, SemanticFailure]{Value: FunctionSymbol()}
	case "func": return result.Ok[SymbolKind, SemanticFailure]{Value: FuncSymbol()}
	case "method": return result.Ok[SymbolKind, SemanticFailure]{Value: MethodSymbol()}
	case "constructor": return result.Ok[SymbolKind, SemanticFailure]{Value: ConstructorSymbol()}
	case "field": return result.Ok[SymbolKind, SemanticFailure]{Value: FieldSymbol()}
	case "variable": return result.Ok[SymbolKind, SemanticFailure]{Value: VariableSymbol()}
	case "var": return result.Ok[SymbolKind, SemanticFailure]{Value: VarSymbol()}
	case "const": return result.Ok[SymbolKind, SemanticFailure]{Value: ConstantSymbol()}
	case "property": return result.Ok[SymbolKind, SemanticFailure]{Value: PropertySymbol()}
	case "class": return result.Ok[SymbolKind, SemanticFailure]{Value: ClassSymbol()}
	case "enum-constant": return result.Ok[SymbolKind, SemanticFailure]{Value: EnumConstantSymbol()}
	default: return result.Err[SymbolKind, SemanticFailure]{Err: InvalidSemantic("symbol kind", value)}
	}
}

func VisibilityOf(value string) result.Result[Visibility, SemanticFailure] {
	switch value {
	case "exported": return result.Ok[Visibility, SemanticFailure]{Value: ExportedVisibility()}
	case "unexported": return result.Ok[Visibility, SemanticFailure]{Value: UnexportedVisibility()}
	case "public": return result.Ok[Visibility, SemanticFailure]{Value: PublicVisibility()}
	case "protected": return result.Ok[Visibility, SemanticFailure]{Value: ProtectedVisibility()}
	case "private": return result.Ok[Visibility, SemanticFailure]{Value: PrivateVisibility()}
	case "package-private": return result.Ok[Visibility, SemanticFailure]{Value: PackageVisibility()}
	default: return result.Err[Visibility, SemanticFailure]{Err: InvalidSemantic("visibility", value)}
	}
}

func VisibilityIdiomOf(value string) result.Result[VisibilityIdiom, SemanticFailure] {
	switch value {
	case "capitalized": return result.Ok[VisibilityIdiom, SemanticFailure]{Value: CapitalizedVisibility()}
	case "underscore": return result.Ok[VisibilityIdiom, SemanticFailure]{Value: UnderscoreVisibility()}
	case "access-modifier": return result.Ok[VisibilityIdiom, SemanticFailure]{Value: AccessModifierVisibility()}
	case "export": return result.Ok[VisibilityIdiom, SemanticFailure]{Value: ExportVisibility()}
	default: return result.Err[VisibilityIdiom, SemanticFailure]{Err: InvalidSemantic("visibility idiom", value)}
	}
}

func DocumentationFormatOf(value string) result.Result[DocumentationFormat, SemanticFailure] {
	switch value {
	case "": return result.Ok[DocumentationFormat, SemanticFailure]{Value: NoDocumentation()}
	case "godoc": return result.Ok[DocumentationFormat, SemanticFailure]{Value: GoDoc()}
	case "docstring": return result.Ok[DocumentationFormat, SemanticFailure]{Value: Docstring()}
	case "javadoc": return result.Ok[DocumentationFormat, SemanticFailure]{Value: JavaDoc()}
	case "tsdoc": return result.Ok[DocumentationFormat, SemanticFailure]{Value: TSDoc()}
	default: return result.Err[DocumentationFormat, SemanticFailure]{Err: InvalidSemantic("documentation format", value)}
	}
}

func InvocationKindOf(value string) result.Result[InvocationKind, SemanticFailure] {
	switch value {
	case "binary": return result.Ok[InvocationKind, SemanticFailure]{Value: BinaryInvocation()}
	case "module": return result.Ok[InvocationKind, SemanticFailure]{Value: ModuleInvocation()}
	case "class": return result.Ok[InvocationKind, SemanticFailure]{Value: ClassInvocation()}
	default: return result.Err[InvocationKind, SemanticFailure]{Err: InvalidSemantic("invocation kind", value)}
	}
}

func HierarchyNodeKindOf(value string) result.Result[HierarchyNodeKind, SemanticFailure] {
	switch value {
	case "group": return result.Ok[HierarchyNodeKind, SemanticFailure]{Value: GroupNode()}
	case "package": return result.Ok[HierarchyNodeKind, SemanticFailure]{Value: PackageNode()}
	default: return result.Err[HierarchyNodeKind, SemanticFailure]{Err: InvalidSemantic("hierarchy node kind", value)}
	}
}

func ConcernKindOf(value string) result.Result[ConcernKind, SemanticFailure] {
	switch value {
	case "any": return result.Ok[ConcernKind, SemanticFailure]{Value: AnyConcern()}
	case "as-any": return result.Ok[ConcernKind, SemanticFailure]{Value: AsAnyConcern()}
	case "non-null-assertion": return result.Ok[ConcernKind, SemanticFailure]{Value: NonNullAssertionConcern()}
	case "ts-ignore": return result.Ok[ConcernKind, SemanticFailure]{Value: TSIgnoreConcern()}
	case "untyped-param": return result.Ok[ConcernKind, SemanticFailure]{Value: UntypedParamConcern()}
	case "untyped-return": return result.Ok[ConcernKind, SemanticFailure]{Value: UntypedReturnConcern()}
	case "loose-equality": return result.Ok[ConcernKind, SemanticFailure]{Value: LooseEqualityConcern()}
	default: return result.Err[ConcernKind, SemanticFailure]{Err: InvalidSemantic("concern", value)}
	}
}

func BoundOf(value string) result.Result[Bound, SemanticFailure] {
	if value == "O(1)" { return result.Ok[Bound, SemanticFailure]{Value: ConstantBound()} }
	if value == "O(n)" { return result.Ok[Bound, SemanticFailure]{Value: LinearBound()} }
	if strings.HasPrefix(value, "O(n^") && strings.HasSuffix(value, ")") {
		degree, err := strconv.Atoi(value[4:len(value)-1])
		if err == nil && degree > 1 { return result.Ok[Bound, SemanticFailure]{Value: PolynomialBound(degree)} }
	}
	return result.Err[Bound, SemanticFailure]{Err: InvalidSemantic("complexity bound", value)}
}

func ComplexityMeaning(c Complexity) result.Result[ComplexityEstimate, SemanticFailure] {
	switch c.Method {
	case "deferred": return result.Ok[ComplexityEstimate, SemanticFailure]{Value: ComplexityDeferred()}
	case "recursive": return result.Ok[ComplexityEstimate, SemanticFailure]{Value: ComplexityRecursive()}
	case "loop-nesting":
		if c.Time == nil || c.Space == nil { return result.Err[ComplexityEstimate, SemanticFailure]{Err: InvalidSemantic("complexity", "missing bound")} }
		time, tf := result.Unpack(BoundOf(*c.Time)); if tf != nil { return result.Err[ComplexityEstimate, SemanticFailure]{Err: tf} }
		space, sf := result.Unpack(BoundOf(*c.Space)); if sf != nil { return result.Err[ComplexityEstimate, SemanticFailure]{Err: sf} }
		return result.Ok[ComplexityEstimate, SemanticFailure]{Value: ComplexityLoopNesting(time, space)}
	default: return result.Err[ComplexityEstimate, SemanticFailure]{Err: InvalidSemantic("complexity method", c.Method)}
	}
}

func SemanticSymbolOf(symbol Symbol) result.Result[SemanticSymbol, SemanticFailure] {
	id, idFailure := result.Unpack(NewSymbolID(symbol.ID))
	if idFailure != nil { return result.Err[SemanticSymbol, SemanticFailure]{Err: InvalidSemantic("symbol id", symbol.ID)} }
	kind, kindFailure := result.Unpack(SymbolKindOf(symbol.Kind))
	if kindFailure != nil { return result.Err[SemanticSymbol, SemanticFailure]{Err: kindFailure} }
	visibility, visibilityFailure := result.Unpack(VisibilityOf(symbol.Visibility))
	if visibilityFailure != nil { return result.Err[SemanticSymbol, SemanticFailure]{Err: visibilityFailure} }
	idiom, idiomFailure := result.Unpack(VisibilityIdiomOf(symbol.VisibilityIdiom))
	if idiomFailure != nil { return result.Err[SemanticSymbol, SemanticFailure]{Err: idiomFailure} }
	file, fileFailure := result.Unpack(NewRelativeSourcePath(symbol.Location.File))
	if fileFailure != nil { return result.Err[SemanticSymbol, SemanticFailure]{Err: InvalidSemantic("source path", symbol.Location.File)} }
	line, lineFailure := result.Unpack(PositiveSourceLine(symbol.Location.Line))
	if lineFailure != nil { return result.Err[SemanticSymbol, SemanticFailure]{Err: InvalidSemantic("source line", strconv.Itoa(symbol.Location.Line))} }
	column, columnFailure := result.Unpack(PositiveSourceColumn(symbol.Location.Col))
	if columnFailure != nil { return result.Err[SemanticSymbol, SemanticFailure]{Err: InvalidSemantic("source column", strconv.Itoa(symbol.Location.Col))} }
	documentation, docFailure := result.Unpack(DocumentationFormatOf(symbol.Doc.Format))
	if docFailure != nil { return result.Err[SemanticSymbol, SemanticFailure]{Err: docFailure} }
	complexity, complexityFailure := result.Unpack(ComplexityMeaning(symbol.Complexity))
	if complexityFailure != nil { return result.Err[SemanticSymbol, SemanticFailure]{Err: complexityFailure} }
	var invocation InvocationMeaning = NotInvokable()
	if symbol.Invocation != nil {
		invocationKind, invocationFailure := result.Unpack(InvocationKindOf(symbol.Invocation.Kind))
		if invocationFailure != nil { return result.Err[SemanticSymbol, SemanticFailure]{Err: invocationFailure} }
		invocation = Invokable(invocationKind, symbol.Invocation.How)
	}
	concerns := make([]ConcernKind, len(symbol.Concerns))
	for i, raw := range symbol.Concerns {
		concern, failure := result.Unpack(ConcernKindOf(raw))
		if failure != nil { return result.Err[SemanticSymbol, SemanticFailure]{Err: failure} }
		concerns[i] = concern
	}
	calls := make([]SemanticCall, len(symbol.Calls))
	for i, raw := range symbol.Calls {
		resolution, failure := result.Unpack(CallResolutionOf(raw))
		if failure != nil { return result.Err[SemanticSymbol, SemanticFailure]{Err: failure} }
		count, countFailure := result.Unpack(PositiveCallCount(raw.Count))
		if countFailure != nil { return result.Err[SemanticSymbol, SemanticFailure]{Err: InvalidSemantic("call count", strconv.Itoa(raw.Count))} }
		var arity *Arity
		if raw.Arity != nil {
			value, arityFailure := result.Unpack(NonnegativeArity(*raw.Arity))
			if arityFailure != nil { return result.Err[SemanticSymbol, SemanticFailure]{Err: InvalidSemantic("arity", strconv.Itoa(*raw.Arity))} }
			arity = &value
		}
		calls[i] = SemanticCall{Target: raw.Target, Resolution: resolution, Arity: arity, Evidence: EvidenceOf(raw.ArgTypes), Count: count}
	}
	return result.Ok[SemanticSymbol, SemanticFailure]{Value: SemanticSymbol{
		ID: id, Kind: kind, Visibility: visibility, VisibilityIdiom: idiom,
		File: file, Line: line, Column: column, Documentation: documentation,
		Invocation: invocation, Complexity: complexity, Concerns: concerns, Calls: calls,
	}}
}

func SemanticPackageOf(pkg Package) result.Result[SemanticPackage, SemanticFailure] {
	id, idFailure := result.Unpack(NewPackageID(pkg.ID))
	if idFailure != nil { return result.Err[SemanticPackage, SemanticFailure]{Err: InvalidSemantic("package id", pkg.ID)} }
	language, languageFailure := result.Unpack(LanguageOf(pkg.Language))
	if languageFailure != nil { return result.Err[SemanticPackage, SemanticFailure]{Err: languageFailure} }
	symbols := make([]SemanticSymbol, len(pkg.Symbols))
	for i, raw := range pkg.Symbols {
		symbol, failure := result.Unpack(SemanticSymbolOf(raw))
		if failure != nil { return result.Err[SemanticPackage, SemanticFailure]{Err: failure} }
		symbols[i] = symbol
	}
	return result.Ok[SemanticPackage, SemanticFailure]{Value: SemanticPackage{ID: id, Language: language, Symbols: symbols}}
}
