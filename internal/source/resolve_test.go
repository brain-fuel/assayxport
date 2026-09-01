package source

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The resolver's tests construct real repositories with the git binary --
// the whole point of shelling out is the real binary's behavior -- and skip
// when git is unavailable.

func requireGitBin(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
}

// git runs a git command in dir with a fixed identity and fails the test on
// error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir,
		"-c", "user.name=ax-test", "-c", "user.email=ax-test@example.invalid",
		"-c", "protocol.file.allow=always",
	}, args...)
	cmd := exec.Command("git", full...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, errb.String())
	}
	return strings.TrimSpace(out.String())
}

// mkRepo builds a repo with two commits on main, an annotated tag v1.0.0 on
// the first, and a dev branch off the first commit. Returned SHAs are keyed
// "c1", "c2", "dev".
func mkRepo(t *testing.T) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	writeFile(t, dir, "a.txt", "one\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "c1")
	c1 := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "tag", "-a", "v1.0.0", "-m", "first")
	writeFile(t, dir, "a.txt", "two\n")
	writeFile(t, dir, "b/c.txt", "nested\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "c2")
	c2 := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "branch", "dev", c1)
	return dir, map[string]string{"c1": c1, "c2": c2, "dev": c1}
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// repoState snapshots everything a resolve must not mutate.
func repoState(t *testing.T, dir string) string {
	t.Helper()
	return git(t, dir, "rev-parse", "HEAD") + "\n" +
		git(t, dir, "status", "--porcelain") + "\n" +
		git(t, dir, "stash", "list")
}

func TestResolvePlainDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "x")
	got, err := Resolve(context.Background(), dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer got.Cleanup()
	if got.Kind != "dir" || got.Dir != dir || got.Commit != "" {
		t.Fatalf("got %+v", got)
	}
	if got.Label != filepath.Base(dir) {
		t.Errorf("abs-path label = %q, want base %q", got.Label, filepath.Base(dir))
	}
}

func TestResolveMissingDir(t *testing.T) {
	if _, err := Resolve(context.Background(), filepath.Join(t.TempDir(), "nope"), Options{}); err == nil {
		t.Fatal("want error for missing directory")
	}
}

func TestResolveLocalRefs(t *testing.T) {
	requireGitBin(t)
	repo, shas := mkRepo(t)
	// Dirty the working tree so mutation would be visible.
	writeFile(t, repo, "dirty.txt", "uncommitted\n")
	before := repoState(t, repo)

	tests := []struct {
		ref     string
		commit  string
		content string // expected a.txt content at that ref
	}{
		{"v1.0.0", shas["c1"], "one\n"},
		{"main", shas["c2"], "two\n"},
		{"dev", shas["c1"], "one\n"},
		{"HEAD~1", shas["c1"], "one\n"},
		{shas["c1"][:8], shas["c1"], "one\n"}, // abbreviated SHA
		{shas["c2"], shas["c2"], "two\n"},     // full SHA
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			got, err := Resolve(context.Background(), repo+"#"+tt.ref, Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer got.Cleanup()
			if got.Kind != "local-git" || got.Commit != tt.commit || got.Ref != tt.ref {
				t.Fatalf("got %+v, want commit %s", got, tt.commit)
			}
			if got.Dir == repo {
				t.Fatal("materialized into the repo itself")
			}
			if c := readFile(t, got.Dir, "a.txt"); c != tt.content {
				t.Errorf("a.txt = %q, want %q", c, tt.content)
			}
			if _, err := os.Stat(filepath.Join(got.Dir, "dirty.txt")); err == nil {
				t.Error("working-tree-only file leaked into the ref materialization")
			}
			want := filepath.Base(repo) + "#" + tt.commit[:12]
			if got.Label != want {
				t.Errorf("label = %q, want %q", got.Label, want)
			}
		})
	}
	if after := repoState(t, repo); after != before {
		t.Errorf("resolution mutated the repository:\nbefore: %q\nafter:  %q", before, after)
	}
}

