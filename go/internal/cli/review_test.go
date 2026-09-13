package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewUnknownFlagsAreUsageErrorsBeforeReview(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "unknown flag", args: []string{"review", "t-aaaaaaaaaaaa", "--verdict", "comment", "--text", "looks fine", "--unexpected"}, unknown: true},
		{name: "unknown flag before task", args: []string{"review", "--unexpected", "t-aaaaaaaaaaaa", "--verdict", "comment", "--text", "looks fine"}, unknown: true},
		{name: "unknown flag after --verdict", args: []string{"review", "t-aaaaaaaaaaaa", "--verdict", "comment", "--unexpected", "--text", "looks fine"}, unknown: true},
		{name: "extra positional", args: []string{"review", "t-aaaaaaaaaaaa", "--verdict", "comment", "--text", "looks fine", "extra"}},
		{name: "extra after dashdash", args: []string{"review", "t-aaaaaaaaaaaa", "--verdict", "comment", "--text", "looks fine", "--", "extra"}},
		{name: "missing --verdict value", args: []string{"review", "t-aaaaaaaaaaaa", "--verdict"}},
		{name: "missing --text value", args: []string{"review", "t-aaaaaaaaaaaa", "--verdict", "comment", "--text"}},
		{name: "--text and --file", args: []string{"review", "t-aaaaaaaaaaaa", "--verdict", "comment", "--text", "looks fine", "--file", "note.md"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home, before := reviewUsageLab(t)
			stdout, stderr, err := runReviewCLI(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertReviewUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
			assertReviewTaskUnchanged(t, home, before)
		})
	}
}

func TestReviewCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		usage     bool
		unknown   bool
		ok        bool
		domainErr string
	}{
		{name: "valid --verdict --text", args: []string{"review", "t-aaaaaaaaaaaa", "--verdict", "comment", "--text", "looks fine"}, ok: true},
		{name: "valid after dashdash", args: []string{"review", "--verdict", "comment", "--text", "looks fine", "--", "t-aaaaaaaaaaaa"}, ok: true},
		{name: "valid --verdict= --text= --policy-reviewed", args: []string{"review", "t-aaaaaaaaaaaa", "--verdict=comment", "--text=looks fine", "--policy-reviewed"}, ok: true},
		{name: "valid --tool", args: []string{"review", "t-aaaaaaaaaaaa", "--verdict", "comment", "--tool", "made", "--text", "looks fine"}, ok: true},
		{name: "unknown flag", args: []string{"review", "t-aaaaaaaaaaaa", "--verdict", "comment", "--text", "looks fine", "--unexpected"}, usage: true, unknown: true},
		{name: "extra positional", args: []string{"review", "t-aaaaaaaaaaaa", "--verdict", "comment", "--text", "looks fine", "extra"}, usage: true},
		{name: "missing task", args: []string{"review", "--verdict", "comment", "--text", "looks fine"}, usage: true},
		{name: "missing --verdict", args: []string{"review", "t-aaaaaaaaaaaa", "--text", "looks fine"}, usage: true},
		{name: "missing --text and --file", args: []string{"review", "t-aaaaaaaaaaaa", "--verdict", "comment"}, usage: true},
		{name: "--text and --file", args: []string{"review", "t-aaaaaaaaaaaa", "--verdict", "comment", "--text", "looks fine", "--file", "note.md"}, usage: true},
		{name: "missing --verdict value", args: []string{"review", "t-aaaaaaaaaaaa", "--verdict"}, usage: true},
		{name: "invalid --candidate", args: []string{"review", "t-aaaaaaaaaaaa", "--verdict", "comment", "--candidate", "not-a-sha", "--text", "looks fine"}, domainErr: "--candidate must be a full 40-hex commit SHA"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home, before := reviewUsageLab(t)
			stdout, stderr, err := runReviewCLI(t, home, tc.args...)
			switch {
			case tc.usage:
				assertReviewUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				assertReviewTaskUnchanged(t, home, before)
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
				assertReviewTaskUnchanged(t, home, before)
			}
		})
	}
}

func reviewUsageLab(t *testing.T) (home, before string) {
	t.Helper()
	home = writeDesignatedHome(t)
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "reported", "repository": "owner/repo",
"questions": [], "evidence": [], "report": {"text": "done"}, "notice": null, "attention": [], "brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship"}`)
	before = readNamedTask(t, home, "t-aaaaaaaaaaaa")
	return home, before
}

func runReviewCLI(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertReviewUsageError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected usage failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "registered coordinator") || strings.Contains(msg, "Herdr pane") || strings.Contains(msg, "--candidate must") {
		t.Fatalf("err = %v, want a usage failure", err)
	}
	usage := strings.Contains(msg, "unknown flag") ||
		strings.Contains(msg, "accepts 1 arg") ||
		strings.Contains(msg, "flag needs an argument") ||
		strings.Contains(msg, "required flag") ||
		strings.Contains(msg, "if any flags in the group") ||
		strings.Contains(msg, "at least one of the flags") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid review")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func assertReviewTaskUnchanged(t *testing.T, home, before string) {
	t.Helper()
	if got := readNamedTask(t, home, "t-aaaaaaaaaaaa"); got != before {
		t.Fatalf("task.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
	}
}
