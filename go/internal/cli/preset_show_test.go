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

func TestPresetListAndShow_matchThePythonReferenceStdout(t *testing.T) {
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

	settingsJSON := `{"schema": 1, "worker": {"preset": "fast"}, "presets": {"fast": {"harness": "codex", "model": "gpt-5", "reasoning": "high", "args": ["--flag"], "revision": 3}, "slow": {"harness": "claude", "revision": 1}}}`

	cases := []struct {
		name          string
		writeSettings bool
		args          []string
	}{
		{name: "list on empty store", args: []string{"preset", "list"}},
		{name: "list with presets", writeSettings: true, args: []string{"preset", "list"}},
		{name: "show a preset with model/reasoning/args", writeSettings: true, args: []string{"preset", "show", "fast"}},
		{name: "show a bare preset", writeSettings: true, args: []string{"preset", "show", "slow"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.writeSettings {
				if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(settingsJSON), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			fullArgs := append([]string{"--home", home}, tc.args...)

			want, err := exec.Command(reference, fullArgs...).Output()
			if err != nil {
				t.Fatalf("python reference failed: %v", err)
			}

			var stdout, stderr bytes.Buffer
			root := NewRoot(reference, &stdout, &stderr)
			root.SetArgs(fullArgs)
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("go command failed: %v (stderr=%s)", err, stderr.String())
			}

			if stdout.String() != string(want) {
				t.Fatalf("go output =\n%s\nwant (python reference)\n%s", stdout.String(), want)
			}
		})
	}
}

func TestPresetUnknownFlagsAreUsageErrorsBeforeSet(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "list unknown flag", args: []string{"preset", "list", "--unexpected"}, unknown: true},
		{name: "list extra positional", args: []string{"preset", "list", "extra"}},
		{name: "show unknown flag", args: []string{"preset", "show", "fast", "--unexpected"}, unknown: true},
		{name: "show extra positional", args: []string{"preset", "show", "fast", "extra"}},
		{name: "set unknown flag", args: []string{"preset", "set", "fast", "--unexpected"}, unknown: true},
		{name: "set unknown flag before name", args: []string{"preset", "set", "--unexpected", "fast"}, unknown: true},
		{name: "set extra positional", args: []string{"preset", "set", "fast", "extra"}},
		{name: "set extra after --harness", args: []string{"preset", "set", "fast", "--harness", "claude", "extra"}},
		{name: "set extra after dashdash", args: []string{"preset", "set", "fast", "--", "extra"}},
		{name: "set missing name", args: []string{"preset", "set", "--harness", "claude"}},
		{name: "set missing --harness value", args: []string{"preset", "set", "fast", "--harness"}},
		{name: "delete extra positional", args: []string{"preset", "delete", "fast", "extra"}},
		{name: "delete unknown flag", args: []string{"preset", "delete", "fast", "--unexpected"}, unknown: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home := writeDesignatedHome(t)
			writeSettingsFile(t, home, `{"schema": 1, "presets": {"fast": {"harness": "codex", "revision": 1}}}`)
			before := readSettingsFile(t, home)
			stdout, stderr, err := runPreset(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertPresetUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
			if got := readSettingsFile(t, home); got != before {
				t.Fatalf("settings.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
			}
		})
	}
}

func TestPresetCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		herdr     bool
		success   bool
		usage     bool
		unknown   bool
		domainErr string
	}{
		{name: "valid list", args: []string{"preset", "list"}, success: true},
		{name: "valid list after dashdash", args: []string{"preset", "list", "--"}, success: true},
		{name: "valid show", args: []string{"preset", "show", "fast"}, success: true},
		{name: "valid show after dashdash", args: []string{"preset", "show", "--", "fast"}, success: true},
		{name: "valid set --harness", args: []string{"preset", "set", "fast", "--harness", "claude"}, domainErr: "Herdr pane"},
		{name: "valid set --harness=", args: []string{"preset", "set", "fast", "--harness=claude"}, domainErr: "Herdr pane"},
		{name: "valid set --arg repeatable", args: []string{"preset", "set", "fast", "--arg", "--flag", "--arg=--other"}, domainErr: "Herdr pane"},
		{name: "valid delete", args: []string{"preset", "delete", "fast"}, domainErr: "Herdr pane"},
		{name: "preset missing subcommand", args: []string{"preset"}, usage: true},
		{name: "list extra positional", args: []string{"preset", "list", "extra"}, usage: true},
		{name: "list unknown flag", args: []string{"preset", "list", "--unexpected"}, usage: true, unknown: true},
		{name: "show missing name", args: []string{"preset", "show"}, usage: true},
		{name: "show extra positional", args: []string{"preset", "show", "fast", "extra"}, usage: true},
		{name: "set missing name", args: []string{"preset", "set"}, usage: true},
		{name: "set unknown flag", args: []string{"preset", "set", "fast", "--unexpected"}, usage: true, unknown: true},
		{name: "set missing --harness value", args: []string{"preset", "set", "fast", "--harness"}, usage: true},
		{name: "delete missing name", args: []string{"preset", "delete"}, usage: true},
		{name: "delete unknown flag", args: []string{"preset", "delete", "fast", "--unexpected"}, usage: true, unknown: true},
		{name: "set requires coordinator", args: []string{"preset", "set", "fast", "--harness", "claude"}, herdr: true, domainErr: "registered coordinator"},
		{name: "delete requires coordinator", args: []string{"preset", "delete", "fast"}, herdr: true, domainErr: "registered coordinator"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := writeDesignatedHome(t)
			writeSettingsFile(t, home, `{"schema": 1, "presets": {"fast": {"harness": "codex", "revision": 1}}}`)
			if tc.herdr {
				herdrEnv(t, home)
				t.Setenv("HERDR_PANE_ID", "w-other:p1")
			} else {
				clearHerdrEnv(t)
			}
			before := readSettingsFile(t, home)
			stdout, stderr, err := runPreset(t, home, tc.args...)
			switch {
			case tc.success:
				if err != nil {
					t.Fatalf("err = %v stderr=%s stdout=%s", err, stderr, stdout)
				}
				if stdout == "" {
					t.Fatal("expected preset output")
				}
			case tc.usage:
				assertPresetUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				if got := readSettingsFile(t, home); got != before {
					t.Fatalf("settings.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
				}
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s", stdout)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.domainErr)
				}
				if got := readSettingsFile(t, home); got != before {
					t.Fatalf("settings.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
				}
			}
		})
	}
}

func runPreset(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertPresetUsageError(t *testing.T, err error) {
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
		strings.Contains(msg, "invalid preset")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}
