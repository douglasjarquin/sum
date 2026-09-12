package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeHookHealth(t *testing.T, home, healthJSON string) {
	t.Helper()
	dir := filepath.Join(home, "hook")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "health.json"), []byte(healthJSON), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeStateJSON(t *testing.T, home, stateJSON string) {
	t.Helper()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(stateJSON), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHookStatus_matchesThePythonReferenceAcrossScenarios(t *testing.T) {
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
	// hook status observes a live Herdr session only when HERDR_ENV=1 and HERDR_PANE_ID are set; blank them so
	// both the Go port and the Python reference take the records-only (ctx=None) path, exactly like an ordinary
	// non-Herdr test invocation. This mirrors the existing per-package convention for isolating Herdr-adjacent env.
	for _, v := range []string{"HERDR_ENV", "HERDR_PANE_ID", "HERDR_SESSION", "HERDR_SOCKET_PATH", "SUM_SESSION"} {
		t.Setenv(v, "")
		os.Unsetenv(v)
	}

	t.Run("no health.json", func(t *testing.T) {
		home := t.TempDir()
		assertHookMatches(t, reference, home, []string{"hook", "status"})
	})

	t.Run("disabled health with one pending obligation", func(t *testing.T) {
		home := t.TempDir()
		writeHookHealth(t, home, `{"schema": 1, "enabled": false, "plugin_id": null, "events": 3, "handled": 2, "ignored": 1,
"errors": [{"at": "2026-01-01T00:00:00+00:00", "stage": "dispatch", "error": "boom", "event": "pane.exited"}],
"last_event": "pane.exited", "last_error": "boom"}`)
		baseSha := "0123456789abcdef0123456789abcdef01234567"
		writeTaskFixture(t, home, "t-aaaaaaaaaaaa", fmt.Sprintf(`{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repoA",
"questions": [{"id": "q1", "status": "open", "created_at": "2026-01-01T00:00:00+00:00", "text": "which way?"}],
"evidence": [], "report": null, "notice": null, "attention": [], "brief": "do thing", "base_sha": %q, "kind": "task"}`, baseSha))
		assertHookMatches(t, reference, home, []string{"hook", "status"})
	})

	t.Run("enabled with plugin_id but no instance identity yet", func(t *testing.T) {
		home := t.TempDir()
		writeHookHealth(t, home, `{"schema": 1, "enabled": true, "plugin_id": "sum.returns.deadbeefcafe", "events": 5, "handled": 5, "ignored": 0,
"errors": [], "last_event": "pane.agent_detected", "last_error": null, "manifest_sha256": "abc123",
"manifest_path": "/tmp/plugin/herdr-plugin.toml", "linked_at": "2026-01-01T00:00:00+00:00"}`)
		assertHookMatches(t, reference, home, []string{"hook", "status"})
	})

	t.Run("enabled with plugin_id, instance present, mismatched manifest hash", func(t *testing.T) {
		home := t.TempDir()
		writeStateJSON(t, home, `{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00", "instance": "deadbeefcafe0123"}`)
		writeHookHealth(t, home, `{"schema": 1, "enabled": true, "plugin_id": "sum.returns.deadbeefcafe", "events": 5, "handled": 5, "ignored": 0,
"errors": [], "last_event": "pane.agent_detected", "last_error": null, "manifest_sha256": "not-the-real-hash",
"manifest_path": "/tmp/plugin/herdr-plugin.toml", "linked_at": "2026-01-01T00:00:00+00:00"}`)
		assertHookMatches(t, reference, home, []string{"hook", "status"})
	})

	t.Run("malformed health.json schema is a command-level failure", func(t *testing.T) {
		home := t.TempDir()
		writeHookHealth(t, home, `{"schema": 2, "enabled": true}`)
		assertHookFailureMatches(t, reference, home, []string{"hook", "status"})
	})
}

func assertHookMatches(t *testing.T, reference, home string, args []string) {
	t.Helper()
	fullArgs := append([]string{"--home", home}, args...)
	want, err := exec.Command(reference, fullArgs...).Output()
	if err != nil {
		t.Fatalf("python reference failed: %v", err)
	}
	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs(fullArgs)
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("go command failed: %v (stderr=%s)", err, stderr.String())
	}
	if stdout.String() != string(want) {
		t.Fatalf("go output =\n%s\nwant (python reference)\n%s", stdout.String(), want)
	}
}

func assertHookFailureMatches(t *testing.T, reference, home string, args []string) {
	t.Helper()
	fullArgs := append([]string{"--home", home}, args...)
	cmd := exec.Command(reference, fullArgs...)
	var pyStderr bytes.Buffer
	cmd.Stderr = &pyStderr
	if err := cmd.Run(); err == nil {
		t.Fatalf("expected python reference to fail, got success with stderr=%s", pyStderr.String())
	}
	var pyPayload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(pyStderr.Bytes(), &pyPayload); err != nil {
		t.Fatalf("python stderr is not the expected error JSON: %v (stderr=%s)", err, pyStderr.String())
	}

	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs(fullArgs)
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected go command to fail, got success with stdout=%s", stdout.String())
	}
	if err.Error() != pyPayload.Error {
		t.Fatalf("go error = %q, want (python reference) %q", err.Error(), pyPayload.Error)
	}
}

func TestHookEnable_requiresCoordinator(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRoot("", &bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"--home", home, "hook", "enable"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected coordinator requirement")
	}
}
