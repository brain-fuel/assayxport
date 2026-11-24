package publication

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
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
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func fastPolling(t *testing.T) {
	t.Helper()
	npmDelay, npmTries, pyDelay, pyTries := npmPollDelay, npmPollAttempts, pypiPollDelay, pypiPollAttempts
	npmPollDelay, npmPollAttempts, pypiPollDelay, pypiPollAttempts = time.Millisecond, 3, time.Millisecond, 3
	t.Cleanup(func() {
		npmPollDelay, npmPollAttempts, pypiPollDelay, pypiPollAttempts = npmDelay, npmTries, pyDelay, pyTries
	})
}

// isolateCredentials removes every ambient credential so a test sees only
// what it sets, and points HOME at an empty directory.
func isolateCredentials(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"NPM_TOKEN", "PYPI_API_TOKEN", "TWINE_PASSWORD", "TWINE_USERNAME", "AX_NPM_REGISTRY_URL", "AX_PYPI_URL", "AX_PYPI_JSON_URL"} {
		t.Setenv(name, "")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func writeTGZ(t *testing.T, path, name, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	body := fmt.Sprintf(`{"name":%q,"version":%q}`, name, version)
	if err := tw.WriteHeader(&tar.Header{Name: "package/package.json", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write([]byte(body))
	_ = tw.Close()
	_ = zw.Close()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func integrityOf(data []byte) string {
	sum := sha512.Sum512(data)
	return "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
}

// fakeNPM puts an `npm` shell script on PATH. It records its arguments and
// the NPM_TOKEN it saw, and marks the tarball (second argument) published.
func fakeNPM(t *testing.T) (logFile, markers string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake npm is a shell script")
	}
	dir, markers := t.TempDir(), t.TempDir()
	logFile = filepath.Join(t.TempDir(), "npm.log")
	script := "#!/bin/sh\necho \"args: $*\" >> \"$FAKE_NPM_LOG\"\necho \"token: $NPM_TOKEN\" >> \"$FAKE_NPM_LOG\"\ntouch \"$FAKE_NPM_MARKERS/$(basename \"$2\")\"\n"
	if err := os.WriteFile(filepath.Join(dir, "npm"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_NPM_LOG", logFile)
	t.Setenv("FAKE_NPM_MARKERS", markers)
	return logFile, markers
}

type npmFixture struct {
	root, markers, logFile string
	cfg                    Config
	data                   map[string][]byte
	server                 *httptest.Server
	mu                     sync.Mutex
	preexisting            map[string]string // name -> integrity already on the registry
	gets                   []string
}

func newNPMFixture(t *testing.T) *npmFixture {
	t.Helper()
	isolateCredentials(t)
	fastPolling(t)
	f := &npmFixture{root: t.TempDir(), data: map[string][]byte{}, preexisting: map[string]string{}}
	f.logFile, f.markers = fakeNPM(t)
	f.data["@acme/core"] = writeTGZ(t, filepath.Join(f.root, "dist", "acme-core-1.2.3.tgz"), "@acme/core", "1.2.3")
	f.data["acme-cli"] = writeTGZ(t, filepath.Join(f.root, "dist", "acme-cli-1.2.3.tgz"), "acme-cli", "1.2.3")
	files := map[string]string{"@acme/core": "acme-core-1.2.3.tgz", "acme-cli": "acme-cli-1.2.3.tgz"}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.gets = append(f.gets, r.URL.EscapedPath())
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/1.2.3")
		if integrity, ok := f.preexisting[name]; ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"dist": map[string]string{"integrity": integrity}})
			return
		}
		if _, err := os.Stat(filepath.Join(f.markers, files[name])); err == nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"dist": map[string]string{"integrity": integrityOf(f.data[name])}})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.server.Close)
	f.cfg = Config{Version: "1.2.3", NPMRegistry: f.server.URL, NPMPackages: []string{"@acme/core=dist/acme-core-1.2.3.tgz", "acme-cli=dist/acme-cli-1.2.3.tgz@next"}}
	return f
}

