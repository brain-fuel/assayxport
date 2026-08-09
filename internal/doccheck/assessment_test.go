package doccheck

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectJavadocRejectsReadmePlaceholder(t *testing.T) {
	path := archive(t, map[string]string{"README.md": "documentation soon"})
	got, err := InspectJavadoc(path)
	if err != nil { t.Fatal(err) }
	if got.Status != Placeholder || len(got.Issues) != 1 || got.Issues[0] != "DOC_JAVADOC_PLACEHOLDER" { t.Fatalf("assessment = %+v", got) }
}

func TestInspectJavadocAcceptsStandardDocletShape(t *testing.T) {
	path := archive(t, map[string]string{
		"element-list": "com.example\n", "index-all.html": "index",
		"com/example/Demo.html": "type", "com/example/package-summary.html": "package",
	})
	got, err := InspectJavadoc(path)
	if err != nil || got.Status != Complete { t.Fatalf("assessment = %+v, err = %v", got, err) }
}

func TestInspectSourcesRequiresJavaDeclaration(t *testing.T) {
	placeholder := archive(t, map[string]string{"README": "soon"})
	if got, _ := InspectSources(placeholder); got.Status != Placeholder { t.Fatalf("assessment = %+v", got) }
	real := archive(t, map[string]string{"com/example/Demo.java": "class Demo {}"})
	if got, err := InspectSources(real); err != nil || got.Status != Complete { t.Fatalf("assessment = %+v, err = %v", got, err) }
}

func archive(t *testing.T, files map[string]string) string {
	t.Helper(); path := filepath.Join(t.TempDir(), "docs.jar")
	f, err := os.Create(path); if err != nil { t.Fatal(err) }
	zw := zip.NewWriter(f)
	for name, body := range files { w, e := zw.Create(name); if e != nil { t.Fatal(e) }; if _, e = w.Write([]byte(body)); e != nil { t.Fatal(e) } }
	if err := zw.Close(); err != nil { t.Fatal(err) }; if err := f.Close(); err != nil { t.Fatal(err) }
	return path
}
