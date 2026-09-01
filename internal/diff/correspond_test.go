package diff

import (
	"reflect"
	"testing"

	"goforge.dev/assayxport/internal/emit"
	"goforge.dev/assayxport/internal/schema"
	"pgregory.net/rapid"
)

func TestNormalizeTypeKind(t *testing.T) {
	tests := []struct {
		lang, typ, want string
	}{
		{"go", "int", "scalar"}, {"go", "*float64", "scalar"}, {"go", "string", "string"},
		{"go", "[]byte", "collection"}, {"go", "[4]int", "collection"}, {"go", "...string", "collection"},
		{"go", "map[string]int", "map"}, {"go", "error", "error"}, {"go", "func(int) bool", "func"},
		{"go", "any", "unknown"}, {"go", "interface{}", "unknown"}, {"go", "*bytes.Buffer", "object"},
		{"go", "", "unknown"},
		{"python", "int", "scalar"}, {"python", "str", "string"}, {"python", "bytes", "string"},
		{"python", "list[int]", "collection"}, {"python", "List[int]", "collection"},
		{"python", "typing.Dict[str, int]", "map"}, {"python", "dict", "map"},
		{"python", "Optional[int]", "scalar"}, {"python", "Optional[None]", "unknown"},
		{"python", "Callable[[int], bool]", "func"}, {"python", "ValueError", "error"},
		{"python", "None", "none"}, {"python", "Any", "unknown"}, {"python", "Widget", "object"},
		{"java", "int", "scalar"}, {"java", "Integer", "scalar"}, {"java", "String", "string"},
		{"java", "List<String>", "collection"}, {"java", "String[]", "collection"}, {"java", "int...", "collection"},
		{"java", "Map<String, Integer>", "map"}, {"java", "java.util.Map<K, V>", "map"},
		{"java", "IOException", "error"}, {"java", "Throwable", "error"},
		{"java", "Function<A, B>", "func"}, {"java", "void", "none"}, {"java", "Object", "unknown"},
		{"java", "Optional<String>", "string"}, {"java", "Widget", "object"},
		{"typescript", "number", "scalar"}, {"typescript", "string", "string"},
		{"typescript", "string[]", "collection"}, {"typescript", "Array<number>", "collection"},
		{"typescript", "Map<string, number>", "map"}, {"typescript", "Record<string, unknown>", "map"},
		{"typescript", "(x: number) => boolean", "func"}, {"typescript", "void", "none"},
		{"typescript", "any", "unknown"}, {"typescript", "TypeError", "error"}, {"typescript", "Widget", "object"},
	}
	for _, tt := range tests {
		if got := normalizeTypeKind(tt.lang, tt.typ); got != tt.want {
			t.Errorf("normalizeTypeKind(%s, %q) = %q, want %q", tt.lang, tt.typ, got, tt.want)
		}
	}
}

