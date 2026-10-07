package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Safe against the live installation: the non-designated branch of `init`
// never writes (no store.lock, no atomic_json) — same read-only observation
// `./bin/sumctl init` already performs on every ordinary invocation here.
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

func TestInitUnknownFlagsAreUsageErrorsBeforeInit(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "unknown flag", args: []string{"init", "--unexpected"}, unknown: true},
		{name: "unknown flag before role", args: []string{"init", "--unexpected", "--role", "developer"}, unknown: true},
		{name: "extra positional", args: []string{"init", "extra"}},
		{name: "extra after dashdash", args: []string{"init", "--", "extra"}},
		{name: "extra after --reclaim", args: []string{"init", "--reclaim", "extra"}},
		{name: "missing --role value", args: []string{"init", "--role"}},
		{name: "missing --task value", args: []string{"init", "--task"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home := writeDesignatedHome(t)
			before := readContextFile(t, home)
			stdout, stderr, err := runInit(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertInitUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
			if got := readContextFile(t, home); got != before {
				t.Fatalf("context.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
			}
		})
	}
}

func TestInitCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		herdr     bool
		success   bool
		usage     bool
		unknown   bool
		invalid   bool
		domainErr string
	}{
		{name: "valid init", args: []string{"init"}, herdr: true, success: true},
		{name: "valid init after dashdash", args: []string{"init", "--"}, herdr: true, success: true},
		{name: "valid init --role coordinator", args: []string{"init", "--role", "coordinator"}, herdr: true, success: true},
		{name: "valid init --role=", args: []string{"init", "--role=coordinator"}, herdr: true, success: true},
		{name: "valid init --reclaim", args: []string{"init", "--reclaim"}, herdr: true, success: true},
		{name: "valid init --reclaim=", args: []string{"init", "--reclaim=true"}, herdr: true, success: true},
		{name: "unknown flag", args: []string{"init", "--unexpected"}, usage: true, unknown: true},
		{name: "extra positional", args: []string{"init", "extra"}, usage: true},
		{name: "extra after --reclaim", args: []string{"init", "--reclaim", "extra"}, usage: true},
		{name: "unrecognized role", args: []string{"init", "--role", "bogus"}, invalid: true},
		{name: "missing --role value", args: []string{"init", "--role"}, usage: true},
		{name: "requires pane without herdr", args: []string{"init"}, domainErr: "Herdr pane"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := writeDesignatedHome(t)
			if tc.herdr {
				herdrEnv(t, home)
			} else {
				clearHerdrEnv(t)
			}
			before := readContextFile(t, home)
			stdout, stderr, err := runInit(t, home, tc.args...)
			switch {
			case tc.success:
				if err != nil {
					t.Fatalf("err = %v stderr=%s stdout=%s", err, stderr, stdout)
				}
				if stdout == "" {
					t.Fatal("expected init output")
				}
			case tc.invalid:
				if err == nil {
					t.Fatalf("expected invalid init arguments, stdout=%s", stdout)
				}
				if err.Error() != "invalid init arguments" {
					t.Fatalf("err = %v, want invalid init arguments", err)
				}
				if got := readContextFile(t, home); got != before {
					t.Fatalf("context.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
				}
			case tc.usage:
				assertInitUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				if got := readContextFile(t, home); got != before {
					t.Fatalf("context.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
				}
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s", stdout)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.domainErr)
				}
				if got := readContextFile(t, home); got != before {
					t.Fatalf("context.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
				}
			}
		})
	}
}

