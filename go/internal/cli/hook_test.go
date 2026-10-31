package cli

import (
	"bytes"
	"context"
	"os"
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

func TestHookStatus_pinsStdoutAcrossScenarios(t *testing.T) {
	clearNativeEnv(t)
	t.Setenv("SUM_NOW", "2026-01-02T00:00:00+00:00")
	t.Run("no observer health", func(t *testing.T) {
		home := writeDesignatedHome(t)
		assertHookMatches(t, home, []string{"hook", "status"})
	})
	t.Run("disabled observer with one pending obligation", func(t *testing.T) {
		home := writeDesignatedHome(t)
		writeHookHealth(t, home, `{"schema":2,"enabled":false,"errors":[]}`)
		home = writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{"schema":2,"id":"t-aaaaaaaaaaaa","status":"running","repository":"owner/repo","questions":[{"id":"q1","status":"open","created_at":"2026-01-01T00:00:00+00:00","text":"which way?"}],"evidence":[],"report":null,"attention":[]}`)
		out := assertHookMatches(t, home, []string{"hook", "status"})
		if !strings.Contains(out, "oldest_age_s: 86400\n") {
			t.Fatalf("pinned native observer age must be one day: %s", out)
		}
	})
	t.Run("saved native observer health", func(t *testing.T) {
		home := writeDesignatedHome(t)
		writeHookHealth(t, home, `{"schema":2,"enabled":true,"core_id":"test-core","session":"sum-test","address":"t3","events":5,"errors":[]}`)
		assertHookMatches(t, home, []string{"hook", "status"})
	})
	t.Run("malformed observer health is refused", func(t *testing.T) {
		home := writeDesignatedHome(t)
		writeHookHealth(t, home, `[]`)
		assertHookFailureMatches(t, home, []string{"hook", "status"})
	})
}

func assertHookMatches(t *testing.T, home string, args []string) string {
	t.Helper()
	return assertStdoutGolden(t, home, args, scenarioGolden(t))
}

func assertHookFailureMatches(t *testing.T, home string, args []string) {
	t.Helper()
	assertErrorGolden(t, home, args, scenarioGolden(t))
}

func TestHookEnable_requiresCoordinator(t *testing.T) {
	clearNativeEnv(t)
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
			clearNativeEnv(t)
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
		{name: "valid disable --unlink", args: []string{"hook", "disable", "--unlink"}, domainErr: "HC session"},
		{name: "valid disable --unlink=", args: []string{"hook", "disable", "--unlink=true"}, domainErr: "HC session"},
		{name: "valid disable --unlink with herdr", args: []string{"hook", "disable", "--unlink"}, herdr: true, domainErr: "registration identity mismatch"},
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
		{name: "enable requires pane without herdr", args: []string{"hook", "enable"}, domainErr: "HC session"},
		{name: "disable requires pane without herdr", args: []string{"hook", "disable"}, domainErr: "HC session"},
		{name: "enable requires coordinator", args: []string{"hook", "enable"}, herdr: true, domainErr: "registration identity mismatch"},
		{name: "disable requires coordinator", args: []string{"hook", "disable"}, herdr: true, domainErr: "registration identity mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := writeDesignatedHome(t)
			writeHookHealth(t, home, `{"schema": 1, "enabled": false, "plugin_id": null, "events": 0, "handled": 0, "ignored": 0, "errors": []}`)
			if tc.herdr {
				nativeEnv(t, home)
				t.Setenv("SUM_ROLE", "developer")
			} else {
				clearNativeEnv(t)
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
