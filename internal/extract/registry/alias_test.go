package registry

import "testing"

// TestSelectAliases: the names people type for the Node surface (javascript,
// js, ts, node) resolve to the typescript extractor, de-duplicating with it.
func TestSelectAliases(t *testing.T) {
	for _, alias := range []string{"javascript", "js", "ts", "node"} {
		got, err := Select([]string{alias})
		if err != nil {
			t.Fatalf("Select(%q): %v", alias, err)
		}
		if len(got) != 1 || got[0].Language() != "typescript" {
			t.Errorf("Select(%q) = %v extractors, want the typescript extractor", alias, len(got))
		}
	}
	got, err := Select([]string{"typescript", "js", "node"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("aliases should de-duplicate with their canonical language; got %d extractors", len(got))
	}
	if _, err := Select([]string{"cobol"}); err == nil {
		t.Error("unknown language should still error")
	}
}
