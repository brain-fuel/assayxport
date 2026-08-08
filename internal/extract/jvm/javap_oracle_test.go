//go:build jvm_oracle

package jvm

import (
	"archive/zip"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestJavapOracle is an optional differential smoke test. javap is only an
// oracle; production extraction never invokes a JVM tool.
func TestJavapOracle(t *testing.T) {
	javac, e := exec.LookPath("javac")
	if e != nil {
		t.Skip("javac unavailable")
	}
	javap, e := exec.LookPath("javap")
	if e != nil {
		t.Skip("javap unavailable")
	}
	d := t.TempDir()
	src := filepath.Join(d, "Oracle.java")
	code := `package oracle; import java.io.*; import java.util.*; public class Oracle<T extends Comparable<? super T>> { public static final int N=7; protected T value; public Oracle(T v){value=v;} public <U extends T> List<? extends U> map(List<? super T> x) throws IOException{return null;} }`
	if e = os.WriteFile(src, []byte(code), 0644); e != nil {
		t.Fatal(e)
	}
	if out, e := exec.Command(javac, "-parameters", "-d", d, src).CombinedOutput(); e != nil {
		if bytes.Contains(out, []byte("Unable to locate a Java Runtime")) {
			t.Skip("javac launcher has no JDK")
		}
		t.Fatalf("javac: %v: %s", e, out)
	}
	classPath := filepath.Join(d, "oracle", "Oracle.class")
	b, e := os.ReadFile(classPath)
	if e != nil {
		t.Fatal(e)
	}
	c, e := ParseClass(b)
	if e != nil {
		t.Fatal(e)
	}
	out, e := exec.Command(javap, "-classpath", d, "-public", "-s", "oracle.Oracle").CombinedOutput()
	if e != nil {
		t.Fatalf("javap: %v: %s", e, out)
	}
	for _, m := range c.Methods {
		if !apiMember(m.Flags) {
			continue
		}
		name := m.Name
		if name == "<init>" {
			name = "Oracle"
		}
		if !bytes.Contains(out, []byte(name)) || !bytes.Contains(out, []byte(m.Descriptor)) {
			t.Errorf("javap output missing %s %s", name, m.Descriptor)
		}
	}
	jar := filepath.Join(d, "oracle.jar")
	f, e := os.Create(jar)
	if e != nil {
		t.Fatal(e)
	}
	zw := zip.NewWriter(f)
	w, e := zw.Create("oracle/Oracle.class")
	if e != nil {
		t.Fatal(e)
	}
	w.Write(b)
	zw.Close()
	f.Close()
	pkgs, e := ExtractJAR(jar, Options{})
	if e != nil {
		t.Fatal(e)
	}
	if len(pkgs) != 1 || !strings.Contains(pkgs[0].Symbols[0].BinaryName, "Oracle") {
		t.Fatalf("packages=%+v", pkgs)
	}
}