func TestResolveLocalRefCleanup(t *testing.T) {
	requireGitBin(t)
	repo, _ := mkRepo(t)
	got, err := Resolve(context.Background(), repo+"#main", Options{})
	if err != nil {
		t.Fatal(err)
	}
	got.Cleanup()
	if _, err := os.Stat(got.Dir); !os.IsNotExist(err) {
		t.Errorf("cleanup left %s behind", got.Dir)
	}
}

func TestResolveLocalRefOnNonRepo(t *testing.T) {
	requireGitBin(t)
	dir := t.TempDir()
	_, err := Resolve(context.Background(), dir+"#main", Options{})
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("err = %v, want 'not a git repository'", err)
	}
}

func TestResolveLocalUnknownRef(t *testing.T) {
	requireGitBin(t)
	repo, _ := mkRepo(t)
	_, err := Resolve(context.Background(), repo+"#does-not-exist", Options{})
	if err == nil || !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("err = %v, want the missing ref named", err)
	}
}

func TestResolveLiteralHashDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "we#ird")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(context.Background(), dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "dir" || got.Dir != dir {
		t.Fatalf("got %+v", got)
	}
}

// bareOrigin clones repo into a bare repository and returns its file:// URL.
func bareOrigin(t *testing.T, repo string) (string, string) {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "origin.git")
	git(t, filepath.Dir(bare), "clone", "-q", "--bare", repo, bare)
	git(t, bare, "symbolic-ref", "HEAD", "refs/heads/main")
	return bare, "file://" + filepath.ToSlash(bare)
}

func TestResolveRemote(t *testing.T) {
	requireGitBin(t)
	repo, shas := mkRepo(t)
	_, url := bareOrigin(t, repo)
	cache := t.TempDir()
	ctx := context.Background()

	tests := []struct {
		name, spec, commit, ref string
	}{
		{"default branch", url, shas["c2"], ""},
		{"branch", url + "#dev", shas["c1"], "dev"},
		{"tag", url + "#v1.0.0", shas["c1"], "v1.0.0"},
		{"full sha", url + "#" + shas["c2"], shas["c2"], shas["c2"]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(ctx, tt.spec, Options{CacheDir: cache})
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != "remote-git" || got.Commit != tt.commit || got.Ref != tt.ref {
				t.Fatalf("got %+v, want commit %s ref %q", got, tt.commit, tt.ref)
			}
			wantContent := "two\n"
			if tt.commit == shas["c1"] {
				wantContent = "one\n"
			}
			if c := readFile(t, got.Dir, "a.txt"); c != wantContent {
				t.Errorf("a.txt = %q, want %q", c, wantContent)
			}
			if !strings.HasSuffix(got.Label, "#"+tt.commit[:12]) {
				t.Errorf("label %q does not end with #%s", got.Label, tt.commit[:12])
			}
			if strings.Contains(got.Label, "file://") {
				t.Errorf("label %q leaks the URL scheme", got.Label)
			}
		})
	}
}

