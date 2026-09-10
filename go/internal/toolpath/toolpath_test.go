package toolpath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFind_honorsEnvOverrideFirst(t *testing.T) {
	t.Setenv("SUM_HERDR_BIN", "/somewhere/herdr")
	got, err := Find(t.TempDir(), "herdr")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got != "/somewhere/herdr" {
		t.Fatalf("got %q, want the env override", got)
	}
}

func TestFind_honorsHyphenatedNameEnvVar(t *testing.T) {
	t.Setenv("SUM_QUOTA_AXI_BIN", "/somewhere/quota-axi")
	got, err := Find(t.TempDir(), "quota-axi")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got != "/somewhere/quota-axi" {
		t.Fatalf("got %q, want the env override", got)
	}
}

func TestFind_prefersPinnedLocalOverPath(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, ".local", "bin", "widget")
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Find(root, "widget")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got != local {
		t.Fatalf("got %q, want the pinned local %q", got, local)
	}
}

func TestFind_errorsWhenNowhereToBeFound(t *testing.T) {
	_, err := Find(t.TempDir(), "definitely-not-a-real-tool-xyz")
	if err == nil {
		t.Fatal("expected an error for a missing tool")
	}
	want := "Missing definitely-not-a-real-tool-xyz. Run mise run setup."
	if err.Error() != want {
		t.Fatalf("err = %q, want %q", err.Error(), want)
	}
}
