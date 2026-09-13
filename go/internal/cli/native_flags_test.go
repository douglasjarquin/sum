package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeUnknownFlagsAreUsageErrorsBeforeMutation(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "help unknown flag", args: []string{"help", "--unexpected"}, unknown: true},
		{name: "help extra positional", args: []string{"help", "ask", "extra"}},
		{name: "quota unknown flag", args: []string{"quota", "--provider", "codex", "--unexpected"}, unknown: true},
		{name: "quota unknown flag before provider", args: []string{"quota", "--unexpected", "--provider", "codex"}, unknown: true},
		{name: "quota extra positional", args: []string{"quota", "--provider", "codex", "extra"}},
		{name: "quota missing --provider value", args: []string{"quota", "--provider"}},
		{name: "skills check unknown flag", args: []string{"skills", "check", "--unexpected"}, unknown: true},
		{name: "skills check extra positional", args: []string{"skills", "check", "extra"}},
		{name: "skills install unknown flag", args: []string{"skills", "install", "--target", "/tmp/proj", "--source", "src", "--skill", "demo", "--agent", "claude", "--unexpected"}, unknown: true},
		{name: "notes unknown flag", args: []string{"notes", "t-aaaaaaaaaaaa", "--text", "hello", "--unexpected"}, unknown: true},
		{name: "notes unknown flag before task", args: []string{"notes", "--unexpected", "t-aaaaaaaaaaaa", "--text", "hello"}, unknown: true},
		{name: "notes extra positional", args: []string{"notes", "t-aaaaaaaaaaaa", "--text", "hello", "extra"}},
		{name: "notes --text and --file", args: []string{"notes", "t-aaaaaaaaaaaa", "--text", "hello", "--file", "note.md"}},
		{name: "herdr unknown flag", args: []string{"herdr", "--unexpected"}, unknown: true},
		{name: "archive unknown flag", args: []string{"archive", "t-aaaaaaaaaaaa", "--unexpected"}, unknown: true},
		{name: "archive unknown flag before task", args: []string{"archive", "--unexpected", "t-aaaaaaaaaaaa"}, unknown: true},
		{name: "archive extra positional", args: []string{"archive", "t-aaaaaaaaaaaa", "extra"}},
		{name: "ask unknown flag", args: []string{"ask", "t-aaaaaaaaaaaa", "--text", "Keep going?", "--unexpected"}, unknown: true},
		{name: "ask unknown flag before task", args: []string{"ask", "--unexpected", "t-aaaaaaaaaaaa", "--text", "Keep going?"}, unknown: true},
		{name: "ask extra after --text", args: []string{"ask", "t-aaaaaaaaaaaa", "--text", "Keep going?", "extra"}},
		{name: "ask extra after dashdash", args: []string{"ask", "t-aaaaaaaaaaaa", "--text", "Keep going?", "--", "extra"}},
		{name: "ask --text and --file", args: []string{"ask", "t-aaaaaaaaaaaa", "--text", "Keep going?", "--file", "note.md"}},
		{name: "ask missing --text value", args: []string{"ask", "t-aaaaaaaaaaaa", "--text"}},
		{name: "answer unknown flag", args: []string{"answer", "t-aaaaaaaaaaaa", "q-aaaaaaaaaa", "--text", "yes", "--unexpected"}, unknown: true},
		{name: "answer extra positional", args: []string{"answer", "t-aaaaaaaaaaaa", "q-aaaaaaaaaa", "--text", "yes", "extra"}},
		{name: "answer --text and --file", args: []string{"answer", "t-aaaaaaaaaaaa", "q-aaaaaaaaaa", "--text", "yes", "--file", "note.md"}},
		{name: "resolve unknown flag", args: []string{"resolve", "t-aaaaaaaaaaaa", "q-aaaaaaaaaa", "--unexpected"}, unknown: true},
		{name: "resolve extra positional", args: []string{"resolve", "t-aaaaaaaaaaaa", "q-aaaaaaaaaa", "extra"}},
		{name: "notice unknown flag", args: []string{"notice", "t-aaaaaaaaaaaa", "--unexpected"}, unknown: true},
		{name: "notice extra positional", args: []string{"notice", "t-aaaaaaaaaaaa", "extra"}},
		{name: "pump unknown flag", args: []string{"pump", "--unexpected"}, unknown: true},
		{name: "pump extra positional", args: []string{"pump", "extra"}},
		{name: "backup unknown flag", args: []string{"backup", "/tmp/state.tar.gz", "--unexpected"}, unknown: true},
		{name: "backup extra positional", args: []string{"backup", "/tmp/state.tar.gz", "extra"}},
		{name: "attention unknown flag", args: []string{"attention", "t-aaaaaaaaaaaa", "a-aaaaaaaaaa", "--seen", "--unexpected"}, unknown: true},
		{name: "attention extra positional", args: []string{"attention", "t-aaaaaaaaaaaa", "a-aaaaaaaaaa", "--seen", "extra"}},
		{name: "bind unknown flag", args: []string{"bind", "t-aaaaaaaaaaaa", "--unexpected"}, unknown: true},
		{name: "bind extra positional", args: []string{"bind", "t-aaaaaaaaaaaa", "extra"}},
		{name: "report unknown flag", args: []string{"report", "t-aaaaaaaaaaaa", "--text", "done", "--unexpected"}, unknown: true},
		{name: "report extra positional", args: []string{"report", "t-aaaaaaaaaaaa", "--text", "done", "extra"}},
		{name: "dev list unknown flag", args: []string{"dev", "list", "--unexpected"}, unknown: true},
		{name: "dev prepare unknown flag", args: []string{"dev", "prepare", "--name", "topic", "--unexpected"}, unknown: true},
		{name: "dev remove unknown flag", args: []string{"dev", "remove", "--name", "topic", "--unexpected"}, unknown: true},
		{name: "dev extra positional", args: []string{"dev", "list", "extra"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home, before := nativeUsageLab(t)
			herdrRoot := filepath.Join(home, "fake-herdr")
			herdrBefore := listRegularFiles(t, herdrRoot)
			stdout, stderr, err := runNativeCLI(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertNativeUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
			assertNativeTaskUnchanged(t, home, before)
			if got := listRegularFiles(t, herdrRoot); got != herdrBefore {
				t.Fatalf("herdr state changed:\nbefore:\n%s\nafter:\n%s", herdrBefore, got)
			}
		})
	}
}

func TestNativeCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		herdr     bool
		usage     bool
		unknown   bool
		ok        bool
		parsed    bool
		domainErr string
	}{
		{name: "valid help", args: []string{"help"}, ok: true},
		{name: "valid help after dashdash", args: []string{"help", "--", "ask"}, ok: true},
		{name: "valid help topic", args: []string{"help", "ask"}, ok: true},
		{name: "help extra positional", args: []string{"help", "ask", "extra"}, usage: true},
		{name: "help unknown flag", args: []string{"help", "--unexpected"}, usage: true, unknown: true},
		{name: "valid quota --provider", args: []string{"quota", "--provider", "codex"}, parsed: true},
		{name: "valid quota --provider= --format=", args: []string{"quota", "--provider=codex", "--format=toon"}, parsed: true},
		{name: "quota missing --provider", args: []string{"quota"}, usage: true},
		{name: "quota unknown flag", args: []string{"quota", "--provider", "codex", "--unexpected"}, usage: true, unknown: true},
		{name: "quota invalid --format", args: []string{"quota", "--provider", "codex", "--format", "yaml"}, usage: true},
		{name: "valid skills check", args: []string{"skills", "check"}, ok: true},
		{name: "valid skills check --root=", args: []string{"skills", "check", "--root="}, ok: true},
		{name: "skills missing subcommand", args: []string{"skills"}, usage: true},
		{name: "skills check unknown flag", args: []string{"skills", "check", "--unexpected"}, usage: true, unknown: true},
		{name: "skills install missing --target", args: []string{"skills", "install", "--source", "src", "--skill", "demo", "--agent", "claude"}, usage: true},
		{name: "valid notes --text", args: []string{"notes", "t-aaaaaaaaaaaa", "--text", "hello"}, ok: true},
		{name: "valid notes --text=", args: []string{"notes", "t-aaaaaaaaaaaa", "--text=-keep"}, ok: true},
		{name: "notes missing --text and --file", args: []string{"notes", "t-aaaaaaaaaaaa"}, usage: true},
		{name: "notes unknown flag", args: []string{"notes", "t-aaaaaaaaaaaa", "--text", "hello", "--unexpected"}, usage: true, unknown: true},
		{name: "valid herdr pane list", args: []string{"herdr", "pane", "list"}, domainErr: "Herdr pane"},
		{name: "valid herdr after dashdash", args: []string{"herdr", "--", "pane", "list"}, domainErr: "Herdr pane"},
		{name: "valid herdr with herdr", args: []string{"herdr", "pane", "list"}, herdr: true, domainErr: "not registered"},
		{name: "herdr unknown flag", args: []string{"herdr", "--unexpected"}, usage: true, unknown: true},
		{name: "valid archive", args: []string{"archive", "t-aaaaaaaaaaaa"}, domainErr: "Herdr pane"},
		{name: "valid archive --acknowledge", args: []string{"archive", "t-aaaaaaaaaaaa", "--acknowledge"}, domainErr: "Herdr pane"},
		{name: "valid archive --acknowledge= with herdr", args: []string{"archive", "t-aaaaaaaaaaaa", "--acknowledge=true"}, herdr: true, domainErr: "registered coordinator"},
		{name: "archive unknown flag", args: []string{"archive", "t-aaaaaaaaaaaa", "--unexpected"}, usage: true, unknown: true},
		{name: "valid ask --text", args: []string{"ask", "t-aaaaaaaaaaaa", "--text", "Keep going?"}, ok: true},
		{name: "valid ask --key= --text=", args: []string{"ask", "t-aaaaaaaaaaaa", "--key=k1", "--text=Keep going?"}, ok: true},
		{name: "ask missing --text and --file", args: []string{"ask", "t-aaaaaaaaaaaa"}, usage: true},
		{name: "ask unknown flag", args: []string{"ask", "t-aaaaaaaaaaaa", "--text", "Keep going?", "--unexpected"}, usage: true, unknown: true},
		{name: "valid answer --text", args: []string{"answer", "t-aaaaaaaaaaaa", "q-aaaaaaaaaa", "--text", "yes"}, domainErr: "Question not found"},
		{name: "answer missing --text and --file", args: []string{"answer", "t-aaaaaaaaaaaa", "q-aaaaaaaaaa"}, usage: true},
		{name: "answer unknown flag", args: []string{"answer", "t-aaaaaaaaaaaa", "q-aaaaaaaaaa", "--text", "yes", "--unexpected"}, usage: true, unknown: true},
		{name: "valid resolve", args: []string{"resolve", "t-aaaaaaaaaaaa", "q-aaaaaaaaaa"}, domainErr: "Only an answered question"},
		{name: "resolve extra positional", args: []string{"resolve", "t-aaaaaaaaaaaa", "q-aaaaaaaaaa", "extra"}, usage: true},
		{name: "resolve unknown flag", args: []string{"resolve", "t-aaaaaaaaaaaa", "q-aaaaaaaaaa", "--unexpected"}, usage: true, unknown: true},
		{name: "valid notice", args: []string{"notice", "t-aaaaaaaaaaaa"}, ok: true},
		{name: "valid notice --to=", args: []string{"notice", "t-aaaaaaaaaaaa", "--to=worker"}, ok: true},
		{name: "notice invalid --to", args: []string{"notice", "t-aaaaaaaaaaaa", "--to", "other"}, usage: true},
		{name: "notice unknown flag", args: []string{"notice", "t-aaaaaaaaaaaa", "--unexpected"}, usage: true, unknown: true},
		{name: "valid pump", args: []string{"pump"}, domainErr: "Herdr pane"},
		{name: "valid pump --task= --force", args: []string{"pump", "--task=t-aaaaaaaaaaaa", "--force"}, domainErr: "Herdr pane"},
		{name: "valid pump with herdr", args: []string{"pump"}, herdr: true, domainErr: "sumctl init"},
		{name: "pump unknown flag", args: []string{"pump", "--unexpected"}, usage: true, unknown: true},
		{name: "backup extra positional", args: []string{"backup", "state.tar.gz", "extra"}, usage: true},
		{name: "backup unknown flag", args: []string{"backup", "state.tar.gz", "--unexpected"}, usage: true, unknown: true},
		{name: "valid attention --seen", args: []string{"attention", "t-aaaaaaaaaaaa", "a-aaaaaaaaaa", "--seen"}, domainErr: "Herdr pane"},
		{name: "valid attention --seen= with herdr", args: []string{"attention", "t-aaaaaaaaaaaa", "a-aaaaaaaaaa", "--seen=true"}, herdr: true, domainErr: "registered coordinator"},
		{name: "attention missing --seen", args: []string{"attention", "t-aaaaaaaaaaaa", "a-aaaaaaaaaa"}, usage: true},
		{name: "attention unknown flag", args: []string{"attention", "t-aaaaaaaaaaaa", "a-aaaaaaaaaa", "--seen", "--unexpected"}, usage: true, unknown: true},
		{name: "valid bind", args: []string{"bind", "t-aaaaaaaaaaaa"}, domainErr: "Herdr pane"},
		{name: "valid bind --parent-only --worker-pane=", args: []string{"bind", "t-aaaaaaaaaaaa", "--parent-only", "--worker-pane=w-worker:p2"}, domainErr: "Herdr pane"},
		{name: "valid bind with herdr", args: []string{"bind", "t-aaaaaaaaaaaa"}, herdr: true, domainErr: "registered coordinator"},
		{name: "bind unknown flag", args: []string{"bind", "t-aaaaaaaaaaaa", "--unexpected"}, usage: true, unknown: true},
		{name: "valid report --text", args: []string{"report", "t-aaaaaaaaaaaa", "--text", "done"}, ok: true},
		{name: "report missing --text and --file", args: []string{"report", "t-aaaaaaaaaaaa"}, usage: true},
		{name: "report unknown flag", args: []string{"report", "t-aaaaaaaaaaaa", "--text", "done", "--unexpected"}, usage: true, unknown: true},
		{name: "valid dev list", args: []string{"dev", "list"}, domainErr: "not a sum installation's state home"},
		{name: "dev missing subcommand", args: []string{"dev"}, usage: true},
		{name: "dev prepare missing --name", args: []string{"dev", "prepare"}, usage: true},
		{name: "dev remove missing --name", args: []string{"dev", "remove"}, usage: true},
		{name: "dev prepare unknown flag", args: []string{"dev", "prepare", "--name", "topic", "--unexpected"}, usage: true, unknown: true},
		{name: "dev list unknown flag", args: []string{"dev", "list", "--unexpected"}, usage: true, unknown: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, before := nativeUsageLab(t)
			if tc.herdr {
				herdrEnv(t, home)
			} else {
				clearHerdrEnv(t)
			}
			stdout, stderr, err := runNativeCLI(t, home, tc.args...)
			switch {
			case tc.usage:
				assertNativeUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				assertNativeTaskUnchanged(t, home, before)
			case tc.ok:
				if err != nil {
					t.Fatalf("expected success, err=%v stdout=%s stderr=%s", err, stdout, stderr)
				}
			case tc.parsed:
				if err != nil {
					assertNativeUsageErrorNot(t, err)
				}
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s stderr=%s", stdout, stderr)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.domainErr)
				}
			}
		})
	}
}