func runInit(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertInitUsageError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected usage failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "registered coordinator") || strings.Contains(msg, "Herdr pane") {
		t.Fatalf("err = %v, want a usage failure", err)
	}
	usage := strings.Contains(msg, "unknown flag") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "accepts 0 arg") ||
		strings.Contains(msg, "flag needs an argument") ||
		strings.Contains(msg, "invalid init arguments")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func readContextFile(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "context.json"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A verified coordinator adopts the wake protocol (#240a) at init: a fresh claim records it, and an owner record an
// older helper wrote gains it on the next verified init. Until then delivery to that coordinator is legacy.
func TestInit_coordinatorAdoptsTheWakeProtocol(t *testing.T) {
	home := legacyHome(t, "dev")
	herdrEnv(t, home)
	onHost(t, thisHostRaw, "dev")
	if owner := readJSON(t, filepath.Join(home, "context.json")); owner["wake_protocol"] != nil {
		t.Fatalf("legacy owner already carries wake_protocol: %v", owner)
	}
	mustRole(t, home, "coordinator")
	if owner := readJSON(t, filepath.Join(home, "context.json")); fmt.Sprint(owner["wake_protocol"]) != "1" {
		t.Fatalf("after a verified init, owner = %v, want wake_protocol 1", owner)
	}
}

func TestInit_enablesNativeEventsByDefaultAndPreservesDisableChoice(t *testing.T) {
	home := writeDesignatedHome(t)
	configureNativeEventsForTest(t, home, true)
	herdrEnv(t, home)
	if _, err := runCLI(t, home, "init"); err != nil {
		t.Fatalf("first init: %v", err)
	}
	status, err := runCLI(t, home, "hook", "status")
	if err != nil || decodeObject(t, status)["enabled"] != true {
		t.Fatalf("hook status after first init = %s, err = %v", status, err)
	}
	if _, err := runCLI(t, home, "hook", "disable"); err != nil {
		t.Fatalf("disable hook: %v", err)
	}
	if _, err := runCLI(t, home, "init"); err != nil {
		t.Fatalf("init after disable: %v", err)
	}
	status, err = runCLI(t, home, "hook", "status")
	view := decodeObject(t, status)
	if err != nil || view["enabled"] != false {
		t.Fatalf("hook status after opted-out init = %s, err = %v", status, err)
	}
}

func TestInit_migratesOnlyLegacyHealthThatShowsTheHookOff(t *testing.T) {
	tests := []struct {
		name       string
		enabled    bool
		disabledAt string
		linkedAt   string
		wantEnable bool
	}{
		{name: "legacy disabled", disabledAt: "2026-01-01T00:00:00Z", wantEnable: false},
		{name: "disabled then re-enabled", enabled: true, disabledAt: "2026-01-01T00:00:00Z", linkedAt: "2026-02-01T00:00:00Z", wantEnable: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := writeDesignatedHome(t)
			configureNativeEventsForTest(t, home, true)
			herdrEnv(t, home)
			if _, err := runCLI(t, home, "init"); err != nil {
				t.Fatalf("initial init: %v", err)
			}
			assertNativeEventsEnabledForTest(t, home)
			healthPath := filepath.Join(home, "hook", "health.json")
			health := readJSON(t, healthPath)
			health["enabled"] = tt.enabled
			health["disabled_at"] = tt.disabledAt
			health["linked_at"] = tt.linkedAt
			writeJSONFile(t, healthPath, health)
			if err := os.Remove(filepath.Join(home, "settings.json")); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}

			out, err := runCLI(t, home, "init")
			if err != nil {
				t.Fatalf("init: %v\n%s", err, out)
			}
			activation := decodeObject(t, out)["hook_activation"].(map[string]any)
			if tt.wantEnable && activation["skipped"] != true {
				t.Fatalf("activation = %v, want current enabled hook skipped", activation)
			}
			status, err := runCLI(t, home, "hook", "status")
			if err != nil || decodeObject(t, status)["enabled"] != tt.wantEnable {
				t.Fatalf("hook status = %s, want enabled %v; err = %v", status, tt.wantEnable, err)
			}
		})
	}
}

func TestInit_nativeEventEnableFailureIsDegradedAndFailOpen(t *testing.T) {
	home := writeDesignatedHome(t)
	configureNativeEventsForTest(t, home, true)
	herdrEnv(t, home)
	wrapper := filepath.Join(t.TempDir(), "herdr-failing-plugin-link")
	script := "#!/bin/sh\nif [ \"$3\" = plugin ] && [ \"$4\" = link ]; then echo registry unavailable >&2; exit 1; fi\nexec python3 \"" + os.Getenv("SUM_HERDR_BIN") + "\" \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_HERDR_BIN", wrapper)
	out, err := runCLI(t, home, "init")
	if err != nil {
		t.Fatalf("init failed when automatic hook enable failed: %v\n%s", err, out)
	}
	view := decodeObject(t, out)
	activation := view["hook_activation"].(map[string]any)
	if activation["degraded"] != true || !strings.Contains(fmt.Sprint(activation["reason"]), "registry unavailable") {
		t.Fatalf("hook activation = %v, want degraded reason", activation)
	}
	status, statusErr := runCLI(t, home, "hook", "status")
	statusView := decodeObject(t, status)
	if statusErr != nil || statusView["degraded"] != true || !strings.Contains(fmt.Sprint(statusView["reason"]), "registry unavailable") {
		t.Fatalf("hook status = %s, err = %v; want degraded with registry unavailable", status, statusErr)
	}
}
