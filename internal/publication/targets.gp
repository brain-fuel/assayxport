package publication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Target is one publication channel. Every target is idempotent and
// resumable: Published asks the channel what it already has, and Publish
// only does what is still missing.
type Target interface {
	// Name is the flag and config name: npm, mvn, pypi, go or cargo.
	Name() string
	// Validate checks the configuration and local inputs without network
	// access or any change.
	Validate(cfg Config) []Issue
	// Credentials fails, with an Issue-formatted error, when the target's
	// credentials are absent or unusable. It never prints a secret.
	Credentials() error
	// Published reports whether everything this target owns is already live.
	// A published item that differs from the local one is an error.
	Published(ctx context.Context) (TargetState, error)
	// Publish uploads what is missing and verifies the result.
	Publish(ctx context.Context, progress Progress) error
	// Summary is the stdout report after a successful run.
	Summary() []string
}

// TargetState is what a registry says about a release.
type TargetState struct {
	Done   bool
	Detail string
}

// TargetOptions carries the command-line choices that reach the targets.
type TargetOptions struct {
	NpmrcPath, PypircPath, MavenSettingsPath string
	// Home overrides the home directory used for ~/.npmrc and ~/.pypirc.
	Home string
}

// TargetNames lists every registered target in the default run order.
var TargetNames = []string{"npm", "mvn", "pypi", "go", "cargo"}

// CredentialWarnings receives notices that a credential check was skipped.
var CredentialWarnings io.Writer = os.Stderr

// NewTarget builds the named target for a release checkout.
func NewTarget(name, root string, cfg Config, opts TargetOptions) (Target, error) {
	switch name {
	case "npm": return newNPMTarget(root, cfg, opts), nil
	case "mvn": return &mavenTarget{root: root, cfg: cfg, opts: opts}, nil
	case "pypi": return newPyPITarget(root, cfg, opts), nil
	case "go": return &goTarget{root: root, cfg: cfg}, nil
	case "cargo": return cargoTarget{}, nil
	}
	return nil, Issue{"TARGET_UNKNOWN", "unknown publication target " + fmt.Sprintf("%q", name), "Use one of " + strings.Join(TargetNames, ", ") + "."}
}

// OrderTargets returns the selected targets, deduplicated, in run order: npm,
// mvn, pypi, go. For the goplus-dual policy go runs before mvn, because that
// Maven bundle must be built from the exact remote Go tag.
func OrderTargets(names []string, cfg Config) []string {
	order := []string{"npm", "mvn", "pypi", "go", "cargo"}
	if cfg.Policy == "goplus-dual" { order = []string{"npm", "go", "mvn", "pypi", "cargo"} }
	want := map[string]bool{}
	for _, n := range names { want[n] = true }
	out := []string{}
	for _, n := range order { if want[n] { out = append(out, n); delete(want, n) } }
	rest := []string{}
	for n := range want { rest = append(rest, n) }
	sort.Strings(rest)
	return append(out, rest...)
}

func validateTargetNames(cfg Config) []Issue {
	issues := []Issue{}
	known := map[string]bool{}
	for _, n := range TargetNames { known[n] = true }
	seen := map[string]bool{}
	for _, n := range cfg.Targets {
		if !known[n] { issues = append(issues, Issue{"TARGET_UNKNOWN", "targets lists unknown target " + fmt.Sprintf("%q", n), "Use only " + strings.Join(TargetNames, ", ") + " in targets."}); continue }
		if seen[n] { issues = append(issues, Issue{"TARGET_DUPLICATE", "targets lists " + n + " more than once", "List each target once."}) }
		seen[n] = true
	}
	return issues
}

// PreflightTargets is the preflight for an explicit target selection. It
// loads the configuration, validates only what the selected targets use,
// checks each target's credentials when checkCredentials is set, and keeps
// the clean-checkout and tag gates. Every problem is reported together.
func PreflightTargets(root, configPath string, names []string, opts TargetOptions, checkCredentials bool) (Config, []Target, Report) {
	cfg, issues := loadConfig(configPath)
	for _, issue := range issues { if issue.Code == "CONFIG_MISSING" { return cfg, nil, Report{Issues: []Issue{issue}} } }
	issues = append(issues, validateTargetNames(cfg)...)
	names = OrderTargets(names, cfg)
	selected := map[string]bool{}
	for _, n := range names { selected[n] = true }
	if !selected["mvn"] { issues = append(issues, validateRelease(cfg)...) }
	targets := []Target{}
	for _, name := range names {
		t, err := NewTarget(name, root, cfg, opts)
		if err != nil { issues = append(issues, issueFromError(err)); continue }
		targets = append(targets, t)
		issues = append(issues, t.Validate(cfg)...)
		if checkCredentials { if err := t.Credentials(); err != nil { issues = append(issues, issueFromError(err)) } }
	}
	issues = append(issues, validateGitRelease(root, cfg)...)
	sort.SliceStable(issues, func(i, j int) bool { return issues[i].Code < issues[j].Code })
	return cfg, targets, Report{Issues: issues}
}

var releaseVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func validateRelease(cfg Config) []Issue {
	if strings.TrimSpace(cfg.Version) == "" { return []Issue{{"CONFIG_REQUIRED_MISSING", "required setting version is missing or empty", "Add version = \"X.Y.Z\" to assayxport.toml."}} }
	if !releaseVersionPattern.MatchString(cfg.Version) { return []Issue{{"VERSION_INVALID", "version must be a release X.Y.Z without a v prefix", "Set version to the release version, for example \"0.36.3\"."}} }
	return nil
}

// issueFromError recovers the code, problem and fix from an error written in
// the "[CODE] problem\n  Fix: fix" layout; any other error becomes a generic
// issue that keeps its text.
func issueFromError(err error) Issue {
	var issue Issue
	if errors.As(err, &issue) { return issue }
	text := err.Error()
	code := errorCode(err)
	if code == "" { return Issue{"TARGET_FAILED", text, "Correct the problem and rerun `ax publish`."} }
	body := strings.TrimSpace(strings.TrimPrefix(text, "["+code+"]"))
	problem, fix, _ := strings.Cut(body, "\n  Fix: ")
	return Issue{code, strings.TrimSpace(problem), strings.TrimSpace(fix)}
}

// errorCode returns CODE from an error whose text starts with "[CODE]".
func errorCode(err error) string {
	var issue Issue
	if errors.As(err, &issue) { return issue.Code }
	text := err.Error()
	if !strings.HasPrefix(text, "[") { return "" }
	end := strings.Index(text, "]")
	if end < 2 { return "" }
	return text[1:end]
}

// TargetResult is one line of the final summary.
type TargetResult struct {
	Target, Status, Detail string
}

// RunTargets publishes each target in order. A target that Published reports
// done is skipped. The run stops at the first failure and lists the rest as
// not run, so a rerun resumes where this one stopped.
func RunTargets(ctx context.Context, targets []Target, progress Progress) ([]TargetResult, error) {
	report := func(message string) { if progress != nil { progress(message) } }
	results := []TargetResult{}
	for index, t := range targets {
		report(t.Name() + ": checking what is already published")
		state, err := t.Published(ctx)
		if err == nil && state.Done {
			results = append(results, TargetResult{t.Name(), "skipped", state.Detail})
			report(t.Name() + ": already published; skipped")
			continue
		}
		if err := t.Publish(ctx, func(message string) { report(t.Name() + ": " + message) }); err != nil {
			results = append(results, TargetResult{t.Name(), "failed", firstLine(err.Error())})
			for _, rest := range targets[index+1:] { results = append(results, TargetResult{rest.Name(), "not run", "an earlier target failed"}) }
			return results, err
		}
		results = append(results, TargetResult{t.Name(), "published", ""})
	}
	return results, nil
}

func firstLine(text string) string { line, _, _ := strings.Cut(text, "\n"); return line }

// credentialFileInsecure is the owner-only check for a file that holds a
// secret. POSIX modes mean nothing on Windows, so there the check is skipped
// with a notice rather than failed.
func credentialFileInsecure(path, goos string, mode fs.FileMode) bool {
	if goos == "windows" {
		if CredentialWarnings != nil { fmt.Fprintf(CredentialWarnings, "ax: warning: not checking the permissions of %s on Windows; keep it readable only by you\n", path) }
		return false
	}
	return mode.Perm()&0077 != 0
}

// homeDirectory is the directory ~/.npmrc and ~/.pypirc resolve against.
func homeDirectory(opts TargetOptions) string {
	if opts.Home != "" { return opts.Home }
	home, _ := os.UserHomeDir()
	return home
}

