package jvm

import (
	"archive/zip"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestClassEntryMultiRelease(t *testing.T) {
	tests := []struct {
		name    string
		multi   bool
		logical string
		release int
		ok      bool
	}{
		{"a/B.class", false, "a/B.class", 0, true}, {"META-INF/versions/11/a/B.class", true, "a/B.class", 11, true}, {"META-INF/versions/11/a/B.class", false, "", 0, false}, {"x.txt", true, "", 0, false},
	}
	for _, tt := range tests {
		logical, release, ok, e := classEntry(tt.name, tt.multi)
		if e != nil || logical != tt.logical || release != tt.release || ok != tt.ok {
			t.Errorf("classEntry(%q) = %q,%d,%v,%v", tt.name, logical, release, ok, e)
		}
	}
}

func TestMultiReleaseSelectsOneDefinition(t *testing.T) {
	b, _ := base64.StdEncoding.DecodeString(fixtureClass)
	jar := filepath.Join(t.TempDir(), "multi.jar")
	f, e := os.Create(jar)
	if e != nil {
		t.Fatal(e)
	}
	z := zip.NewWriter(f)
	for name, data := range map[string][]byte{"META-INF/MANIFEST.MF": []byte("Manifest-Version: 1.0\r\nMulti-Release: true\r\n"), "demo/Box.class": b, "META-INF/versions/25/demo/Box.class": b, "META-INF/versions/26/demo/Box.class": []byte("ignored above target")} {
		w, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	pkgs, e := ExtractJAR(jar, Options{JavaRelease: 25})
	if e != nil {
		t.Fatal(e)
	}
	if len(pkgs) != 1 {
		t.Fatalf("packages=%d", len(pkgs))
	}
	types := 0
	for _, s := range pkgs[0].Symbols {
		if s.Kind == "type" {
			types++
		}
	}
	if types != 1 {
		t.Fatalf("type definitions=%d", types)
	}
}
func TestManifestMultiRelease(t *testing.T) {
	if !manifestMultiRelease([]byte("Manifest-Version: 1.0\r\nMulti-Release: true\r\n")) {
		t.Fatal("not recognized")
	}
	if manifestMultiRelease([]byte("Multi-Release: false\n")) {
		t.Fatal("false recognized")
	}
}
func TestMalformedClassDoesNotPanic(t *testing.T) {
	for _, b := range [][]byte{nil, {0xca, 0xfe, 0xba, 0xbe}, {0, 1, 2, 3}} {
		if _, e := ParseClass(b); e == nil {
			t.Errorf("ParseClass(%x) succeeded", b)
		}
	}
}

// Java 25 output for a tiny sealed generic class. Keeping this one curated
// class inline makes the ordinary suite independent of javac and the network.
const fixtureClass = "yv66vgAAAEUAKAoAAgADBwAEDAAFAAYBABBqYXZhL2xhbmcvT2JqZWN0AQAGPGluaXQ+AQADKClWCQAIAAkHAAoMAAsADAEACGRlbW8vQm94AQAFdmFsdWUBABZMamF2YS9sYW5nL0NvbXBhcmFibGU7AQAGQU5TV0VSAQABSQEADUNvbnN0YW50VmFsdWUDAAAAKgEACVNpZ25hdHVyZQEAA1RUOwEAGShMamF2YS9sYW5nL0NvbXBhcmFibGU7KVYBAARDb2RlAQAPTGluZU51bWJlclRhYmxlAQAQTWV0aG9kUGFyYW1ldGVycwEABihUVDspVgEAA21hcAEAIihMamF2YS91dGlsL0xpc3Q7KUxqYXZhL3V0aWwvTGlzdDsBAApFeGNlcHRpb25zBwAcAQATamF2YS9pby9JT0V4Y2VwdGlvbgEAAnhzAQA1PFU6VFQ7PihMamF2YS91dGlsL0xpc3Q8LVRUOz47KUxqYXZhL3V0aWwvTGlzdDwrVFU7PjsBADM8VDo6TGphdmEvbGFuZy9Db21wYXJhYmxlPC1UVDs+Oz5MamF2YS9sYW5nL09iamVjdDsBAApTb3VyY2VGaWxlAQAIQm94LmphdmEBAApEZXByZWNhdGVkAQAZUnVudGltZVZpc2libGVBbm5vdGF0aW9ucwEAFkxqYXZhL2xhbmcvRGVwcmVjYXRlZDsBABNQZXJtaXR0ZWRTdWJjbGFzc2VzBwAnAQAKZGVtby9DaGlsZAAhAAgAAgAAAAIAGQANAA4AAQAPAAAAAgAQAAQACwAMAAEAEQAAAAIAEgACAAEABQATAAMAFAAAACIAAgACAAAACiq3AAEqK7UAB7EAAAABABUAAAAGAAEAAAABABYAAAAFAQALAAAAEQAAAAIAFwABABgAGQAEABQAAAAaAAEAAgAAAAIBsAAAAAEAFQAAAAYAAQAAAAEAGgAAAAQAAQAbABYAAAAFAQAdAAAAEQAAAAIAHgAFABEAAAACAB8AIAAAAAIAIQAiAAAAAAAjAAAABgABACQAAAAlAAAABAABACY="

func TestParseClassFixture(t *testing.T) {
	b, err := base64.StdEncoding.DecodeString(fixtureClass)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseClass(b)
	if err != nil {
		t.Fatal(err)
	}
	if c.Major != 69 || c.Name != "demo.Box" || len(c.Permitted) != 1 || c.Permitted[0] != "demo.Child" {
		t.Fatalf("class = %+v", c)
	}
	if c.Signature == nil || formatTypeParams(c.Signature.TypeParams)[0] != "T extends java.lang.Comparable<? super T>" {
		t.Fatalf("signature = %+v", c.Signature)
	}
	if len(c.Fields) != 2 || c.Fields[0].Constant != int32(42) {
		t.Fatalf("fields = %+v", c.Fields)
	}
	if len(c.Methods) != 2 || c.Methods[1].Generic == nil || len(c.Methods[1].Exceptions) != 1 {
		t.Fatalf("methods = %+v", c.Methods)
	}
}
