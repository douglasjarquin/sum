package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Safe against the live installation: the non-designated branch of `init`
// never writes (no store.lock, no atomic_json) — same read-only observation
// `./bin/sumctl init` already performs on every ordinary invocation here.
func TestInit_matchesThePythonReferenceInThisDevCheckout(t *testing.T) {
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
	if os.Getenv("HERDR_ENV") != "1" || os.Getenv("HERDR_PANE_ID") == "" {
		t.Skip("not running inside a live Herdr pane")
	}

	home := filepath.Join(repoRoot, ".sum")
	args := []string{"--home", home, "init"}

	want, err := exec.Command(reference, args...).Output()
	if err != nil {
		t.Fatalf("python reference failed: %v", err)
	}

	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs(args)
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("go command failed: %v (stderr=%s)", err, stderr.String())
	}

	if normalizeTimestamps(stdout.String()) != normalizeTimestamps(string(want)) {
		t.Fatalf("go output =\n%s\nwant (python reference)\n%s", stdout.String(), want)
	}
}

func TestInit_rejectsUnrecognizedRoleNatively(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	root := NewRoot("", &stdout, &stderr)
	root.SetArgs([]string{"--home", home, "init", "--role", "bogus"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected invalid init arguments")
	}
	if err.Error() != "invalid init arguments" {
		t.Fatalf("err = %v", err)
	}
}
