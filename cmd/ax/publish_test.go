package main

import (
	"os"
	"strings"
	"testing"
)

func TestPublishFailsClosedWithActionablePreflight(t *testing.T) {
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(before) })
	err = run([]string{"publish", "--prepare"})
	if err == nil {
		t.Fatal("publish succeeded without configuration")
	}
	message := err.Error()
	if !strings.Contains(message, "[CONFIG_MISSING]") || !strings.Contains(message, "Fix:") {
		t.Fatalf("failure is not actionable:\n%s", message)
	}
}

func TestPublishRejectsUnexpectedArgumentsWithExactCommand(t *testing.T) {
	err := run([]string{"publish", "surprise"})
	if err == nil || !strings.Contains(err.Error(), "[PUBLISH_ARGUMENT_INVALID]") || !strings.Contains(err.Error(), "ax publish --prepare") {
		t.Fatalf("error = %v", err)
	}
}
