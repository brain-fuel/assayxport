// Package source turns an `ax diff` source spec into a local directory ready
// to scan plus a stable label for output.
//
// The grammar is
//
//	spec   := source [ "#" ref ]
//	source := local-path | remote-url
//
// where a remote-url is anything with a git-relevant URL scheme
// (https://, http://, ssh://, git://) or scp-style user@host:path syntax,
// and a ref is any name the repository's git can resolve to a commit
// (branch, tag, SHA, HEAD~2, ...). The ref separator is the LAST '#' in the
// spec; there is no escape mechanism. A local directory whose own name
// contains '#' therefore cannot carry a ref, but the bare path still
// resolves: a spec that names an existing directory as-typed is treated as
// that directory (checked at resolve time, before ref interpretation).
//
// Resolution shells out to the git binary so credential helpers, the ssh
// agent, insteadOf rewrites, proxies, and CA configuration are inherited.
// It never mutates a user repository: local refs are materialized with
// `git archive` into a scratch directory, and remotes are shallow-fetched
// into a content-addressed cache under os.UserCacheDir().
package source

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Spec is one parsed source spec. Parsing is purely syntactic: no filesystem
// or network probe happens here, so parse behavior is hermetic and testable.
type Spec struct {
	Raw    string // the spec exactly as typed
	Source string // the path or URL part, before any '#'
	Ref    string // the ref after the last '#'; "" when none was given
	Remote bool   // true when Source is URL-shaped (scheme or scp-style)
}

var (
	schemeRE = regexp.MustCompile(`^(https?|ssh|git|file)://`)
	// scp-style: user@host:path -- an '@' then a ':' before any '/'.
	scpRE = regexp.MustCompile(`^[^@/]+@[^:/]+:`)
)

// ParseSpec splits a raw spec into source and ref and classifies it as local
// or remote. It rejects an empty spec, an empty source before '#', and an
// empty ref after '#'.
func ParseSpec(raw string) (Spec, error) {
	if raw == "" {
		return Spec{}, fmt.Errorf("empty source spec")
	}
	src, ref := raw, ""
	if i := strings.LastIndexByte(raw, '#'); i >= 0 {
		src, ref = raw[:i], raw[i+1:]
		if src == "" {
			return Spec{}, fmt.Errorf("source spec %q has no source before '#'", raw)
		}
		if ref == "" {
			return Spec{}, fmt.Errorf("source spec %q has an empty ref after '#'", raw)
		}
	}
	return Spec{
		Raw:    raw,
		Source: src,
		Ref:    ref,
		Remote: schemeRE.MatchString(src) || scpRE.MatchString(src),
	}, nil
}

// canonicalRemote reduces a remote URL to its identity form "host/path":
// lowercased host (port kept -- two hosts on different ports are different
// hosts), userinfo dropped, leading and trailing slashes trimmed, and a
// single trailing ".git" stripped. https://github.com/o/r,
// git@github.com:o/r.git, and ssh://git@github.com/o/r all share one
// canonical form. Path case is preserved (some hosts are case-sensitive).
func canonicalRemote(u string) string {
	var host, p string
	if m := schemeRE.FindString(u); m != "" {
		rest := u[len(m):]
		hostport := rest
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			hostport, p = rest[:slash], rest[slash+1:]
		}
		if at := strings.LastIndexByte(hostport, '@'); at >= 0 {
			hostport = hostport[at+1:]
		}
		host = strings.ToLower(hostport)
	} else if scpRE.MatchString(u) {
		at := strings.IndexByte(u, '@')
		colon := at + strings.IndexByte(u[at:], ':')
		host = strings.ToLower(u[at+1 : colon])
		p = u[colon+1:]
	}
	p = strings.Trim(p, "/")
	p = strings.TrimSuffix(p, ".git")
	return host + "/" + p
}

// remoteHost returns the host part of a canonical remote, for error messages.
func remoteHost(canonical string) string {
	if i := strings.IndexByte(canonical, '/'); i >= 0 {
		return canonical[:i]
	}
	return canonical
}

// labelForPath is the display form of a local path: cleaned, slash-separated,
// as typed -- except an absolute path, which reduces to its final element so
// output stays free of machine-specific filesystem locations.
func labelForPath(p string) string {
	if filepath.IsAbs(p) {
		return filepath.Base(p)
	}
	return path.Clean(filepath.ToSlash(p))
}

// isFullSHA reports whether ref is a full 40-hex-character commit id.
func isFullSHA(ref string) bool {
	if len(ref) != 40 {
		return false
	}
	return isHex(ref)
}

func isHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return len(s) > 0
}

// shortSHA is the 12-character abbreviation used in labels.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
