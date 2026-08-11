package publication

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreflightMissingConfigIsActionable(t *testing.T) {
	_, report := Preflight(t.TempDir())
	if report.OK() || len(report.Issues) != 1 || !strings.Contains(report.Error(), "[CONFIG_MISSING]") || !strings.Contains(report.Error(), "ax publish --init") {
		t.Fatalf("report is not actionable:\n%s", report.Error())
	}
}

func TestInitWritesInferredTemplateAndNeverOverwrites(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module goforge.dev/demo\n")
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	path, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`module = "goforge.dev/demo"`, `artifact_id = "demo"`, `version = "TODO_VERSION"`, `build_manifest = ".goplus/build/java/publication.json"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("template missing %s:\n%s", want, text)
		}
	}
	if _, err := Init(root); err == nil || !strings.Contains(err.Error(), "[CONFIG_EXISTS]") {
		t.Fatalf("second init error = %v", err)
	}
}

func TestPreflightCollectsMissingMetadata(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "assayxport.toml"), "schema_version = 1\nversion = \"bad\"\npolicy = \"mystery\"\n")
	_, report := Preflight(root)
	text := report.Error()
	for _, code := range []string{"CONFIG_REQUIRED_MISSING", "POLICY_UNSUPPORTED", "VERSION_INVALID"} {
		if !strings.Contains(text, "["+code+"]") {
			t.Fatalf("missing %s in:\n%s", code, text)
		}
	}
	if strings.Count(text, "Fix:") != len(report.Issues) {
		t.Fatalf("not every issue has a fix:\n%s", text)
	}
}

func TestPreflightRejectsMissingBuildWithCommand(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "assayxport.toml"), validConfig)
	_, report := Preflight(root)
	text := report.Error()
	if !strings.Contains(text, "[BUILD_MANIFEST_MISSING]") || !strings.Contains(text, "go tool goplus build --target java ./...") {
		t.Fatalf("report:\n%s", text)
	}
}

func TestPreflightParsesDocumentationWaiver(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "assayxport.toml"), validConfig+`
[[documentation.waivers]]
issue = "DOC_MEMBER_MISSING"
symbol = "demo.Parser.Parse"
reason = "Compatibility bridge is documented in the next minor release"
through = "0.4.0"
`)
	cfg, report := Preflight(root)
	if len(cfg.Waivers) != 1 || cfg.Waivers[0].Symbol != "demo.Parser.Parse" {
		t.Fatalf("waivers = %#v", cfg.Waivers)
	}
	if strings.Contains(report.Error(), "CONFIG_UNKNOWN_TABLE") {
		t.Fatalf("documented waiver table was rejected:\n%s", report.Error())
	}
}

func TestPreflightRejectsExpiredOrIncompleteWaiverWithFixes(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "assayxport.toml"), validConfig+`
[[documentation.waivers]]
issue = "TODO"
symbol = "TODO_SYMBOL"
reason = "TODO"
through = "0.3.0"
`)
	_, report := Preflight(root)
	text := report.Error()
	for _, code := range []string{"WAIVER_ISSUE_INVALID", "WAIVER_SYMBOL_INVALID", "WAIVER_REASON_INVALID", "WAIVER_EXPIRED"} {
		if !strings.Contains(text, "["+code+"]") {
			t.Fatalf("missing %s in:\n%s", code, text)
		}
	}
	if strings.Count(text, "Fix:") != len(report.Issues) {
		t.Fatalf("not every waiver issue has a fix:\n%s", text)
	}
}

func TestPreflightRejectsStaleManifestSourceWithRebuildInstruction(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "Generated.java"), "class Generated {}\n")
	if err := os.MkdirAll(filepath.Join(root, ".goplus", "build", "java"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, ".goplus", "build", "java", "publication.json"), `{
  "schema": "goplus.java.build/v2",
  "sources": [{"path":"Generated.java","sha256":"wrong","canonical_documentation_sha256":"present"}],
  "outputs": []
}`)
	_, report := Preflight(rootWithConfig(t, root))
	text := report.Error()
	if !strings.Contains(text, "[BUILD_SOURCE_STALE]") || !strings.Contains(text, "go tool goplus build --target java ./...") {
		t.Fatalf("stale source report is not actionable:\n%s", text)
	}
}

func rootWithConfig(t *testing.T, root string) string {
	t.Helper()
	writeTestFile(t, filepath.Join(root, "assayxport.toml"), validConfig)
	return root
}

func TestGitReleaseGateRequiresCleanMatchingTag(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init")
	gitTest(t, root, "config", "user.email", "test@example.com")
	gitTest(t, root, "config", "user.name", "Test")
	writeTestFile(t, filepath.Join(root, "release.txt"), "ready\n")
	gitTest(t, root, "add", "release.txt")
	gitTest(t, root, "commit", "-m", "release")
	if issues := validateGitRelease(root, Config{Version: "0.4.0"}); len(issues) != 1 || issues[0].Code != "GIT_TAG_MISSING" {
		t.Fatalf("missing-tag issues = %#v", issues)
	}
	gitTest(t, root, "tag", "-a", "v0.4.0", "-m", "release v0.4.0")
	if issues := validateGitRelease(root, Config{Version: "0.4.0"}); len(issues) != 0 {
		t.Fatalf("tagged issues = %#v", issues)
	}
	writeTestFile(t, filepath.Join(root, "release.txt"), "dirty\n")
	issues := validateGitRelease(root, Config{Version: "0.4.0"})
	if len(issues) != 1 || issues[0].Code != "GIT_CHECKOUT_DIRTY" || !strings.Contains(issues[0].Fix, "git status --short") {
		t.Fatalf("dirty issues = %#v", issues)
	}
}

func gitTest(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const validConfig = `schema_version = 1
version = "0.4.0"
policy = "goplus-dual"
[go]
module = "goforge.dev/demo"
repository = "https://github.com/example/demo"
[maven]
group_id = "dev.goforge"
artifact_id = "demo"
build_manifest = ".goplus/build/java/publication.json"
name = "Demo"
description = "Demo library"
url = "https://goforge.dev/demo/"
license_name = "MIT License"
license_url = "https://opensource.org/license/mit"
developer_id = "example"
developer_name = "Example"
developer_email = "example@example.com"
developer_url = "https://example.com"
scm_url = "https://github.com/example/demo"
scm_connection = "scm:git:https://github.com/example/demo.git"
scm_developer_connection = "scm:git:ssh://git@github.com/example/demo.git"
bundle = "dist/central/demo-0.4.0-bundle.zip"
`
