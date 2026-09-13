package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestPRUnknownFlagsAreUsageErrorsBeforePR(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "reconcile unknown flag", args: []string{"pr", "reconcile", "t-aaaaaaaaaaaa", "--number", "12", "--unexpected"}, unknown: true},
		{name: "reconcile unknown flag before task", args: []string{"pr", "reconcile", "--unexpected", "t-aaaaaaaaaaaa", "--number", "12"}, unknown: true},
		{name: "reconcile extra positional", args: []string{"pr", "reconcile", "t-aaaaaaaaaaaa", "--number", "12", "extra"}},
		{name: "reconcile extra after dashdash", args: []string{"pr", "reconcile", "t-aaaaaaaaaaaa", "--number", "12", "--", "extra"}},
		{name: "reconcile missing --number value", args: []string{"pr", "reconcile", "t-aaaaaaaaaaaa", "--number"}},
		{name: "reconcile invalid --number", args: []string{"pr", "reconcile", "t-aaaaaaaaaaaa", "--number", "nope"}},
		{name: "evidence unknown flag", args: []string{"pr", "evidence", "t-aaaaaaaaaaaa", "--run", "r1", "--visibility", "public", "--unexpected"}, unknown: true},
		{name: "evidence extra after --run", args: []string{"pr", "evidence", "t-aaaaaaaaaaaa", "--run", "r1", "--visibility", "public", "extra"}},
		{name: "evidence missing --run value", args: []string{"pr", "evidence", "t-aaaaaaaaaaaa", "--run"}},
		{name: "evidence missing --visibility value", args: []string{"pr", "evidence", "t-aaaaaaaaaaaa", "--run", "r1", "--visibility"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home, before := prUsageLab(t)
			stdout, stderr, err := runPRCLI(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertPRUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
			assertPRTaskUnchanged(t, home, before)
		})
	}
}

func TestPRCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		herdr     bool
		usage     bool
		unknown   bool
		domainErr string
	}{
		{name: "valid reconcile --number --repo --replace", args: []string{"pr", "reconcile", "t-aaaaaaaaaaaa", "--number", "12", "--repo", "owner/repo", "--replace"}, domainErr: "Herdr pane"},
		{name: "valid reconcile --number= --repo=", args: []string{"pr", "reconcile", "t-aaaaaaaaaaaa", "--number=12", "--repo=owner/repo", "--replace=true"}, domainErr: "Herdr pane"},
		{name: "valid reconcile with herdr", args: []string{"pr", "reconcile", "t-aaaaaaaaaaaa", "--number", "12"}, herdr: true, domainErr: "registered coordinator"},
		{name: "valid evidence --run --visibility", args: []string{"pr", "evidence", "t-aaaaaaaaaaaa", "--run", "r1", "--visibility", "public"}, domainErr: "Herdr pane"},
		{name: "valid evidence --run= --visibility=", args: []string{"pr", "evidence", "t-aaaaaaaaaaaa", "--run=r1", "--visibility=public"}, domainErr: "Herdr pane"},
		{name: "valid evidence ignored flags", args: []string{"pr", "evidence", "t-aaaaaaaaaaaa", "--run", "r1", "--visibility", "public", "--scenario", "web", "--dry-run", "--timeout", "5"}, domainErr: "Herdr pane"},
		{name: "valid evidence with herdr", args: []string{"pr", "evidence", "t-aaaaaaaaaaaa", "--run", "r1", "--visibility", "public"}, herdr: true, domainErr: "registered coordinator"},
		{name: "pr missing subcommand", args: []string{"pr"}, usage: true},
		{name: "reconcile missing --number", args: []string{"pr", "reconcile", "t-aaaaaaaaaaaa"}, usage: true},
		{name: "reconcile --number 0", args: []string{"pr", "reconcile", "t-aaaaaaaaaaaa", "--number", "0"}, usage: true},
		{name: "reconcile unknown flag", args: []string{"pr", "reconcile", "t-aaaaaaaaaaaa", "--number", "12", "--unexpected"}, usage: true, unknown: true},
		{name: "reconcile extra positional", args: []string{"pr", "reconcile", "t-aaaaaaaaaaaa", "--number", "12", "extra"}, usage: true},
		{name: "reconcile invalid --number", args: []string{"pr", "reconcile", "t-aaaaaaaaaaaa", "--number", "nope"}, usage: true},
		{name: "evidence missing --run", args: []string{"pr", "evidence", "t-aaaaaaaaaaaa", "--visibility", "public"}, usage: true},
		{name: "evidence missing --visibility", args: []string{"pr", "evidence", "t-aaaaaaaaaaaa", "--run", "r1"}, usage: true},
		{name: "evidence unknown flag", args: []string{"pr", "evidence", "t-aaaaaaaaaaaa", "--run", "r1", "--visibility", "public", "--unexpected"}, usage: true, unknown: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, before := prUsageLab(t)
			if tc.herdr {
				herdrEnv(t, home)
			} else {
				clearHerdrEnv(t)
			}
			stdout, stderr, err := runPRCLI(t, home, tc.args...)
			switch {
			case tc.usage:
				assertPRUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				assertPRTaskUnchanged(t, home, before)
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s stderr=%s", stdout, stderr)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.domainErr)
				}
				assertPRTaskUnchanged(t, home, before)
			}
		})
	}
}

func prUsageLab(t *testing.T) (home, before string) {
	t.Helper()
	home = writeDesignatedHome(t)
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "reported", "repository": "owner/repo",
"questions": [], "evidence": [], "report": {"text": "done"}, "notice": null, "attention": [], "brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship"}`)
	before = readNamedTask(t, home, "t-aaaaaaaaaaaa")
	return home, before
}

func runPRCLI(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertPRUsageError(t *testing.T, err error) {
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
		strings.Contains(msg, "invalid argument") ||
		strings.Contains(msg, "command is required") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid pr")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func assertPRTaskUnchanged(t *testing.T, home, before string) {
	t.Helper()
	if got := readNamedTask(t, home, "t-aaaaaaaaaaaa"); got != before {
		t.Fatalf("task.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
	}
}
