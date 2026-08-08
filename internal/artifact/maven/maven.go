// Package maven identifies and resolves artifacts from Maven-compatible
// repositories. It is intentionally independent of JVM parsing.
package maven

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const Central = "https://repo1.maven.org/maven2"

type Coordinate struct{ GroupID, ArtifactID, Packaging, Classifier, Version string }

func Parse(s string) (Coordinate, error) {
	s = strings.TrimPrefix(s, "mvn:")
	parts := strings.Split(s, ":")
	var c Coordinate
	switch len(parts) {
	case 3:
		c = Coordinate{parts[0], parts[1], "jar", "", parts[2]}
	case 4:
		c = Coordinate{parts[0], parts[1], parts[2], "", parts[3]}
	case 5:
		c = Coordinate{parts[0], parts[1], parts[2], parts[3], parts[4]}
	default:
		return c, fmt.Errorf("invalid Maven coordinates %q: want group:artifact:version, group:artifact:packaging:version, or group:artifact:packaging:classifier:version", s)
	}
	for _, v := range []string{c.GroupID, c.ArtifactID, c.Packaging, c.Version} {
		if v == "" || strings.ContainsAny(v, "/\\ ") {
			return Coordinate{}, fmt.Errorf("invalid Maven coordinates %q", s)
		}
	}
	if strings.HasSuffix(c.Version, "-SNAPSHOT") {
		return Coordinate{}, fmt.Errorf("Maven snapshot %q is unsupported; use a resolved timestamped version", c.Version)
	}
	return c, nil
}
func (c Coordinate) Filename() string {
	classifier := ""
	if c.Classifier != "" {
		classifier = "-" + c.Classifier
	}
	return c.ArtifactID + "-" + c.Version + classifier + "." + c.Packaging
}
func (c Coordinate) RepositoryPath() string {
	return path.Join(strings.ReplaceAll(c.GroupID, ".", "/"), c.ArtifactID, c.Version, c.Filename())
}
func (c Coordinate) String() string {
	if c.Classifier != "" {
		return strings.Join([]string{c.GroupID, c.ArtifactID, c.Packaging, c.Classifier, c.Version}, ":")
	}
	if c.Packaging != "jar" {
		return strings.Join([]string{c.GroupID, c.ArtifactID, c.Packaging, c.Version}, ":")
	}
	return strings.Join([]string{c.GroupID, c.ArtifactID, c.Version}, ":")
}

type ResolvedArtifact struct {
	Path       string
	Coordinate Coordinate
	Repository string
	SHA256     string
	Cached     bool
}
type ArtifactResolver interface {
	Resolve(context.Context, Coordinate) (ResolvedArtifact, error)
}
type Credentials struct{ Username, Password, BearerToken, Authorization string }
type Resolver struct {
	BaseURL, CacheDir, UserAgent string
	Client                       *http.Client
	Credentials                  Credentials
}

func (r *Resolver) Resolve(ctx context.Context, c Coordinate) (ResolvedArtifact, error) {
	if c.Packaging != "jar" {
		return ResolvedArtifact{}, fmt.Errorf("unsupported Maven packaging %q: JVM scanning requires jar", c.Packaging)
	}
	base := r.BaseURL
	if base == "" {
		base = Central
	}
	u, e := url.Parse(base)
	if e != nil || u.Scheme == "" || u.Host == "" {
		return ResolvedArtifact{}, fmt.Errorf("invalid Maven repository URL %q", base)
	}
	repository := safeBase(u)
	u.Path = strings.TrimRight(u.Path, "/") + "/" + c.RepositoryPath()
	cache := r.CacheDir
	if cache == "" {
		home, e := os.UserHomeDir()
		if e != nil {
			return ResolvedArtifact{}, e
		}
		cache = filepath.Join(home, ".m2", "repository")
	}
	dest := filepath.Join(cache, filepath.FromSlash(c.RepositoryPath()))
	if sum, e := validJAR(dest); e == nil {
		return ResolvedArtifact{dest, c, repository, sum, true}, nil
	}
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if e != nil {
		return ResolvedArtifact{}, e
	}
	ua := r.UserAgent
	if ua == "" {
		ua = "assayxport/(devel)"
	}
	req.Header.Set("User-Agent", ua)
	setAuth(req, r.Credentials)
	resp, e := client.Do(req)
	if e != nil {
		return ResolvedArtifact{}, fmt.Errorf("download %s: %w", c, e)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return ResolvedArtifact{}, fmt.Errorf("artifact not found: %s", c)
	case http.StatusUnauthorized, http.StatusForbidden:
		return ResolvedArtifact{}, fmt.Errorf("repository authentication failed for %s (HTTP %d)", c, resp.StatusCode)
	default:
		return ResolvedArtifact{}, fmt.Errorf("repository returned HTTP %d for %s", resp.StatusCode, c)
	}
	if e := os.MkdirAll(filepath.Dir(dest), 0755); e != nil {
		return ResolvedArtifact{}, e
	}
	tmp, e := os.CreateTemp(filepath.Dir(dest), ".assayxport-download-*")
	if e != nil {
		return ResolvedArtifact{}, e
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		tmp.Close()
		if !ok {
			os.Remove(tmpName)
		}
	}()
	h := sha256.New()
	if _, e = io.Copy(io.MultiWriter(tmp, h), resp.Body); e != nil {
		return ResolvedArtifact{}, fmt.Errorf("download %s: %w", c, e)
	}
	if e = tmp.Sync(); e != nil {
		return ResolvedArtifact{}, e
	}
	if e = tmp.Close(); e != nil {
		return ResolvedArtifact{}, e
	}
	zr, e := zip.OpenReader(tmpName)
	if e != nil {
		return ResolvedArtifact{}, fmt.Errorf("corrupt JAR response for %s: %w", c, e)
	}
	zr.Close()
	if e = os.Rename(tmpName, dest); e != nil {
		return ResolvedArtifact{}, e
	}
	ok = true
	return ResolvedArtifact{dest, c, repository, hex.EncodeToString(h.Sum(nil)), false}, nil
}
func setAuth(req *http.Request, c Credentials) {
	if c.Authorization != "" {
		req.Header.Set("Authorization", c.Authorization)
	} else if c.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.BearerToken)
	} else if c.Username != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}
}
func safeBase(u *url.URL) string {
	x := *u
	x.User = nil
	x.RawQuery = ""
	x.Fragment = ""
	return strings.TrimRight(x.String(), "/")
}
func validJAR(name string) (string, error) {
	f, e := os.Open(name)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	z, e := zip.OpenReader(name)
	if e != nil {
		return "", e
	}
	z.Close()
	return hex.EncodeToString(h.Sum(nil)), nil
}
