package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"goforge.dev/assayxport/internal/emit"
)

// TestStreamingMatchesBuffered proves the streaming assay path (assayToDir, used
// by `ax serve` on large repos) writes byte-for-byte the same files as the
// in-RAM path (assayOnce + emit.WriteDir). testdata/mixed has both Java (which
// streams) and Python (buffered via the fallback), so both branches are covered.
func TestStreamingMatchesBuffered(t *testing.T) {
	const tree = "testdata/mixed"

	// Buffered: whole manifest in RAM, then written to disk.
	bufDir := t.TempDir()
	idx, shards, err := assayOnce(tree, nil, true)
	if err != nil {
		t.Fatalf("assayOnce: %v", err)
	}
	if len(idx.Packages) == 0 {
		t.Fatal("no packages assayed")
	}
	if err := emit.WriteDir(bufDir, idx, shards); err != nil {
		t.Fatalf("WriteDir: %v", err)
	}

	// Streaming: shards written one at a time, symbols released as we go.
	strDir := t.TempDir()
	if _, err := assayToDir(tree, nil, true, strDir); err != nil {
		t.Fatalf("assayToDir: %v", err)
	}

	bufFiles := collectJSON(t, bufDir)
	strFiles := collectJSON(t, strDir)

	if len(bufFiles) != len(strFiles) {
		t.Fatalf("file count differs: buffered=%d streaming=%d", len(bufFiles), len(strFiles))
	}
	for rel, bufBytes := range bufFiles {
		strBytes, ok := strFiles[rel]
		if !ok {
			t.Fatalf("streaming missing file %s", rel)
		}
		if string(bufBytes) != string(strBytes) {
			t.Fatalf("file %s differs between buffered and streaming output", rel)
		}
	}
}

// TestStreamingDeterministicWithCollisions covers the hard case: two Java files
// under different roots declaring the same package and class name collide on
// FQCN (module id), the way Kafka's upgrade-system-tests-NN dirs each carry a
// SmokeTestClient. Emission order is then nondeterministic (parallel workers),
// so the writer must order equal-id entries by path to stay byte-stable -- both
// run-to-run and between the streaming and buffered paths.
func TestStreamingDeterministicWithCollisions(t *testing.T) {
	src := t.TempDir()
	// Same package p, same class Foo, three different source roots -> one FQCN.
	for i, root := range []string{"a", "b", "c"} {
		dir := filepath.Join(src, root, "p")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "package p;\npublic class Foo {\n  public int m" + string(rune('0'+i)) + "(int x){ return x+" + string(rune('0'+i)) + "; }\n}\n"
		if err := os.WriteFile(filepath.Join(dir, "Foo.java"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run := func() map[string][]byte {
		d := t.TempDir()
		if _, err := assayToDir(src, nil, true, d); err != nil {
			t.Fatalf("assayToDir: %v", err)
		}
		return collectJSON(t, d)
	}

	// Two streaming runs must be byte-identical despite parallel emission order.
	a, b := run(), run()
	assertSameFiles(t, "streaming run 1", a, "streaming run 2", b)

	// And streaming must match the buffered in-RAM path.
	bufDir := t.TempDir()
	idx, shards, err := assayOnce(src, nil, true)
	if err != nil {
		t.Fatalf("assayOnce: %v", err)
	}
	if err := emit.WriteDir(bufDir, idx, shards); err != nil {
		t.Fatalf("WriteDir: %v", err)
	}
	assertSameFiles(t, "buffered", collectJSON(t, bufDir), "streaming", a)

	// Sanity: all three Foo files survived (no silent FQCN-collision drop).
	var index struct {
		Packages []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(a["assayxport.json"], &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	foos := 0
	for _, p := range index.Packages {
		if p.ID == "p.Foo" {
			foos++
		}
	}
	if foos != 3 {
		t.Fatalf("expected 3 colliding p.Foo entries, got %d", foos)
	}
}

// assertSameFiles fails if two collected file maps differ in keys or bytes.
func assertSameFiles(t *testing.T, an string, a map[string][]byte, bn string, b map[string][]byte) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("%s has %d files, %s has %d", an, len(a), bn, len(b))
	}
	for rel, av := range a {
		bv, ok := b[rel]
		if !ok {
			t.Fatalf("%s missing file %s present in %s", bn, rel, an)
		}
		if string(av) != string(bv) {
			t.Fatalf("file %s differs between %s and %s", rel, an, bn)
		}
	}
}

// collectJSON reads every *.json file under dir into a map keyed by its path
// relative to dir (POSIX slashes).
func collectJSON(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}
