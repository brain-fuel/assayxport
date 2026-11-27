package main

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goforge.dev/assayxport/internal/schema"
)

// flagTree writes a Haskell tree whose `foo` stands under #ifdef FLAG1.
func flagTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, dir, "M.hs", "module M where\n#ifdef FLAG1\nfoo = 1\n#endif\nbar = 2\n")
	return dir
}

func writeDecisions(t *testing.T, dir, text string) {
	t.Helper()
	writeFixture(t, dir, "assayxport.when", text)
}

// symbolWhen reports whether the shards hold the symbol, and its condition.
func symbolWhen(shards map[string]schema.Shard, name string) (found bool, when string) {
	for _, sh := range shards {
		for _, s := range sh.Symbols {
			if s.Name == name {
				return true, s.When
			}
		}
	}
	return false, ""
}

// `ax assay <jar> --stdout` with no flag works: the decisions file is looked
// for only under a directory (review 8, B2; rule D1).
func TestAssayJARWithNoFlag(t *testing.T) {
	jar := filepath.Join(t.TempDir(), "empty.jar")
	f, err := os.Create(jar)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	w, err := z.Create("META-INF/MANIFEST.MF")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("Manifest-Version: 1.0\n")); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	var runErr error
	out := captureStdout(t, func() { runErr = runAssayCmd([]string{jar, "--stdout"}) })
	if runErr != nil {
		t.Fatalf("ax assay %s --stdout: %v", jar, runErr)
	}
	if !strings.Contains(out, `"packages"`) {
		t.Fatalf("no manifest on stdout: %q", out)
	}
}

// A path that is not a directory has no decisions file (D1), and a decisions
// path that cannot exist counts as absent (D2); one that is there and cannot
// be read is a usage error (D3).
func TestDiscoveredLinesBoundaries(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "x.hs", "module X where\n")
	if got, err := discoveredLines(filepath.Join(dir, "x.hs")); err != nil || got != nil {
		t.Fatalf("a file as the root: %v, %v", got, err)
	}
	if got, err := discoveredLines(filepath.Join(dir, "absent")); err != nil || got != nil {
		t.Fatalf("a root that does not exist: %v, %v", got, err)
	}
	asFile := filepath.Join(dir, "t")
	writeFixture(t, asFile, ".assayxport", "x")
	if got, err := discoveredLines(asFile); err != nil || got != nil {
		t.Fatalf(".assayxport as a file: %v, %v", got, err)
	}
	asDir := filepath.Join(dir, "u")
	writeFixture(t, asDir, "assayxport.when/x", "x")
	if got, err := discoveredLines(asDir); err != nil || got != nil {
		t.Fatalf("a decisions path that is a directory is no decisions file (D2): %v, %v", got, err)
	}
	// The old place is not read (O101): a file there decides nothing.
	asOld := filepath.Join(dir, "v")
	writeFixture(t, asOld, ".assayxport/when", "undef FLAG1\n")
	if got, err := discoveredLines(asOld); err != nil || got != nil {
		t.Fatalf("the old decisions path must not be read: %v, %v", got, err)
	}
}

