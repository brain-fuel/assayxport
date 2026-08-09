// Package publication implements the fail-closed local publication preflight.
package publication

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Issue struct { Code, Problem, Fix string }

type Report struct { Issues []Issue }

func (r Report) OK() bool { return len(r.Issues) == 0 }
func (r Report) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "publication preflight failed with %d problem(s):", len(r.Issues))
	for _, issue := range r.Issues {
		fmt.Fprintf(&b, "\n\n[%s] %s\n  Fix: %s", issue.Code, issue.Problem, issue.Fix)
	}
	return b.String()
}

type Config struct { Version, Policy, GoModule, GoRepository, GroupID, ArtifactID, BuildManifest, Name, Description, URL, LicenseName, LicenseURL, DeveloperID, DeveloperName, DeveloperEmail, DeveloperURL, SCMURL, SCMConnection, SCMDeveloperConnection, Bundle string }

// Preflight validates all information needed before publication can proceed.
// It deliberately accumulates errors so one run gives the operator a complete
// and actionable repair list.
func Preflight(root string) (Config, Report) {
	cfg, issues := loadConfig(filepath.Join(root, "assayxport.toml"))
	issues = append(issues, validateConfig(cfg)...)
	issues = append(issues, validateBuild(root, cfg)...)
	sort.SliceStable(issues, func(i, j int) bool { return issues[i].Code < issues[j].Code })
	return cfg, Report{Issues: issues}
}

func loadConfig(path string) (Config, []Issue) {
	f, err := os.Open(path)
	if err != nil { return Config{}, []Issue{{"CONFIG_MISSING", fmt.Sprintf("cannot read %s: %v", path, err), "Create assayxport.toml using the documented schema_version = 1 template, then rerun `ax publish --prepare`."}} }
	defer f.Close()
	cfg, section, schema := Config{}, "", 0
	seen := map[string]bool{}
	issues := []Issue{}
	s := bufio.NewScanner(f)
	for lineNo := 1; s.Scan(); lineNo++ {
		line := strings.TrimSpace(strings.SplitN(s.Text(), "#", 2)[0]); if line == "" { continue }
		if strings.HasPrefix(line, "[") {
			if line != "[go]" && line != "[maven]" { issues = append(issues, Issue{"CONFIG_UNKNOWN_TABLE", fmt.Sprintf("%s:%d uses unsupported table %s", path, lineNo, line), "Use only root keys plus [go], [maven], and documented [[documentation.waivers]] entries."}); continue }
			section = strings.Trim(line, "[]"); continue
		}
		key, raw, ok := strings.Cut(line, "="); key, raw = strings.TrimSpace(key), strings.TrimSpace(raw)
		if !ok { issues = append(issues, Issue{"CONFIG_SYNTAX", fmt.Sprintf("%s:%d is not key = value", path, lineNo), "Write the setting as a TOML key followed by = and a quoted string value."}); continue }
		qualified := section + "." + key; if seen[qualified] { issues = append(issues, Issue{"CONFIG_DUPLICATE", fmt.Sprintf("%s:%d repeats %s", path, lineNo, qualified), "Remove the duplicate setting so publication metadata has one unambiguous value."}); continue }; seen[qualified] = true
		if section == "" && key == "schema_version" { n, e := strconv.Atoi(raw); if e != nil { issues = append(issues, Issue{"CONFIG_SCHEMA_INVALID", "schema_version is not an integer", "Set schema_version = 1."}) } else { schema = n }; continue }
		value, e := strconv.Unquote(raw); if e != nil { issues = append(issues, Issue{"CONFIG_VALUE_INVALID", fmt.Sprintf("%s:%d value for %s must be quoted", path, lineNo, qualified), "Use a TOML quoted string, for example key = \"value\"."}); continue }
		if !set(&cfg, qualified, value) { issues = append(issues, Issue{"CONFIG_UNKNOWN_KEY", fmt.Sprintf("%s:%d contains unknown key %s", path, lineNo, qualified), "Remove the key or replace it with a key from the assayxport.toml schema."}) }
	}
	if err := s.Err(); err != nil { issues = append(issues, Issue{"CONFIG_READ_FAILED", err.Error(), "Make assayxport.toml readable and rerun the preflight."}) }
	if schema != 1 { issues = append(issues, Issue{"CONFIG_SCHEMA_UNSUPPORTED", fmt.Sprintf("schema_version is %d; only 1 is supported", schema), "Set schema_version = 1 and migrate keys to the documented schema."}) }
	return cfg, issues
}

