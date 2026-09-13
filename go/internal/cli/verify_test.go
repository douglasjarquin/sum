package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const verifyCandidate = "0123456789abcdef0123456789abcdef01234567"

func TestVerifyUnknownFlagsAreUsageErrorsBeforeRun(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "unknown flag", args: []string{"verify", "t-aaaaaaaaaaaa", "--unexpected"}, unknown: true},
		{name: "unknown flag before task", args: []string{"verify", "--unexpected", "t-aaaaaaaaaaaa"}, unknown: true},
		{name: "unknown flag after --execute", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate", verifyCandidate, "--execute", "--unexpected"}, unknown: true},
		{name: "extra positional", args: []string{"verify", "t-aaaaaaaaaaaa", "extra"}},
		{name: "extra after dashdash", args: []string{"verify", "t-aaaaaaaaaaaa", "--", "extra"}},
		{name: "extra after --execute", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate", verifyCandidate, "--execute", "extra"}},
		{name: "missing --candidate value", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate"}},
		{name: "missing --run value", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate", verifyCandidate, "--run"}},
		{name: "missing --result value", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate", verifyCandidate, "--result"}},
		{name: "missing --text value", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate", verifyCandidate, "--text"}},
		{name: "missing --base value", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate", verifyCandidate, "--base"}},
		{name: "missing --file value", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate", verifyCandidate, "--file"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home, before := verifyUsageLab(t)
			stdout, stderr, err := runVerifyCLI(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertVerifyUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
			assertVerifyTaskUnchanged(t, home, before)
		})
	}
}

func TestVerifyCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		herdr     bool
		usage     bool
		unknown   bool
		domainErr string
	}{
		{name: "valid --execute", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate", verifyCandidate, "--execute"}, domainErr: "Herdr pane"},
		{name: "valid --execute after dashdash", args: []string{"verify", "--candidate", verifyCandidate, "--execute", "--", "t-aaaaaaaaaaaa"}, domainErr: "Herdr pane"},
		{name: "valid --execute=", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate=" + verifyCandidate, "--execute=true"}, domainErr: "Herdr pane"},
		{name: "valid --result --text", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate", verifyCandidate, "--result", "pass", "--text", "ok"}, domainErr: "Herdr pane"},
		{name: "valid --result= --text=", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate=" + verifyCandidate, "--result=pass", "--text=ok"}, domainErr: "Herdr pane"},
		{name: "valid --run", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate", verifyCandidate, "--run", "/tmp/run.json"}, domainErr: "Herdr pane"},
		{name: "valid --run=", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate", verifyCandidate, "--run=/tmp/run.json"}, domainErr: "Herdr pane"},
		{name: "valid --base --file", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate", verifyCandidate, "--base", "HEAD", "--file", "notes.md", "--result", "pass"}, domainErr: "Herdr pane"},
		{name: "valid --execute with herdr", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate", verifyCandidate, "--execute"}, herdr: true, domainErr: "registered coordinator"},
		{name: "valid --run --execute conflict with herdr", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate", verifyCandidate, "--run", "/tmp/run.json", "--execute"}, herdr: true, domainErr: "Pass either --run PATH"},
		{name: "unknown flag", args: []string{"verify", "t-aaaaaaaaaaaa", "--unexpected"}, usage: true, unknown: true},
		{name: "unknown flag before task", args: []string{"verify", "--unexpected", "t-aaaaaaaaaaaa"}, usage: true, unknown: true},
		{name: "extra positional", args: []string{"verify", "t-aaaaaaaaaaaa", "extra"}, usage: true},
		{name: "extra after --execute", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate", verifyCandidate, "--execute", "extra"}, usage: true},
		{name: "missing task", args: []string{"verify", "--candidate", verifyCandidate, "--execute"}, usage: true},
		{name: "missing --candidate value", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate"}, usage: true},
		{name: "missing --candidate", args: []string{"verify", "t-aaaaaaaaaaaa"}, usage: true},
		{name: "missing --run value", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate", verifyCandidate, "--run"}, usage: true},
		{name: "missing --text value", args: []string{"verify", "t-aaaaaaaaaaaa", "--candidate", verifyCandidate, "--text"}, usage: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, before := verifyUsageLab(t)
			if tc.herdr {
				herdrEnv(t, home)
			} else {
				clearHerdrEnv(t)
			}
			stdout, stderr, err := runVerifyCLI(t, home, tc.args...)
			switch {
			case tc.usage:
				assertVerifyUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				assertVerifyTaskUnchanged(t, home, before)
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s stderr=%s", stdout, stderr)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.domainErr)
				}
				assertVerifyTaskUnchanged(t, home, before)
			}
		})
	}
}

func verifyUsageLab(t *testing.T) (home, before string) {
	t.Helper()
	home = writeDesignatedHome(t)
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repo",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship", "brief_path": "brief.md"}`)
	before = readVerifyTask(t, home)
	return home, before
}

func runVerifyCLI(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertVerifyUsageError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected usage failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "registered coordinator") || strings.Contains(msg, "Herdr pane") || strings.Contains(msg, "Pass either --run PATH") {
		t.Fatalf("err = %v, want a usage failure", err)
	}
	usage := strings.Contains(msg, "unknown flag") ||
		strings.Contains(msg, "accepts 1 arg") ||
		strings.Contains(msg, "flag needs an argument") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid verify")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func readVerifyTask(t *testing.T, home string) string {
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

func assertVerifyTaskUnchanged(t *testing.T, home, before string) {
	t.Helper()
	if got := readVerifyTask(t, home); got != before {
		t.Fatalf("task.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
	}
}
