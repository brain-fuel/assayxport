package diff

import (
	"sort"
	"strings"

	"goforge.dev/assayxport/internal/schema"
)

// CorrespondOptions configures similarity matching.
type CorrespondOptions struct {
	// MinLines is the triviality gate: a symbol participates only when its
	// body spans at least this many source lines. Getters, one-line
	// delegators, and empty constructors fall below it.
	MinLines int
	// MinScore is the minimum combined score (0-1000) for a pair to be
	// reported.
	MinScore int
	// Top caps the number of reported candidates.
	Top int
	// SameLanguageOnly drops cross-language pairs entirely.
	SameLanguageOnly bool
	// Weights combine the per-signal scores; see DefaultWeights.
	Weights Weights
}

// DefaultWeights are the fixed combined-score weights: name similarity
// weighs most (it is the strongest human signal), signature shape next,
// call-neighborhood overlap least (it is the sparsest).
func DefaultWeights() Weights { return Weights{Signature: 350, Name: 400, Calls: 250} }

// ComputeCorrespond proposes ranked candidate pairs between two unrelated
// package sets. Scoring is deterministic, offline, and integer-only: each
// signal is 0-1000, and every threshold that shaped the result is the
// caller's to record. Candidates are proposals for human review, never
// assertions of equivalence.
func ComputeCorrespond(a, b []schema.Package, opt CorrespondOptions) *Correspond {
	as := collectEligible(a, opt.MinLines)
	bs := collectEligible(b, opt.MinLines)
	out := &Correspond{
		Eligible:   EligibleCounts{A: len(as), B: len(bs)},
		Candidates: []Candidate{},
	}

	// Blocking: a pair is scored only when it shares a name token or has an
	// identical signature-shape key, indexed so generation stays near-linear.
	byShape := map[string][]int{}
	byToken := map[string][]int{}
	for i, s := range bs {
		byShape[s.shapeKey] = append(byShape[s.shapeKey], i)
		for tok := range s.nameTokens {
			byToken[tok] = append(byToken[tok], i)
		}
	}

	for _, sa := range as {
		seen := map[int]bool{}
		var cands []int
		for _, i := range byShape[sa.shapeKey] {
			if !seen[i] {
				seen[i] = true
				cands = append(cands, i)
			}
		}
		for tok := range sa.nameTokens {
			for _, i := range byToken[tok] {
				if !seen[i] {
					seen[i] = true
					cands = append(cands, i)
				}
			}
		}
		sort.Ints(cands)
		for _, i := range cands {
			sb := bs[i]
			if opt.SameLanguageOnly && sa.language != sb.language {
				continue
			}
			scores := scorePair(sa, sb, opt.Weights)
			if scores.Combined < opt.MinScore {
				continue
			}
			out.Candidates = append(out.Candidates, Candidate{
				A:            CandidateSide{Ref: sa.ref, Language: sa.language, Name: sa.name, Lines: sa.lines},
				B:            CandidateSide{Ref: sb.ref, Language: sb.language, Name: sb.name, Lines: sb.lines},
				SameLanguage: sa.language == sb.language,
				Scores:       scores,
			})
		}
	}

	sort.Slice(out.Candidates, func(i, j int) bool {
		ci, cj := out.Candidates[i], out.Candidates[j]
		if ci.Scores.Combined != cj.Scores.Combined {
			return ci.Scores.Combined > cj.Scores.Combined
		}
		if ci.A.Ref != cj.A.Ref {
			return ci.A.Ref < cj.A.Ref
		}
		return ci.B.Ref < cj.B.Ref
	})
	if opt.Top > 0 && len(out.Candidates) > opt.Top {
		out.Candidates = out.Candidates[:opt.Top]
	}
	return out
}

// eligibleSymbol is one function-like symbol prepared for scoring.
type eligibleSymbol struct {
	ref        string
	name       string
	language   string
	lines      int
	kinds      []string       // normalized kind sequence: params ++ "/" ++ returns
	shapeKey   string         // kinds joined, plus the variadic marker
	nameTokens map[string]int // token multiset of the symbol name
	callTokens map[string]int // token multiset of normalized callees; nil = no call data
}

// functionLike accepts every function-kind spelling the extractors emit: Go
// uses "func", Python/TS use "function", plus methods and constructors.
func functionLike(kind string) bool {
	return kind == "func" || kind == "function" || kind == "method" || kind == "constructor"
}

