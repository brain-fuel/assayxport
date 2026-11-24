package publication

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// goPollDelay and goPollAttempts bound how long a freshly pushed tag may
// take to appear on the module proxy and pkg.go.dev.
var (
	goPollDelay = 15 * time.Second
	goPollAttempts = 20
)

var goTagPrefixPattern = regexp.MustCompile(`^([A-Za-z0-9._-]+/)+$`)

// goTarget verifies the Go release (tag on origin, module proxy, pkg.go.dev).
// With [go] tag_prefix it also owns the tag: it creates <prefix>v<version>
// at v<version>^{} when missing and pushes only that tag.
type goTarget struct {
	root string
	cfg Config
	lines []string
}

func (t *goTarget) Name() string { return "go" }
func (t *goTarget) Summary() []string { return t.lines }
func (t *goTarget) Credentials() error { return nil }

func (t *goTarget) tag() string { return t.cfg.GoTagPrefix + "v" + t.cfg.Version }

func (t *goTarget) Validate(cfg Config) []Issue {
	issues := validateRelease(cfg)
	if strings.TrimSpace(cfg.GoModule) == "" { issues = append(issues, Issue{"CONFIG_REQUIRED_MISSING", "required setting go.module is missing or empty", "Add [go] module = \"example.com/module\" to assayxport.toml; it is the module path the proxy serves."}) }
	if cfg.GoTagPrefix != "" {
		if !goTagPrefixPattern.MatchString(cfg.GoTagPrefix) { issues = append(issues, Issue{"GO_TAG_PREFIX_INVALID", fmt.Sprintf("go.tag_prefix %q must be a directory path ending in /", cfg.GoTagPrefix), "Use the module's directory inside the repository, for example \"hosts/go/\"."}) }
		if gitOutput(t.root, "remote", "get-url", "origin") == "" { issues = append(issues, Issue{"GIT_ORIGIN_MISSING", "go.tag_prefix is set but the checkout has no origin remote to push the tag to", "Add the remote with `git remote add origin <url>`."}) }
	}
	return issues
}

func (t *goTarget) Published(ctx context.Context) (TargetState, error) {
	err := verifyGoTag(ctx, t.root, t.cfg, t.tag())
	if err == nil { return TargetState{Done: true, Detail: "tag " + t.tag() + " is on origin and served by the proxy"}, nil }
	if errorCode(err) == "REMOTE_TAG_MISSING" || errorCode(err) == "GO_PROXY_VERSION_MISSING" || errorCode(err) == "PKG_GO_DEV_NOT_INDEXED" { return TargetState{}, nil }
	return TargetState{}, err
}

func (t *goTarget) Publish(ctx context.Context, progress Progress) error {
	report := func(message string) { if progress != nil { progress(message) } }
	pushed := false
	if t.cfg.GoTagPrefix != "" {
		var err error
		if pushed, err = t.ensureTag(ctx, report); err != nil { return err }
	}
	report("verifying the remote Go tag, module proxy, and pkg.go.dev")
	for attempt := 1; ; attempt++ {
		err := verifyGoTag(ctx, t.root, t.cfg, t.tag())
		if err == nil { break }
		code := errorCode(err)
		waiting := code == "GO_PROXY_VERSION_MISSING" || code == "PKG_GO_DEV_NOT_INDEXED" || code == "REMOTE_TAG_MISSING"
		if !pushed || !waiting || attempt >= goPollAttempts { return err }
		report("waiting for the proxy to serve " + t.tag())
		select {
		case <-ctx.Done(): return ctx.Err()
		case <-time.After(goPollDelay):
		}
	}
	if err := recordItem(t.root, t.cfg, "go", t.tag(), gitOutput(t.root, "rev-parse", "HEAD")); err != nil { return err }
	t.lines = append(t.lines, "go "+t.cfg.GoModule+"@v"+t.cfg.Version+" is live (tag "+t.tag()+")")
	return nil
}

// ensureTag creates the local annotated tag at v<version>^{} when missing and
// pushes just that tag when origin lacks it. It never forces, and it fails if
// either side points anywhere but the release commit.
func (t *goTarget) ensureTag(ctx context.Context, report func(string)) (bool, error) {
	tag, head := t.tag(), gitOutput(t.root, "rev-parse", "HEAD")
	release := "v" + t.cfg.Version
	if gitOutput(t.root, "rev-parse", "-q", "--verify", "refs/tags/"+tag) == "" {
		report("creating annotated tag " + tag + " at " + release + "^{}")
		if out, err := exec.CommandContext(ctx, "git", "-C", t.root, "tag", "-a", tag, "-m", "release "+tag, release+"^{}").CombinedOutput(); err != nil {
			return false, Issue{"GO_TAG_CREATE_FAILED", fmt.Sprintf("git tag %s failed: %v: %s", tag, err, strings.TrimSpace(string(out))), "Check that " + release + " exists locally, then rerun `ax publish --go`."}
		}
	}
	if local := gitOutput(t.root, "rev-list", "-n", "1", tag); local != head {
		return false, Issue{"GO_TAG_COMMIT_MISMATCH", fmt.Sprintf("local tag %s points to %s but the release checkout is %s", tag, local, head), "Tags are never moved: delete the unpublished local tag yourself if it is wrong, then rerun."}
	}
	remote, err := t.remoteTag(ctx, tag)
	if err != nil { return false, err }
	switch {
	case remote == "":
		report("pushing tag " + tag + " to origin")
		if out, err := exec.CommandContext(ctx, "git", "-C", t.root, "push", "origin", "refs/tags/"+tag+":refs/tags/"+tag).CombinedOutput(); err != nil {
			return false, Issue{"GO_TAG_PUSH_FAILED", fmt.Sprintf("git push of %s failed: %v: %s", tag, err, strings.TrimSpace(string(out))), "Fix the push error (credentials, network), then rerun `ax publish --go`."}
		}
		t.lines = append(t.lines, "go tag "+tag+": pushed")
		return true, nil
	case remote != head:
		return false, Issue{"GO_REMOTE_TAG_MISMATCH", fmt.Sprintf("origin already has %s pointing to %s, not the release commit %s", tag, remote, head), "Never move a published tag: reconcile it by hand, or release a new version."}
	}
	report("tag " + tag + " is already on origin")
	return false, nil
}

// remoteTag returns the commit origin's tag peels to, or "" if absent.
func (t *goTarget) remoteTag(ctx context.Context, tag string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", t.root, "ls-remote", "--tags", "origin", "refs/tags/"+tag, "refs/tags/"+tag+"^{}").CombinedOutput()
	if err != nil { return "", Issue{"REMOTE_TAG_UNAVAILABLE", fmt.Sprintf("cannot read %s from origin: %v: %s", tag, err, strings.TrimSpace(string(out))), "Verify the origin remote and network, then rerun."} }
	remote := ""
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 { remote = fields[0]; if strings.HasSuffix(fields[1], "^{}") { break } }
	}
	return remote, nil
}