func (f *npmFixture) target(t *testing.T, opts TargetOptions) Target {
	t.Helper()
	target, err := NewTarget("npm", f.root, f.cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func TestParseNPMPackagesTagsAndErrors(t *testing.T) {
	pkgs, issues := parseNPMPackages([]string{"@a/b=dist/b-1.tgz", "c=dist/c-1.tgz@next", "d=weird@dir/d-1.tgz", "e=dist/e-1.tar.gz@beta"})
	if len(issues) != 0 {
		t.Fatalf("issues = %v", issues)
	}
	want := []npmPackage{{"@a/b", "dist/b-1.tgz", ""}, {"c", "dist/c-1.tgz", "next"}, {"d", "weird@dir/d-1.tgz", ""}, {"e", "dist/e-1.tar.gz", "beta"}}
	for i := range want {
		if pkgs[i] != want[i] {
			t.Errorf("package %d = %+v, want %+v", i, pkgs[i], want[i])
		}
	}
	_, issues = parseNPMPackages([]string{"noequals", "x=dist/x.zip", "BAD NAME=dist/x.tgz", "y=dist/y.tgz@bad tag"})
	if len(issues) != 4 {
		t.Fatalf("issues = %v", issues)
	}
}

func TestNPMPublishesMissingSkipsMatchingAndIsIdempotent(t *testing.T) {
	f := newNPMFixture(t)
	t.Setenv("NPM_TOKEN", "secret-token")
	f.preexisting["@acme/core"] = integrityOf(f.data["@acme/core"])
	target := f.target(t, TargetOptions{})
	if issues := target.Validate(f.cfg); len(issues) != 0 {
		t.Fatalf("validate = %v", issues)
	}
	if err := target.Credentials(); err != nil {
		t.Fatal(err)
	}
	if state, err := target.Published(context.Background()); err != nil || state.Done {
		t.Fatalf("before: %+v %v", state, err)
	}
	results, err := RunTargets(context.Background(), []Target{target}, nil)
	if err != nil || results[0].Status != "published" {
		t.Fatalf("results = %+v err = %v", results, err)
	}
	log, _ := os.ReadFile(f.logFile)
	text := string(log)
	if strings.Count(text, "args:") != 1 || !strings.Contains(text, "acme-cli-1.2.3.tgz --access public --registry "+f.server.URL+" --tag next") {
		t.Fatalf("npm log:\n%s", text)
	}
	if !strings.Contains(text, "token: secret-token") {
		t.Fatalf("npm did not receive NPM_TOKEN:\n%s", text)
	}
	state, err := os.ReadFile(targetStatePath(f.root, "1.2.3", "npm"))
	if err != nil || !strings.Contains(string(state), "@acme/core@1.2.3") || strings.Contains(string(state), "secret-token") {
		t.Fatalf("state = %s, err = %v", state, err)
	}
	results, err = RunTargets(context.Background(), []Target{f.target(t, TargetOptions{})}, nil)
	if err != nil || results[0].Status != "skipped" {
		t.Fatalf("rerun results = %+v err = %v", results, err)
	}
	if after, _ := os.ReadFile(f.logFile); string(after) != text {
		t.Fatal("a rerun invoked npm again")
	}
	if len(f.gets) == 0 || !strings.HasPrefix(f.gets[0], "/@acme%2Fcore/1.2.3") {
		t.Fatalf("scoped name was not escaped: %v", f.gets)
	}
}

func TestNPMVersionMismatchFailsBeforePublishing(t *testing.T) {
	f := newNPMFixture(t)
	t.Setenv("NPM_TOKEN", "x")
	f.preexisting["acme-cli"] = "sha512-different"
	target := f.target(t, TargetOptions{})
	results, err := RunTargets(context.Background(), []Target{target}, nil)
	if err == nil || errorCode(err) != "NPM_VERSION_MISMATCH" || results[0].Status != "failed" {
		t.Fatalf("results = %+v err = %v", results, err)
	}
	if _, statErr := os.Stat(f.logFile); statErr == nil {
		t.Fatal("npm publish ran although a version mismatched")
	}
}

func TestNPMPublishVerificationFailureIsReported(t *testing.T) {
	f := newNPMFixture(t)
	t.Setenv("NPM_TOKEN", "x")
	f.cfg.NPMPackages = f.cfg.NPMPackages[:1]
	// The registry keeps answering 404 although npm "succeeded".
	f.server.Config.Handler = http.NotFoundHandler()
	err := f.target(t, TargetOptions{}).Publish(context.Background(), nil)
	if errorCode(err) != "NPM_PUBLISH_UNVERIFIED" {
		t.Fatalf("err = %v", err)
	}
}

func TestNPMValidateRejectsBadInputs(t *testing.T) {
	f := newNPMFixture(t)
	f.cfg.NPMPackages = []string{"@acme/core=dist/acme-core-1.2.3.tgz", "@acme/core=dist/acme-core-1.2.3.tgz", "acme-cli=dist/missing.tgz", "other=dist/acme-cli-1.2.3.tgz", "../x=../x.tgz"}
	f.cfg.NPMRegistry = "http://registry.example.com"
	got := codes(f.target(t, TargetOptions{}).Validate(f.cfg))
	for _, code := range []string{"NPM_PACKAGE_DUPLICATE", "NPM_TARBALL_MISSING", "NPM_TARBALL_MISMATCH", "NPM_REGISTRY_INVALID", "NPM_PACKAGE_INVALID"} {
		if !strings.Contains(got, code) {
			t.Errorf("missing %s in %s", code, got)
		}
	}
	empty := Config{Version: "1.2.3"}
	if got := codes(newNPMTarget(f.root, empty, TargetOptions{}).Validate(empty)); !strings.Contains(got, "CONFIG_REQUIRED_MISSING") {
		t.Errorf("empty packages: %s", got)
	}
}

func TestNPMCredentialSources(t *testing.T) {
	f := newNPMFixture(t)
	registryKey := "//" + strings.TrimPrefix(f.server.URL, "http://") + "/:_authToken"
	missing := f.target(t, TargetOptions{}).Credentials()
	if errorCode(missing) != "NPM_CREDENTIALS_MISSING" || !strings.Contains(missing.Error(), "Fix:") {
		t.Fatalf("missing = %v", missing)
	}
	// Project .npmrc holding a ${VAR} reference needs no owner-only mode.
	if err := os.WriteFile(filepath.Join(f.root, ".npmrc"), []byte(registryKey+"=${MY_NPM}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.target(t, TargetOptions{}).Credentials(); errorCode(err) != "NPM_CREDENTIALS_MISSING" {
		t.Fatalf("unset reference should be missing: %v", err)
	}
	t.Setenv("MY_NPM", "from-reference")
	if err := f.target(t, TargetOptions{}).Credentials(); err != nil {
		t.Fatal(err)
	}
	// A literal token in an explicit file must be owner-only on Unix.
	explicit := filepath.Join(t.TempDir(), "npmrc")
	if err := os.WriteFile(explicit, []byte("; comment\n"+registryKey+"=literal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := f.target(t, TargetOptions{NpmrcPath: explicit}).Credentials(); errorCode(err) != "NPM_NPMRC_PERMISSIONS_INSECURE" {
			t.Fatalf("insecure = %v", err)
		}
		_ = os.Chmod(explicit, 0o600)
	}
	target := f.target(t, TargetOptions{NpmrcPath: explicit})
	if err := target.Credentials(); err != nil || target.(*npmTarget).token != "literal" {
		t.Fatalf("explicit = %v", err)
	}
	if err := f.target(t, TargetOptions{NpmrcPath: filepath.Join(t.TempDir(), "absent")}).Credentials(); errorCode(err) != "NPM_NPMRC_UNREADABLE" {
		t.Fatalf("unreadable = %v", err)
	}
	// ~/.npmrc is read last; NPM_TOKEN wins over every file.
	_ = os.Remove(filepath.Join(f.root, ".npmrc"))
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".npmrc"), []byte(registryKey+"=home-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home2 := f.target(t, TargetOptions{Home: home})
	if err := home2.Credentials(); err != nil || home2.(*npmTarget).token != "home-token" {
		t.Fatalf("home = %v", err)
	}
	t.Setenv("NPM_TOKEN", "env-token")
	env := f.target(t, TargetOptions{Home: home})
	if err := env.Credentials(); err != nil || env.(*npmTarget).token != "env-token" {
		t.Fatalf("env = %v", err)
	}
}

func TestNPMRCTokenWalksRegistryPathParents(t *testing.T) {
	text := "//registry.example.com/:_authToken=host\n//registry.example.com/npm/:_authToken=\"scoped\"\n"
	for registry, want := range map[string]string{"https://registry.example.com": "host", "https://registry.example.com/npm": "scoped", "https://registry.example.com/npm/team": "scoped", "https://other.example.com": ""} {
		if got, _ := npmrcToken(text, registry); got != want {
			t.Errorf("%s = %q, want %q", registry, got, want)
		}
	}
}

// --- PyPI ---

func writeWheel(t *testing.T, path, name, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	dist := strings.ReplaceAll(name, "-", "_") + "-" + version + ".dist-info"
	w, _ := zw.Create(dist + "/METADATA")
	_, _ = fmt.Fprintf(w, "Metadata-Version: 2.1\nName: %s\nVersion: %s\nSummary: A demo\nClassifier: License :: OSI Approved :: MIT License\nClassifier: Programming Language :: Python :: 3\nRequires-Python: >=3.9\nProject-URL: Home, https://example.com\nDescription-Content-Type: text/markdown\n\nLong description.\n", name, version)
	w, _ = zw.Create(dist + "/WHEEL")
	_, _ = w.Write([]byte("Wheel-Version: 1.0\n"))
	_ = zw.Close()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeSdist(t *testing.T, path, name, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	body := fmt.Sprintf("Metadata-Version: 2.1\nName: %s\nVersion: %s\nSummary: A demo\n\n", name, version)
	_ = tw.WriteHeader(&tar.Header{Name: name + "-" + version + "/PKG-INFO", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte(body))
	_ = tw.Close()
	_ = zw.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256Hex(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

type pypiFixture struct {
	root       string
	cfg        Config
	wheel      []byte
	sdist      []byte
	server     *httptest.Server
	mu         sync.Mutex
	remote     map[string]string // filename -> sha256 listed by the JSON API
	uploads    []map[string][]string
	auth       []string
	uploadBody map[string][]byte
}

func newPyPIFixture(t *testing.T) *pypiFixture {
	t.Helper()
	isolateCredentials(t)
	fastPolling(t)
	f := &pypiFixture{root: t.TempDir(), remote: map[string]string{}, uploadBody: map[string][]byte{}}
	f.wheel = writeWheel(t, filepath.Join(f.root, "dist", "demo_pkg-1.2.3-py3-none-any.whl"), "demo-pkg", "1.2.3")
	f.sdist = writeSdist(t, filepath.Join(f.root, "dist", "demo-pkg-1.2.3.tar.gz"), "demo-pkg", "1.2.3")
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/legacy/":
			user, pass, _ := r.BasicAuth()
			f.auth = append(f.auth, user+":"+pass)
			if err := r.ParseMultipartForm(32 << 20); err != nil {
				t.Errorf("multipart: %v", err)
				http.Error(w, "bad", http.StatusBadRequest)
				return
			}
			file, header, err := r.FormFile("content")
			if err != nil {
				t.Errorf("content: %v", err)
				return
			}
			data, _ := io.ReadAll(file)
			if r.ContentLength <= 0 {
				t.Errorf("no Content-Length")
			}
			f.uploads = append(f.uploads, r.MultipartForm.Value)
			f.uploadBody[header.Filename] = data
			f.remote[header.Filename] = sha256Hex(data)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/pypi/demo-pkg/1.2.3/json":
			if len(f.remote) == 0 {
				http.NotFound(w, r)
				return
			}
			urls := []map[string]any{}
			for name, digest := range f.remote {
				urls = append(urls, map[string]any{"filename": name, "digests": map[string]string{"sha256": digest}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"urls": urls})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	t.Setenv("AX_PYPI_URL", f.server.URL+"/legacy/")
	t.Setenv("AX_PYPI_JSON_URL", f.server.URL+"/pypi")
	f.cfg = Config{Version: "1.2.3", PyPIProject: "Demo_Pkg", PyPIFiles: []string{"dist/demo_pkg-1.2.3-py3-none-any.whl", "dist/demo-pkg-1.2.3.tar.gz"}}
	return f
}

func (f *pypiFixture) target(t *testing.T, opts TargetOptions) Target {
	t.Helper()
	target, err := NewTarget("pypi", f.root, f.cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func TestPyPIUploadsBothFilesWithMetadataAndDigests(t *testing.T) {
	f := newPyPIFixture(t)
	t.Setenv("PYPI_API_TOKEN", "pypi-secret")
	target := f.target(t, TargetOptions{})
	if issues := target.Validate(f.cfg); len(issues) != 0 {
		t.Fatalf("validate = %v", issues)
	}
	if err := target.Credentials(); err != nil {
		t.Fatal(err)
	}
	results, err := RunTargets(context.Background(), []Target{target}, nil)
	if err != nil || results[0].Status != "published" {
		t.Fatalf("results = %+v err = %v", results, err)
	}
	if len(f.uploads) != 2 {
		t.Fatalf("uploads = %d", len(f.uploads))
	}
	wheel := f.uploads[0]
	if wheel[":action"][0] != "file_upload" || wheel["protocol_version"][0] != "1" || wheel["name"][0] != "demo-pkg" || wheel["version"][0] != "1.2.3" || wheel["filetype"][0] != "bdist_wheel" || wheel["pyversion"][0] != "py3" {
		t.Fatalf("wheel form = %v", wheel)
	}
	if wheel["sha256_digest"][0] != sha256Hex(f.wheel) || len(wheel["blake2_256_digest"][0]) != 64 {
		t.Fatalf("digests = %v", wheel)
	}
	if len(wheel["classifiers"]) != 2 || wheel["requires_python"][0] != ">=3.9" || wheel["project_urls"][0] != "Home, https://example.com" || wheel["description_content_type"][0] != "text/markdown" || strings.TrimSpace(wheel["description"][0]) != "Long description." {
		t.Fatalf("metadata fields = %v", wheel)
	}
	if sdist := f.uploads[1]; sdist["filetype"][0] != "sdist" || sdist["pyversion"][0] != "source" {
		t.Fatalf("sdist form = %v", sdist)
	}
	if f.auth[0] != "__token__:pypi-secret" {
		t.Fatalf("auth = %v", f.auth)
	}
	if !bytes.Equal(f.uploadBody["demo-pkg-1.2.3.tar.gz"], f.sdist) {
		t.Fatal("uploaded content differs")
	}
	// Idempotent: everything is live, so nothing more is uploaded.
	results, err = RunTargets(context.Background(), []Target{f.target(t, TargetOptions{})}, nil)
	if err != nil || results[0].Status != "skipped" || len(f.uploads) != 2 {
		t.Fatalf("rerun = %+v err = %v uploads = %d", results, err, len(f.uploads))
	}
}

func TestPyPIResumesWithOnlyTheMissingFile(t *testing.T) {
	f := newPyPIFixture(t)
	t.Setenv("PYPI_API_TOKEN", "tok")
	f.remote["demo_pkg-1.2.3-py3-none-any.whl"] = sha256Hex(f.wheel)
	target := f.target(t, TargetOptions{})
	_ = target.Credentials()
	if err := target.Publish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(f.uploads) != 1 || f.uploads[0]["filetype"][0] != "sdist" {
		t.Fatalf("uploads = %v", f.uploads)
	}
}

func TestPyPIFileMismatchFailsWithoutUploading(t *testing.T) {
	f := newPyPIFixture(t)
	t.Setenv("PYPI_API_TOKEN", "tok")
	f.remote["demo-pkg-1.2.3.tar.gz"] = strings.Repeat("0", 64)
	target := f.target(t, TargetOptions{})
	_ = target.Credentials()
	_, err := RunTargets(context.Background(), []Target{target}, nil)
	if errorCode(err) != "PYPI_FILE_MISMATCH" || len(f.uploads) != 0 {
		t.Fatalf("err = %v uploads = %d", err, len(f.uploads))
	}
}

func TestPyPIValidateChecksMetadataAgainstConfig(t *testing.T) {
	f := newPyPIFixture(t)
	f.cfg.PyPIProject = "other"
	f.cfg.PyPIFiles = append(f.cfg.PyPIFiles, "dist/absent.whl", "dist/notes.txt")
	writeTestFile(t, filepath.Join(f.root, "dist", "notes.txt"), "x")
	got := codes(f.target(t, TargetOptions{}).Validate(f.cfg))
	for _, code := range []string{"PYPI_METADATA_MISMATCH", "PYPI_FILE_UNREADABLE"} {
		if !strings.Contains(got, code) {
			t.Errorf("missing %s in %s", code, got)
		}
	}
	bare := Config{Version: "1.2.3"}
	if got := codes(newPyPITarget(f.root, bare, TargetOptions{}).Validate(bare)); strings.Count(got, "CONFIG_REQUIRED_MISSING") != 2 {
		t.Errorf("bare = %s", got)
	}
}

func TestPyPICredentialSources(t *testing.T) {
	f := newPyPIFixture(t)
	missing := f.target(t, TargetOptions{}).Credentials()
	if errorCode(missing) != "PYPI_CREDENTIALS_MISSING" || !strings.Contains(missing.Error(), "Fix:") {
		t.Fatalf("missing = %v", missing)
	}
	rc := filepath.Join(t.TempDir(), "pypirc")
	if err := os.WriteFile(rc, []byte("[distutils]\nindex-servers = pypi\n\n[pypi]\nusername = __token__\npassword = pypi-from-file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := f.target(t, TargetOptions{PypircPath: rc}).Credentials(); errorCode(err) != "PYPI_PYPIRC_PERMISSIONS_INSECURE" {
			t.Fatalf("insecure = %v", err)
		}
		_ = os.Chmod(rc, 0o600)
	}
	file := f.target(t, TargetOptions{PypircPath: rc})
	if err := file.Credentials(); err != nil || file.(*pypiTarget).password != "pypi-from-file" {
		t.Fatalf("file = %v", err)
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".pypirc"), []byte("[pypi]\npassword = pypi-home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := f.target(t, TargetOptions{Home: home})
	if err := h.Credentials(); err != nil || h.(*pypiTarget).username != "__token__" || h.(*pypiTarget).password != "pypi-home" {
		t.Fatalf("home = %v", err)
	}
	if err := f.target(t, TargetOptions{PypircPath: filepath.Join(t.TempDir(), "none")}).Credentials(); errorCode(err) != "PYPI_PYPIRC_UNREADABLE" {
		t.Fatalf("unreadable = %v", err)
	}
	t.Setenv("TWINE_PASSWORD", "pypi-twine")
	tw := f.target(t, TargetOptions{Home: home})
	if err := tw.Credentials(); err != nil || tw.(*pypiTarget).password != "pypi-twine" {
		t.Fatalf("twine = %v", err)
	}
	t.Setenv("PYPI_API_TOKEN", "pypi-api")
	api := f.target(t, TargetOptions{Home: home})
	if err := api.Credentials(); err != nil || api.(*pypiTarget).password != "pypi-api" {
		t.Fatalf("api = %v", err)
	}
}

// --- shared ---

func TestCredentialFileCheckSkipsWindowsWithWarning(t *testing.T) {
	var warnings bytes.Buffer
	saved := CredentialWarnings
	CredentialWarnings = &warnings
	defer func() { CredentialWarnings = saved }()
	if credentialFileInsecure(`C:\Users\me\.npmrc`, "windows", 0o666) {
		t.Fatal("windows modes must not fail the check")
	}
	if !strings.Contains(warnings.String(), "Windows") {
		t.Fatalf("warning = %q", warnings.String())
	}
	warnings.Reset()
	if !credentialFileInsecure("/h/.npmrc", "linux", 0o644) || credentialFileInsecure("/h/.npmrc", "darwin", 0o600) || warnings.Len() != 0 {
		t.Fatal("POSIX check changed")
	}
}

func TestReleaseStateLinesAreTolerated(t *testing.T) {
	for line, want := range map[string]bool{
		"?? .assayxport/releases/1.2.3.json":      true,
		"?? .assayxport/releases/1.2.3.npm.json":  true,
		"?? .assayxport/releases/1.2.3.pypi.json": true,
		"?? .assayxport/releases/1.2.4.npm.json":  false,
		"?? .assayxport/releases/1.2.3.npm.txt":   false,
		"?? .assayxport/releases/1.2.3.a/b.json":  false,
		" M README.md":                            false,
	} {
		if got := releaseStateLine(line, "1.2.3"); got != want {
			t.Errorf("%q = %v, want %v", line, got, want)
		}
	}
}

func TestSchemaTwoParsesTargetTablesAndSchemaOneRejectsThem(t *testing.T) {
	text := `schema_version = 2
version = "1.2.3"
targets = ["npm", "pypi"]

[go]
module = "example.com/m"
tag_prefix = "hosts/go/"

[npm]
registry = "https://registry.example.com"
packages = ["a=dist/a-1.2.3.tgz", "b=dist/b-1.2.3.tgz@next"]

[pypi]
project = "demo"
repository = "https://upload.example.com/legacy/"
json_url = "https://pypi.example.com/pypi"
files = ["dist/a.whl"]
`
	cfg, issues := loadConfigFromText(t, t.TempDir(), text)
	if len(issues) != 0 || cfg.Schema != 2 {
		t.Fatalf("issues = %v schema = %d", issues, cfg.Schema)
	}
	if strings.Join(cfg.Targets, ",") != "npm,pypi" || len(cfg.NPMPackages) != 2 || cfg.NPMRegistry != "https://registry.example.com" || cfg.GoTagPrefix != "hosts/go/" || cfg.PyPIProject != "demo" || cfg.PyPIJSONURL == "" || len(cfg.PyPIFiles) != 1 {
		t.Fatalf("cfg = %+v", cfg)
	}
	_, issues = loadConfigFromText(t, t.TempDir(), strings.Replace(text, "schema_version = 2", "schema_version = 1", 1))
	got := codes(issues)
	if strings.Count(got, "CONFIG_KEY_UNSUPPORTED") != 8 {
		t.Fatalf("schema 1 issues = %s", got)
	}
	_, issues = loadConfigFromText(t, t.TempDir(), strings.Replace(text, "schema_version = 2", "schema_version = 3", 1))
	if !strings.Contains(codes(issues), "CONFIG_SCHEMA_UNSUPPORTED") {
		t.Fatalf("schema 3 issues = %s", codes(issues))
	}
}

func TestSchemaOneStillParsesExactlyAsBefore(t *testing.T) {
	cfg, issues := loadConfigFromText(t, t.TempDir(), repoConfig)
	if len(issues) != 0 || cfg.Schema != 1 || len(cfg.Artifacts) != 3 || len(cfg.Targets) != 0 {
		t.Fatalf("cfg = %+v issues = %v", cfg, issues)
	}
	_, issues = loadConfigFromText(t, t.TempDir(), "schema_version = 1\nartifacts = [\"a\", \"b\"]\n")
	if len(issues) != 0 {
		t.Fatalf("array artifacts: %v", issues)
	}
	_, issues = loadConfigFromText(t, t.TempDir(), "schema_version = 1\nartifacts = [a]\n")
	if codes(issues) != "CONFIG_VALUE_INVALID" {
		t.Fatalf("bad array: %v", issues)
	}
}

func TestTargetOrderAndNames(t *testing.T) {
	if got := strings.Join(OrderTargets([]string{"go", "npm", "mvn", "pypi", "npm"}, Config{}), ","); got != "npm,mvn,pypi,go" {
		t.Errorf("default order = %s", got)
	}
	if got := strings.Join(OrderTargets([]string{"mvn", "go"}, Config{Policy: "goplus-dual"}), ","); got != "go,mvn" {
		t.Errorf("goplus-dual order = %s", got)
	}
	if got := codes(validateTargetNames(Config{Targets: []string{"npm", "npm", "ruby"}})); got != "TARGET_DUPLICATE,TARGET_UNKNOWN" {
		t.Errorf("names = %s", got)
	}
	if _, err := NewTarget("ruby", ".", Config{}, TargetOptions{}); errorCode(err) != "TARGET_UNKNOWN" {
		t.Errorf("err = %v", err)
	}
}

func TestCargoIsRegisteredButNotImplemented(t *testing.T) {
	target, err := NewTarget("cargo", ".", Config{}, TargetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := codes(target.Validate(Config{})); got != "TARGET_NOT_IMPLEMENTED" {
		t.Fatalf("validate = %s", got)
	}
	if err = target.Publish(context.Background(), nil); errorCode(err) != "TARGET_NOT_IMPLEMENTED" {
		t.Fatalf("publish = %v", err)
	}
}

func TestIssueErrorRoundTripsThroughIssueFromError(t *testing.T) {
	issue := Issue{"SOME_CODE", "something broke", "do the thing"}
	if got := issueFromError(fmt.Errorf("wrapped: %w", issue)); got.Code != "SOME_CODE" {
		t.Fatalf("wrapped = %+v", got)
	}
	if got := issueFromError(fmt.Errorf("[CENTRAL_X] problem text\n  Fix: fix text")); got != (Issue{"CENTRAL_X", "problem text", "fix text"}) {
		t.Fatalf("parsed = %+v", got)
	}
	if got := issueFromError(fmt.Errorf("plain")); got.Code != "TARGET_FAILED" {
		t.Fatalf("plain = %+v", got)
	}
}
