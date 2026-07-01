package registry

import "testing"

// mixedFixture is the polyglot fixture: a Go package (calc) plus a Python
// module (thing.py) under one directory. Path is relative to this package.
const mixedFixture = "../../../cmd/assayxport/testdata/mixed"

// TestRunPolyglotMerge covers Finding 5: Run(All(), mixed) genuinely merges Go
// and Python units and reports both languages.
func TestRunPolyglotMerge(t *testing.T) {
	pkgs, langs, err := Run(All(), mixedFixture)
	if err != nil {
		t.Fatalf("Run(All(), mixed): %v", err)
	}
	if len(langs) != 2 || langs[0] != "go" || langs[1] != "python" {
		t.Fatalf("languages = %v, want [go python]", langs)
	}
	var goUnit, pyUnit bool
	for _, p := range pkgs {
		switch p.Language {
		case "go":
			goUnit = true
		case "python":
			pyUnit = true
		}
	}
	if !goUnit || !pyUnit {
		t.Fatalf("merged units missing a language: go=%v python=%v", goUnit, pyUnit)
	}
}

// TestRunSelectSubsetPolyglot covers the --lang subset path end-to-end: only the
// python extractor runs over the mixed fixture, yielding only python units.
func TestRunSelectSubsetPolyglot(t *testing.T) {
	exts, err := Select([]string{"python"})
	if err != nil {
		t.Fatal(err)
	}
	pkgs, langs, err := Run(exts, mixedFixture)
	if err != nil {
		t.Fatalf("Run(python, mixed): %v", err)
	}
	if len(langs) != 1 || langs[0] != "python" {
		t.Fatalf("languages = %v, want [python]", langs)
	}
	for _, p := range pkgs {
		if p.Language != "python" {
			t.Fatalf("subset produced non-python unit %q (%s)", p.ID, p.Language)
		}
	}
	if len(pkgs) == 0 {
		t.Fatal("expected at least one python unit from mixed fixture")
	}
}

func TestSelectUnknownLang(t *testing.T) {
	_, err := Select([]string{"java"})
	if err == nil {
		t.Fatal("expected error for unregistered language java")
	}
}

func TestSelectSubset(t *testing.T) {
	exts, err := Select([]string{"python"})
	if err != nil {
		t.Fatal(err)
	}
	if len(exts) != 1 || exts[0].Language() != "python" {
		t.Fatalf("Select(python) = %v", exts)
	}
}

func TestSelectDedupsRepeatedLang(t *testing.T) {
	exts, err := Select([]string{"go", "go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(exts) != 1 {
		t.Fatalf("Select([go, go]) = %d extractors, want 1 (deduped)", len(exts))
	}
}

func TestAllRegistered(t *testing.T) {
	langs := map[string]bool{}
	for _, e := range All() {
		langs[e.Language()] = true
	}
	if !langs["go"] || !langs["python"] {
		t.Fatalf("registry missing go/python: %v", langs)
	}
}
