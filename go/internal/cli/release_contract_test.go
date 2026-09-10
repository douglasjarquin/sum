package cli

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestReleaseContract_matchesThePythonReferenceByteForByte(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	reference := filepath.Join(repoRoot, "bin", "sumctl")
	if _, statErr := exec.LookPath("python3"); statErr != nil {
		t.Skip("python3 not on PATH")
	}

	home := t.TempDir()
	want, err := exec.Command(reference, "--home", home, "release-contract").Output()
	if err != nil {
		t.Fatalf("python reference failed: %v", err)
	}

	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs([]string{"--home", home, "release-contract"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("go release-contract failed: %v (stderr=%s)", err, stderr.String())
	}

	if stdout.String() != string(want) {
		t.Fatalf("go output =\n%s\nwant (python reference)\n%s", stdout.String(), want)
	}
}