// collectEligible flattens packages into scorable symbols, applying the
// triviality gate. Symbols without line-span information (EndLine 0, e.g.
// declaration-only artifact scans) are excluded: their size cannot be
// vouched for. Results are sorted by ref so downstream iteration order is
// input-order independent.
func collectEligible(pkgs []schema.Package, minLines int) []eligibleSymbol {
	var out []eligibleSymbol
	for _, p := range pkgs {
		for _, s := range p.Symbols {
			if !functionLike(s.Kind) {
				continue
			}
			lines := s.Location.EndLine - s.Location.Line + 1
			if s.Location.EndLine == 0 || lines < minLines {
				continue
			}
			kinds, variadic := kindSequence(p.Language, s.Signature)
			out = append(out, eligibleSymbol{
				ref:        p.ID + "#" + s.ID,
				name:       s.Name,
				language:   p.Language,
				lines:      lines,
				kinds:      kinds,
				shapeKey:   strings.Join(kinds, ",") + variadic,
				nameTokens: tokenMultiset([]string{s.Name}),
				callTokens: calleeTokens(s.Calls),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ref < out[j].ref })
	return out
}

// kindSequence renders a signature as its normalized kind sequence:
// parameter kinds, a "/" separator, then return kinds. Void/None returns
// drop out (kindNone), so "no results" reads identically across languages.
func kindSequence(language string, sig *schema.Signature) ([]string, string) {
	seq := []string{}
	variadic := ""
	if sig == nil {
		return append(seq, "/"), variadic
	}
	for _, p := range sig.Params {
		seq = append(seq, normalizeTypeKind(language, p.Type))
	}
	seq = append(seq, "/")
	for _, r := range sig.Returns {
		if k := normalizeTypeKind(language, r.Type); k != kindNone {
			seq = append(seq, k)
		}
	}
	if sig.Variadic {
		variadic = "+v"
	}
	return seq, variadic
}

// scorePair computes the three signals and their weighted combination, all
// in integer arithmetic on the 0-1000 scale.
func scorePair(a, b eligibleSymbol, w Weights) Scores {
	s := Scores{
		Signature: diceLCS(a.kinds, b.kinds),
		Name:      diceMultiset(a.nameTokens, b.nameTokens),
	}
	num := w.Signature*s.Signature + w.Name*s.Name
	den := w.Signature + w.Name
	// No call data on either side means the signal is absent, not zero.
	if a.callTokens != nil && b.callTokens != nil {
		calls := diceMultiset(a.callTokens, b.callTokens)
		s.Calls = &calls
		num += w.Calls * calls
		den += w.Calls
	}
	if den > 0 {
		s.Combined = num / den
	}
	return s
}

// diceLCS is Dice over an order-preserving longest common subsequence:
// 1000 * 2*LCS(a,b) / (len(a)+len(b)). Order matters -- (string, int) vs
// (int, string) stays close but not identical -- which is why this is not a
// multiset measure.
func diceLCS(a, b []string) int {
	if len(a)+len(b) == 0 {
		return 0
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
			} else if prev[j] >= cur[j-1] {
				cur[j] = prev[j]
			} else {
				cur[j] = cur[j-1]
			}
		}
		prev, cur = cur, prev
		for j := range cur {
			cur[j] = 0
		}
	}
	return 1000 * 2 * prev[len(b)] / (len(a) + len(b))
}

// diceMultiset is Dice over token multisets:
// 1000 * 2*|A intersect B| / (|A|+|B|).
func diceMultiset(a, b map[string]int) int {
	sizeA, sizeB, common := 0, 0, 0
	for _, n := range a {
		sizeA += n
	}
	for tok, n := range b {
		sizeB += n
		m := a[tok]
		if m < n {
			common += m
		} else {
			common += n
		}
	}
	if sizeA+sizeB == 0 {
		return 0
	}
	return 1000 * 2 * common / (sizeA + sizeB)
}

// tokenMultiset splits identifiers on camelCase, PascalCase, snake_case,
// kebab-case, and digit boundaries, lowercases, and applies one cheap
// stemming rule (strip a single trailing "ing", "ed", or "s" when at least
// three characters remain), so validateEmailAddress and
// email_address_is_valid land near each other.
func tokenMultiset(names []string) map[string]int {
	m := map[string]int{}
	for _, name := range names {
		for _, tok := range splitIdentifier(name) {
			m[stemToken(tok)]++
		}
	}
	return m
}

func splitIdentifier(s string) []string {
	var toks []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			toks = append(toks, strings.ToLower(string(cur)))
			cur = nil
		}
	}
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case r == '_' || r == '-' || r == '.' || r == ' ' || r == '$':
			flush()
		case r >= '0' && r <= '9':
			if len(cur) > 0 && !(cur[len(cur)-1] >= '0' && cur[len(cur)-1] <= '9') {
				flush()
			}
			cur = append(cur, r)
		case r >= 'A' && r <= 'Z':
			// A lower->upper boundary starts a token; an upper run keeps
			// going until a lower follows (HTTPServer -> http, server).
			if len(cur) > 0 {
				last := cur[len(cur)-1]
				nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
				if last < 'A' || last > 'Z' || nextLower {
					flush()
				}
			}
			cur = append(cur, r)
		default:
			if len(cur) > 0 && cur[len(cur)-1] >= '0' && cur[len(cur)-1] <= '9' {
				flush()
			}
			cur = append(cur, r)
		}
	}
	flush()
	return toks
}

func stemToken(tok string) string {
	for _, suffix := range []string{"ing", "ed", "s"} {
		if strings.HasSuffix(tok, suffix) && len(tok)-len(suffix) >= 3 {
			return tok[:len(tok)-len(suffix)]
		}
	}
	return tok
}

// calleeAliases folds language-specific stdlib spellings of one operation
// into a shared token, applied to the lowercased final identifier of each
// call target. The table is versioned with the tool; growing it sharpens the
// calls signal without changing its shape.
var calleeAliases = map[string]string{
	"println":  "print",
	"printf":   "print",
	"fprintf":  "print",
	"sprintf":  "format",
	"format":   "format",
	"len":      "length",
	"size":     "length",
	"append":   "add",
	"push":     "add",
	"strip":    "trim",
	"trimspace": "trim",
}

// calleeTokens is the normalized call-neighborhood multiset: for every edge,
// the final identifier of the target (stdlib aliases folded), token-split.
// Returns nil when the symbol has no call data at all, so the signal can be
// reported as absent rather than zero.
func calleeTokens(calls []schema.Call) map[string]int {
	if len(calls) == 0 {
		return nil
	}
	m := map[string]int{}
	for _, c := range calls {
		final := c.Target
		for _, sep := range []string{"::", "/", "#"} {
			if i := strings.LastIndex(final, sep); i >= 0 {
				final = final[i+len(sep):]
			}
		}
		final = tailIdentifier(final)
		lower := strings.ToLower(final)
		if alias, ok := calleeAliases[lower]; ok {
			m[alias]++
			continue
		}
		for _, tok := range splitIdentifier(final) {
			m[stemToken(tok)]++
		}
	}
	return m
}
