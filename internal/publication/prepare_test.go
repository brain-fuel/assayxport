package publication

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareBuildsDeterministicSignedCentralBundle(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")
	t.Setenv("AX_MAVEN_SIGNING_KEY", filepath.Join(root, "key.asc"))
	paths := []string{"dist/demo-0.4.0.jar", "dist/demo-0.4.0-sources.jar", "dist/demo-0.4.0-javadoc.jar"}
	manifest := buildManifest{Schema: "goplus.java.build/v2"}
	for _, path := range paths {
		data := []byte("artifact:" + path)
		artifactPath := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, artifactPath, string(data))
		sum := sha256.Sum256(data)
		manifest.Outputs = append(manifest.Outputs, struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		}{path, hex.EncodeToString(sum[:])})
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, ".goplus/build/java/publication.json")
	if err = os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifestPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfigFromText(t, root, validConfig)
	cfg.Bundle = "dist/central/demo-0.4.0-bundle.zip"
	first, err := Prepare(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	a, err := os.ReadFile(first.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Prepare(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(second.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("bundle is not deterministic")
	}
	zr, err := zip.OpenReader(first.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	names := []string{}
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	joined := strings.Join(names, "\n")
	for _, want := range []string{"demo-0.4.0.jar", "demo-0.4.0-sources.jar", "demo-0.4.0-javadoc.jar", "demo-0.4.0.pom", "demo-0.4.0.jar.asc", "demo-0.4.0.jar.sha512"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("bundle missing %s", want)
		}
	}
}

func loadConfigFromText(t *testing.T, root, text string) (Config, []Issue) {
	t.Helper()
	writeTestFile(t, filepath.Join(root, "assayxport.toml"), text)
	return loadConfig(filepath.Join(root, "assayxport.toml"))
}
