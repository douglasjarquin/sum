package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefreshUnknownFlagsAreUsageErrorsBeforeRefresh(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "status unknown flag", args: []string{"refresh", "status", "--unexpected"}, unknown: true},
		{name: "status extra positional", args: []string{"refresh", "status", "extra"}},
		{name: "status extra after dashdash", args: []string{"refresh", "status", "--", "extra"}},
		{name: "status extra after --task", args: []string{"refresh", "status", "--task", "t-aaaaaaaaaaaa", "extra"}},
		{name: "status missing --task value", args: []string{"refresh", "status", "--task"}},
		{name: "request unknown flag", args: []string{"refresh", "request", "--unexpected"}, unknown: true},
		{name: "request unknown flag after --coordinator", args: []string{"refresh", "request", "--coordinator", "--unexpected"}, unknown: true},
		{name: "request extra positional", args: []string{"refresh", "request", "extra"}},
		{name: "request extra after --task", args: []string{"refresh", "request", "--task", "t-aaaaaaaaaaaa", "extra"}},
		{name: "request missing --task value", args: []string{"refresh", "request", "--task"}},
		{name: "adopt unknown flag", args: []string{"refresh", "adopt", "--coordinator", "r1", "--unexpected"}, unknown: true},
		{name: "adopt extra positional", args: []string{"refresh", "adopt", "--coordinator", "r1", "extra"}},
		{name: "adopt extra after dashdash", args: []string{"refresh", "adopt", "--coordinator", "--", "r1", "extra"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home, before := refreshUsageLab(t)
			stdout, stderr, err := runRefreshCLI(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertRefreshUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
			assertRefreshStateUnchanged(t, home, before)
		})
	}
}

func TestRefreshCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		herdr     bool
		success   bool
		usage     bool
		unknown   bool
		domainErr string
	}{
		{name: "valid status", args: []string{"refresh", "status"}, success: true},
		{name: "valid status after dashdash", args: []string{"refresh", "status", "--"}, success: true},
		{name: "valid status --task", args: []string{"refresh", "status", "--task", "t-aaaaaaaaaaaa"}, success: true},
		{name: "valid status --task= repeated", args: []string{"refresh", "status", "--task=t-aaaaaaaaaaaa", "--task", "t-bbbbbbbbbbbb"}, success: true},
		{name: "valid request --task --coordinator", args: []string{"refresh", "request", "--task", "t-aaaaaaaaaaaa", "--coordinator"}, domainErr: "Herdr pane"},
		{name: "valid request --task= --coordinator=", args: []string{"refresh", "request", "--task=t-aaaaaaaaaaaa", "--coordinator=true"}, domainErr: "Herdr pane"},
		{name: "valid request with herdr", args: []string{"refresh", "request", "--coordinator"}, herdr: true, domainErr: "registered coordinator"},
		{name: "valid adopt --coordinator", args: []string{"refresh", "adopt", "--coordinator", "r1"}, domainErr: "Herdr pane"},
		{name: "valid adopt --coordinator= after dashdash", args: []string{"refresh", "adopt", "--coordinator=true", "--", "r1"}, domainErr: "Herdr pane"},
		{name: "valid adopt with herdr", args: []string{"refresh", "adopt", "--coordinator", "r1"}, herdr: true, domainErr: "registered coordinator"},
		{name: "refresh missing subcommand", args: []string{"refresh"}, usage: true},
		{name: "status extra positional", args: []string{"refresh", "status", "extra"}, usage: true},
		{name: "status unknown flag", args: []string{"refresh", "status", "--unexpected"}, usage: true, unknown: true},
		{name: "request extra positional", args: []string{"refresh", "request", "extra"}, usage: true},
		{name: "request unknown flag", args: []string{"refresh", "request", "--unexpected"}, usage: true, unknown: true},
		{name: "request missing --task value", args: []string{"refresh", "request", "--task"}, usage: true},
		{name: "adopt missing revision", args: []string{"refresh", "adopt", "--coordinator"}, usage: true},
		{name: "adopt missing --coordinator", args: []string{"refresh", "adopt", "r1"}, usage: true},
		{name: "adopt unknown flag", args: []string{"refresh", "adopt", "--coordinator", "r1", "--unexpected"}, usage: true, unknown: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, before := refreshUsageLab(t)
			if tc.herdr {
				herdrEnv(t, home)
			} else {
				clearHerdrEnv(t)
			}
			stdout, stderr, err := runRefreshCLI(t, home, tc.args...)
			switch {
			case tc.success:
				if err != nil {
					t.Fatalf("err = %v stderr=%s stdout=%s", err, stderr, stdout)
				}
				if stdout == "" {
					t.Fatal("expected status output")
				}
			case tc.usage:
				assertRefreshUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				assertRefreshStateUnchanged(t, home, before)
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s", stdout)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.domainErr)
				}
				assertRefreshStateUnchanged(t, home, before)
			}
		})
	}
}

func refreshUsageLab(t *testing.T) (home, before string) {
	t.Helper()
	home = writeDesignatedHome(t)
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repo",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship", "brief_path": "brief.md"}`)
	before = readRefreshState(t, home)
	return home, before
}

func runRefreshCLI(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertRefreshUsageError(t *testing.T, err error) {
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
		strings.Contains(msg, "command is required") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid refresh")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func readRefreshState(t *testing.T, home string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		rel, _ := filepath.Rel(home, path)
		b.WriteString(rel)
		b.WriteByte('\n')
		b.Write(data)
		b.WriteByte('\n')
		return nil
	})
	return b.String()
}

func assertRefreshStateUnchanged(t *testing.T, home, before string) {
	t.Helper()
	if got := readRefreshState(t, home); got != before {
		t.Fatalf("state changed:\nbefore:\n%s\nafter:\n%s", before, got)
	}
}
