package source

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Options configures resolution.
type Options struct {
	// CacheDir overrides the remote-source cache root
	// (default: os.UserCacheDir()/assayxport).
	CacheDir string
	// Offline forbids all network access: no ls-remote, no fetch. Remote
	// refs resolve only through the cache and its recorded prior
	// resolutions.
	Offline bool
}

// Resolved is a source ready to scan.
type Resolved struct {
	Dir    string // local directory to scan
	Label  string // stable display label for output
	Kind   string // "dir" | "local-git" | "remote-git"
	Commit string // full commit SHA; "" for Kind "dir"
	Ref    string // the ref as given; "" when none
	// Canonical identifies the underlying repository for drift-mode
	// auto-detection: the canonical "host/path" for a remote, the absolute
	// repository path for a local source. Never emitted in output.
	Canonical string
	// Cleanup releases any scratch materialization. Always non-nil; a
	// no-op for in-place directories and cache hits.
	Cleanup func()
}

func noop() {}

// DefaultCacheDir returns the default cache root for remote sources.
func DefaultCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate user cache dir: %w", err)
	}
	return filepath.Join(base, "assayxport"), nil
}

// Resolve parses raw and materializes it into a scannable directory. It
// never mutates a user working tree, index, stash, or HEAD, and never
// prompts for credentials (GIT_TERMINAL_PROMPT=0 on every git invocation).
func Resolve(ctx context.Context, raw string, opt Options) (Resolved, error) {
	sp, err := ParseSpec(raw)
	if err != nil {
		return Resolved{}, err
	}
	if sp.Remote {
		return resolveRemote(ctx, sp, opt)
	}
	return resolveLocal(ctx, sp)
}

func resolveLocal(ctx context.Context, sp Spec) (Resolved, error) {
	// A directory that exists exactly as typed wins over ref splitting, so
	// a directory whose own name contains '#' still resolves (it just
	// cannot carry a ref).
	if fi, err := os.Stat(sp.Raw); err == nil && fi.IsDir() && sp.Ref != "" {
		sp = Spec{Raw: sp.Raw, Source: sp.Raw}
	}
	abs, err := filepath.Abs(sp.Source)
	if err != nil {
		return Resolved{}, fmt.Errorf("resolve %q: %w", sp.Raw, err)
	}
	fi, err := os.Stat(sp.Source)
	if err != nil {
		return Resolved{}, fmt.Errorf("resolve %q: %w", sp.Raw, err)
	}
	if !fi.IsDir() {
		return Resolved{}, fmt.Errorf("resolve %q: not a directory", sp.Raw)
	}
	if sp.Ref == "" {
		return Resolved{
			Dir:       sp.Source,
			Label:     labelForPath(sp.Source),
			Kind:      "dir",
			Canonical: abs,
			Cleanup:   noop,
		}, nil
	}
	if err := requireGit(); err != nil {
		return Resolved{}, fmt.Errorf("resolve %q: %w", sp.Raw, err)
	}
	sha, err := runGit(ctx, sp.Source, "rev-parse", "--verify", "--quiet", sp.Ref+"^{commit}")
	if err != nil {
		if _, gerr := runGit(ctx, sp.Source, "rev-parse", "--git-dir"); gerr != nil {
			return Resolved{}, fmt.Errorf("resolve %q: %s is not a git repository (a #ref needs one)", sp.Raw, sp.Source)
		}
		return Resolved{}, fmt.Errorf("resolve %q: ref %q not found in %s: %w", sp.Raw, sp.Ref, sp.Source, err)
	}
	sha = strings.TrimSpace(sha)
	tmp, err := os.MkdirTemp("", "ax-source-*")
	if err != nil {
		return Resolved{}, fmt.Errorf("resolve %q: %w", sp.Raw, err)
	}
	if err := archiveInto(ctx, sp.Source, sha, tmp); err != nil {
		os.RemoveAll(tmp)
		return Resolved{}, fmt.Errorf("resolve %q: %w", sp.Raw, err)
	}
	return Resolved{
		Dir:       tmp,
		Label:     labelForPath(sp.Source) + "#" + shortSHA(sha),
		Kind:      "local-git",
		Commit:    sha,
		Ref:       sp.Ref,
		Canonical: abs,
		Cleanup:   func() { os.RemoveAll(tmp) },
	}, nil
}

