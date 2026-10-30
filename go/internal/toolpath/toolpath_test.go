package toolpath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFind_honorsEnvOverrideFirst(t *testing.T) {
	t.Setenv("SUM_HC_BIN", "/somewhere/hc")
	got, err := Find(t.TempDir(), "hc")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got != "/somewhere/hc" {
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

func TestFind_HCRequiresPinnedArtifact(t *testing.T) {
	t.Setenv("SUM_HC_BIN", "")
	root := t.TempDir()
	bin := t.TempDir()
	candidate := filepath.Join(bin, "hc")
	if err := os.WriteFile(candidate, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if _, err := Find(root, "hc"); err == nil {
		t.Fatal("ambient hc executable substituted for exact staged runtime")
	}
}
