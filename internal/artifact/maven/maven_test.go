package maven

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCoordinate(t *testing.T) {
	c, e := Parse("mvn:org.slf4j:slf4j-api:2.0.17")
	if e != nil {
		t.Fatal(e)
	}
	if got, want := c.RepositoryPath(), "org/slf4j/slf4j-api/2.0.17/slf4j-api-2.0.17.jar"; got != want {
		t.Fatalf("path=%q want %q", got, want)
	}
	x, e := Parse("g:a:jar:tests:1.2.3")
	if e != nil || x.Filename() != "a-1.2.3-tests.jar" {
		t.Fatalf("classifier: %+v %v", x, e)
	}
}
func TestCoordinateRejects(t *testing.T) {
	for _, s := range []string{"a:b", "a:b:1-SNAPSHOT", "a/b:c:1", "a::1"} {
		if _, e := Parse(s); e == nil {
			t.Errorf("Parse(%q) succeeded", s)
		}
	}
}
func FuzzCoordinateNoPanic(f *testing.F) {
	f.Add("mvn:g:a:1")
	f.Fuzz(func(t *testing.T, s string) { Parse(s) })
}

func TestResolverDownloadsAtomicallyAndCaches(t *testing.T) {
	var body bytes.Buffer
	z := zip.NewWriter(&body)
	w, _ := z.Create("x.txt")
	w.Write([]byte("ok"))
	z.Close()
	hits := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("auth=%q", r.Header.Get("Authorization"))
		}
		w.Write(body.Bytes())
	}))
	defer s.Close()
	cache := t.TempDir()
	r := Resolver{BaseURL: s.URL, CacheDir: cache, Credentials: Credentials{BearerToken: "secret"}}
	c, _ := Parse("g:a:1")
	one, e := r.Resolve(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	two, e := r.Resolve(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	if one.Cached || !two.Cached || hits != 1 || one.SHA256 != two.SHA256 {
		t.Fatalf("one=%+v two=%+v hits=%d", one, two, hits)
	}
	if _, e = os.Stat(filepath.Join(cache, filepath.FromSlash(c.RepositoryPath()))); e != nil {
		t.Fatal(e)
	}
}