func resolveRemote(ctx context.Context, sp Spec, opt Options) (Resolved, error) {
	if err := requireGit(); err != nil {
		return Resolved{}, fmt.Errorf("resolve %q: %w", sp.Raw, err)
	}
	cacheRoot := opt.CacheDir
	if cacheRoot == "" {
		var err error
		if cacheRoot, err = DefaultCacheDir(); err != nil {
			return Resolved{}, err
		}
	}
	canon := canonicalRemote(sp.Source)
	host := remoteHost(canon)
	urlDir := filepath.Join(cacheRoot, "git", hashPrefix(canon))

	sha, refName := "", ""
	switch {
	case isFullSHA(sp.Ref):
		sha = strings.ToLower(sp.Ref)
	case opt.Offline:
		var err error
		if sha, err = lookupRef(urlDir, refKey(sp.Ref)); err != nil {
			return Resolved{}, fmt.Errorf("resolve %q offline: %w", sp.Raw, err)
		}
	default:
		var err error
		if sha, refName, err = lsRemote(ctx, sp.Source, host, sp.Ref); err != nil {
			return Resolved{}, fmt.Errorf("resolve %q: %w", sp.Raw, err)
		}
	}

	tree := filepath.Join(urlDir, sha, "tree")
	if _, err := os.Stat(tree); err != nil {
		if opt.Offline {
			return Resolved{}, fmt.Errorf("resolve %q offline: commit %s of %s is not cached", sp.Raw, shortSHA(sha), canon)
		}
		if err := fetchIntoCache(ctx, sp.Source, host, canon, sha, refName, urlDir); err != nil {
			return Resolved{}, fmt.Errorf("resolve %q: %w", sp.Raw, err)
		}
	}
	if !opt.Offline {
		if err := recordRef(urlDir, canon, refKey(sp.Ref), sha); err != nil {
			return Resolved{}, fmt.Errorf("resolve %q: %w", sp.Raw, err)
		}
	}
	return Resolved{
		Dir:       tree,
		Label:     canon + "#" + shortSHA(sha),
		Kind:      "remote-git",
		Commit:    sha,
		Ref:       sp.Ref,
		Canonical: canon,
		Cleanup:   noop,
	}, nil
}

// refKey is the refs.json key for a given ref: the ref itself, or "HEAD"
// for the remote's default branch.
func refKey(ref string) string {
	if ref == "" {
		return "HEAD"
	}
	return ref
}

func hashPrefix(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

// lsRemote resolves a ref (or the default branch, for ref == "") to a commit
// SHA without cloning. For a named ref it prefers, in order: the peeled
// annotated tag, the tag, the branch, then any exact advertised name.
func lsRemote(ctx context.Context, url, host, ref string) (sha, refName string, err error) {
	if ref == "" {
		out, err := runGit(ctx, "", "ls-remote", "--symref", url, "HEAD")
		if err != nil {
			return "", "", fmt.Errorf("%s: %w", host, err)
		}
		var branch, head string
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Split(line, "\t")
			if len(fields) != 2 {
				continue
			}
			if strings.HasPrefix(fields[0], "ref: ") && fields[1] == "HEAD" {
				branch = strings.TrimPrefix(fields[0], "ref: ")
			}
			if fields[1] == "HEAD" && !strings.HasPrefix(fields[0], "ref: ") {
				head = fields[0]
			}
		}
		if head == "" {
			return "", "", fmt.Errorf("%s: remote %s advertises no HEAD (empty repository?)", host, url)
		}
		return head, branch, nil
	}
	out, err := runGit(ctx, "", "ls-remote", url, ref, ref+"^{}")
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", host, err)
	}
	byName := map[string]string{}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 2 {
			continue
		}
		byName[fields[1]] = fields[0]
		names = append(names, fields[1])
	}
	for _, cand := range []string{
		"refs/tags/" + ref + "^{}",
		"refs/tags/" + ref,
		"refs/heads/" + ref,
		ref,
	} {
		if s, ok := byName[cand]; ok {
			return s, strings.TrimSuffix(cand, "^{}"), nil
		}
	}
	if len(names) == 1 {
		return byName[names[0]], strings.TrimSuffix(names[0], "^{}"), nil
	}
	if isHex(ref) {
		return "", "", fmt.Errorf("%s: %q is not an advertised ref; an abbreviated commit cannot be resolved remotely -- use a full 40-character SHA, a branch, or a tag", host, ref)
	}
	return "", "", fmt.Errorf("%s: ref %q not found on %s", host, ref, url)
}

