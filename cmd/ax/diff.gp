package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"goforge.dev/assayxport/internal/diff"
	"goforge.dev/assayxport/internal/emit"
	"goforge.dev/assayxport/internal/extract"
	"goforge.dev/assayxport/internal/extract/golang"
	"goforge.dev/assayxport/internal/extract/registry"
	"goforge.dev/assayxport/internal/schema"
	"goforge.dev/assayxport/internal/source"
)

// exitCodeError requests a specific process exit code from main. ax diff
// follows git-diff convention: exit 1 means "differences found" (only under
// --exit-code), exit 2 means the tool itself failed -- so CI can tell
// "sources differ" from "the diff broke".
type exitCodeError struct {
	code int
	msg  string
}

func (e *exitCodeError) Error() string { return e.msg }

// errDiffDifferences is the silent exit-1 for --exit-code: the document was
// already written, there is nothing further to say.
var errDiffDifferences = &exitCodeError{code: 1}

func diffFailf(format string, args ...any) error {
	return &exitCodeError{code: 2, msg: fmt.Sprintf(format, args...)}
}

func runDiffCmd(args []string) error {
	fs := flag.NewFlagSet("ax diff", flag.ContinueOnError)
	mode := fs.String("mode", "", "comparison mode: drift or correspond (default: auto by shared identity)")
	labelA := fs.String("label-a", "", "display label for the first source")
	labelB := fs.String("label-b", "", "display label for the second source")
	offline := fs.Bool("offline", false, "forbid network access; resolve remotes from the cache only")
	cacheDir := fs.String("cache-dir", "", "remote-source cache directory (default: user cache dir)")
	out := fs.String("out", ".", "directory to write assayxport-diff.json into")
	stdoutFlag := fs.Bool("stdout", false, "print the JSON document to stdout; write no files")
	format := fs.String("format", "json", "output format: json or text (text prints a summary, writes no file)")
	exitCode := fs.Bool("exit-code", false, "exit 1 when differences (or candidates) are found, like git diff")
	minLines := fs.Int("min-lines", 5, "correspond: minimum source-line span for a symbol to participate")
	minScore := fs.Int("min-score", 400, "correspond: minimum combined score (0-1000) to report a pair")
	top := fs.Int("top", 200, "correspond: maximum number of candidate pairs to report")
	sameLangOnly := fs.Bool("same-language-only", false, "correspond: drop cross-language candidate pairs")
	ignoreParamNames := fs.Bool("ignore-param-names", false, "drift: a parameter rename is not signature drift")
	quiet := fs.Bool("quiet", false, "suppress progress on stderr")
	var langs stringsFlag
	fs.Var(&langs, "lang", "language to compare (repeatable; default: all)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `usage: ax diff <source-a> <source-b> [flags]

A source is a directory (./pkg), a local repo at a ref (./repo#v1.2.0), or a
remote repo (https://github.com/owner/repo#main, git@host:owner/repo.git).`)
		fs.PrintDefaults()
	}
	// Allow both "ax diff a b --flags" and "ax diff --flags a b".
	var specs []string
	for len(args) > 0 && len(specs) < 2 && args[0] != "" && args[0] != "--" && args[0][0] != '-' {
		specs = append(specs, args[0])
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return &exitCodeError{code: 2, msg: ""} // flag already printed the problem
	}
	for _, a := range fs.Args() {
		if len(specs) == 2 {
			return diffFailf("unexpected argument %q: diff takes exactly two sources", a)
		}
		specs = append(specs, a)
	}
	if len(specs) != 2 {
		fs.Usage()
		return diffFailf("expected two sources, got %d", len(specs))
	}
	if *format != "json" && *format != "text" {
		return diffFailf("invalid --format %q: want json or text", *format)
	}
	if *mode != "" && *mode != "drift" && *mode != "correspond" {
		return diffFailf("invalid --mode %q: want drift or correspond", *mode)
	}
	progress := func(msg string) {
		if !*quiet {
			fmt.Fprintln(os.Stderr, "ax:", msg)
		}
	}

	ctx := context.Background()
	srcOpt := source.Options{CacheDir: *cacheDir, Offline: *offline}
	progress("resolving " + specs[0])
	resA, err := source.Resolve(ctx, specs[0], srcOpt)
	if err != nil {
		return diffFailf("%v", err)
	}
	defer resA.Cleanup()
	progress("resolving " + specs[1])
	resB, err := source.Resolve(ctx, specs[1], srcOpt)
	if err != nil {
		return diffFailf("%v", err)
	}
	defer resB.Cleanup()

	progress("assaying " + displayLabel(resA, *labelA))
	pkgsA, languagesA, moduleA, err := diffScan(resA.Dir, langs, *offline)
	if err != nil {
		return diffFailf("source %s: %v", displayLabel(resA, *labelA), err)
	}
	progress("assaying " + displayLabel(resB, *labelB))
	pkgsB, languagesB, moduleB, err := diffScan(resB.Dir, langs, *offline)
	if err != nil {
		return diffFailf("source %s: %v", displayLabel(resB, *labelB), err)
	}

	m := *mode
	selectedBy := "flag"
	if m == "" {
		selectedBy = "auto"
		if sameRepoIdentity(resA, resB, moduleA, moduleB) {
			m = "drift"
		} else {
			m = "correspond"
		}
	}

	doc := diff.Doc{
		SchemaVersion:  diff.SchemaVersion,
		Tool:           "assayxport",
		Mode:           m,
		ModeSelectedBy: selectedBy,
		Sources: []diff.Source{
			sourceEntry(resA, *labelA, languagesA),
			sourceEntry(resB, *labelB, languagesB),
		},
		Options: diff.Options{Lang: restrictedLangs(langs)},
	}

	var found bool
	switch m {
	case "drift":
		ipn := *ignoreParamNames
		doc.Options.IgnoreParamNames = &ipn
		d := diff.ComputeDrift(pkgsA, pkgsB, diff.DriftOptions{IgnoreParamNames: ipn})
		doc.Drift = d
		found = d.HasDifferences()
	case "correspond":
		ml, ms, tp, so := *minLines, *minScore, *top, *sameLangOnly
		w := diff.DefaultWeights()
		doc.Options.MinLines = &ml
		doc.Options.MinScore = &ms
		doc.Options.Top = &tp
		doc.Options.Weights = &w
		doc.Options.SameLanguageOnly = &so
		c := diff.ComputeCorrespond(pkgsA, pkgsB, diff.CorrespondOptions{
			MinLines: ml, MinScore: ms, Top: tp, SameLanguageOnly: so, Weights: w,
		})
		doc.Correspond = c
		found = c.HasCandidates()
	}

	if *format == "text" {
		fmt.Print(diff.Text(doc))
	} else {
		b, err := emit.Marshal(doc)
		if err != nil {
			return diffFailf("encode diff: %v", err)
		}
		if *stdoutFlag {
			if _, err := os.Stdout.Write(b); err != nil {
				return diffFailf("%v", err)
			}
		} else {
			if err := os.MkdirAll(*out, 0o755); err != nil {
				return diffFailf("%v", err)
			}
			path := filepath.Join(*out, "assayxport-diff.json")
			if err := os.WriteFile(path, b, 0o644); err != nil {
				return diffFailf("%v", err)
			}
			progress(fmt.Sprintf("wrote %s (%s mode)", path, m))
		}
	}
	if *exitCode && found {
		return errDiffDifferences
	}
	return nil
}

// diffScan extracts one resolved source in memory. Unlike ax assay, a
// tolerated per-language failure is FATAL here: a diff computed against a
// half-loaded package graph would be confidently wrong, so the only way to
// skip a failing language is the explicit --lang restriction (which the
// output header records).
func diffScan(dir string, langs stringsFlag, offline bool) ([]schema.Package, []string, string, error) {
	exts, err := registry.Select(langs)
	if err != nil {
		return nil, nil, "", err
	}
	if offline {
		// Keep the Go loader off the network too: module resolution must
		// come from the local cache or fail loudly.
		restore := setenvTemp("GOFLAGS", strings.TrimSpace(os.Getenv("GOFLAGS")+" -mod=mod"))
		defer restore()
		restoreProxy := setenvTemp("GOPROXY", "off")
		defer restoreProxy()
	}
	var pkgs []schema.Package
	var languages []string
	switch o := registry.RunOutcome(exts, dir).(type) {
	case extract.ExtractionComplete:
		pkgs, languages = o.Packages, o.Languages
	case extract.ExtractionPartial:
		return nil, nil, "", fmt.Errorf("extraction incomplete (%s); a diff needs complete extraction, use --lang to exclude a language deliberately", joinFailures(o.Warnings))
	case extract.ExtractionFailed:
		return nil, nil, "", fmt.Errorf("extraction failed: %s", joinFailures(o.Failures))
	}
	module := ""
	for _, e := range exts {
		if ge, ok := e.(*golang.Extractor); ok {
			module = ge.Module()
			break
		}
	}
	return pkgs, languages, module, nil
}

func setenvTemp(key, value string) func() {
	old, had := os.LookupEnv(key)
	os.Setenv(key, value)
	return func() {
		if had {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	}
}

func joinFailures(fs []extract.ExtractionFailure) string {
	parts := make([]string, 0, len(fs))
	for _, f := range fs {
		parts = append(parts, f.Language+": "+f.Cause.Error())
	}
	return strings.Join(parts, "; ")
}

func displayLabel(r source.Resolved, override string) string {
	if override != "" {
		return override
	}
	return r.Label
}

func sourceEntry(r source.Resolved, override string, languages []string) diff.Source {
	return diff.Source{
		Label:      displayLabel(r, override),
		Kind:       r.Kind,
		Commit:     r.Commit,
		Languages:  languages,
		Extraction: extractionModes(languages),
	}
}

// extractionModes records the depth of truth per contributing language, from
// a fixed table: Go is the one semantic extractor, everything else is
// tree-sitter syntactic.
func extractionModes(languages []string) map[string]string {
	m := map[string]string{}
	for _, l := range languages {
		if l == "go" {
			m[l] = "semantic"
		} else {
			m[l] = "syntactic"
		}
	}
	return m
}

// sameRepoIdentity decides the auto mode: drift when the two sources are the
// same repository (canonical remote URL or local repo path) or declare the
// same Go module; correspond otherwise.
func sameRepoIdentity(a, b source.Resolved, moduleA, moduleB string) bool {
	if a.Canonical != "" && a.Canonical == b.Canonical {
		return true
	}
	return moduleA != "" && moduleA == moduleB
}

// restrictedLangs records a --lang restriction (alias-resolved, sorted) for
// the output header; nil when the run was unrestricted.
func restrictedLangs(langs stringsFlag) []string {
	if len(langs) == 0 {
		return nil
	}
	exts, err := registry.Select(langs)
	if err != nil {
		return nil // Select already failed the scan; nothing to record
	}
	out := make([]string, 0, len(exts))
	for _, e := range exts {
		out = append(out, e.Language())
	}
	sort.Strings(out)
	return out
}
