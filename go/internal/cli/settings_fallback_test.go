package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettings_unknownSubcommandIsGoError(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRoot("", &bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"--home", home, "settings", "explode"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected unrecognized arguments")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("err = %v, want unknown command", err)
	}
}

func TestSettingsUnknownFlagsAreUsageErrorsBeforeSet(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "show unknown flag", args: []string{"settings", "show", "--unexpected"}, unknown: true},
		{name: "show extra positional", args: []string{"settings", "show", "extra"}},
		{name: "show extra after dashdash", args: []string{"settings", "show", "--", "extra"}},
		{name: "set unknown flag", args: []string{"settings", "set", "--unexpected"}, unknown: true},
		{name: "set unknown flag before --global", args: []string{"settings", "set", "--unexpected", "--global", "3"}, unknown: true},
		{name: "set extra positional", args: []string{"settings", "set", "extra"}},
		{name: "set extra after --global", args: []string{"settings", "set", "--global", "3", "extra"}},
		{name: "set extra after dashdash", args: []string{"settings", "set", "--", "extra"}},
		{name: "set missing --global value", args: []string{"settings", "set", "--global"}},
		{name: "set invalid --global", args: []string{"settings", "set", "--global", "nope"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home := writeDesignatedHome(t)
			writeSettingsFile(t, home, `{"schema": 1, "capacity": {"global": 2, "per_repository": 1}}`)
			before := readSettingsFile(t, home)
			stdout, stderr, err := runSettings(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertSettingsUsageError(t, err)
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

func TestSettingsCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		herdr     bool
		success   bool
		usage     bool
		unknown   bool
		domainErr string
	}{
		{name: "valid show", args: []string{"settings", "show"}, success: true},
		{name: "valid show after dashdash", args: []string{"settings", "show", "--"}, success: true},
		{name: "valid set --global", args: []string{"settings", "set", "--global", "3"}, domainErr: "Herdr pane"},
		{name: "valid set --global=", args: []string{"settings", "set", "--global=3"}, domainErr: "Herdr pane"},
		{name: "valid set --clear-capacity", args: []string{"settings", "set", "--clear-capacity"}, domainErr: "Herdr pane"},
		{name: "valid set --worker-preset=", args: []string{"settings", "set", "--worker-preset=fast"}, domainErr: "Herdr pane"},
		{name: "settings missing subcommand", args: []string{"settings"}, usage: true},
		{name: "show extra positional", args: []string{"settings", "show", "extra"}, usage: true},
		{name: "show unknown flag", args: []string{"settings", "show", "--unexpected"}, usage: true, unknown: true},
		{name: "set extra positional", args: []string{"settings", "set", "extra"}, usage: true},
		{name: "set unknown flag", args: []string{"settings", "set", "--unexpected"}, usage: true, unknown: true},
		{name: "set missing --global value", args: []string{"settings", "set", "--global"}, usage: true},
		{name: "set invalid --global", args: []string{"settings", "set", "--global", "nope"}, usage: true},
		{name: "set requires pane without herdr", args: []string{"settings", "set"}, domainErr: "Herdr pane"},
		{name: "set requires coordinator", args: []string{"settings", "set", "--global", "3"}, herdr: true, domainErr: "registered coordinator"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := writeDesignatedHome(t)
			writeSettingsFile(t, home, `{"schema": 1, "capacity": {"global": 2, "per_repository": 1}}`)
			if tc.herdr {
				herdrEnv(t, home)
				t.Setenv("HERDR_PANE_ID", "w-other:p1")
			} else {
				clearHerdrEnv(t)
			}
			before := readSettingsFile(t, home)
			stdout, stderr, err := runSettings(t, home, tc.args...)
			switch {
			case tc.success:
				if err != nil {
					t.Fatalf("err = %v stderr=%s stdout=%s", err, stderr, stdout)
				}
				if stdout == "" {
					t.Fatal("expected settings output")
				}
			case tc.usage:
				assertSettingsUsageError(t, err)
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

func runSettings(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertSettingsUsageError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected usage failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "registered coordinator") || strings.Contains(msg, "Herdr pane") || strings.Contains(msg, "Give --global") {
		t.Fatalf("err = %v, want a usage failure", err)
	}
	usage := strings.Contains(msg, "unknown flag") ||
		strings.Contains(msg, "accepts 0 arg") ||
		strings.Contains(msg, "flag needs an argument") ||
		strings.Contains(msg, "invalid argument") ||
		strings.Contains(msg, "command is required") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid settings")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func writeSettingsFile(t *testing.T, home, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(content+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readSettingsFile(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "settings.json"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
