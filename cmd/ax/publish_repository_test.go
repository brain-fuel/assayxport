package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const repositoryPOM = `<?xml version="1.0"?><project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion>
<groupId>dev.example</groupId><artifactId>%s</artifactId><version>1.2.3</version><packaging>%s</packaging>
<name>Demo</name><description>A demo.</description><url>https://example.dev/demo</url>
<licenses><license><name>MIT</name><url>https://opensource.org/license/mit</url></license></licenses>
<developers><developer><name>Dev</name></developer></developers>
<scm><connection>scm:git:https://example.dev/demo.git</connection><developerConnection>scm:git:ssh://git@example.dev/demo.git</developerConnection><url>https://example.dev/demo</url></scm></project>`

func gitIn(t *testing.T, root string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func writeFileAt(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// releaseCheckout builds a tagged, clean checkout whose ignored target/
// directory holds a staged two-artifact Maven repository. The configuration
// lives in a separate directory, as it does for an already-tagged release.
func releaseCheckout(t *testing.T) (root, configPath string) {
	t.Helper()
	root = t.TempDir()
	gitIn(t, root, "init")
	gitIn(t, root, "config", "user.email", "test@example.com")
	gitIn(t, root, "config", "user.name", "Test")
	writeFileAt(t, filepath.Join(root, ".gitignore"), "target/\n")
	gitIn(t, root, "add", ".gitignore")
	gitIn(t, root, "commit", "-m", "release")
	gitIn(t, root, "tag", "-a", "v1.2.3", "-m", "release")
	staged := filepath.Join(root, "target", "central-staging", "dev", "example")
	writeFileAt(t, filepath.Join(staged, "demo-parent", "1.2.3", "demo-parent-1.2.3.pom"), strings.Replace(strings.Replace(repositoryPOM, "%s", "demo-parent", 1), "%s", "pom", 1))
	writeFileAt(t, filepath.Join(staged, "demo", "1.2.3", "demo-1.2.3.pom"), strings.Replace(strings.Replace(repositoryPOM, "%s", "demo", 1), "%s", "jar", 1))
	for _, suffix := range []string{".jar", "-sources.jar", "-javadoc.jar"} {
		writeFileAt(t, filepath.Join(staged, "demo", "1.2.3", "demo-1.2.3"+suffix), "bytes"+suffix)
	}
	configPath = filepath.Join(t.TempDir(), "assayxport.toml")
	writeFileAt(t, configPath, `schema_version = 1
version = "1.2.3"
policy = "maven-repository"
group_id = "dev.example"
staging = "target/central-staging"
artifacts = "demo-parent,demo"
`)
	return root, configPath
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(before) })
}

func TestPublishRepositoryPrepareWritesOneSignedBundle(t *testing.T) {
	root, configPath := releaseCheckout(t)
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")
	t.Setenv("AX_MAVEN_SIGNING_KEY", filepath.Join(t.TempDir(), "key.asc"))
	chdir(t, root)
	if err := run([]string{"publish", "--prepare", "-q", "--config", configPath}); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(root, "target", "central-staging-bundle.zip")
	zr, err := zip.OpenReader(bundle)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, want := range []string{"dev/example/demo/1.2.3/demo-1.2.3.jar.asc", "dev/example/demo-parent/1.2.3/demo-parent-1.2.3.pom.sha512"} {
		if !names[want] {
			t.Fatalf("bundle lacks %s", want)
		}
	}
}

func TestPublishRepositoryRootArgumentAndConfigFailures(t *testing.T) {
	root, configPath := releaseCheckout(t)
	t.Setenv("AX_MAVEN_SIGNING_KEY", filepath.Join(t.TempDir(), "key.asc"))
	chdir(t, t.TempDir())
	if err := run([]string{"publish", "--prepare", "-q", "--config", filepath.Join(root, "missing.toml"), root}); err == nil || !strings.Contains(err.Error(), "[CONFIG_MISSING]") {
		t.Fatalf("missing config error = %v", err)
	}
	if err := run([]string{"publish", "--prepare", "-q", "--config", configPath, root}); err != nil {
		t.Fatalf("root argument: %v", err)
	}
	writeFileAt(t, filepath.Join(root, "stray.txt"), "dirty")
	if err := run([]string{"publish", "--prepare", "-q", "--config", configPath, root}); err == nil || !strings.Contains(err.Error(), "[GIT_CHECKOUT_DIRTY]") {
		t.Fatalf("dirty error = %v", err)
	}
}

func TestPublishRepositoryUploadsPromotesAgainstFakeCentral(t *testing.T) {
	root, configPath := releaseCheckout(t)
	var mu sync.Mutex
	var uploaded []byte
	var uploadName string
	var statuses atomic.Int32
	promoted := false
	keyserver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer keyserver.Close()
	central := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/publisher/upload":
			if r.URL.Query().Get("publishingType") != "USER_MANAGED" {
				t.Errorf("publishingType = %s", r.URL.Query().Get("publishingType"))
			}
			file, _, err := r.FormFile("bundle")
			if err != nil {
				t.Error(err)
				return
			}
			data, _ := io.ReadAll(file)
			mu.Lock()
			uploaded, uploadName = data, r.URL.Query().Get("name")
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("deployment-9"))
		case r.URL.Path == "/api/v1/publisher/status":
			state := "PUBLISHED"
			if statuses.Add(1) == 1 {
				state = "VALIDATED"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"deploymentId": "deployment-9", "deploymentState": state, "purls": []string{"pkg:maven/dev.example/demo@1.2.3"}})
		case r.URL.Path == "/api/v1/publisher/deployment/deployment-9":
			promoted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer central.Close()
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")
	t.Setenv("AX_MAVEN_SIGNING_KEY", filepath.Join(t.TempDir(), "key.asc"))
	t.Setenv("AX_MAVEN_CENTRAL_URL", central.URL)
	t.Setenv("AX_MAVEN_KEYSERVER_URL", keyserver.URL)
	t.Setenv("MAVEN_CENTRAL_USERNAME", "user")
	t.Setenv("MAVEN_CENTRAL_PASSWORD", "password")
	chdir(t, root)
	if err := run([]string{"publish", "-q", "--config", configPath}); err != nil {
		t.Fatal(err)
	}
	if !promoted {
		t.Fatal("deployment was not promoted")
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(uploadName, "dev.example") || !strings.Contains(uploadName, "2 artifacts") {
		t.Fatalf("deployment name = %q", uploadName)
	}
	zr, err := zip.NewReader(bytes.NewReader(uploaded), int64(len(uploaded)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range zr.File {
		found = found || f.Name == "dev/example/demo/1.2.3/demo-1.2.3-javadoc.jar.asc"
	}
	if !found {
		t.Fatal("uploaded bundle lacks the signed javadoc jar")
	}
	state, err := os.ReadFile(filepath.Join(root, ".assayxport", "releases", "1.2.3.json"))
	if err != nil || !strings.Contains(string(state), `"state": "PUBLISHED"`) {
		t.Fatalf("state = %s, err = %v", state, err)
	}
}
