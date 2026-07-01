package python

import "testing"

func find(fs []pyFile, rel string) *pyFile {
	for i := range fs {
		if fs[i].Rel == rel {
			return &fs[i]
		}
	}
	return nil
}

func TestDiscover(t *testing.T) {
	fs, err := discover("testdata/proj")
	if err != nil {
		t.Fatal(err)
	}
	// sorted by Rel
	for i := 1; i < len(fs); i++ {
		if fs[i-1].Rel > fs[i].Rel {
			t.Fatalf("not sorted: %q > %q", fs[i-1].Rel, fs[i].Rel)
		}
	}
	cases := []struct{ rel, mod, pkg string; isInit bool }{
		{"pkg/__init__.py", "pkg", "pkg", true},
		{"pkg/mod.py", "pkg.mod", "pkg", false},
		{"pkg/sub/__init__.py", "pkg.sub", "pkg.sub", true},
		{"pkg/sub/leaf.py", "pkg.sub.leaf", "pkg.sub", false},
		{"script.py", "script", "", false},
	}
	for _, c := range cases {
		f := find(fs, c.rel)
		if f == nil {
			t.Fatalf("missing %s", c.rel)
		}
		if f.ModuleID != c.mod || f.PackageID != c.pkg || f.IsInit != c.isInit {
			t.Fatalf("%s => mod=%q pkg=%q init=%v; want mod=%q pkg=%q init=%v",
				c.rel, f.ModuleID, f.PackageID, f.IsInit, c.mod, c.pkg, c.isInit)
		}
	}
}
