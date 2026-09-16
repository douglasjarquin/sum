package environment

import (
	"fmt"
	"os"
	"path/filepath"
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
	mise := filepath.Join(root, "mise")
	script := fmt.Sprintf("#!/bin/sh\nif [ -z \"$MISE_TRUSTED_CONFIG_PATHS\" ]; then\n  printf 'config is not trusted\\n' >&2\n  exit 1\nfi\nprintf '%%s\\n' '[{\"name\":\"verify\",\"source\":\"%s\"}]'\n", filepath.Join(root, "mise.toml"))
	if err := os.WriteFile(mise, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_MISE_BIN", mise)

	status := VerificationContractStatus(root)
	value, _ := status.Get("status")
	if value != "standardized" {
		t.Fatalf("status = %v, want standardized", value)
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
	script := fmt.Sprintf("#!/bin/sh\nif [ -z \"$MISE_TRUSTED_CONFIG_PATHS\" ]; then exit 1; fi\nprintf '%%s\\n' '[{\"name\":\"verify\",\"source\":\"%s\"}]'\n", source)
	if err := os.WriteFile(mise, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_MISE_BIN", mise)

	status := VerificationContractStatus(root)
	value, _ := status.Get("status")
	if value != "not-yet-standardized" {
		t.Fatalf("status = %v, want not-yet-standardized", value)
	}
}