func TestSplitIdentifier(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"validateEmailAddress", []string{"validate", "email", "address"}},
		{"email_address_is_valid", []string{"email", "address", "is", "valid"}},
		{"PascalCase", []string{"pascal", "case"}},
		{"HTTPServer2Start", []string{"http", "server", "2", "start"}},
		{"kebab-case-name", []string{"kebab", "case", "name"}},
		{"__dunder__", []string{"dunder"}},
	}
	for _, tt := range tests {
		if got := splitIdentifier(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("splitIdentifier(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestStemToken(t *testing.T) {
	tests := []struct{ in, want string }{
		{"errors", "error"}, {"parsed", "pars"}, {"parsing", "pars"},
		{"is", "is"}, {"add", "add"}, {"address", "addres"},
	}
	for _, tt := range tests {
		if got := stemToken(tt.in); got != tt.want {
			t.Errorf("stemToken(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDiceLCSOrderSensitive(t *testing.T) {
	same := []string{"string", "scalar", "/", "scalar"}
	if got := diceLCS(same, same); got != 1000 {
		t.Errorf("identical sequences = %d, want 1000", got)
	}
	swapped := []string{"scalar", "string", "/", "scalar"}
	got := diceLCS(same, swapped)
	if got >= 1000 || got < 500 {
		t.Errorf("transposed sequences = %d, want close but under 1000", got)
	}
	if diceLCS([]string{"scalar"}, []string{"object"}) != 0 {
		t.Error("disjoint sequences must score 0")
	}
}

// eligible builds a scorable function symbol.
func eligible(name string, lines int, paramTypes []string, returns []string, calls ...string) schema.Symbol {
	params := make([]schema.Param, 0, len(paramTypes))
	for _, p := range paramTypes {
		params = append(params, schema.Param{Type: p})
	}
	rets := make([]schema.Param, 0, len(returns))
	for _, r := range returns {
		rets = append(rets, schema.Param{Type: r})
	}
	var edges []schema.Call
	for _, c := range calls {
		edges = append(edges, schema.Call{Target: c, Kind: "unresolved", Count: 1})
	}
	return schema.Symbol{
		ID: name, Name: name, Kind: "function",
		Location:   schema.Location{File: "f", Line: 1, EndLine: lines},
		Signature:  &schema.Signature{Params: params, Returns: rets},
		Complexity: schema.DeferredComplexity(),
		Calls:      edges,
	}
}

func defaultOpts() CorrespondOptions {
	return CorrespondOptions{MinLines: 5, MinScore: 400, Top: 200, Weights: DefaultWeights()}
}

func TestCorrespondCrossLanguage(t *testing.T) {
	a := []schema.Package{{ID: "mod/validate", Language: "go", Path: "validate", Symbols: []schema.Symbol{
		eligible("ValidateEmailAddress", 12, []string{"string"}, []string{"bool"}),
	}}}
	b := []schema.Package{{ID: "checks", Language: "python", Path: "checks", Symbols: []schema.Symbol{
		eligible("email_address_is_valid", 9, []string{"str"}, []string{"bool"}),
	}}}
	c := ComputeCorrespond(a, b, defaultOpts())
	if c.Eligible.A != 1 || c.Eligible.B != 1 {
		t.Fatalf("eligible %+v", c.Eligible)
	}
	if len(c.Candidates) != 1 {
		t.Fatalf("candidates %+v", c.Candidates)
	}
	cand := c.Candidates[0]
	if cand.SameLanguage {
		t.Error("go/python pair must be tagged cross-language")
	}
	if cand.Scores.Signature != 1000 {
		t.Errorf("identical normalized shapes must score 1000, got %d", cand.Scores.Signature)
	}
	if cand.Scores.Calls != nil {
		t.Errorf("no call data must report null, got %d", *cand.Scores.Calls)
	}
	if cand.Scores.Name < 400 || cand.Scores.Name >= 1000 {
		t.Errorf("name score %d out of expected band", cand.Scores.Name)
	}
	if cand.A.Ref != "mod/validate#ValidateEmailAddress" || cand.B.Ref != "checks#email_address_is_valid" {
		t.Errorf("refs %q %q", cand.A.Ref, cand.B.Ref)
	}
}

func TestCorrespondTrivialityGate(t *testing.T) {
	tiny := eligible("helper", 2, nil, nil)
	noSpan := eligible("mystery", 10, nil, nil)
	noSpan.Location.EndLine = 0
	big := eligible("helper", 8, nil, nil)
	a := []schema.Package{{ID: "a", Language: "go", Symbols: []schema.Symbol{tiny, noSpan, big}}}
	c := ComputeCorrespond(a, a, defaultOpts())
	if c.Eligible.A != 1 || c.Eligible.B != 1 {
		t.Fatalf("gate failed: %+v", c.Eligible)
	}
}

func TestCorrespondSameLanguageOnly(t *testing.T) {
	a := []schema.Package{{ID: "a", Language: "go", Symbols: []schema.Symbol{
		eligible("parseConfig", 10, []string{"string"}, []string{"error"}),
	}}}
	b := []schema.Package{{ID: "b", Language: "python", Symbols: []schema.Symbol{
		eligible("parse_config", 10, []string{"str"}, []string{"ConfigError"}),
	}}}
	if c := ComputeCorrespond(a, b, defaultOpts()); len(c.Candidates) != 1 {
		t.Fatalf("cross-language pair expected: %+v", c.Candidates)
	}
	opts := defaultOpts()
	opts.SameLanguageOnly = true
	if c := ComputeCorrespond(a, b, opts); len(c.Candidates) != 0 {
		t.Fatalf("same-language-only must drop the pair: %+v", c.Candidates)
	}
}

func TestCorrespondCallsSignal(t *testing.T) {
	a := []schema.Package{{ID: "a", Language: "go", Symbols: []schema.Symbol{
		eligible("renderReport", 10, nil, nil, "fmt.Sprintf", "sort.Strings"),
	}}}
	b := []schema.Package{{ID: "b", Language: "python", Symbols: []schema.Symbol{
		eligible("render_report", 10, nil, nil, "format", "sorted"),
	}}}
	c := ComputeCorrespond(a, b, defaultOpts())
	if len(c.Candidates) != 1 {
		t.Fatalf("candidates %+v", c.Candidates)
	}
	calls := c.Candidates[0].Scores.Calls
	if calls == nil || *calls == 0 {
		t.Fatalf("stdlib-normalized callees must overlap, got %v", calls)
	}
}

func TestCorrespondTopAndOrdering(t *testing.T) {
	mk := func(lang string, names ...string) []schema.Package {
		syms := make([]schema.Symbol, 0, len(names))
		for _, n := range names {
			syms = append(syms, eligible(n, 10, []string{"string"}, []string{"bool"}))
		}
		return []schema.Package{{ID: lang + "p", Language: lang, Symbols: syms}}
	}
	a := mk("go", "checkValue", "checkValueFully")
	b := mk("go", "checkValue", "somethingElseEntirely")
	opts := defaultOpts()
	opts.MinScore = 0
	c := ComputeCorrespond(a, b, opts)
	if len(c.Candidates) < 2 {
		t.Fatalf("want multiple candidates, got %+v", c.Candidates)
	}
	for i := 1; i < len(c.Candidates); i++ {
		if c.Candidates[i].Scores.Combined > c.Candidates[i-1].Scores.Combined {
			t.Fatal("candidates not sorted by combined desc")
		}
	}
	if c.Candidates[0].A.Ref != "gop#checkValue" || c.Candidates[0].B.Ref != "gop#checkValue" {
		t.Errorf("best pair should be the exact name match: %+v", c.Candidates[0])
	}
	opts.Top = 1
	if c := ComputeCorrespond(a, b, opts); len(c.Candidates) != 1 {
		t.Fatalf("--top must cap output, got %d", len(c.Candidates))
	}
}

// Properties: every signal stays on the integer 0-1000 scale, the set
// measures are symmetric, and shuffled input order changes nothing.
func TestCorrespondScoreRangeProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		kinds := []string{"scalar", "string", "collection", "map", "error", "func", "object", "unknown", "/"}
		gen := rapid.SliceOfN(rapid.SampledFrom(kinds), 0, 8)
		a := gen.Draw(t, "a")
		b := gen.Draw(t, "b")
		ab, ba := diceLCS(a, b), diceLCS(b, a)
		if ab != ba {
			t.Fatalf("diceLCS asymmetric: %d vs %d", ab, ba)
		}
		if ab < 0 || ab > 1000 {
			t.Fatalf("diceLCS out of range: %d", ab)
		}
	})
	rapid.Check(t, func(t *rapid.T) {
		gen := rapid.MapOfN(rapid.StringMatching(`[a-z]{1,6}`), rapid.IntRange(1, 4), 0, 8)
		a := gen.Draw(t, "a")
		b := gen.Draw(t, "b")
		ab, ba := diceMultiset(a, b), diceMultiset(b, a)
		if ab != ba {
			t.Fatalf("diceMultiset asymmetric: %d vs %d", ab, ba)
		}
		if ab < 0 || ab > 1000 {
			t.Fatalf("diceMultiset out of range: %d", ab)
		}
	})
}

func TestCorrespondOrderIndependenceProperty(t *testing.T) {
	names := []string{"parseInput", "parse_input", "renderOutput", "render_output", "checkState", "check_state"}
	var symsA, symsB []schema.Symbol
	for i, n := range names {
		s := eligible(n, 6+i, []string{"string"}, []string{"bool"})
		if i%2 == 0 {
			symsA = append(symsA, s)
		} else {
			symsB = append(symsB, s)
		}
	}
	base := func(perm []int, syms []schema.Symbol, lang string) []schema.Package {
		shuffled := make([]schema.Symbol, len(syms))
		for i, j := range perm {
			shuffled[i] = syms[j]
		}
		return []schema.Package{{ID: lang + "p", Language: lang, Symbols: shuffled}}
	}
	opts := defaultOpts()
	opts.MinScore = 0
	want, err := emit.Marshal(ComputeCorrespond(
		base([]int{0, 1, 2}, symsA, "go"), base([]int{0, 1, 2}, symsB, "python"), opts))
	if err != nil {
		t.Fatal(err)
	}
	rapid.Check(t, func(t *rapid.T) {
		permA := rapid.Permutation([]int{0, 1, 2}).Draw(t, "permA")
		permB := rapid.Permutation([]int{0, 1, 2}).Draw(t, "permB")
		got, err := emit.Marshal(ComputeCorrespond(
			base(permA, symsA, "go"), base(permB, symsB, "python"), opts))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("shuffled input changed output:\n%s\n---\n%s", want, got)
		}
	})
}
