package java

import "testing"

func TestDiscoverFindsJavaFilesSorted(t *testing.T) {
	files, err := discover("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no .java files discovered")
	}
	for i := 1; i < len(files); i++ {
		if files[i-1].Rel >= files[i].Rel {
			t.Fatalf("not sorted: %q >= %q", files[i-1].Rel, files[i].Rel)
		}
	}
	for _, f := range files {
		if f.Rel == "" || f.Abs == "" {
			t.Fatalf("empty path in %+v", f)
		}
	}
}

func TestDiscoverSkipsBuildDirs(t *testing.T) {
	files, err := discover("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Rel == "target/Generated.java" {
			t.Fatalf("build dir not skipped: %q", f.Rel)
		}
	}
}