// `ax serve` reads the decisions file at every pass, in both modes, and
// --when-no-discovery leaves it unread (D5, D7).
func TestServePassReadsDecisionsFile(t *testing.T) {
	dir := flagTree(t)
	langs := stringsFlag{"haskell"}
	_, shards, err := assayPass(dir, langs, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if found, when := symbolWhen(shards, "foo"); !found || when == "" {
		t.Fatalf("with no decisions file foo stands under its condition: %v %q", found, when)
	}
	writeDecisions(t, dir, "undef FLAG1\n")
	_, shards, err = assayPass(dir, langs, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if found, _ := symbolWhen(shards, "foo"); found {
		t.Fatal("the decisions file says undef FLAG1: foo must be absent")
	}
	_, shards, err = assayPass(dir, langs, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if found, when := symbolWhen(shards, "foo"); !found || when == "" {
		t.Fatalf("--when-no-discovery: foo stands under its condition: %v %q", found, when)
	}
	// The lazy pass writes the same: read the shards back from disk.
	out := t.TempDir()
	if _, err := assayPassToDir(dir, langs, true, false, out); err != nil {
		t.Fatal(err)
	}
	for rel, b := range collectJSON(t, out) {
		if strings.Contains(string(b), `"name": "foo"`) || strings.Contains(string(b), `"name":"foo"`) {
			t.Fatalf("lazy pass: %s holds foo though the decisions file says undef FLAG1", rel)
		}
	}
	// A line that cannot be read fails the pass; the next pass, after the file
	// is mended, works again (the commands keep running).
	writeDecisions(t, dir, "B = 2\n")
	if _, _, err := assayPass(dir, langs, true, false); err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Fatalf("a bad line must fail the pass and say so: %v", err)
	}
	writeDecisions(t, dir, "FLAG1\n")
	_, shards, err = assayPass(dir, langs, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if found, when := symbolWhen(shards, "foo"); !found || when != "" {
		t.Fatalf("FLAG1 defined: foo has no condition: %v %q", found, when)
	}
}

// A change to the decisions file is a change of the tree for `ax watch` and
// `ax serve`, and the pass it starts reads the edited file (D5).
func TestWatchPassAfterDecisionsEdit(t *testing.T) {
	dir := flagTree(t)
	langs := stringsFlag{"haskell"}
	writeDecisions(t, dir, "FLAG1\n")
	before := digest(dir)
	if _, shards, err := assayPass(dir, langs, true, false); err != nil {
		t.Fatal(err)
	} else if found, when := symbolWhen(shards, "foo"); !found || when != "" {
		t.Fatalf("first pass: foo with no condition: %v %q", found, when)
	}
	if digest(dir) != before {
		t.Fatal("the digest changed with no edit")
	}
	writeDecisions(t, dir, "# edited, and longer\nundef FLAG1\n")
	if digest(dir) == before {
		t.Fatal("an edit to the decisions file must change the tree's digest")
	}
	if _, shards, err := assayPass(dir, langs, true, false); err != nil {
		t.Fatal(err)
	} else if found, _ := symbolWhen(shards, "foo"); found {
		t.Fatal("the pass after the edit must read the new file: foo must be absent")
	}
}

// `ax diff` reads the decisions file of each source's directory, and
// --when-no-discovery leaves it unread (D6).
func TestDiffScanReadsDecisionsFile(t *testing.T) {
	dir := flagTree(t)
	writeDecisions(t, dir, "undef FLAG1\n")
	langs := stringsFlag{"haskell"}
	pkgs, _, _, err := diffScan(dir, langs, false, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkgs {
		for _, s := range p.Symbols {
			if s.Name == "foo" {
				t.Fatal("decisions read: foo must be absent")
			}
		}
	}
	pkgs, _, _, err = diffScan(dir, langs, false, true)
	if err != nil {
		t.Fatal(err)
	}
	has := false
	for _, p := range pkgs {
		for _, s := range p.Symbols {
			has = has || s.Name == "foo"
		}
	}
	if !has {
		t.Fatal("--when-no-discovery: foo stands under its condition")
	}
	writeDecisions(t, dir, "B = 2\n")
	if _, _, _, err := diffScan(dir, langs, false, false); err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Fatalf("a bad decisions file must fail the scan, naming it: %v", err)
	}
}

// A Haskell source is a watched input: an edit to a .hs file changes the
// tree's digest and the pass it starts reads the edit. The extractor skips
// .lhs and .hs-boot, so they do not count.
func TestWatchSeesHaskellEdit(t *testing.T) {
	dir := flagTree(t)
	langs := stringsFlag{"haskell"}
	before := digest(dir)
	writeFixture(t, dir, "M.hs", "module M where\n#ifdef FLAG1\nfoo = 1\n#endif\nbar = 2\nbaz = 3\n")
	if digest(dir) == before {
		t.Fatal("an edit to a .hs file must change the tree's digest")
	}
	_, shards, err := assayPass(dir, langs, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if found, _ := symbolWhen(shards, "baz"); !found {
		t.Fatal("the pass after the edit must read it: baz is missing")
	}
	after := digest(dir)
	writeFixture(t, dir, "N.lhs", "module N where\n")
	writeFixture(t, dir, "O.hs-boot", "module O where\n")
	if digest(dir) != after {
		t.Fatal(".lhs and .hs-boot are not watched")
	}
}

// A .hs file counts for the digest exactly when the extractor's walk reads it
// (N3, law HSX-45).
func TestWatchDigestFollowsSkippedPath(t *testing.T) {
	for rel, read := range map[string]bool{
		"dist-newstyle/build/autogen/Paths_p.hs": false,
		"A.hs":                                   true,
		"dist/build/B.hs":                        false,
		".stack-work/dist/C.hs":                  false,
		"src/dist/D.hs":                          false,
		"src/E.hs":                               true,
		"distant/G.hs":                           true,
		"src/.hidden/H.hs":                       false,
	} {
		dir := flagTree(t)
		before := digest(dir)
		writeFixture(t, dir, rel, "module W where\nw = 1\n")
		if changed := digest(dir) != before; changed != read {
			t.Errorf("%s: digest changed = %v, extractor reads it = %v", rel, changed, read)
		}
	}
}

// The first pass of `ax serve` in its default (progressive) mode reads the
// decisions file (review 9, S1; law HSX-44).
func TestRunProgressiveReadsDecisionsFile(t *testing.T) {
	dir := flagTree(t)
	writeDecisions(t, dir, "undef FLAG1\n")
	for _, noDiscovery := range []bool{false, true} {
		out := t.TempDir()
		cur := &current{}
		if err := runProgressive(dir, stringsFlag{"haskell"}, true, noDiscovery, out, cur, newFocusRegistry()); err != nil {
			t.Fatal(err)
		}
		held := false
		for rel, b := range collectJSON(t, out) {
			if strings.Contains(string(b), `"name":"foo"`) || strings.Contains(string(b), `"name": "foo"`) {
				held = true
				_ = rel
			}
		}
		if held == !noDiscovery {
			t.Fatalf("noDiscovery=%v: foo present = %v", noDiscovery, held)
		}
	}
}

// `ax serve` on a decisions file it cannot read ends at its start with status 2,
// in every mode (review 10, N2).
func TestServeEndsOnBadDecisionsFile(t *testing.T) {
	dir := flagTree(t)
	writeDecisions(t, dir, "this is not a line\n")
	for _, args := range [][]string{{dir, "--no-watch"}, {dir}, {dir, "--no-wasm"}} {
		err := runServeCmd(append(args, "--lang", "haskell", "--port", "0"))
		if err == nil {
			t.Fatalf("serve %v: no error on an unreadable decisions file", args)
		}
		var ee *exitCodeError
		if !errors.As(err, &ee) || ee.code != 2 {
			t.Fatalf("serve %v: want status 2, got %v", args, err)
		}
	}
}

// The watcher's digest follows a .hs file under vendor/ and a linked root
// (review 10, O128, O129).
func TestDigestVendorAndLinkedRoot(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	writeFixture(t, real, "A.hs", "module A where\n")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks")
	}
	before := digest(link)
	writeFixture(t, real, "vendor/V.hs", "module V where\n")
	if digest(link) == before {
		t.Fatal("digest did not see vendor/V.hs under a linked root")
	}
	before = digest(link)
	writeFixture(t, real, "vendor/notes.py", "x = 1\n")
	if digest(link) != before {
		t.Fatal("digest saw a .py file under vendor/")
	}
}
