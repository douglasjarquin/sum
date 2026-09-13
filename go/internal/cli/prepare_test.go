package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareUnknownFlagsAreUsageErrorsBeforePrepare(t *testing.T) {
	brief := writePrepareBrief(t)
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "prepare unknown flag", args: []string{"prepare", "--unexpected"}, unknown: true},
		{name: "prepare unknown flag before brief", args: []string{"prepare", "--unexpected", "--brief", brief, "--approved"}, unknown: true},
		{name: "prepare extra positional", args: []string{"prepare", "--brief", brief, "--approved", "extra"}},
		{name: "prepare extra after dashdash", args: []string{"prepare", "--brief", brief, "--approved", "--", "extra"}},
		{name: "prepare extra after --approved", args: []string{"prepare", "--approved", "extra"}},
		{name: "prepare missing --brief value", args: []string{"prepare", "--brief"}},
		{name: "prepare missing --repo value", args: []string{"prepare", "--repo"}},
		{name: "prepare missing --arg value", args: []string{"prepare", "--brief", brief, "--arg"}},
		{name: "prepare missing --kind value", args: []string{"prepare", "--kind"}},
		{name: "dispatch unknown flag", args: []string{"dispatch", "--unexpected"}, unknown: true},
		{name: "dispatch unknown flag before brief", args: []string{"dispatch", "--unexpected", "--brief", brief, "--approved"}, unknown: true},
		{name: "dispatch extra positional", args: []string{"dispatch", "--brief", brief, "--approved", "extra"}},
		{name: "dispatch extra after dashdash", args: []string{"dispatch", "--brief", brief, "--approved", "--", "extra"}},
		{name: "dispatch missing --brief value", args: []string{"dispatch", "--brief"}},
		{name: "dispatch missing --arg value", args: []string{"dispatch", "--brief", brief, "--arg"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home := writeDesignatedHome(t)
			before := readTasksDir(t, home)
			stdout, stderr, err := runPrepareCLI(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertPrepareUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
			if got := readTasksDir(t, home); got != before {
				t.Fatalf("tasks changed:\nbefore:\n%s\nafter:\n%s", before, got)
			}
		})
	}
}

func TestPrepareCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      func(brief string) []string
		herdr     bool
		usage     bool
		unknown   bool
		invalid   string
		domainErr string
	}{
		{
			name: "valid prepare",
			args: func(brief string) []string {
				return []string{"prepare", "--repo", "owner/repo", "--brief", brief, "--harness", "codex", "--approved"}
			},
			domainErr: "Herdr pane",
		},
		{
			name: "valid prepare after dashdash",
			args: func(brief string) []string {
				return []string{"prepare", "--repo", "owner/repo", "--brief", brief, "--approved", "--"}
			},
			domainErr: "Herdr pane",
		},
		{
			name: "valid prepare equals flags",
			args: func(brief string) []string {
				return []string{"prepare", "--repo=owner/repo", "--brief=" + brief, "--harness=codex", "--approved"}
			},
			domainErr: "Herdr pane",
		},
		{
			name: "valid prepare --kind scout",
			args: func(brief string) []string {
				return []string{"prepare", "--repo", "owner/repo", "--brief", brief, "--kind", "scout", "--approved"}
			},
			domainErr: "Herdr pane",
		},
		{
			name: "valid prepare --kind=scout",
			args: func(brief string) []string {
				return []string{"prepare", "--repo", "owner/repo", "--brief", brief, "--kind=scout", "--approved"}
			},
			domainErr: "Herdr pane",
		},
		{
			name: "valid prepare --same-as-you --preset --arg",
			args: func(brief string) []string {
				return []string{"prepare", "--repo", "owner/repo", "--brief", brief, "--same-as-you", "--preset", "fast", "--arg", "foo", "--arg=bar", "--approved"}
			},
			domainErr: "Herdr pane",
		},
		{
			name: "valid prepare --approved=",
			args: func(brief string) []string {
				return []string{"prepare", "--repo", "owner/repo", "--brief", brief, "--approved=true"}
			},
			domainErr: "Herdr pane",
		},
		{
			name: "valid prepare with herdr",
			args: func(brief string) []string {
				return []string{"prepare", "--repo", "owner/repo", "--brief", brief, "--approved"}
			},
			herdr:     true,
			domainErr: "registered coordinator",
		},
		{
			name: "valid dispatch",
			args: func(brief string) []string {
				return []string{"dispatch", "--repo", "owner/repo", "--brief", brief, "--harness", "codex", "--approved"}
			},
			domainErr: "Herdr pane",
		},
		{
			name: "valid dispatch equals flags",
			args: func(brief string) []string {
				return []string{"dispatch", "--repo=owner/repo", "--brief=" + brief, "--approved"}
			},
			domainErr: "Herdr pane",
		},
		{
			name: "valid dispatch --arg and --base",
			args: func(brief string) []string {
				return []string{"dispatch", "--repo", "owner/repo", "--brief", brief, "--base", "HEAD", "--arg", "foo", "--approved"}
			},
			domainErr: "Herdr pane",
		},
		{
			name: "valid dispatch with herdr",
			args: func(brief string) []string {
				return []string{"dispatch", "--repo", "owner/repo", "--brief", brief, "--approved"}
			},
			herdr:     true,
			domainErr: "registered coordinator",
		},
		{
			name: "prepare unknown flag",
			args: func(string) []string {
				return []string{"prepare", "--unexpected"}
			},
			usage:   true,
			unknown: true,
		},
		{
			name: "dispatch unknown flag",
			args: func(string) []string {
				return []string{"dispatch", "--unexpected"}
			},
			usage:   true,
			unknown: true,
		},
		{
			name: "prepare extra positional",
			args: func(brief string) []string {
				return []string{"prepare", "--brief", brief, "--approved", "extra"}
			},
			usage: true,
		},
		{
			name: "prepare missing --brief value",
			args: func(string) []string {
				return []string{"prepare", "--brief"}
			},
			usage: true,
		},
		{
			name: "prepare missing brief",
			args: func(string) []string {
				return []string{"prepare", "--repo", "owner/repo", "--approved"}
			},
			invalid: "invalid prepare arguments",
		},
		{
			name: "prepare invalid --kind",
			args: func(brief string) []string {
				return []string{"prepare", "--brief", brief, "--kind", "bogus", "--approved"}
			},
			invalid: "invalid prepare arguments",
		},
		{
			name: "dispatch missing brief",
			args: func(string) []string {
				return []string{"dispatch", "--repo", "owner/repo", "--approved"}
			},
			invalid: "invalid dispatch arguments",
		},
		{
			name: "dispatch invalid --kind",
			args: func(brief string) []string {
				return []string{"dispatch", "--brief", brief, "--kind", "bogus", "--approved"}
			},
			invalid: "invalid dispatch arguments",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := writeDesignatedHome(t)
			brief := writePrepareBrief(t)
			if tc.herdr {
				herdrEnv(t, home)
			} else {
				clearHerdrEnv(t)
			}
			before := readTasksDir(t, home)
			stdout, stderr, err := runPrepareCLI(t, home, tc.args(brief)...)
			switch {
			case tc.invalid != "":
				if err == nil {
					t.Fatalf("expected %s, stdout=%s", tc.invalid, stdout)
				}
				if err.Error() != tc.invalid {
					t.Fatalf("err = %v, want %s", err, tc.invalid)
				}
				if got := readTasksDir(t, home); got != before {
					t.Fatalf("tasks changed:\nbefore:\n%s\nafter:\n%s", before, got)
				}
			case tc.usage:
				assertPrepareUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				if got := readTasksDir(t, home); got != before {
					t.Fatalf("tasks changed:\nbefore:\n%s\nafter:\n%s", before, got)
				}
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s stderr=%s", stdout, stderr)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.domainErr)
				}
				if got := readTasksDir(t, home); got != before {
					t.Fatalf("tasks changed:\nbefore:\n%s\nafter:\n%s", before, got)
				}
			}
		})
	}
}

func writePrepareBrief(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(path, []byte("Do the thing.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func runPrepareCLI(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertPrepareUsageError(t *testing.T, err error) {
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
		strings.Contains(msg, "flag needs an argument") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid prepare") ||
		strings.Contains(msg, "invalid dispatch")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func readTasksDir(t *testing.T, home string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return strings.Join(names, "\n")
}
