package publication

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const repoConfig = `schema_version = 1
version = "1.2.3"
policy = "maven-repository"
group_id = "dev.example"
staging = "target/central-staging"
artifacts = "demo-parent,demo,demo-pom-only"
`

func pomText(group, artifact, version, packaging, parent, extra string) string {
	parentXML := ""
	if parent != "" {
		parentXML = fmt.Sprintf("<parent><groupId>%s</groupId><artifactId>%s</artifactId><version>%s</version></parent>", group, parent, version)
	}
	return fmt.Sprintf(`<?xml version="1.0"?><project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion>%s<groupId>%s</groupId><artifactId>%s</artifactId><version>%s</version><packaging>%s</packaging>%s</project>`,
		parentXML, group, artifact, version, packaging, extra)
}

const metadata = `<url>https://example.dev/demo</url>
<licenses><license><name>MIT</name><url>https://opensource.org/license/mit</url></license></licenses>
<developers><developer><id>d</id><name>Dev</name></developer></developers>
<scm><connection>scm:git:https://example.dev/demo.git</connection><developerConnection>scm:git:ssh://git@example.dev/demo.git</developerConnection><url>https://example.dev/demo</url></scm>`

const named = `<name>Demo</name><description>A demo.</description>`

// stageFile writes one staged file under the example group.
func stageFile(t *testing.T, root, artifact, version, name, body string) {
	t.Helper()
	dir := filepath.Join(root, "target", "central-staging", "dev", "example", artifact, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, name), body)
}

// stageValid stages a parent pom carrying the shared metadata, a jar module
// inheriting it, and a pom-packaging module. It mirrors what Maven's Central
// plugin stages, including build checksums.
func stageValid(t *testing.T, root string) {
	t.Helper()
	stageFile(t, root, "demo-parent", "1.2.3", "demo-parent-1.2.3.pom", pomText("dev.example", "demo-parent", "1.2.3", "pom", "", named+metadata))
	stageFile(t, root, "demo-parent", "1.2.3", "demo-parent-1.2.3.pom.md5", "stale")
	stageFile(t, root, "demo", "1.2.3", "demo-1.2.3.pom", pomText("dev.example", "demo", "1.2.3", "jar", "demo-parent", named))
	for _, suffix := range []string{".jar", "-sources.jar", "-javadoc.jar"} {
		stageFile(t, root, "demo", "1.2.3", "demo-1.2.3"+suffix, "bytes"+suffix)
		stageFile(t, root, "demo", "1.2.3", "demo-1.2.3"+suffix+".sha1", "stale")
		stageFile(t, root, "demo", "1.2.3", "demo-1.2.3"+suffix+".sha512", "stale")
	}
	stageFile(t, root, "demo-pom-only", "1.2.3", "demo-pom-only-1.2.3.pom", pomText("dev.example", "demo-pom-only", "1.2.3", "pom", "demo-parent", named))
	stageFile(t, root, "demo", "", "maven-metadata-central-staging.xml", "<metadata/>")
}

func repoFixture(t *testing.T) (string, Config) {
	t.Helper()
	root := t.TempDir()
	stageValid(t, root)
	cfg, issues := loadConfigFromText(t, root, repoConfig)
	if len(issues) != 0 {
		t.Fatalf("config issues: %#v", issues)
	}
	return root, cfg
}

