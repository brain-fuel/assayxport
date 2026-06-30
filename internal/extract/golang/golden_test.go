package golang

import (
	"flag"
	"os"
	"testing"

	"goforge.dev/assayxport/internal/emit"
)

var update = flag.Bool("update", false, "update golden file")

func TestSampleGolden(t *testing.T) {
	pkgs, err := New().Extract("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	idx, shards := emit.Manifest(pkgs, "example.com/sample", []string{"go"})
	got, err := emit.Combined(idx, shards)
	if err != nil {
		t.Fatal(err)
	}
	const golden = "testdata/sample.golden.json"
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update first): %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("golden mismatch; re-run with -update if intended.\n--- got ---\n%s", got)
	}
}
