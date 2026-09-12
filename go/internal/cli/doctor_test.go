package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

var isoTimestamp = regexp.MustCompile(`"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\+00:00"`)

func normalizeTimestamps(s string) string {
	return isoTimestamp.ReplaceAllString(s, `"<at>"`)
}

// Safe against the live installation: doctor is documented "never binds" and
// this port only reads (tool lookups, a herdr pane-get, file existence
// checks) — the same observation `./bin/sumctl doctor` already performs here.
func TestDoctor_matchesThePythonReferenceInThisDevCheckout(t *testing.T) {
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
	args := []string{"--home", home, "doctor"}

	pythonCmd := exec.Command(reference, args...)
	want, pythonErr := pythonCmd.Output()
	pythonExit := 0
	if pythonErr != nil {
		if exitErr, ok := pythonErr.(*exec.ExitError); ok {
			pythonExit = exitErr.ExitCode()
		} else {
			t.Fatalf("python reference failed: %v", pythonErr)
		}
	}

	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs(args)
	goExit := 0
	if err := root.ExecuteContext(context.Background()); err != nil {
		if exitErr, ok := err.(*ExitError); ok {
			goExit = exitErr.Code
		} else {
			t.Fatalf("go command failed: %v (stderr=%s)", err, stderr.String())
		}
	}

	gotNormalized := normalizeTimestamps(stdout.String())
	wantNormalized := normalizeTimestamps(string(want))
	if gotNormalized != wantNormalized {
		t.Fatalf("go output =\n%s\nwant (python reference)\n%s", stdout.String(), want)
	}
	if goExit != pythonExit {
		t.Fatalf("go exit = %d, want (python reference) %d", goExit, pythonExit)
	}
}

func TestDoctor_extraArgsAreGoErrors(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRoot("", &bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"--home", home, "doctor", "--unexpected"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected unrecognized arguments")
	}
}
