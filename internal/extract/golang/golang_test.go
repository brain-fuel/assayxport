package golang

import "testing"

func TestExtractFindsPackagesSorted(t *testing.T) {
	pkgs, err := New().Extract("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range pkgs {
		ids = append(ids, p.ID)
	}
	want := []string{"example.com/sample/calc", "example.com/sample/cmd/tool"}
	if len(ids) != len(want) {
		t.Fatalf("got package ids %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("package ids %v not sorted/expected %v", ids, want)
		}
	}
}

func TestExtractPackageMetadata(t *testing.T) {
	pkgs, err := New().Extract("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	var calc *struct{ Name, Path, Doc string }
	for _, p := range pkgs {
		if p.ID == "example.com/sample/calc" {
			calc = &struct{ Name, Path, Doc string }{p.Name, p.Path, p.Doc}
		}
	}
	if calc == nil {
		t.Fatal("calc package not found")
	}
	if calc.Name != "calc" || calc.Path != "calc" {
		t.Fatalf("calc meta = %+v, want name=calc path=calc", calc)
	}
	if calc.Doc == "" {
		t.Fatalf("calc package doc should be non-empty")
	}
}
