package source

import "testing"

func TestParseSpec(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    Spec
		wantErr bool
	}{
		{name: "dot", raw: ".", want: Spec{Raw: ".", Source: "."}},
		{name: "relative dir", raw: "./sub/dir", want: Spec{Raw: "./sub/dir", Source: "./sub/dir"}},
		{name: "absolute dir", raw: "/abs/path", want: Spec{Raw: "/abs/path", Source: "/abs/path"}},
		{name: "local tag", raw: "./repo#v1.2.0", want: Spec{Raw: "./repo#v1.2.0", Source: "./repo", Ref: "v1.2.0"}},
		{name: "local branch", raw: "repo#main", want: Spec{Raw: "repo#main", Source: "repo", Ref: "main"}},
		{name: "dot with relative rev", raw: ".#HEAD~3", want: Spec{Raw: ".#HEAD~3", Source: ".", Ref: "HEAD~3"}},
		{name: "local short sha", raw: "../fork#a1b2c3d", want: Spec{Raw: "../fork#a1b2c3d", Source: "../fork", Ref: "a1b2c3d"}},
		{name: "https remote", raw: "https://github.com/owner/repo", want: Spec{Raw: "https://github.com/owner/repo", Source: "https://github.com/owner/repo", Remote: true}},
		{name: "https remote with ref", raw: "https://gitlab.example.com/g/sub/repo#develop", want: Spec{Raw: "https://gitlab.example.com/g/sub/repo#develop", Source: "https://gitlab.example.com/g/sub/repo", Ref: "develop", Remote: true}},
		{name: "scp remote with ref", raw: "git@github.com:owner/repo.git#v2.0.0", want: Spec{Raw: "git@github.com:owner/repo.git#v2.0.0", Source: "git@github.com:owner/repo.git", Ref: "v2.0.0", Remote: true}},
		{name: "scp remote", raw: "git@bitbucket.org:owner/repo", want: Spec{Raw: "git@bitbucket.org:owner/repo", Source: "git@bitbucket.org:owner/repo", Remote: true}},
		{name: "ssh remote", raw: "ssh://git@github.com/o/r#main", want: Spec{Raw: "ssh://git@github.com/o/r#main", Source: "ssh://git@github.com/o/r", Ref: "main", Remote: true}},
		{name: "git protocol", raw: "git://host.example/o/r", want: Spec{Raw: "git://host.example/o/r", Source: "git://host.example/o/r", Remote: true}},
		{name: "hash inside path splits at last hash", raw: "./we#ird#main", want: Spec{Raw: "./we#ird#main", Source: "./we#ird", Ref: "main"}},
		{name: "file url is a git transport", raw: "file:///x/y", want: Spec{Raw: "file:///x/y", Source: "file:///x/y", Remote: true}},
		{name: "empty", raw: "", wantErr: true},
		{name: "only ref", raw: "#main", wantErr: true},
		{name: "empty ref local", raw: "./repo#", wantErr: true},
		{name: "empty ref remote", raw: "https://github.com/o/r#", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSpec(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseSpec(%q) = %+v, want error", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSpec(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("ParseSpec(%q) = %+v, want %+v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestCanonicalRemote(t *testing.T) {
	tests := []struct {
		raw, want string
	}{
		{"https://github.com/Owner/Repo", "github.com/Owner/Repo"},
		{"https://GitHub.com/owner/repo.git", "github.com/owner/repo"},
		{"git@github.com:owner/repo.git", "github.com/owner/repo"},
		{"ssh://git@github.com/owner/repo", "github.com/owner/repo"},
		{"https://user:pass@host.example:8443/team/repo/", "host.example:8443/team/repo"},
		{"git@bitbucket.org:owner/repo", "bitbucket.org/owner/repo"},
	}
	for _, tt := range tests {
		if got := canonicalRemote(tt.raw); got != tt.want {
			t.Errorf("canonicalRemote(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
	// The three spellings of one repository share one identity.
	a := canonicalRemote("https://github.com/o/r")
	b := canonicalRemote("git@github.com:o/r.git")
	c := canonicalRemote("ssh://git@github.com/o/r")
	if a != b || b != c {
		t.Errorf("spellings disagree: %q %q %q", a, b, c)
	}
}

func TestLabelForPath(t *testing.T) {
	tests := []struct{ raw, want string }{
		{".", "."},
		{"./repo", "repo"},
		{"sub/dir/", "sub/dir"},
		{"/abs/path/repo", "repo"},
	}
	for _, tt := range tests {
		if got := labelForPath(tt.raw); got != tt.want {
			t.Errorf("labelForPath(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}
