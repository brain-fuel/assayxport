package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func isolate(t *testing.T) {
	t.Helper()
	for _, name := range []string{"NPM_TOKEN", "PYPI_API_TOKEN", "TWINE_PASSWORD", "TWINE_USERNAME", "AX_NPM_REGISTRY_URL", "AX_PYPI_URL", "AX_PYPI_JSON_URL"} {
		t.Setenv(name, "")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func tarball(t *testing.T, path string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = zw.Close()
	writeFileAt(t, path, buf.String())
	return buf.Bytes()
}

func wheel(t *testing.T, path string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("demo-1.2.3.dist-info/METADATA")
	_, _ = w.Write([]byte("Metadata-Version: 2.1\nName: demo\nVersion: 1.2.3\n\n"))
	_ = zw.Close()
	writeFileAt(t, path, buf.String())
	return buf.Bytes()
}

const targetsConfig = `schema_version = 2
version = "1.2.3"
targets = ["npm", "pypi"]

[npm]
registry = "%s"
packages = ["@acme/core=target/dist/acme-core-1.2.3.tgz"]

[pypi]
project = "demo"
files = ["target/dist/demo-1.2.3-py3-none-any.whl", "target/dist/demo-1.2.3.tar.gz"]
`

type registries struct {
	npm, pypi     *httptest.Server
	mu            sync.Mutex
	npmPublished  bool
	npmData       []byte
	pypiRemote    map[string]string
	requests      int
	pypiUploads   int
	markerDir     string
	npmLog, token string
}

func targetsCheckout(t *testing.T) (root, configPath string, r *registries) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake npm is a shell script")
	}
	isolate(t)
	root, _ = releaseCheckout(t)
	r = &registries{pypiRemote: map[string]string{}, markerDir: t.TempDir()}
	r.npmData = tarball(t, filepath.Join(root, "target", "dist", "acme-core-1.2.3.tgz"), map[string]string{"package/package.json": `{"name":"@acme/core","version":"1.2.3"}`})
	whl := wheel(t, filepath.Join(root, "target", "dist", "demo-1.2.3-py3-none-any.whl"))
	sd := tarball(t, filepath.Join(root, "target", "dist", "demo-1.2.3.tar.gz"), map[string]string{"demo-1.2.3/PKG-INFO": "Metadata-Version: 2.1\nName: demo\nVersion: 1.2.3\n\n"})
	_ = whl
	_ = sd
	r.npm = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.requests++
		if _, err := os.Stat(filepath.Join(r.markerDir, "published")); err != nil {
			http.NotFound(w, req)
			return
		}
		sum := sha256Of512(r.npmData)
		_ = json.NewEncoder(w).Encode(map[string]any{"dist": map[string]string{"integrity": sum}})
	}))
	r.pypi = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.requests++
		switch {
		case req.Method == http.MethodPost:
			if err := req.ParseMultipartForm(1 << 24); err != nil {
				t.Error(err)
				return
			}
			file, header, _ := req.FormFile("content")
			data, _ := io.ReadAll(file)
			sum := sha256.Sum256(data)
			r.pypiRemote[header.Filename] = hex.EncodeToString(sum[:])
			r.pypiUploads++
		case strings.HasSuffix(req.URL.Path, "/1.2.3/json") && len(r.pypiRemote) > 0:
			urls := []map[string]any{}
			for name, digest := range r.pypiRemote {
				urls = append(urls, map[string]any{"filename": name, "digests": map[string]string{"sha256": digest}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"urls": urls})
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(r.npm.Close)
	t.Cleanup(r.pypi.Close)
	bin := t.TempDir()
	r.npmLog = filepath.Join(t.TempDir(), "npm.log")
	script := "#!/bin/sh\necho \"$*\" >> \"" + r.npmLog + "\"\ntouch \"" + filepath.Join(r.markerDir, "published") + "\"\n"
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AX_PYPI_URL", r.pypi.URL+"/legacy/")
	t.Setenv("AX_PYPI_JSON_URL", r.pypi.URL+"/pypi")
	configPath = filepath.Join(t.TempDir(), "assayxport.toml")
	writeFileAt(t, configPath, fmt.Sprintf(targetsConfig, r.npm.URL))
	return root, configPath, r
}

func sha256Of512(data []byte) string {
	return integrity(data)
}

func TestPublishTargetsMissingCredentialsFailBeforeAnyUpload(t *testing.T) {
	root, configPath, r := targetsCheckout(t)
	err := run([]string{"publish", "-q", "--npm", "--pypi", "--config", configPath, root})
	if err == nil {
		t.Fatal("expected a preflight failure")
	}
	for _, code := range []string{"[NPM_CREDENTIALS_MISSING]", "[PYPI_CREDENTIALS_MISSING]"} {
		if !strings.Contains(err.Error(), code) {
			t.Errorf("missing %s in:\n%v", code, err)
		}
	}
	if !strings.Contains(err.Error(), "Fix:") || r.requests != 0 || r.pypiUploads != 0 {
		t.Fatalf("requests = %d uploads = %d\n%v", r.requests, r.pypiUploads, err)
	}
	if _, statErr := os.Stat(r.npmLog); statErr == nil {
		t.Fatal("npm ran")
	}
}

func TestPublishTargetsNpmAndPypiEndToEndAndRerun(t *testing.T) {
	root, configPath, r := targetsCheckout(t)
	t.Setenv("NPM_TOKEN", "npm-secret")
	t.Setenv("PYPI_API_TOKEN", "pypi-secret")
	chdir(t, root)
	if err := run([]string{"publish", "-q", "--all", "--config", configPath}); err != nil {
		t.Fatal(err)
	}
	log, _ := os.ReadFile(r.npmLog)
	if !strings.Contains(string(log), "publish") || !strings.Contains(string(log), "--access public") || r.pypiUploads != 2 {
		t.Fatalf("npm log = %q uploads = %d", log, r.pypiUploads)
	}
	for _, name := range []string{"1.2.3.npm.json", "1.2.3.pypi.json"} {
		if data, err := os.ReadFile(filepath.Join(root, ".assayxport", "releases", name)); err != nil || strings.Contains(string(data), "secret") {
			t.Fatalf("%s: %s %v", name, data, err)
		}
	}
	// The state files are untracked, yet the next run's clean-checkout gate
	// passes and nothing is published twice.
	if err := run([]string{"publish", "-q", "--npm", "--pypi", "--config", configPath}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(r.npmLog)
	if string(after) != string(log) || r.pypiUploads != 2 {
		t.Fatalf("rerun republished: npm log %q uploads %d", after, r.pypiUploads)
	}
}

func TestPublishTargetsOnlyRunsSelectedTargets(t *testing.T) {
	root, configPath, r := targetsCheckout(t)
	t.Setenv("PYPI_API_TOKEN", "pypi-secret")
	// npm is configured but not selected: it needs no credentials and never runs.
	if err := run([]string{"publish", "-q", "--pypi", "--config", configPath, root}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(r.npmLog); err == nil || r.pypiUploads != 2 {
		t.Fatalf("npm ran or pypi uploads = %d", r.pypiUploads)
	}
}

func TestPublishCargoIsNotImplemented(t *testing.T) {
	root, configPath, _ := targetsCheckout(t)
	err := run([]string{"publish", "-q", "--cargo", "--config", configPath, root})
	if err == nil || !strings.Contains(err.Error(), "[TARGET_NOT_IMPLEMENTED]") {
		t.Fatalf("err = %v", err)
	}
}

func TestPublishAllNeedsTargetsList(t *testing.T) {
	root, _, _ := targetsCheckout(t)
	cfg := filepath.Join(t.TempDir(), "assayxport.toml")
	writeFileAt(t, cfg, "schema_version = 2\nversion = \"1.2.3\"\n")
	err := run([]string{"publish", "-q", "--all", "--config", cfg, root})
	if err == nil || !strings.Contains(err.Error(), "[TARGETS_EMPTY]") {
		t.Fatalf("err = %v", err)
	}
}

func TestPublishTargetsKeepsGitTagGate(t *testing.T) {
	root, configPath, _ := targetsCheckout(t)
	gitIn(t, root, "tag", "-d", "v1.2.3")
	t.Setenv("PYPI_API_TOKEN", "x")
	err := run([]string{"publish", "-q", "--pypi", "--config", configPath, root})
	if err == nil || !strings.Contains(err.Error(), "[GIT_TAG_MISSING]") {
		t.Fatalf("err = %v", err)
	}
}

// --- Go tag and push ---

func goCheckout(t *testing.T) (root, configPath, origin string) {
	t.Helper()
	isolate(t)
	root, _ = releaseCheckout(t)
	origin = t.TempDir()
	if out, err := exec.Command("git", "init", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	gitIn(t, root, "remote", "add", "origin", origin)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(proxy.Close)
	t.Setenv("AX_GO_PROXY_URL", proxy.URL)
	t.Setenv("AX_PKG_GO_DEV_URL", proxy.URL)
	configPath = filepath.Join(t.TempDir(), "assayxport.toml")
	writeFileAt(t, configPath, "schema_version = 2\nversion = \"1.2.3\"\n\n[go]\nmodule = \"example.com/demo/hosts/go\"\ntag_prefix = \"hosts/go/\"\n")
	return root, configPath, origin
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func TestPublishGoCreatesAnnotatedTagAndPushesOnlyIt(t *testing.T) {
	root, configPath, origin := goCheckout(t)
	gitIn(t, root, "branch", "side")
	if err := run([]string{"publish", "-q", "--go", "--config", configPath, root}); err != nil {
		t.Fatal(err)
	}
	head := gitOut(t, root, "rev-parse", "HEAD")
	if kind := gitOut(t, root, "cat-file", "-t", "hosts/go/v1.2.3"); kind != "tag" {
		t.Fatalf("tag object type = %s", kind)
	}
	if peeled := gitOut(t, origin, "rev-parse", "hosts/go/v1.2.3^{commit}"); peeled != head {
		t.Fatalf("origin tag peels to %s, want %s", peeled, head)
	}
	if refs := gitOut(t, origin, "for-each-ref", "--format=%(refname)"); refs != "refs/tags/hosts/go/v1.2.3" {
		t.Fatalf("origin refs = %q (only the one tag may be pushed)", refs)
	}
	// Rerun: the tag is there and verified, nothing changes.
	if err := run([]string{"publish", "-q", "--go", "--config", configPath, root}); err != nil {
		t.Fatal(err)
	}
}

func TestPublishGoRefusesRemoteTagElsewhere(t *testing.T) {
	root, configPath, origin := goCheckout(t)
	gitIn(t, root, "commit", "--allow-empty", "-m", "elsewhere")
	gitIn(t, root, "push", "origin", "HEAD:refs/tags/hosts/go/v1.2.3")
	gitIn(t, root, "checkout", "--detach", "v1.2.3")
	err := run([]string{"publish", "-q", "--go", "--config", configPath, root})
	if err == nil || !strings.Contains(err.Error(), "[GO_REMOTE_TAG_MISMATCH]") {
		t.Fatalf("err = %v", err)
	}
	if gitOut(t, origin, "rev-parse", "hosts/go/v1.2.3") == gitOut(t, root, "rev-parse", "HEAD") {
		t.Fatal("the remote tag was moved")
	}
}

func TestPublishGoWithoutPrefixOnlyVerifies(t *testing.T) {
	root, _, origin := goCheckout(t)
	cfg := filepath.Join(t.TempDir(), "assayxport.toml")
	writeFileAt(t, cfg, "schema_version = 2\nversion = \"1.2.3\"\n\n[go]\nmodule = \"example.com/demo\"\n")
	err := run([]string{"publish", "-q", "--go", "--config", cfg, root})
	if err == nil || !strings.Contains(err.Error(), "[REMOTE_TAG_MISSING]") {
		t.Fatalf("err = %v", err)
	}
	if refs := gitOut(t, origin, "for-each-ref"); refs != "" {
		t.Fatalf("origin changed: %s", refs)
	}
}

func integrity(data []byte) string {
	sum := sha512.Sum512(data)
	return "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
}
