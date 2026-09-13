package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	want := decodeCLIError(t, pyStderr.Bytes())
	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs(fullArgs)
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected go command to fail, got success with stdout=%s", stdout.String())
	}
	if err.Error() != want {
		t.Fatalf("go error = %q, want (python reference) %q", err.Error(), want)
	}
}

func TestHookEnable_requiresCoordinator(t *testing.T) {
	clearHerdrEnv(t)
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

func TestHookUnknownFlagsAreUsageErrorsBeforeEnableDisable(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "enable unknown flag", args: []string{"hook", "enable", "--unexpected"}, unknown: true},
		{name: "enable extra positional", args: []string{"hook", "enable", "extra"}},
		{name: "enable extra after dashdash", args: []string{"hook", "enable", "--", "extra"}},
		{name: "enable --unlink", args: []string{"hook", "enable", "--unlink"}, unknown: true},
		{name: "disable unknown flag", args: []string{"hook", "disable", "--unexpected"}, unknown: true},
		{name: "disable extra positional", args: []string{"hook", "disable", "extra"}},
		{name: "disable extra after --unlink", args: []string{"hook", "disable", "--unlink", "extra"}},
		{name: "disable extra after dashdash", args: []string{"hook", "disable", "--", "extra"}},
		{name: "status unknown flag", args: []string{"hook", "status", "--unexpected"}, unknown: true},
		{name: "status extra positional", args: []string{"hook", "status", "extra"}},
		{name: "event unknown flag", args: []string{"hook", "event", "--unexpected"}, unknown: true},
		{name: "event extra positional", args: []string{"hook", "event", "extra"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home := writeDesignatedHome(t)
			writeHookHealth(t, home, `{"schema": 1, "enabled": false, "plugin_id": null, "events": 0, "handled": 0, "ignored": 0, "errors": []}`)
			before := readHookHealth(t, home)
			stdout, stderr, err := runHook(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertHookUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
			assertHookHealthUnchanged(t, home, before)
		})
	}
}

func TestHookCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		herdr     bool
		success   bool
		usage     bool
		unknown   bool
		domainErr string
	}{
		{name: "valid status", args: []string{"hook", "status"}, success: true},
		{name: "valid status after dashdash", args: []string{"hook", "status", "--"}, success: true},
		{name: "valid disable --unlink", args: []string{"hook", "disable", "--unlink"}, domainErr: "Herdr pane"},
		{name: "valid disable --unlink=", args: []string{"hook", "disable", "--unlink=true"}, domainErr: "Herdr pane"},
		{name: "valid disable --unlink with herdr", args: []string{"hook", "disable", "--unlink"}, herdr: true, domainErr: "registered coordinator"},
		{name: "hook missing subcommand", args: []string{"hook"}, usage: true},
		{name: "status extra positional", args: []string{"hook", "status", "extra"}, usage: true},
		{name: "status unknown flag", args: []string{"hook", "status", "--unexpected"}, usage: true, unknown: true},
		{name: "enable extra positional", args: []string{"hook", "enable", "extra"}, usage: true},
		{name: "enable unknown flag", args: []string{"hook", "enable", "--unexpected"}, usage: true, unknown: true},
		{name: "enable extra after dashdash", args: []string{"hook", "enable", "--", "extra"}, usage: true},
		{name: "disable unknown flag", args: []string{"hook", "disable", "--unexpected"}, usage: true, unknown: true},
		{name: "disable extra after --unlink", args: []string{"hook", "disable", "--unlink", "extra"}, usage: true},
		{name: "event extra positional", args: []string{"hook", "event", "extra"}, usage: true},
		{name: "event unknown flag", args: []string{"hook", "event", "--unexpected"}, usage: true, unknown: true},
		{name: "enable requires pane without herdr", args: []string{"hook", "enable"}, domainErr: "Herdr pane"},
		{name: "disable requires pane without herdr", args: []string{"hook", "disable"}, domainErr: "Herdr pane"},
		{name: "enable requires coordinator", args: []string{"hook", "enable"}, herdr: true, domainErr: "registered coordinator"},
		{name: "disable requires coordinator", args: []string{"hook", "disable"}, herdr: true, domainErr: "registered coordinator"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := writeDesignatedHome(t)
			writeHookHealth(t, home, `{"schema": 1, "enabled": false, "plugin_id": null, "events": 0, "handled": 0, "ignored": 0, "errors": []}`)
			if tc.herdr {
				herdrEnv(t, home)
				t.Setenv("HERDR_PANE_ID", "w-other:p1")
			} else {
				clearHerdrEnv(t)
			}
			before := readHookHealth(t, home)
			stdout, stderr, err := runHook(t, home, tc.args...)
			switch {
			case tc.success:
				if err != nil {
					t.Fatalf("err = %v stderr=%s stdout=%s", err, stderr, stdout)
				}
				if stdout == "" {
					t.Fatal("expected status output")
				}
			case tc.usage:
				assertHookUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				assertHookHealthUnchanged(t, home, before)
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s", stdout)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.domainErr)
				}
				assertHookHealthUnchanged(t, home, before)
			}
		})
	}
}

func runHook(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertHookUsageError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected usage failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "registered coordinator") || strings.Contains(msg, "Herdr pane") {
		t.Fatalf("err = %v, want a usage failure", err)
	}
	usage := strings.Contains(msg, "unknown flag") ||
		strings.Contains(msg, "accepts 0 arg") ||
		strings.Contains(msg, "command is required") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid hook")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func readHookHealth(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "hook", "health.json"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertHookHealthUnchanged(t *testing.T, home, before string) {
	t.Helper()
	if got := readHookHealth(t, home); got != before {
		t.Fatalf("hook/health.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
	}
}