// fetchIntoCache shallow-fetches one commit and materializes its tree into
// the cache slot <urlDir>/<sha>/tree with a meta.json beside it. The slot is
// assembled in a scratch dir on the same volume and renamed into place, so a
// slot is either absent or complete.
func fetchIntoCache(ctx context.Context, url, host, canon, sha, refName, urlDir string) error {
	if err := os.MkdirAll(urlDir, 0o755); err != nil {
		return err
	}
	work, err := os.MkdirTemp(urlDir, "fetch-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	repo := filepath.Join(work, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		return err
	}
	if _, err := runGit(ctx, repo, "init", "-q"); err != nil {
		return err
	}
	// Fetching the SHA directly works when the server allows SHA wants (all
	// the major hosts do) or when the SHA is an advertised tip; otherwise
	// fall back to fetching the resolved ref name and verifying it still
	// points at the SHA ls-remote reported.
	if _, err := runGit(ctx, repo, "fetch", "-q", "--depth", "1", url, sha); err != nil {
		if refName == "" {
			return fmt.Errorf("%s: cannot fetch commit %s: the server does not allow fetching by SHA and no ref name is known for it: %w", host, shortSHA(sha), err)
		}
		if _, ferr := runGit(ctx, repo, "fetch", "-q", "--depth", "1", url, refName); ferr != nil {
			return fmt.Errorf("%s: %w", host, ferr)
		}
		got, gerr := runGit(ctx, repo, "rev-parse", "FETCH_HEAD")
		if gerr != nil {
			return fmt.Errorf("%s: %w", host, gerr)
		}
		if strings.TrimSpace(got) != sha {
			return fmt.Errorf("%s: %s moved from %s to %s during resolution; rerun", host, refName, shortSHA(sha), shortSHA(strings.TrimSpace(got)))
		}
	}
	slot := filepath.Join(work, "slot")
	tree := filepath.Join(slot, "tree")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		return err
	}
	if err := archiveInto(ctx, repo, sha, tree); err != nil {
		return fmt.Errorf("%s: %w", host, err)
	}
	meta, err := json.Marshal(map[string]string{"url": url, "canonical": canon, "commit": sha})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(slot, "meta.json"), append(meta, '\n'), 0o644); err != nil {
		return err
	}
	dest := filepath.Join(urlDir, sha)
	if err := os.Rename(slot, dest); err != nil {
		// A concurrent resolve may have won the rename; the slot content is
		// determined by the immutable SHA, so an existing slot is equivalent.
		if _, serr := os.Stat(filepath.Join(dest, "tree")); serr == nil {
			return nil
		}
		return err
	}
	return nil
}

// refsFile records prior (ref -> sha) resolutions for one remote, so
// --offline can reuse them.
type refsFile struct {
	Canonical string            `json:"canonical"`
	Refs      map[string]string `json:"refs"`
}

func refsPath(urlDir string) string { return filepath.Join(urlDir, "refs.json") }

func lookupRef(urlDir, key string) (string, error) {
	b, err := os.ReadFile(refsPath(urlDir))
	if err != nil {
		return "", fmt.Errorf("ref %q has never been resolved online for this remote", key)
	}
	var rf refsFile
	if err := json.Unmarshal(b, &rf); err != nil {
		return "", fmt.Errorf("corrupt ref cache %s: %w", refsPath(urlDir), err)
	}
	sha, ok := rf.Refs[key]
	if !ok {
		return "", fmt.Errorf("ref %q has never been resolved online for this remote", key)
	}
	return sha, nil
}

func recordRef(urlDir, canon, key, sha string) error {
	if err := os.MkdirAll(urlDir, 0o755); err != nil {
		return err
	}
	rf := refsFile{Canonical: canon, Refs: map[string]string{}}
	if b, err := os.ReadFile(refsPath(urlDir)); err == nil {
		_ = json.Unmarshal(b, &rf) // a corrupt file is rebuilt
		if rf.Refs == nil {
			rf.Refs = map[string]string{}
		}
	}
	rf.Canonical = canon
	rf.Refs[key] = sha
	b, err := json.MarshalIndent(rf, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(urlDir, "refs-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, refsPath(urlDir))
}

// archiveInto materializes commit sha of the repository at repoDir into dest
// via `git archive` piped through the in-process tar reader. No worktree,
// index, stash, or HEAD state is touched, and no system tar is needed.
func archiveInto(ctx context.Context, repoDir, sha, dest string) error {
	cmd := gitCommand(ctx, repoDir, "archive", "--format=tar", sha)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	untarErr := untar(out, dest)
	waitErr := cmd.Wait()
	if waitErr != nil {
		return fmt.Errorf("git archive %s: %s", shortSHA(sha), gitErrDetail(waitErr, stderr.String()))
	}
	return untarErr
}

// requireGit fails fast, with a clear message, when the git binary is absent.
func requireGit() error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("the git binary is required to resolve git sources but was not found on PATH")
	}
	return nil
}

// gitCommand builds a git invocation that can never prompt for credentials.
func gitCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	full := args
	if dir != "" {
		full = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd
}

// runGit runs git and returns stdout; on failure the error carries the
// trimmed stderr, which is where git puts the actionable detail.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := gitCommand(ctx, dir, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", args[0], gitErrDetail(err, stderr.String()))
	}
	return stdout.String(), nil
}

// gitErrDetail prefers git's own stderr over the bare exit status.
func gitErrDetail(err error, stderr string) string {
	s := strings.TrimSpace(stderr)
	if s == "" {
		return err.Error()
	}
	// Keep the message single-line-ish: the last non-empty lines carry the
	// actionable detail (auth errors, missing refs).
	lines := strings.Split(s, "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	return strings.Join(lines, "; ")
}
