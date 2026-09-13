package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepairUnknownFlagsAreUsageErrorsBeforeRepair(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "send unknown flag", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1", "--text", "fix", "--unexpected"}, unknown: true},
		{name: "send unknown flag before task", args: []string{"repair", "send", "--unexpected", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1", "--text", "fix"}, unknown: true},
		{name: "send extra positional", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1", "--text", "fix", "extra"}},
		{name: "send extra after dashdash", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1", "--text", "fix", "--", "extra"}},
		{name: "send missing --attempt value", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt"}},
		{name: "send missing --key value", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key"}},
		{name: "send missing --text value", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1", "--text"}},
		{name: "send --text and --file", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1", "--text", "fix", "--file", "note.md"}},
		{name: "extend unknown flag", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--additional", "2", "--approved", "--text", "grant", "--unexpected"}, unknown: true},
		{name: "extend extra after --text", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--additional", "2", "--approved", "--text", "grant", "extra"}},
		{name: "extend missing --additional value", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--additional"}},
		{name: "extend invalid --additional", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--additional", "nope", "--text", "grant"}},
		{name: "extend --text and --file", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--additional", "2", "--text", "grant", "--file", "note.md"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home, before := repairUsageLab(t)
			stdout, stderr, err := runRepairCLI(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertRepairUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
			assertRepairTaskUnchanged(t, home, before)
		})
	}
}

func TestRepairCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		herdr     bool
		usage     bool
		unknown   bool
		domainErr string
	}{
		{name: "valid send --text", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1", "--text", "fix"}, domainErr: "Herdr pane"},
		{name: "valid send --attempt= --key= --text=", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt=x-aaaaaaaaaaaa", "--key=k1", "--text=fix"}, domainErr: "Herdr pane"},
		{name: "valid send with herdr", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1", "--text", "fix"}, herdr: true, domainErr: "registered coordinator"},
		{name: "valid extend --approved --text", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--additional", "2", "--approved", "--text", "grant"}, domainErr: "Herdr pane"},
		{name: "valid extend --question= --additional= --approved=", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question=q-aaaaaaaaaa", "--additional=2", "--approved=true", "--text=grant"}, domainErr: "Herdr pane"},
		{name: "valid extend with herdr", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--additional", "2", "--approved", "--text", "grant"}, herdr: true, domainErr: "registered coordinator"},
		{name: "repair missing subcommand", args: []string{"repair"}, usage: true},
		{name: "send missing --attempt", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--key", "k1", "--text", "fix"}, usage: true},
		{name: "send missing --key", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--text", "fix"}, usage: true},
		{name: "send missing --text and --file", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1"}, usage: true},
		{name: "send --text and --file", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1", "--text", "fix", "--file", "note.md"}, usage: true},
		{name: "send unknown flag", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1", "--text", "fix", "--unexpected"}, usage: true, unknown: true},
		{name: "extend missing --question", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--additional", "2", "--text", "grant"}, usage: true},
		{name: "extend missing --additional", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--text", "grant"}, usage: true},
		{name: "extend invalid --additional", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--additional", "nope", "--text", "grant"}, usage: true},
		{name: "extend unknown flag", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--additional", "2", "--text", "grant", "--unexpected"}, usage: true, unknown: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, before := repairUsageLab(t)
			if tc.herdr {
				herdrEnv(t, home)
			} else {
				clearHerdrEnv(t)
			}
			stdout, stderr, err := runRepairCLI(t, home, tc.args...)
			switch {
			case tc.usage:
				assertRepairUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				assertRepairTaskUnchanged(t, home, before)
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s stderr=%s", stdout, stderr)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.domainErr)
				}
				assertRepairTaskUnchanged(t, home, before)
			}
		})
	}
}

func repairUsageLab(t *testing.T) (home, before string) {
	t.Helper()
	home = writeDesignatedHome(t)
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "waiting", "repository": "owner/repo",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship", "brief_path": "brief.md"}`)
	before = readNamedTask(t, home, "t-aaaaaaaaaaaa")
	return home, before
}

func runRepairCLI(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertRepairUsageError(t *testing.T, err error) {
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
		strings.Contains(msg, "if any flags in the group") ||
		strings.Contains(msg, "at least one of the flags") ||
		strings.Contains(msg, "command is required") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "invalid argument") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid repair")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func assertRepairTaskUnchanged(t *testing.T, home, before string) {
	t.Helper()
	if got := readNamedTask(t, home, "t-aaaaaaaaaaaa"); got != before {
		t.Fatalf("task.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
	}
}
