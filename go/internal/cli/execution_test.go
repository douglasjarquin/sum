package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutionUnknownFlagsAreUsageErrorsBeforeExecution(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "show unknown flag", args: []string{"execution", "show", "t-aaaaaaaaaaaa", "--unexpected"}, unknown: true},
		{name: "show unknown flag before task", args: []string{"execution", "show", "--unexpected", "t-aaaaaaaaaaaa"}, unknown: true},
		{name: "show extra positional", args: []string{"execution", "show", "t-aaaaaaaaaaaa", "extra"}},
		{name: "show extra after dashdash", args: []string{"execution", "show", "t-aaaaaaaaaaaa", "--", "extra"}},
		{name: "park unknown flag", args: []string{"execution", "park", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--unexpected"}, unknown: true},
		{name: "park unknown flag before task", args: []string{"execution", "park", "--unexpected", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa"}, unknown: true},
		{name: "park extra after --attempt", args: []string{"execution", "park", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "extra"}},
		{name: "park missing --attempt value", args: []string{"execution", "park", "t-aaaaaaaaaaaa", "--attempt"}},
		{name: "resume unknown flag", args: []string{"execution", "resume", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--unexpected"}, unknown: true},
		{name: "resume extra positional", args: []string{"execution", "resume", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "extra"}},
		{name: "resume missing --attempt value", args: []string{"execution", "resume", "t-aaaaaaaaaaaa", "--attempt"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home, before := executionUsageLab(t)
			stdout, stderr, err := runExecutionCLI(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertExecutionUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
			assertExecutionTaskUnchanged(t, home, before)
		})
	}
}

func TestExecutionCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		herdr     bool
		usage     bool
		unknown   bool
		ok        bool
		domainErr string
	}{
		{name: "valid show", args: []string{"execution", "show", "t-aaaaaaaaaaaa"}, ok: true},
		{name: "valid show after dashdash", args: []string{"execution", "show", "--", "t-aaaaaaaaaaaa"}, ok: true},
		{name: "valid park --attempt", args: []string{"execution", "park", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa"}, domainErr: "Herdr pane"},
		{name: "valid park --attempt=", args: []string{"execution", "park", "t-aaaaaaaaaaaa", "--attempt=x-aaaaaaaaaaaa"}, domainErr: "Herdr pane"},
		{name: "valid park with herdr", args: []string{"execution", "park", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa"}, herdr: true, domainErr: "registered coordinator"},
		{name: "valid resume --attempt", args: []string{"execution", "resume", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa"}, domainErr: "Herdr pane"},
		{name: "valid resume --attempt=", args: []string{"execution", "resume", "t-aaaaaaaaaaaa", "--attempt=x-aaaaaaaaaaaa"}, domainErr: "Herdr pane"},
		{name: "valid resume with herdr", args: []string{"execution", "resume", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa"}, herdr: true, domainErr: "registered coordinator"},
		{name: "execution missing subcommand", args: []string{"execution"}, usage: true},
		{name: "show extra positional", args: []string{"execution", "show", "t-aaaaaaaaaaaa", "extra"}, usage: true},
		{name: "show unknown flag", args: []string{"execution", "show", "t-aaaaaaaaaaaa", "--unexpected"}, usage: true, unknown: true},
		{name: "park missing --attempt", args: []string{"execution", "park", "t-aaaaaaaaaaaa"}, usage: true},
		{name: "park missing --attempt value", args: []string{"execution", "park", "t-aaaaaaaaaaaa", "--attempt"}, usage: true},
		{name: "park unknown flag", args: []string{"execution", "park", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--unexpected"}, usage: true, unknown: true},
		{name: "resume extra after --attempt", args: []string{"execution", "resume", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "extra"}, usage: true},
		{name: "resume unknown flag", args: []string{"execution", "resume", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--unexpected"}, usage: true, unknown: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, before := executionUsageLab(t)
			if tc.herdr {
				herdrEnv(t, home)
			} else {
				clearHerdrEnv(t)
			}
			stdout, stderr, err := runExecutionCLI(t, home, tc.args...)
			switch {
			case tc.usage:
				assertExecutionUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				assertExecutionTaskUnchanged(t, home, before)
			case tc.ok:
				if err != nil {
					t.Fatalf("expected success, err=%v stdout=%s stderr=%s", err, stdout, stderr)
				}
				if !strings.Contains(stdout, "t-aaaaaaaaaaaa") {
					t.Fatalf("stdout = %s, want task id", stdout)
				}
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s stderr=%s", stdout, stderr)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.domainErr)
				}
				assertExecutionTaskUnchanged(t, home, before)
			}
		})
	}
}

func executionUsageLab(t *testing.T) (home, before string) {
	t.Helper()
	home = writeDesignatedHome(t)
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repo",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship", "brief_path": "brief.md"}`)
	before = readNamedTask(t, home, "t-aaaaaaaaaaaa")
	return home, before
}

func runExecutionCLI(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertExecutionUsageError(t *testing.T, err error) {
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
		strings.Contains(msg, "accepts 0 arg") ||
		strings.Contains(msg, "flag needs an argument") ||
		strings.Contains(msg, "required flag") ||
		strings.Contains(msg, "command is required") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid execution")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func readNamedTask(t *testing.T, home, taskID string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "tasks", taskID, "task.json"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertExecutionTaskUnchanged(t *testing.T, home, before string) {
	t.Helper()
	if got := readNamedTask(t, home, "t-aaaaaaaaaaaa"); got != before {
		t.Fatalf("task.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
	}
}
