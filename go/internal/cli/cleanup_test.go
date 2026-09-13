package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanupUnknownFlagsAreUsageErrorsBeforeCleanup(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "unknown flag", args: []string{"cleanup", "t-aaaaaaaaaaaa", "--unexpected"}, unknown: true},
		{name: "unknown flag before task", args: []string{"cleanup", "--unexpected", "t-aaaaaaaaaaaa"}, unknown: true},
		{name: "unknown flag after --apply", args: []string{"cleanup", "t-aaaaaaaaaaaa", "--apply", "--unexpected"}, unknown: true},
		{name: "extra positional", args: []string{"cleanup", "t-aaaaaaaaaaaa", "extra"}},
		{name: "extra after dashdash", args: []string{"cleanup", "t-aaaaaaaaaaaa", "--", "extra"}},
		{name: "extra after --apply", args: []string{"cleanup", "t-aaaaaaaaaaaa", "--apply", "extra"}},
		{name: "missing --number value", args: []string{"cleanup", "t-aaaaaaaaaaaa", "--number"}},
		{name: "invalid --number", args: []string{"cleanup", "t-aaaaaaaaaaaa", "--number", "nope"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home, before := cleanupUsageLab(t)
			stdout, stderr, err := runCleanupCLI(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertCleanupUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
			assertCleanupTaskUnchanged(t, home, before)
		})
	}
}

func TestCleanupCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		herdr     bool
		usage     bool
		unknown   bool
		domainErr string
	}{
		{name: "valid task", args: []string{"cleanup", "t-aaaaaaaaaaaa"}, domainErr: "Herdr pane"},
		{name: "valid task after dashdash", args: []string{"cleanup", "--", "t-aaaaaaaaaaaa"}, domainErr: "Herdr pane"},
		{name: "valid --apply --reviewer-only --number", args: []string{"cleanup", "t-aaaaaaaaaaaa", "--apply", "--reviewer-only", "--number", "12"}, domainErr: "Herdr pane"},
		{name: "valid --apply= --number=", args: []string{"cleanup", "t-aaaaaaaaaaaa", "--apply=true", "--reviewer-only=true", "--number=12"}, domainErr: "Herdr pane"},
		{name: "valid with herdr", args: []string{"cleanup", "t-aaaaaaaaaaaa", "--apply"}, herdr: true, domainErr: "registered coordinator"},
		{name: "unknown flag", args: []string{"cleanup", "t-aaaaaaaaaaaa", "--unexpected"}, usage: true, unknown: true},
		{name: "unknown flag before task", args: []string{"cleanup", "--unexpected", "t-aaaaaaaaaaaa"}, usage: true, unknown: true},
		{name: "extra positional", args: []string{"cleanup", "t-aaaaaaaaaaaa", "extra"}, usage: true},
		{name: "extra after --apply", args: []string{"cleanup", "t-aaaaaaaaaaaa", "--apply", "extra"}, usage: true},
		{name: "missing task", args: []string{"cleanup"}, usage: true},
		{name: "missing --number value", args: []string{"cleanup", "t-aaaaaaaaaaaa", "--number"}, usage: true},
		{name: "invalid --number", args: []string{"cleanup", "t-aaaaaaaaaaaa", "--number", "nope"}, usage: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, before := cleanupUsageLab(t)
			if tc.herdr {
				herdrEnv(t, home)
			} else {
				clearHerdrEnv(t)
			}
			stdout, stderr, err := runCleanupCLI(t, home, tc.args...)
			switch {
			case tc.usage:
				assertCleanupUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				assertCleanupTaskUnchanged(t, home, before)
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s stderr=%s", stdout, stderr)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.domainErr)
				}
				assertCleanupTaskUnchanged(t, home, before)
			}
		})
	}
}

func cleanupUsageLab(t *testing.T) (home, before string) {
	t.Helper()
	home = writeDesignatedHome(t)
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repo",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship", "brief_path": "brief.md"}`)
	before = readCleanupTask(t, home)
	return home, before
}

func runCleanupCLI(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertCleanupUsageError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected usage failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "registered coordinator") || strings.Contains(msg, "Herdr pane") {
		t.Fatalf("err = %v, want a usage failure", err)
	}
	usage := strings.Contains(msg, "unknown flag") ||
		strings.Contains(msg, "accepts 1 arg") ||
		strings.Contains(msg, "flag needs an argument") ||
		strings.Contains(msg, "invalid argument") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid cleanup")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func readCleanupTask(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "tasks", "t-aaaaaaaaaaaa", "task.json"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertCleanupTaskUnchanged(t *testing.T, home, before string) {
	t.Helper()
	if got := readCleanupTask(t, home); got != before {
		t.Fatalf("task.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
	}
}