// httpEndpoint accepts https, and http only for a loopback host (local
// registries and tests), so a credential is never sent in the clear.
func httpEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" { return false }
	if u.Scheme == "https" { return true }
	host := u.Hostname()
	return u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")
}

// targetState is the non-secret record of a target's progress, kept in
// .assayxport/releases/<version>.<target>.json.
type targetState struct {
	Target  string            `json:"target"`
	Version string            `json:"version"`
	Items   map[string]string `json:"items"`
}

func targetStatePath(root, version, target string) string { return filepath.Join(root, ".assayxport", "releases", version+"."+target+".json") }

func readTargetState(path string) (targetState, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) { return targetState{Items: map[string]string{}}, nil }
	if err != nil { return targetState{}, err }
	var state targetState
	if err = json.Unmarshal(data, &state); err != nil { return state, fmt.Errorf("invalid release state %s: %w", path, err) }
	if state.Items == nil { state.Items = map[string]string{} }
	return state, nil
}

func writeTargetState(path string, state targetState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil { return err }
	data = append(data, '\n')
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil { return err }
	return os.WriteFile(path, data, 0o600)
}

// recordItem saves one published item (a package or file and its digest).
func recordItem(root string, cfg Config, target, key, digest string) error {
	path := targetStatePath(root, cfg.Version, target)
	state, err := readTargetState(path)
	if err != nil { return err }
	state.Target, state.Version = target, cfg.Version
	state.Items[key] = digest
	return writeTargetState(path, state)
}

// mavenTarget wraps the Central flow unchanged: prepare the signed bundle,
// verify the Go half for goplus-dual, then upload, validate and promote.
type mavenTarget struct {
	root string
	cfg Config
	opts TargetOptions
	lines []string
}

func (t *mavenTarget) Name() string { return "mvn" }
func (t *mavenTarget) Validate(cfg Config) []Issue { return validateMaven(t.root, cfg) }
func (t *mavenTarget) Credentials() error {
	_, _, err := centralCredentials(t.root, t.opts.MavenSettingsPath)
	return err
}
func (t *mavenTarget) Published(ctx context.Context) (TargetState, error) {
	state, err := readReleaseState(filepath.Join(t.root, ".assayxport", "releases", t.cfg.Version+".json"))
	if err != nil { return TargetState{}, err }
	if state.State == "PUBLISHED" { return TargetState{Done: true, Detail: "Central deployment " + state.DeploymentID + " is PUBLISHED"}, nil }
	return TargetState{}, nil
}
func (t *mavenTarget) Publish(ctx context.Context, progress Progress) error {
	report := func(message string) { if progress != nil { progress(message) } }
	report("preparing deterministic signed Central bundle")
	prepared, err := Prepare(ctx, t.root, t.cfg)
	if err != nil { return fmt.Errorf("[BUNDLE_PREPARATION_FAILED] %v\n  Fix: correct the signing or artifact error, then rerun; no upload was attempted", err) }
	t.lines = append(t.lines, "Central bundle: "+prepared.Bundle, "OpenPGP fingerprint: "+prepared.Fingerprint)
	report("verifying the remote Go tag, module proxy, and pkg.go.dev")
	if err = VerifyGoRelease(ctx, t.root, t.cfg); err != nil { return err }
	deployment, err := Publish(ctx, t.root, t.cfg, prepared, t.opts.MavenSettingsPath, progress)
	if err != nil { return err }
	t.lines = append(t.lines, "Central deployment: "+deployment.DeploymentID, "Central state: "+deployment.DeploymentState)
	t.lines = append(t.lines, deployment.PURLs...)
	return nil
}
func (t *mavenTarget) Summary() []string { return t.lines }

// cargoTarget is registered as the pattern for the next channel: it is
// selectable, and reports plainly that it is not implemented.
type cargoTarget struct{}

var errCargo = Issue{"TARGET_NOT_IMPLEMENTED", "the cargo target is not implemented yet", "Publish crates with `cargo publish` for now; npm, mvn, pypi and go are available."}

func (cargoTarget) Name() string { return "cargo" }
func (cargoTarget) Validate(cfg Config) []Issue { return []Issue{errCargo} }
func (cargoTarget) Credentials() error { return nil }
func (cargoTarget) Published(ctx context.Context) (TargetState, error) { return TargetState{}, errCargo }
func (cargoTarget) Publish(ctx context.Context, progress Progress) error { return errCargo }
func (cargoTarget) Summary() []string { return nil }
