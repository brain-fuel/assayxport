package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestResolvedVersionNonEmpty(t *testing.T) {
	if resolvedVersion() == "" {
		t.Fatal("resolvedVersion() returned empty")
	}
}

// TestVersionCommand checks that every documented spelling prints "ax <version>"
// to stdout and exits cleanly.
func TestVersionCommand(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}, {"-v"}} {
		out := captureStdout(t, func() {
			if err := run(args); err != nil {
				t.Fatalf("run(%v) = %v", args, err)
			}
		})
		if !strings.HasPrefix(out, "ax ") || strings.TrimSpace(out) == "ax" {
			t.Errorf("run(%v) printed %q, want \"ax <version>\"", args, out)
		}
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	fn()
	w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}
