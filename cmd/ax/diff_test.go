package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"goforge.dev/assayxport/internal/diff"
)

// pyFixture writes a small Python tree whose drift against pyFixtureB is
// known: hello gains a parameter, dropped disappears, added appears.
func pyFixtureA(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, dir, "mod.py", `def hello(name):
    """Say hello."""
    out = "hi "
    for i in range(3):
        out = out + name
    return out


def dropped():
    return 1
`)
	return dir
}

func pyFixtureB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, dir, "mod.py", `def hello(name, greeting):
    """Say hello."""
    out = greeting
    for i in range(3):
        out = out + name
    return out


def added():
    return 2
`)
	return dir
}

func writeFixture(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readDiffDoc(t *testing.T, outDir string) (diff.Doc, []byte) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(outDir, "assayxport-diff.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc diff.Doc
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("unmarshal diff doc: %v", err)
	}
	return doc, b
}

func TestDiffCLIDrift(t *testing.T) {
	a, b := pyFixtureA(t), pyFixtureB(t)
	out := t.TempDir()
	err := runDiffCmd([]string{a, b, "--mode", "drift", "--out", out, "--quiet",
		"--label-a", "left", "--label-b", "right"})
	if err != nil {
		t.Fatal(err)
	}
	doc, raw := readDiffDoc(t, out)
	if doc.Mode != "drift" || doc.ModeSelectedBy != "flag" {
		t.Fatalf("mode %s/%s", doc.Mode, doc.ModeSelectedBy)
	}
	if doc.Sources[0].Label != "left" || doc.Sources[1].Label != "right" {
		t.Fatalf("labels %+v", doc.Sources)
	}
	if doc.Sources[0].Extraction["python"] != "syntactic" {
		t.Fatalf("extraction %+v", doc.Sources[0].Extraction)
	}
	if doc.Drift == nil || doc.Correspond != nil {
		t.Fatal("drift doc must carry only the drift section")
	}
	s := doc.Drift.Summary
	if s.SymbolsAdded != 1 || s.SymbolsRemoved != 1 || s.SymbolsChanged != 1 {
		t.Fatalf("summary %+v", s)
	}
	if doc.Options.IgnoreParamNames == nil || *doc.Options.IgnoreParamNames {
		t.Fatalf("options %+v", doc.Options)
	}
	// Golden: a second run over identical inputs is byte-identical.
	out2 := t.TempDir()
	if err := runDiffCmd([]string{a, b, "--mode", "drift", "--out", out2, "--quiet",
		"--label-a", "left", "--label-b", "right"}); err != nil {
		t.Fatal(err)
	}
	_, raw2 := readDiffDoc(t, out2)
	if !bytes.Equal(raw, raw2) {
		t.Fatal("two runs over identical inputs produced different bytes")
	}
}

func TestDiffCLICorrespondAuto(t *testing.T) {
	a, b := pyFixtureA(t), pyFixtureB(t)
	out := t.TempDir()
	if err := runDiffCmd([]string{a, b, "--out", out, "--quiet"}); err != nil {
		t.Fatal(err)
	}
	doc, _ := readDiffDoc(t, out)
	if doc.Mode != "correspond" || doc.ModeSelectedBy != "auto" {
		t.Fatalf("unrelated dirs must auto-select correspond: %s/%s", doc.Mode, doc.ModeSelectedBy)
	}
	if doc.Correspond == nil || len(doc.Correspond.Candidates) == 0 {
		t.Fatalf("hello/hello should correspond: %+v", doc.Correspond)
	}
	best := doc.Correspond.Candidates[0]
	if !best.SameLanguage || !strings.HasSuffix(best.A.Ref, "#hello") {
		t.Fatalf("best candidate %+v", best)
	}
	if doc.Options.MinLines == nil || *doc.Options.MinLines != 5 || doc.Options.Weights == nil {
		t.Fatalf("correspond options not recorded: %+v", doc.Options)
	}
}

