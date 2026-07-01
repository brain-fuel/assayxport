package registry

import "testing"

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

func TestAllRegistered(t *testing.T) {
	langs := map[string]bool{}
	for _, e := range All() {
		langs[e.Language()] = true
	}
	if !langs["go"] || !langs["python"] {
		t.Fatalf("registry missing go/python: %v", langs)
	}
}
