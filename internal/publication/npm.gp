package publication

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// NPMRegistry is the registry used when neither [npm] registry nor
// AX_NPM_REGISTRY_URL says otherwise.
const NPMRegistry = "https://registry.npmjs.org"

// npmPollDelay and npmPollAttempts bound how long a fresh publish may take
// to show up in the registry's version document.
var (
	npmPollDelay = 2 * time.Second
	npmPollAttempts = 15
)

type npmPackage struct {
	Name, Path, Tag string
}

var (
	npmNamePattern = regexp.MustCompile(`^(@[a-z0-9~][a-z0-9._~-]*/)?[a-z0-9~][a-z0-9._~-]*$`)
	npmTagPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// parseNPMPackages reads [npm] packages entries: "name=path/to.tgz", or
// "name=path/to.tgz@tag" to publish under that dist-tag. A tag is only
// recognised after a path that ends in .tgz or .tar.gz, so an "@" inside a
// directory name is never taken for one.
func parseNPMPackages(entries []string) ([]npmPackage, []Issue) {
	pkgs, issues := []npmPackage{}, []Issue{}
	fix := "Write each entry as \"name=path/to/name-1.2.3.tgz\", or \"name=path/to/name-1.2.3.tgz@next\" to publish under a dist-tag."
	for _, entry := range entries {
		name, rest, ok := strings.Cut(entry, "=")
		name, rest = strings.TrimSpace(name), strings.TrimSpace(rest)
		if !ok || name == "" || rest == "" { issues = append(issues, Issue{"NPM_PACKAGE_INVALID", fmt.Sprintf("npm.packages entry %q is not name=path", entry), fix}); continue }
		tag := ""
		if !isTarball(rest) {
			if i := strings.LastIndex(rest, "@"); i > 0 && isTarball(rest[:i]) {
				rest, tag = rest[:i], rest[i+1:]
			} else {
				issues = append(issues, Issue{"NPM_PACKAGE_INVALID", fmt.Sprintf("npm.packages entry %q does not name a .tgz tarball", entry), fix}); continue
			}
		}
		if !npmNamePattern.MatchString(name) { issues = append(issues, Issue{"NPM_PACKAGE_INVALID", fmt.Sprintf("%q is not a valid npm package name", name), "Use a lowercase npm package name, optionally scoped, such as @scope/name."}); continue }
		if tag != "" && !npmTagPattern.MatchString(tag) { issues = append(issues, Issue{"NPM_PACKAGE_INVALID", fmt.Sprintf("dist-tag %q for %s is not valid", tag, name), "Use a plain tag such as next or beta."}); continue }
		pkgs = append(pkgs, npmPackage{name, rest, tag})
	}
	return pkgs, issues
}

func isTarball(p string) bool { return strings.HasSuffix(p, ".tgz") || strings.HasSuffix(p, ".tar.gz") }

type npmTarget struct {
	root string
	cfg Config
	opts TargetOptions
	pkgs []npmPackage
	token, tokenSource string
	http *http.Client
	lines []string
}

func newNPMTarget(root string, cfg Config, opts TargetOptions) *npmTarget {
	pkgs, _ := parseNPMPackages(cfg.NPMPackages)
	return &npmTarget{root: root, cfg: cfg, opts: opts, pkgs: pkgs, http: &http.Client{Timeout: 2 * time.Minute}}
}

func (t *npmTarget) Name() string { return "npm" }
func (t *npmTarget) Summary() []string { return t.lines }

func (t *npmTarget) registry() string {
	if v := strings.TrimSpace(os.Getenv("AX_NPM_REGISTRY_URL")); v != "" { return strings.TrimRight(v, "/") }
	if t.cfg.NPMRegistry != "" { return strings.TrimRight(t.cfg.NPMRegistry, "/") }
	return NPMRegistry
}

func (t *npmTarget) Validate(cfg Config) []Issue {
	issues := validateRelease(cfg)
	if len(cfg.NPMPackages) == 0 { issues = append(issues, Issue{"CONFIG_REQUIRED_MISSING", "required setting npm.packages is missing or empty", "Add [npm] packages = [\"name=path/to/name-1.2.3.tgz\"] to assayxport.toml (schema_version = 2), in publish order."}) }
	pkgs, parse := parseNPMPackages(cfg.NPMPackages)
	issues = append(issues, parse...)
	if registry := t.registry(); !httpEndpoint(registry) { issues = append(issues, Issue{"NPM_REGISTRY_INVALID", "npm registry " + registry + " is not an https URL", "Set [npm] registry or AX_NPM_REGISTRY_URL to an https:// registry URL."}) }
	seen := map[string]bool{}
	for _, pkg := range pkgs {
		if seen[pkg.Name] { issues = append(issues, Issue{"NPM_PACKAGE_DUPLICATE", pkg.Name + " is listed more than once in npm.packages", "List each package once."}); continue }
		seen[pkg.Name] = true
		file, ok := safePath(t.root, pkg.Path)
		if !ok { issues = append(issues, Issue{"NPM_TARBALL_PATH_INVALID", pkg.Name + " tarball path escapes the project root", "Use a relative path inside the checkout."}); continue }
		name, version, err := readTarballPackage(file)
		if err != nil { issues = append(issues, Issue{"NPM_TARBALL_MISSING", fmt.Sprintf("cannot read the %s tarball %s: %v", pkg.Name, pkg.Path, err), "Build the tarball (for example `npm pack`) before publishing, and point npm.packages at it."}); continue }
		if name != pkg.Name || (cfg.Version != "" && version != cfg.Version) { issues = append(issues, Issue{"NPM_TARBALL_MISMATCH", fmt.Sprintf("%s holds %s@%s but the release expects %s@%s", pkg.Path, name, version, pkg.Name, cfg.Version), "Rebuild the tarball for this release, or correct the package name or version."}) }
	}
	if _, err := exec.LookPath("npm"); err != nil { issues = append(issues, Issue{"NPM_CLI_MISSING", "the npm command is not on PATH", "Install Node.js and npm; ax publishes through `npm publish` so web login and one-time passwords work at your terminal."}) }
	return issues
}

// readTarballPackage returns the name and version in an npm tarball's
// package.json (npm packs it as <top>/package.json).
func readTarballPackage(file string) (string, string, error) {
	f, err := os.Open(file)
	if err != nil { return "", "", err }
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil { return "", "", err }
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF { return "", "", fmt.Errorf("no package.json inside") }
		if err != nil { return "", "", err }
		if h.Typeflag != tar.TypeReg { continue }
		if parts := strings.Split(h.Name, "/"); len(parts) != 2 || parts[1] != "package.json" { continue }
		var manifest struct{ Name, Version string }
		data, err := io.ReadAll(io.LimitReader(tr, 4<<20))
		if err != nil { return "", "", err }
		if err = json.Unmarshal(data, &manifest); err != nil { return "", "", fmt.Errorf("package.json: %w", err) }
		return manifest.Name, manifest.Version, nil
	}
}