func codes(issues []Issue) string {
	out := []string{}
	for _, issue := range issues {
		out = append(out, issue.Code)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func TestRepositoryConfigParsesRootKeysAndArrays(t *testing.T) {
	root, cfg := repoFixture(t)
	if cfg.Policy != "maven-repository" || cfg.GroupID != "dev.example" || cfg.Staging != "target/central-staging" || strings.Join(cfg.Artifacts, "|") != "demo-parent|demo|demo-pom-only" {
		t.Fatalf("config = %#v", cfg)
	}
	if issues := validateConfig(cfg); len(issues) != 0 {
		t.Fatalf("config issues: %#v", issues)
	}
	array, _ := loadConfigFromText(t, root, strings.Replace(repoConfig, `artifacts = "demo-parent,demo,demo-pom-only"`, `artifacts = ["demo-parent", "demo", "demo-pom-only"]`, 1))
	if strings.Join(array.Artifacts, "|") != "demo-parent|demo|demo-pom-only" {
		t.Fatalf("array artifacts = %#v", array.Artifacts)
	}
}

func TestRepositoryAcceptsStagedReactorWithBuildChecksums(t *testing.T) {
	root, cfg := repoFixture(t)
	if issues := validateRepository(root, cfg); len(issues) != 0 {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestRepositoryConfigRejectsOtherPolicyKeysAndBadValues(t *testing.T) {
	cfg := Config{Version: "1.2.3-SNAPSHOT", Policy: "maven-repository", GroupID: "dev.example", Staging: "../out", Artifacts: []string{"a", "a", "Bad Id"}, GoModule: "x", Bundle: "target/central-staging/bundle.zip"}
	got := codes(validateConfig(cfg))
	for _, want := range []string{"VERSION_INVALID", "STAGING_PATH_INVALID", "REPO_ARTIFACT_DUPLICATE", "MAVEN_ARTIFACT_INVALID", "CONFIG_KEY_UNSUPPORTED"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
	inside := Config{Version: "1.2.3", Policy: "maven-repository", GroupID: "dev.example", Staging: "target/central-staging", Artifacts: []string{"a"}, Bundle: "target/central-staging/bundle.zip"}
	if got := codes(validateConfig(inside)); got != "BUNDLE_INSIDE_STAGING" {
		t.Fatalf("inside = %s", got)
	}
}

func TestRepositoryMissingStagingIsOneActionableIssue(t *testing.T) {
	root := t.TempDir()
	cfg, _ := loadConfigFromText(t, root, repoConfig)
	issues := validateRepository(root, cfg)
	if len(issues) != 1 || issues[0].Code != "STAGING_MISSING" || issues[0].Fix == "" {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestRepositoryAccumulatesEveryProblem(t *testing.T) {
	root, cfg := repoFixture(t)
	cfg.Artifacts = []string{"demo-parent", "demo", "demo-pom-only", "ghost"}
	base := filepath.Join(root, "target", "central-staging", "dev", "example")
	remove := func(rel string) {
		if err := os.Remove(filepath.Join(base, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
	}
	remove("demo/1.2.3/demo-1.2.3-javadoc.jar")
	remove("demo/1.2.3/demo-1.2.3-sources.jar")
	remove("demo/1.2.3/demo-1.2.3.jar")
	stageFile(t, root, "demo", "1.2.3", "demo-1.2.3.jar.asc", "build signature")
	stageFile(t, root, "extra", "1.2.3", "extra-1.2.3.pom", "<project/>")
	stageFile(t, root, "demo", "1.2.4-SNAPSHOT", "demo-1.2.4-SNAPSHOT.jar", "x")
	stageFile(t, root, "demo", "1.2.0", "demo-1.2.0.jar", "x")
	stageFile(t, root, "demo-pom-only", "1.2.3", "demo-pom-only-1.2.3.pom",
		pomText("dev.other", "wrong", "9.9.9", "pom", "", "")) // wrong coordinates, no metadata
	got := codes(validateRepository(root, cfg))
	for _, want := range []string{
		"REPO_ARTIFACT_NOT_STAGED", "REPO_ARTIFACT_NOT_NAMED", "STAGING_SNAPSHOT", "STAGING_VERSION_MISMATCH",
		"REPO_POM_COORDINATE_MISMATCH", "REPO_JAR_MISSING", "REPO_SOURCES_MISSING", "REPO_JAVADOC_MISSING",
		"REPO_POM_METADATA_MISSING", "STAGING_SIGNATURE_PRESENT",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
}

func TestRepositoryMetadataFollowsStagedParentOnly(t *testing.T) {
	root, cfg := repoFixture(t)
	// A child of an unstaged parent cannot inherit url/licenses/developers/scm.
	stageFile(t, root, "demo-pom-only", "1.2.3", "demo-pom-only-1.2.3.pom", pomText("dev.example", "demo-pom-only", "1.2.3", "pom", "elsewhere", named))
	issues := validateRepository(root, cfg)
	if len(issues) != 1 || issues[0].Code != "REPO_POM_METADATA_MISSING" || !strings.Contains(issues[0].Problem, "licenses") || strings.Contains(issues[0].Problem, "name,") {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestRepositoryRejectsSnapshotParentAndEmptyJar(t *testing.T) {
	root, cfg := repoFixture(t)
	stageFile(t, root, "demo", "1.2.3", "demo-1.2.3.pom", pomText("dev.example", "demo", "1.2.3", "jar", "demo-parent", named)+"")
	stageFile(t, root, "demo", "1.2.3", "demo-1.2.3.jar", "")
	got := codes(validateRepository(root, cfg))
	if got != "REPO_JAR_MISSING" {
		t.Fatalf("empty jar = %s", got)
	}
	snapshotParent := strings.Replace(pomText("dev.example", "demo", "1.2.3", "jar", "demo-parent", named), "<version>1.2.3</version></parent>", "<version>1.2.3-SNAPSHOT</version></parent>", 1)
	stageFile(t, root, "demo", "1.2.3", "demo-1.2.3.pom", snapshotParent)
	got = codes(validateRepository(root, cfg))
	if !strings.Contains(got, "REPO_POM_PARENT_MISMATCH") || !strings.Contains(got, "REPO_POM_SNAPSHOT") {
		t.Fatalf("snapshot parent = %s", got)
	}
}

func bundleEntries(t *testing.T, path string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string]string{}
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = string(data)
	}
	return out
}

func TestPrepareRepositoryBuildsDeterministicSignedBundle(t *testing.T) {
	root, cfg := repoFixture(t)
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")
	t.Setenv("AX_MAVEN_SIGNING_KEY", filepath.Join(root, "key.asc"))
	first, err := Prepare(t.Context(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "target", "central-staging-bundle.zip"); first.Bundle != want {
		t.Fatalf("bundle = %s, want %s", first.Bundle, want)
	}
	a, _ := os.ReadFile(first.Bundle)
	second, err := Prepare(t.Context(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(second.Bundle)
	if !bytes.Equal(a, b) {
		t.Fatal("bundle is not deterministic")
	}
	entries := bundleEntries(t, first.Bundle)
	names := []string{}
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	prefix := "dev/example/"
	for _, want := range []string{
		prefix + "demo-parent/1.2.3/demo-parent-1.2.3.pom",
		prefix + "demo/1.2.3/demo-1.2.3.jar",
		prefix + "demo/1.2.3/demo-1.2.3-sources.jar",
		prefix + "demo/1.2.3/demo-1.2.3-javadoc.jar",
		prefix + "demo/1.2.3/demo-1.2.3.pom",
		prefix + "demo-pom-only/1.2.3/demo-pom-only-1.2.3.pom",
	} {
		for _, suffix := range []string{"", ".asc", ".md5", ".sha1", ".sha256", ".sha512", ".asc.md5", ".asc.sha1"} {
			if _, ok := entries[want+suffix]; !ok {
				t.Fatalf("bundle missing %s%s; have %v", want, suffix, names)
			}
		}
	}
	// 6 files, each with .asc plus 4 checksums for both: 6 * (1 + 4 + 1 + 4).
	if len(names) != 60 {
		t.Fatalf("bundle has %d entries: %v", len(names), names)
	}
	for _, name := range names {
		if strings.Contains(name, "maven-metadata") {
			t.Fatalf("bookkeeping leaked into bundle: %s", name)
		}
	}
	if entries[prefix+"demo/1.2.3/demo-1.2.3.jar.sha1"] == "stale" || entries[prefix+"demo-parent/1.2.3/demo-parent-1.2.3.pom.md5"] == "stale" {
		t.Fatal("build checksums were not regenerated")
	}
	if !strings.HasPrefix(entries[prefix+"demo/1.2.3/demo-1.2.3.jar.asc"], "-----BEGIN PGP SIGNATURE-----") {
		t.Fatal("jar is not signed")
	}
	if first.Fingerprint == "" || len(first.PublicKey) == 0 {
		t.Fatalf("prepared = %#v", first)
	}
}

func TestPreflightConfigReadsConfigOutsideCheckout(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init")
	gitTest(t, root, "config", "user.email", "test@example.com")
	gitTest(t, root, "config", "user.name", "Test")
	writeTestFile(t, filepath.Join(root, ".gitignore"), "target/\n")
	stageValid(t, root)
	gitTest(t, root, "add", ".gitignore")
	gitTest(t, root, "commit", "-m", "release")
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "assayxport.toml")
	writeTestFile(t, configPath, repoConfig)
	if _, report := PreflightConfig(root, configPath); len(report.Issues) != 1 || report.Issues[0].Code != "GIT_TAG_MISSING" {
		t.Fatalf("untagged = %#v", report.Issues)
	}
	gitTest(t, root, "tag", "-a", "v1.2.3", "-m", "release")
	if _, report := PreflightConfig(root, configPath); !report.OK() {
		t.Fatalf("tagged = %#v", report.Issues)
	}
	writeTestFile(t, filepath.Join(root, "stray.txt"), "dirty")
	if _, report := PreflightConfig(root, configPath); len(report.Issues) != 1 || report.Issues[0].Code != "GIT_CHECKOUT_DIRTY" {
		t.Fatalf("dirty = %#v", report.Issues)
	}
}