func set(c *Config, key, value string) bool {
	switch key {
	case ".version": c.Version=value; case ".policy": c.Policy=value
	case "go.module": c.GoModule=value; case "go.repository": c.GoRepository=value
	case "maven.group_id": c.GroupID=value; case "maven.artifact_id": c.ArtifactID=value; case "maven.build_manifest": c.BuildManifest=value
	case "maven.name": c.Name=value; case "maven.description": c.Description=value; case "maven.url": c.URL=value
	case "maven.license_name": c.LicenseName=value; case "maven.license_url": c.LicenseURL=value
	case "maven.developer_id": c.DeveloperID=value; case "maven.developer_name": c.DeveloperName=value; case "maven.developer_email": c.DeveloperEmail=value; case "maven.developer_url": c.DeveloperURL=value
	case "maven.scm_url": c.SCMURL=value; case "maven.scm_connection": c.SCMConnection=value; case "maven.scm_developer_connection": c.SCMDeveloperConnection=value; case "maven.bundle": c.Bundle=value
	default: return false
	}; return true
}

func validateConfig(c Config) []Issue {
	issues := []Issue{}
	required := []struct{name, value string}{{"version",c.Version},{"policy",c.Policy},{"go.module",c.GoModule},{"go.repository",c.GoRepository},{"maven.group_id",c.GroupID},{"maven.artifact_id",c.ArtifactID},{"maven.build_manifest",c.BuildManifest},{"maven.name",c.Name},{"maven.description",c.Description},{"maven.url",c.URL},{"maven.license_name",c.LicenseName},{"maven.license_url",c.LicenseURL},{"maven.developer_id",c.DeveloperID},{"maven.developer_name",c.DeveloperName},{"maven.developer_email",c.DeveloperEmail},{"maven.developer_url",c.DeveloperURL},{"maven.scm_url",c.SCMURL},{"maven.scm_connection",c.SCMConnection},{"maven.scm_developer_connection",c.SCMDeveloperConnection},{"maven.bundle",c.Bundle}}
	for _, item := range required { if strings.TrimSpace(item.value)=="" { issues=append(issues, Issue{"CONFIG_REQUIRED_MISSING", "required setting "+item.name+" is missing or empty", "Add "+item.name+" to assayxport.toml with the authoritative publication value."}) } }
	if c.Policy!="" && c.Policy!="goplus-dual" && c.Policy!="java-library" { issues=append(issues, Issue{"POLICY_UNSUPPORTED", "policy "+strconv.Quote(c.Policy)+" is unsupported", "Set policy = \"goplus-dual\" for Go+ dual publication or \"java-library\" for an ordinary Java library."}) }
	if c.Version!="" && !regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(c.Version) { issues=append(issues, Issue{"VERSION_INVALID", "version must be a release X.Y.Z without a v prefix", "Set version to the Maven version, for example \"0.4.0\"; the Go version is derived as v0.4.0."}) }
	if c.GroupID!="" && !regexp.MustCompile(`^[A-Za-z0-9_]+(\.[A-Za-z0-9_]+)+$`).MatchString(c.GroupID) { issues=append(issues,Issue{"MAVEN_GROUP_INVALID","maven.group_id is not a reverse-domain Maven group","Use a controlled reverse-domain group such as \"dev.goforge\"."}) }
	if c.ArtifactID!="" && !regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`).MatchString(c.ArtifactID) { issues=append(issues,Issue{"MAVEN_ARTIFACT_INVALID","maven.artifact_id contains unsupported characters","Use a lowercase Maven artifact id containing letters, digits, dots, underscores, or hyphens."}) }
	if c.DeveloperEmail!="" { if _,err:=mail.ParseAddress(c.DeveloperEmail); err!=nil { issues=append(issues,Issue{"DEVELOPER_EMAIL_INVALID","maven.developer_email is not a valid address","Set developer_email to a monitored publisher contact address."}) } }
	for _, item:=range []struct{name,value string}{{"go.repository",c.GoRepository},{"maven.url",c.URL},{"maven.license_url",c.LicenseURL},{"maven.developer_url",c.DeveloperURL},{"maven.scm_url",c.SCMURL}} { if item.value!="" { parsed,err:=url.Parse(item.value); if err!=nil||parsed.Scheme!="https"||parsed.Host=="" { issues=append(issues,Issue{"URL_INVALID",item.name+" must be an absolute HTTPS URL","Set "+item.name+" to its canonical https:// URL."}) } } }
	if c.Bundle!="" { if _,ok:=safePath(".",c.Bundle); !ok { issues=append(issues,Issue{"BUNDLE_PATH_INVALID","maven.bundle escapes the project root","Use a relative path such as dist/central/name-version-bundle.zip."}) } }
	return issues
}

type buildManifest struct { Schema string `json:"schema"`; Outputs []struct{Path string `json:"path"`; SHA256 string `json:"sha256"`} `json:"outputs"` }
func validateBuild(root string, c Config) []Issue {
	if c.BuildManifest=="" { return nil }; path, ok := safePath(root,c.BuildManifest); if !ok { return []Issue{{"BUILD_MANIFEST_PATH_INVALID","maven.build_manifest escapes the project root","Use a relative path inside the checkout, normally .goplus/build/java/publication.json."}} }
	data, err := os.ReadFile(path); if err != nil { return []Issue{{"BUILD_MANIFEST_MISSING",fmt.Sprintf("cannot read build manifest %s: %v",c.BuildManifest,err),"Run `go tool goplus build --target java ./...` from the clean tagged checkout, then rerun `ax publish --prepare`."}} }
	var m buildManifest; if err:=json.Unmarshal(data,&m); err!=nil { return []Issue{{"BUILD_MANIFEST_INVALID",err.Error(),"Regenerate it with `go tool goplus build --target java ./...`; do not hand-edit publication.json."}} }
	issues:=[]Issue{}; if m.Schema!="goplus.java.build/v2" { issues=append(issues,Issue{"BUILD_MANIFEST_SCHEMA",fmt.Sprintf("build manifest schema is %q",m.Schema),"Rebuild with the required Go+ release so it emits goplus.java.build/v2."}) }
	if len(m.Outputs)<3 { issues=append(issues,Issue{"BUILD_OUTPUTS_INCOMPLETE","build manifest does not list main, sources, and Javadoc artifacts","Rebuild with Go+ schema v2 and configure jar, sources_jar, and javadoc_jar outputs."}) }
	for _, out:=range m.Outputs { artifact, safe:=safePath(root,out.Path); if !safe { issues=append(issues,Issue{"BUILD_OUTPUT_PATH_INVALID","manifest output escapes project root: "+out.Path,"Regenerate the manifest; output paths must be relative and remain inside the checkout."}); continue }; bytes,e:=os.ReadFile(artifact); if e!=nil { issues=append(issues,Issue{"BUILD_OUTPUT_MISSING","manifest output is missing: "+out.Path,"Run `go tool goplus build --target java ./...` and do not move artifacts before publishing."}); continue }; sum:=sha256.Sum256(bytes); if hex.EncodeToString(sum[:])!=out.SHA256 { issues=append(issues,Issue{"BUILD_OUTPUT_STALE","artifact digest does not match the manifest: "+out.Path,"Regenerate all Java artifacts and publication.json in one `go tool goplus build --target java ./...` invocation."}) } }
	return issues
}
func safePath(root, rel string)(string,bool){ if filepath.IsAbs(rel){return "",false}; clean:=filepath.Clean(filepath.FromSlash(rel)); if clean==".."||strings.HasPrefix(clean,".."+string(filepath.Separator)){return "",false}; return filepath.Join(root,clean),true }
