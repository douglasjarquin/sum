package environment

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerificationContractStatusTrustsTargetConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "VERIFY.md"), []byte("# Verification contract\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mise.toml"), []byte("[tasks]\nverify = \"true\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "trust-bypass")
	mise := filepath.Join(root, "mise")
	script := fmt.Sprintf("#!/bin/sh\nif [ -n \"$MISE_TRUSTED_CONFIG_PATHS\" ]; then : > \"%s\"; fi\nprintf '%%s\\n' '[{\"name\":\"verify\",\"source\":\"%s\"}]'\n", marker, filepath.Join(root, "mise.toml"))
	if err := os.WriteFile(mise, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_MISE_BIN", mise)

	status := VerificationContractStatus(root)
	value, _ := status.Get("status")
	if value != "standardized" {
		t.Fatalf("status = %v, want standardized", value)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("target configuration was trusted")
	}
}

func TestVerificationContractStatusDoesNotAdoptInheritedTask(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "VERIFY.md"), []byte("# Verification contract\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(parent, "mise-tasks", "verify")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mise := filepath.Join(parent, "mise")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' '[{\"name\":\"verify\",\"source\":\"%s\"}]'\n", source)
	if err := os.WriteFile(mise, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_MISE_BIN", mise)

	status := VerificationContractStatus(root)
	value, _ := status.Get("status")
	if value != "not-yet-standardized" {
		t.Fatalf("status = %v, want not-yet-standardized", value)
	}
	runtime := t.TempDir()
	misePath := filepath.Join(runtime, ".local", "bin", "mise")
	if err := os.MkdirAll(filepath.Dir(misePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(misePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	passive := VerificationContractStatusAtDispatch(root, runtime)
	passiveWhy, _ := passive.Get("why")
	if !strings.Contains(fmt.Sprint(passiveWhy), "inherited") {
		t.Fatalf("passive why = %v, want inherited-task explanation", passiveWhy)
	}
}

func TestVerificationContractStatusAtRuntimeDoesNotUseTargetLocalMise(t *testing.T) {
	root := t.TempDir()
	runtime := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "VERIFY.md"), []byte("# Verification contract\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mise.toml"), []byte("[tasks]\nverify = \"true\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	targetMarker := filepath.Join(t.TempDir(), "target-ran")
	runtimeMarker := filepath.Join(t.TempDir(), "runtime-ran")
	t.Setenv("SUM_TARGET_MISE_MARKER", targetMarker)
	t.Setenv("SUM_RUNTIME_MISE_MARKER", runtimeMarker)
	t.Setenv("SUM_MISE_TASKS_OUTPUT", fmt.Sprintf(`[{"name":"verify","source":%q}]`, filepath.Join(root, "mise.toml")))
	t.Setenv("SUM_MISE_BIN", "")
	targetMise := filepath.Join(root, ".local", "bin", "mise")
	runtimeMise := filepath.Join(runtime, ".local", "bin", "mise")
	for _, entry := range []struct {
		path   string
		marker string
	}{
		{path: targetMise, marker: "$SUM_TARGET_MISE_MARKER"},
		{path: runtimeMise, marker: "$SUM_RUNTIME_MISE_MARKER"},
	} {
		if err := os.MkdirAll(filepath.Dir(entry.path), 0o755); err != nil {
			t.Fatal(err)
		}
		script := fmt.Sprintf("#!/bin/sh\n: > \"%s\"\nprintf '%%s\\n' \"$SUM_MISE_TASKS_OUTPUT\"\n", entry.marker)
		if err := os.WriteFile(entry.path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	status := VerificationContractStatusAtDispatch(root, runtime)
	value, _ := status.Get("status")
	if value != "standardized" {
		t.Fatalf("status = %v, want standardized", value)
	}
	if _, err := os.Stat(targetMarker); err == nil {
		t.Fatal("target-local mise was executed")
	}
	if _, err := os.Stat(runtimeMarker); err == nil {
		t.Fatal("runtime mise was executed during passive discovery")
	}
}

func TestPassiveDiscoveryRejectsSymlinkedMiseConfig(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "VERIFY.md"), []byte("# Verification contract\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "mise.toml"), []byte("[tasks]\nverify = \"true\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "mise.toml"), filepath.Join(root, "mise.toml")); err != nil {
		t.Fatal(err)
	}
	origins := passiveMiseTaskOriginsAtBase(root)
	if problem, _ := origins.Get("error"); problem == nil {
		t.Fatalf("origins = %v, want a symlink refusal", origins)
	}
}

func TestPassiveDiscoveryBoundsAncestorTraversal(t *testing.T) {
	base := t.TempDir()
	root := base
	for i := 0; i < passiveParentLimit+1; i++ {
		root = filepath.Join(root, fmt.Sprintf("level-%02d", i))
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "VERIFY.md"), []byte("# Verification contract\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inherited := filepath.Join(base, "mise-tasks", "verify")
	if err := os.MkdirAll(filepath.Dir(inherited), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inherited, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runtime := t.TempDir()
	mise := filepath.Join(runtime, ".local", "bin", "mise")
	if err := os.MkdirAll(filepath.Dir(mise), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mise, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	status := VerificationContractStatusAtDispatch(root, runtime)
	if value, _ := status.Get("status"); value != "not-yet-standardized" {
		t.Fatalf("status = %v, want not-yet-standardized", value)
	}
	if why, _ := status.Get("why"); strings.Contains(fmt.Sprint(why), "inherited") {
		t.Fatalf("why = %v, want the bounded scan not to reach the distant parent", why)
	}
}
