package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompiledEntrypoint_errorJSONMatchesPythonSeparatorsAndEscaping(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	reference := filepath.Join(repoRoot, "bin", "sumctl")
	if _, statErr := os.Stat(reference); statErr != nil {
		t.Skipf("reference bin/sumctl not found: %v", statErr)
	}

	dir := t.TempDir()
	binary := filepath.Join(dir, "sumctl-go")
	build := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, output)
	}

	home := t.TempDir()
	args := []string{"--home", home, "preset", "show", "nope"}

	pythonCombined, _ := exec.Command(reference, args...).CombinedOutput()
	wantStderr := extractLast(string(pythonCombined))

	goCmd := exec.Command(binary, args...)
	goCombined, _ := goCmd.CombinedOutput()
	gotStderr := extractLast(string(goCombined))

	if gotStderr != wantStderr {
		t.Fatalf("go stderr = %q, want (python reference) %q", gotStderr, wantStderr)
	}
	if !strings.HasPrefix(gotStderr, `{"error": `) {
		t.Fatalf("go stderr does not use Python's default json.dumps separators: %q", gotStderr)
	}
}

func extractLast(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}