func TestDiffCLIAutoDriftSameSource(t *testing.T) {
	a := pyFixtureA(t)
	out := t.TempDir()
	err := runDiffCmd([]string{a, a, "--out", out, "--quiet", "--exit-code"})
	if err != nil {
		t.Fatalf("identical sources with --exit-code must exit 0: %v", err)
	}
	doc, _ := readDiffDoc(t, out)
	if doc.Mode != "drift" || doc.ModeSelectedBy != "auto" {
		t.Fatalf("same source must auto-select drift: %s/%s", doc.Mode, doc.ModeSelectedBy)
	}
	if doc.Drift.HasDifferences() {
		t.Fatalf("self-diff drifted: %+v", doc.Drift.Summary)
	}
}

func TestDiffCLIExitCodes(t *testing.T) {
	a, b := pyFixtureA(t), pyFixtureB(t)
	// Differences + --exit-code => code 1, and the document is still written.
	out := t.TempDir()
	err := runDiffCmd([]string{a, b, "--mode", "drift", "--out", out, "--quiet", "--exit-code"})
	var ee *exitCodeError
	if !errors.As(err, &ee) || ee.code != 1 {
		t.Fatalf("want exit 1, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "assayxport-diff.json")); err != nil {
		t.Fatal("document must be written before the exit-1 signal")
	}
	// Without --exit-code, differences are still exit 0.
	if err := runDiffCmd([]string{a, b, "--mode", "drift", "--out", t.TempDir(), "--quiet"}); err != nil {
		t.Fatalf("without --exit-code: %v", err)
	}
	// Operational failure => code 2.
	err = runDiffCmd([]string{filepath.Join(a, "missing"), b, "--quiet"})
	if !errors.As(err, &ee) || ee.code != 2 {
		t.Fatalf("want exit 2 for a missing source, got %v", err)
	}
	// Wrong arity => code 2.
	err = runDiffCmd([]string{a, "--quiet"})
	if !errors.As(err, &ee) || ee.code != 2 {
		t.Fatalf("want exit 2 for one source, got %v", err)
	}
}

func TestDiffCLITextWritesNoFile(t *testing.T) {
	a, b := pyFixtureA(t), pyFixtureB(t)
	out := t.TempDir()
	if err := runDiffCmd([]string{a, b, "--mode", "drift", "--out", out, "--quiet", "--format", "text"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "assayxport-diff.json")); !os.IsNotExist(err) {
		t.Fatal("--format text must not write the JSON document")
	}
}

// Two different specs naming the same commit -- the branch name and the full
// SHA -- must produce byte-identical output, labels included.
func TestDiffCLISameCommitTwoSpecs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	repo := t.TempDir()
	gitRun := func(args ...string) string {
		t.Helper()
		full := append([]string{"-C", repo,
			"-c", "user.name=ax-test", "-c", "user.email=ax-test@example.invalid"}, args...)
		cmd := exec.Command("git", full...)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, errb.String())
		}
		return strings.TrimSpace(out.String())
	}
	gitRun("init", "-q", "-b", "main")
	writeFixture(t, repo, "mod.py", "def hello(name):\n    return name\n")
	gitRun("add", ".")
	gitRun("commit", "-q", "-m", "c1")
	sha := gitRun("rev-parse", "HEAD")

	other := pyFixtureB(t)
	run := func(spec string) []byte {
		out := t.TempDir()
		if err := runDiffCmd([]string{spec, other, "--mode", "drift", "--out", out, "--quiet", "--label-b", "other"}); err != nil {
			t.Fatal(err)
		}
		_, raw := readDiffDoc(t, out)
		return raw
	}
	byBranch := run(repo + "#main")
	bySHA := run(repo + "#" + sha)
	if !bytes.Equal(byBranch, bySHA) {
		t.Fatalf("branch spec and SHA spec of one commit differ:\n%s\n---\n%s", byBranch, bySHA)
	}
	var doc diff.Doc
	if err := json.Unmarshal(byBranch, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Sources[0].Commit != sha || !strings.HasSuffix(doc.Sources[0].Label, "#"+sha[:12]) {
		t.Fatalf("source header %+v", doc.Sources[0])
	}
	if strings.Contains(string(byBranch), repo) {
		t.Fatal("output leaks the repository's filesystem location")
	}
}
