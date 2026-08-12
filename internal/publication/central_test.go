package publication

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPublishUploadsValidatesPromotesAndPersists(t *testing.T) {
	var statuses atomic.Int32
	promoted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("missing authorization")
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/publisher/upload"):
			if got := r.URL.Query().Get("publishingType"); got != "USER_MANAGED" {
				t.Errorf("publishingType=%s", got)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("deployment-1"))
		case r.URL.Path == "/api/v1/publisher/status":
			state := "VALIDATED"
			if statuses.Add(1) > 1 {
				state = "PUBLISHED"
			}
			_ = json.NewEncoder(w).Encode(Deployment{DeploymentID: "deployment-1", DeploymentState: state, PURLs: []string{"pkg:maven/dev.goforge/demo@0.4.0"}})
		case r.URL.Path == "/api/v1/publisher/deployment/deployment-1":
			promoted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	root := t.TempDir()
	bundle := filepath.Join(root, "bundle.zip")
	if err := os.WriteFile(bundle, []byte("bundle"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAVEN_CENTRAL_USERNAME", "user")
	t.Setenv("MAVEN_CENTRAL_PASSWORD", "password")
	t.Setenv("AX_MAVEN_CENTRAL_URL", server.URL)
	d, err := Publish(context.Background(), root, Config{Version: "0.4.0", GroupID: "dev.goforge", ArtifactID: "demo"}, Prepared{Bundle: bundle}, "")
	if err != nil {
		t.Fatal(err)
	}
	if d.DeploymentState != "PUBLISHED" || !promoted {
		t.Fatalf("deployment=%+v promoted=%v", d, promoted)
	}
	state, err := os.ReadFile(filepath.Join(root, ".assayxport/releases/0.4.0.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), `"state": "PUBLISHED"`) {
		t.Fatalf("state=%s", state)
	}
}

func TestPublishRequiresCredentials(t *testing.T) {
	t.Setenv("MAVEN_CENTRAL_USERNAME", "")
	t.Setenv("MAVEN_CENTRAL_PASSWORD", "")
	_, err := Publish(context.Background(), t.TempDir(), Config{}, Prepared{}, "")
	if err == nil || !strings.Contains(err.Error(), "[CENTRAL_CREDENTIALS_MISSING]") {
		t.Fatalf("error=%v", err)
	}
}

func TestCentralCredentialsReadRepositoryMavenSettings(t *testing.T) {
	t.Setenv("MAVEN_CENTRAL_USERNAME", "")
	t.Setenv("MAVEN_CENTRAL_PASSWORD", "")
	root := t.TempDir()
	settings := `<server><id>${server}</id><username>file-user</username><password>file-password</password></server>`
	if err := os.WriteFile(filepath.Join(root, "maven_settings.xml"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	username, password, err := centralCredentials(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if username != "file-user" || password != "file-password" {
		t.Fatal("repository settings credentials were not selected")
	}
}

func TestCentralCredentialsAcceptExplicitStandardSettings(t *testing.T) {
	t.Setenv("MAVEN_CENTRAL_USERNAME", "")
	t.Setenv("MAVEN_CENTRAL_PASSWORD", "")
	root := t.TempDir()
	settings := `<settings><servers><server><id>other</id><username>wrong</username><password>wrong</password></server><server><id>central</id><username>selected</username><password>secret</password></server></servers></settings>`
	if err := os.WriteFile(filepath.Join(root, "custom.xml"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	username, password, err := centralCredentials(root, "./custom.xml")
	if err != nil {
		t.Fatal(err)
	}
	if username != "selected" || password != "secret" {
		t.Fatal("named Central server was not selected")
	}
}

func TestCentralCredentialsEnvironmentIsAuthoritative(t *testing.T) {
	t.Setenv("MAVEN_CENTRAL_USERNAME", "env-user")
	t.Setenv("MAVEN_CENTRAL_PASSWORD", "env-password")
	username, password, err := centralCredentials(t.TempDir(), "missing.xml")
	if err != nil {
		t.Fatal(err)
	}
	if username != "env-user" || password != "env-password" {
		t.Fatal("environment credentials were not authoritative")
	}
}

func TestCentralCredentialsRejectInsecureSettings(t *testing.T) {
	t.Setenv("MAVEN_CENTRAL_USERNAME", "")
	t.Setenv("MAVEN_CENTRAL_PASSWORD", "")
	root := t.TempDir()
	path := filepath.Join(root, "maven_settings.xml")
	if err := os.WriteFile(path, []byte(`<server><username>user</username><password>password</password></server>`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := centralCredentials(root, "")
	if err == nil || !strings.Contains(err.Error(), "[MAVEN_SETTINGS_PERMISSIONS_INSECURE]") || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("error=%v", err)
	}
}

func TestProxyEscapeAndHTTPFailureAreActionable(t *testing.T) {
	if got := proxyEscape("Example.com/Mod"); got != "!example.com/!mod" {
		t.Fatalf("proxy escape = %q", got)
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	err := requireHTTP(context.Background(), server.URL+"/missing", "GO_PROXY_VERSION_MISSING", "publish the tag")
	if err == nil || !strings.Contains(err.Error(), "[GO_PROXY_VERSION_MISSING]") || !strings.Contains(err.Error(), "Fix: publish the tag") {
		t.Fatalf("error = %v", err)
	}
}