func TestResolveRemoteUnknownRef(t *testing.T) {
	requireGitBin(t)
	repo, _ := mkRepo(t)
	_, url := bareOrigin(t, repo)
	_, err := Resolve(context.Background(), url+"#nope", Options{CacheDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v, want the missing ref named", err)
	}
}

func TestResolveRemoteCacheHitNeedsNoNetwork(t *testing.T) {
	requireGitBin(t)
	repo, shas := mkRepo(t)
	bare, url := bareOrigin(t, repo)
	cache := t.TempDir()
	ctx := context.Background()
	spec := url + "#" + shas["c2"]
	if _, err := Resolve(ctx, spec, Options{CacheDir: cache}); err != nil {
		t.Fatal(err)
	}
	// A SHA is immutable, so the cached tree must satisfy the same spec with
	// the origin gone entirely.
	if err := os.RemoveAll(bare); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(ctx, spec, Options{CacheDir: cache})
	if err != nil {
		t.Fatalf("cache hit still hit the network: %v", err)
	}
	if got.Commit != shas["c2"] {
		t.Fatalf("got %+v", got)
	}
}

func TestResolveRemoteBranchReResolvesEachRun(t *testing.T) {
	requireGitBin(t)
	repo, _ := mkRepo(t)
	bare, url := bareOrigin(t, repo)
	cache := t.TempDir()
	ctx := context.Background()
	if _, err := Resolve(ctx, url+"#main", Options{CacheDir: cache}); err != nil {
		t.Fatal(err)
	}
	// A branch name is mutable: without --offline it must be re-resolved,
	// so a vanished origin is an error, not a stale cache hit.
	if err := os.RemoveAll(bare); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(ctx, url+"#main", Options{CacheDir: cache}); err == nil {
		t.Fatal("want re-resolution failure once the origin is gone")
	}
	// --offline explicitly opts into the recorded resolution.
	got, err := Resolve(ctx, url+"#main", Options{CacheDir: cache, Offline: true})
	if err != nil {
		t.Fatalf("offline reuse: %v", err)
	}
	if got.Kind != "remote-git" {
		t.Fatalf("got %+v", got)
	}
}

func TestResolveRemoteOfflineUnseenRef(t *testing.T) {
	requireGitBin(t)
	repo, _ := mkRepo(t)
	_, url := bareOrigin(t, repo)
	_, err := Resolve(context.Background(), url+"#dev", Options{CacheDir: t.TempDir(), Offline: true})
	if err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("err = %v, want offline resolution failure", err)
	}
}

// Two specs naming the same commit -- the branch name and the SHA itself --
// must produce the same label, commit, and tree.
func TestResolveRemoteSameCommitTwoSpecs(t *testing.T) {
	requireGitBin(t)
	repo, shas := mkRepo(t)
	_, url := bareOrigin(t, repo)
	cache := t.TempDir()
	ctx := context.Background()
	byBranch, err := Resolve(ctx, url+"#main", Options{CacheDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	bySHA, err := Resolve(ctx, url+"#"+shas["c2"], Options{CacheDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	if byBranch.Label != bySHA.Label || byBranch.Commit != bySHA.Commit || byBranch.Dir != bySHA.Dir {
		t.Fatalf("branch and SHA disagree:\n%+v\n%+v", byBranch, bySHA)
	}
}

// A full SHA that is not an advertised tip cannot be fetched from a server
// that disallows SHA wants (the file:// default) and has no ref fallback; the
// error must say so rather than producing a wrong tree. With the server
// opt-in, the same spec resolves.
func TestResolveRemoteNonTipSHA(t *testing.T) {
	requireGitBin(t)
	repo, shas := mkRepo(t)
	bare, url := bareOrigin(t, repo)
	ctx := context.Background()
	// c1 is reachable but only advertised via tag/branch tips, not as main's
	// tip. Note dev and v1.0.0 also point at c1, so this exercises the
	// server's advertised-want check only if git rejects it; either outcome
	// below is honest, and the opt-in path must always work.
	_, err := Resolve(ctx, url+"#"+shas["c1"], Options{CacheDir: t.TempDir()})
	if err != nil && !strings.Contains(err.Error(), shas["c1"][:12]) {
		t.Fatalf("refusal must name the commit: %v", err)
	}
	git(t, bare, "config", "uploadpack.allowAnySHA1InWant", "true")
	got, err := Resolve(ctx, url+"#"+shas["c1"], Options{CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if got.Commit != shas["c1"] {
		t.Fatalf("got %+v", got)
	}
}

func TestResolveRemoteMissingGit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := Resolve(context.Background(), "https://github.com/o/r", Options{CacheDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "git binary") {
		t.Fatalf("err = %v, want missing-git message", err)
	}
}

func TestUntarRejectsTraversal(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	body := []byte("evil")
	if err := tw.WriteHeader(&tar.Header{Name: "../escape.txt", Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	if err := untar(&buf, t.TempDir()); err == nil {
		t.Fatal("want traversal rejection")
	}
}