// Credentials finds a registry token: NPM_TOKEN, else an _authToken for the
// registry in an .npmrc (--npmrc-path, or the project's, then ~/.npmrc).
func (t *npmTarget) Credentials() error {
	if token := strings.TrimSpace(os.Getenv("NPM_TOKEN")); token != "" {
		t.token, t.tokenSource = token, "NPM_TOKEN"
		return nil
	}
	files := []string{}
	explicit := strings.TrimSpace(t.opts.NpmrcPath)
	if explicit != "" {
		if !filepath.IsAbs(explicit) { explicit = filepath.Join(t.root, explicit) }
		files = append(files, explicit)
	} else {
		files = append(files, filepath.Join(t.root, ".npmrc"))
		if home := homeDirectory(t.opts); home != "" { files = append(files, filepath.Join(home, ".npmrc")) }
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if os.IsNotExist(err) && explicit == "" { continue }
		if err != nil { return Issue{"NPM_NPMRC_UNREADABLE", fmt.Sprintf("cannot read %s: %v", file, err), "Correct --npmrc-path and its permissions, or export NPM_TOKEN."} }
		token, literal := npmrcToken(string(data), t.registry())
		if token == "" { continue }
		if literal {
			if info, statErr := os.Stat(file); statErr == nil && credentialFileInsecure(file, runtime.GOOS, info.Mode()) {
				return Issue{"NPM_NPMRC_PERMISSIONS_INSECURE", file + " holds a token and is readable by group or other users", fmt.Sprintf("Run `chmod 600 %q`, then rerun `ax publish`.", file)}
			}
		}
		t.token, t.tokenSource = token, file
		return nil
	}
	return Issue{"NPM_CREDENTIALS_MISSING", "no npm credentials: NPM_TOKEN is unset and no .npmrc holds an auth token for " + t.registry(), "Export NPM_TOKEN, or run `npm login` (it writes //" + registryHostPath(t.registry()) + "/:_authToken to ~/.npmrc), or pass `ax publish --npmrc-path ./.npmrc`."}
}