func nativeUsageLab(t *testing.T) (home, before string) {
	t.Helper()
	home = writeDesignatedHome(t)
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repo",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship", "brief_path": "brief.md"}`)
	before = readNamedTask(t, home, "t-aaaaaaaaaaaa")
	return home, before
}

func runNativeCLI(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertNativeUsageError(t *testing.T, err error) {
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
		strings.Contains(msg, "accepts at most") ||
		strings.Contains(msg, "accepts 2 arg") ||
		strings.Contains(msg, "flag needs an argument") ||
		strings.Contains(msg, "required flag") ||
		strings.Contains(msg, "if any flags in the group") ||
		strings.Contains(msg, "at least one of the flags") ||
		strings.Contains(msg, "command is required") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid quota") ||
		strings.Contains(msg, "invalid notice") ||
		strings.Contains(msg, "invalid attention")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func assertNativeUsageErrorNot(t *testing.T, err error) {
	t.Helper()
	msg := err.Error()
	if strings.Contains(msg, "unknown flag") ||
		strings.Contains(msg, "accepts ") ||
		strings.Contains(msg, "required flag") ||
		strings.Contains(msg, "flag needs an argument") ||
		strings.Contains(msg, "command is required") {
		t.Fatalf("err = %v, want flags accepted", err)
	}
}

func assertNativeTaskUnchanged(t *testing.T, home, before string) {
	t.Helper()
	if got := readNamedTask(t, home, "t-aaaaaaaaaaaa"); got != before {
		t.Fatalf("task.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
	}
}

func TestNativeQuotaFormatDoesNotRedefinePersistentFlag(t *testing.T) {
	clearHerdrEnv(t)
	home, _ := nativeUsageLab(t)
	stdout, stderr, err := runNativeCLI(t, home, "quota", "--provider", "codex", "--format", "compact")
	if err != nil {
		if strings.Contains(err.Error(), "unknown flag") || strings.Contains(err.Error(), "redefined") {
			t.Fatalf("err = %v, want quota to accept --format compact", err)
		}
		assertNativeUsageErrorNot(t, err)
		return
	}
	if stdout == "" && stderr == "" {
		t.Fatal("quota --format compact produced no output")
	}
}

func TestNativeBackupUnknownFlagDoesNotWriteArchive(t *testing.T) {
	clearHerdrEnv(t)
	home, _ := nativeUsageLab(t)
	dest := filepath.Join(home, "state.tar.gz")
	stdout, stderr, err := runNativeCLI(t, home, "backup", dest, "--unexpected")
	if err == nil {
		t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
	}
	assertNativeUsageError(t, err)
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("backup wrote %s: %v", dest, statErr)
	}
}
