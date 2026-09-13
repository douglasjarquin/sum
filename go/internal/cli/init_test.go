package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