func registryHostPath(registry string) string {
	u, err := url.Parse(registry)
	if err != nil { return registry }
	return u.Host + strings.TrimRight(u.Path, "/")
}

// npmrcToken returns the _authToken for registry from .npmrc text, trying the
// registry's path and then each parent, as npm does. literal is false when
// the value came from a ${VAR} reference rather than from the file.
func npmrcToken(text, registry string) (token string, literal bool) {
	u, err := url.Parse(registry)
	if err != nil { return "", false }
	values := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' { continue }
		key, value, ok := strings.Cut(line, "=")
		if !ok { continue }
		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), "\"'")
	}
	dir := "//" + u.Host + strings.TrimRight(u.Path, "/") + "/"
	for {
		if raw, ok := values[dir+":_authToken"]; ok {
			expanded := os.Expand(raw, os.Getenv)
			return strings.TrimSpace(expanded), !strings.Contains(raw, "$")
		}
		if dir == "//"+u.Host+"/" { return "", false }
		trimmed := strings.TrimSuffix(dir, "/")
		dir = trimmed[:strings.LastIndex(trimmed, "/")+1]
	}
}

func (t *npmTarget) integrity(pkg npmPackage) (string, string, error) {
	file, ok := safePath(t.root, pkg.Path)
	if !ok { return "", "", fmt.Errorf("unsafe tarball path %s", pkg.Path) }
	data, err := os.ReadFile(file)
	if err != nil { return "", "", err }
	s512 := sha512.Sum512(data)
	s1 := sha1.Sum(data)
	return "sha512-" + base64.StdEncoding.EncodeToString(s512[:]), hex.EncodeToString(s1[:]), nil
}

type npmCheck int

const (
	npmMissing npmCheck = iota
	npmSame
)

// check asks the registry about one package version.
func (t *npmTarget) check(ctx context.Context, pkg npmPackage) (npmCheck, error) {
	integrity, shasum, err := t.integrity(pkg)
	if err != nil { return npmMissing, Issue{"NPM_TARBALL_MISSING", fmt.Sprintf("cannot read the %s tarball: %v", pkg.Name, err), "Rebuild the tarball before publishing."} }
	endpoint := t.registry() + "/" + url.PathEscape(pkg.Name) + "/" + url.PathEscape(t.cfg.Version)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil { return npmMissing, err }
	req.Header.Set("Accept", "application/json")
	if t.token != "" { req.Header.Set("Authorization", "Bearer "+t.token) }
	resp, err := t.http.Do(req)
	if err != nil { return npmMissing, Issue{"NPM_REGISTRY_UNAVAILABLE", fmt.Sprintf("cannot reach %s: %v", endpoint, err), "Check the network and the registry URL, then rerun `ax publish --npm`."} }
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound { io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)); return npmMissing, nil }
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return npmMissing, Issue{"NPM_REGISTRY_ERROR", fmt.Sprintf("%s returned HTTP %d: %s", endpoint, resp.StatusCode, strings.TrimSpace(string(body))), "Check the registry and your access, then rerun `ax publish --npm`."}
	}
	var doc struct{ Dist struct{ Integrity, Shasum string } }
	if err = json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&doc); err != nil { return npmMissing, Issue{"NPM_REGISTRY_ERROR", "unreadable version document from " + endpoint + ": " + err.Error(), "Rerun `ax publish --npm`; if it persists, check the registry."} }
	same := doc.Dist.Integrity == integrity || (doc.Dist.Integrity == "" && doc.Dist.Shasum != "" && doc.Dist.Shasum == shasum)
	if !same { return npmMissing, Issue{"NPM_VERSION_MISMATCH", fmt.Sprintf("%s@%s is already published with different contents (registry %s, local %s)", pkg.Name, t.cfg.Version, doc.Dist.Integrity, integrity), "npm versions are immutable: publish a new version, or restore the exact tarball that was published."} }
	return npmSame, nil
}

