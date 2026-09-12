package quota

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRun_codexDefaultFormatIsToon(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	root := t.TempDir()
	argvFile := filepath.Join(t.TempDir(), "argv")
	writeFakeTool(t, root, "remainder", argvFile, "toon-body", 0)
	var stdout, stderr bytes.Buffer
	code, err := Run(root, "codex", "", &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("code %d stderr %q", code, stderr.String())
	}
	if stdout.String() != "toon-body" {
		t.Fatalf("stdout %q", stdout.String())
	}
	got := strings.TrimSpace(readFile(t, argvFile))
	if !strings.Contains(got, "--format toon") {
		t.Fatalf("argv %q", got)
	}
}

func TestRun_codexJSONAndCompactPassThrough(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	root := t.TempDir()
	argvFile := filepath.Join(t.TempDir(), "argv")
	writeFakeTool(t, root, "remainder", argvFile, "{}", 0)
	var stdout bytes.Buffer
	if _, err := Run(root, "codex", "json", &stdout, ioDiscard()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.TrimSpace(readFile(t, argvFile)), "--format json") {
		t.Fatalf("json argv %q", readFile(t, argvFile))
	}
	if _, err := Run(root, "codex", "compact", &stdout, ioDiscard()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.TrimSpace(readFile(t, argvFile)), "--format compact") {
		t.Fatalf("compact argv %q", readFile(t, argvFile))
	}
}

func TestRun_codexMissingRemainderDoesNotCallQuotaAxi(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SUM_REMAINDER_BIN", "")
	var stdout, stderr bytes.Buffer
	code, err := Run(t.TempDir(), "codex", "", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error")
	}
	if code != 1 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(err.Error(), "quota-axi is not used") {
		t.Fatalf("error %v", err)
	}
}

func TestRun_otherProviderUsesQuotaAxi(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	root := t.TempDir()
	argvFile := filepath.Join(t.TempDir(), "argv")
	writeFakeTool(t, root, "quota-axi", argvFile, "axi", 0)
	var stdout bytes.Buffer
	code, err := Run(root, "claude", "", &stdout, ioDiscard())
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 || stdout.String() != "axi" {
		t.Fatalf("code %d stdout %q", code, stdout.String())
	}
	got := strings.TrimSpace(readFile(t, argvFile))
	if strings.Contains(got, "remainder") || strings.Contains(got, "--format toon") {
		t.Fatalf("argv %q", got)
	}
}

type discard struct{}

func ioDiscard() *discard { return &discard{} }

func (*discard) Write(p []byte) (int, error) { return len(p), nil }

func writeFakeTool(t *testing.T, root, name, argvFile, stdout string, code int) {
	t.Helper()
	dir := filepath.Join(root, ".local", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s' \"$*\" > " + strconv.Quote(argvFile) + "\nprintf %s " + strconv.Quote(stdout) + "\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
