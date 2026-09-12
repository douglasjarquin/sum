package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompiledEntrypoint_errorUsesTOON(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "sumctl")
	build := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, output)
	}

	home := t.TempDir()
	cmd := exec.Command(binary, "--home", home, "preset", "show", "nope")
	combined, _ := cmd.CombinedOutput()
	got := extractLast(string(combined))
	if !strings.HasPrefix(got, "error:") {
		t.Fatalf("stderr is not TOON error: %q", got)
	}
}

func extractLast(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

func TestHelperPathPrefersExecutable(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip(err)
	}
}