func (t *npmTarget) Published(ctx context.Context) (TargetState, error) {
	for _, pkg := range t.pkgs {
		state, err := t.check(ctx, pkg)
		if err != nil { return TargetState{}, err }
		if state != npmSame { return TargetState{}, nil }
	}
	return TargetState{Done: true, Detail: fmt.Sprintf("%d package(s) match the registry", len(t.pkgs))}, nil
}

func (t *npmTarget) Publish(ctx context.Context, progress Progress) error {
	report := func(message string) { if progress != nil { progress(message) } }
	// Look at every package first, so a mismatch anywhere stops the run
	// before the first upload.
	states := make([]npmCheck, len(t.pkgs))
	for i, pkg := range t.pkgs {
		state, err := t.check(ctx, pkg)
		if err != nil { return err }
		states[i] = state
	}
	for i, pkg := range t.pkgs {
		state, err := states[i], error(nil)
		key := pkg.Name + "@" + t.cfg.Version
		integrity, _, _ := t.integrity(pkg)
		if state == npmSame {
			report(key + " is already published with the same contents; skipped")
			t.lines = append(t.lines, "npm "+key+": already published")
			if err = recordItem(t.root, t.cfg, "npm", key, integrity); err != nil { return err }
			continue
		}
		report("publishing " + key)
		if err = t.runNPM(ctx, pkg); err != nil { return err }
		if err = t.await(ctx, pkg); err != nil { return err }
		if err = recordItem(t.root, t.cfg, "npm", key, integrity); err != nil { return err }
		t.lines = append(t.lines, "npm "+key+": published")
		report(key + " is live on the registry")
	}
	return nil
}

// runNPM runs `npm publish` with the terminal attached, so web login and
// one-time-password prompts work. A token reaches npm through its
// environment only; nothing is written to disk.
func (t *npmTarget) runNPM(ctx context.Context, pkg npmPackage) error {
	file, _ := safePath(t.root, pkg.Path)
	args := []string{"publish", file, "--access", "public", "--registry", t.registry()}
	if pkg.Tag != "" { args = append(args, "--tag", pkg.Tag) }
	cmd := exec.CommandContext(ctx, "npm", args...)
	cmd.Dir = t.root
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = os.Environ()
	if t.tokenSource == "NPM_TOKEN" && t.token != "" {
		cmd.Env = append(cmd.Env, "NPM_TOKEN="+t.token, "npm_config_//"+registryHostPath(t.registry())+"/:_authToken="+t.token)
	}
	if err := cmd.Run(); err != nil {
		return Issue{"NPM_PUBLISH_FAILED", fmt.Sprintf("`npm publish` of %s@%s failed: %v", pkg.Name, t.cfg.Version, err), "Fix the npm error shown above and rerun `ax publish --npm`; packages already published are skipped."}
	}
	return nil
}

// await waits for the registry to serve the tarball that was just published.
func (t *npmTarget) await(ctx context.Context, pkg npmPackage) error {
	for attempt := 1; ; attempt++ {
		state, err := t.check(ctx, pkg)
		if err != nil { return err }
		if state == npmSame { return nil }
		if attempt >= npmPollAttempts { return Issue{"NPM_PUBLISH_UNVERIFIED", fmt.Sprintf("%s@%s is not visible on %s after `npm publish` succeeded", pkg.Name, t.cfg.Version, t.registry()), "Wait a minute and rerun `ax publish --npm`; it verifies and skips what is live."} }
		select {
		case <-ctx.Done(): return ctx.Err()
		case <-time.After(npmPollDelay):
		}
	}
}
