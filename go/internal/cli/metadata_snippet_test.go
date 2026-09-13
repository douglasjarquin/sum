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

func TestMetadataSnippet_matchesThePythonReferenceAcrossScenarios(t *testing.T) {
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

	cases := []struct {
		name string
		home func(t *testing.T) string
		args []string
	}{
		{name: "plain", home: func(t *testing.T) string { return t.TempDir() }, args: []string{"metadata", "snippet"}},
		{name: "raw", home: func(t *testing.T) string { return t.TempDir() }, args: []string{"metadata", "snippet", "--raw"}},
		{name: "home path contains spaces (exercises shlex quoting)", home: func(t *testing.T) string {
			dir := filepath.Join(t.TempDir(), "dir with spaces")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			return dir
		}, args: []string{"metadata", "snippet"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home := tc.home(t)
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

func TestMetadataEnableDisableSync_unknownFlagOrExtraPositionalDoesNotRunDomain(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "enable unknown flag", args: []string{"metadata", "enable", "--unexpected"}, unknown: true},
		{name: "enable extra positional", args: []string{"metadata", "enable", "extra"}},
		{name: "enable extra after dashdash", args: []string{"metadata", "enable", "--", "extra"}},
		{name: "enable extra after --notify", args: []string{"metadata", "enable", "--notify", "extra"}},
		{name: "disable unknown flag", args: []string{"metadata", "disable", "--unexpected"}, unknown: true},
		{name: "disable --notify", args: []string{"metadata", "disable", "--notify"}, unknown: true},
		{name: "disable extra positional", args: []string{"metadata", "disable", "extra"}},
		{name: "sync unknown flag", args: []string{"metadata", "sync", "--unexpected"}, unknown: true},
		{name: "sync --notify", args: []string{"metadata", "sync", "--notify"}, unknown: true},
		{name: "sync extra positional", args: []string{"metadata", "sync", "extra"}},
		{name: "inbox unknown flag", args: []string{"metadata", "inbox", "--unexpected"}, unknown: true},
		{name: "inbox extra positional", args: []string{"metadata", "inbox", "extra"}},
		{name: "snippet unknown flag", args: []string{"metadata", "snippet", "--unexpected"}, unknown: true},
		{name: "status unknown flag", args: []string{"metadata", "status", "--unexpected"}, unknown: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home := writeDesignatedHome(t)
			before := readMetadataState(t, home)
			stdout, stderr, err := runMetadata(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertMetadataUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
			assertMetadataStateUnchanged(t, home, before)
		})
	}
}

func TestMetadataCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		herdr     bool
		success   bool
		usage     bool
		unknown   bool
		domainErr string
	}{
		{name: "valid snippet", args: []string{"metadata", "snippet"}, success: true},
		{name: "valid snippet after dashdash", args: []string{"metadata", "snippet", "--"}, success: true},
		{name: "valid snippet --raw", args: []string{"metadata", "snippet", "--raw"}, success: true},
		{name: "valid snippet --raw=", args: []string{"metadata", "snippet", "--raw=true"}, success: true},
		{name: "valid status", args: []string{"metadata", "status"}, success: true},
		{name: "valid status after dashdash", args: []string{"metadata", "status", "--"}, success: true},
		{name: "valid enable --notify", args: []string{"metadata", "enable", "--notify"}, herdr: true, domainErr: "registered coordinator"},
		{name: "valid enable --notify=", args: []string{"metadata", "enable", "--notify=true"}, herdr: true, domainErr: "registered coordinator"},
		{name: "metadata missing subcommand", args: []string{"metadata"}, usage: true},
		{name: "snippet extra positional", args: []string{"metadata", "snippet", "extra"}, usage: true},
		{name: "snippet unknown flag", args: []string{"metadata", "snippet", "--unexpected"}, usage: true, unknown: true},
		{name: "snippet extra after --raw", args: []string{"metadata", "snippet", "--raw", "extra"}, usage: true},
		{name: "status extra positional", args: []string{"metadata", "status", "extra"}, usage: true},
		{name: "status unknown flag", args: []string{"metadata", "status", "--unexpected"}, usage: true, unknown: true},
		{name: "enable extra positional", args: []string{"metadata", "enable", "extra"}, usage: true},
		{name: "enable unknown flag", args: []string{"metadata", "enable", "--unexpected"}, usage: true, unknown: true},
		{name: "enable extra after --notify", args: []string{"metadata", "enable", "--notify", "extra"}, usage: true},
		{name: "enable extra after dashdash", args: []string{"metadata", "enable", "--", "extra"}, usage: true},
		{name: "disable unknown flag", args: []string{"metadata", "disable", "--unexpected"}, usage: true, unknown: true},
		{name: "disable --notify", args: []string{"metadata", "disable", "--notify"}, usage: true, unknown: true},
		{name: "sync unknown flag", args: []string{"metadata", "sync", "--unexpected"}, usage: true, unknown: true},
		{name: "sync --notify", args: []string{"metadata", "sync", "--notify"}, usage: true, unknown: true},
		{name: "inbox unknown flag", args: []string{"metadata", "inbox", "--unexpected"}, usage: true, unknown: true},
		{name: "enable requires coordinator", args: []string{"metadata", "enable"}, herdr: true, domainErr: "registered coordinator"},
		{name: "enable requires pane without herdr", args: []string{"metadata", "enable"}, domainErr: "Herdr pane"},
		{name: "disable requires pane without herdr", args: []string{"metadata", "disable"}, domainErr: "Herdr pane"},
		{name: "sync requires pane without herdr", args: []string{"metadata", "sync"}, domainErr: "Herdr pane"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := writeDesignatedHome(t)
			if tc.herdr {
				herdrEnv(t, home)
				t.Setenv("HERDR_PANE_ID", "w-other:p1")
			} else {
				clearHerdrEnv(t)
			}
			before := readMetadataState(t, home)
			stdout, stderr, err := runMetadata(t, home, tc.args...)
			switch {
			case tc.success:
				if err != nil {
					t.Fatalf("err = %v stderr=%s stdout=%s", err, stderr, stdout)
				}
				if stdout == "" {
					t.Fatal("expected snippet or status output")
				}
			case tc.usage:
				assertMetadataUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				assertMetadataStateUnchanged(t, home, before)
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s", stdout)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.domainErr)
				}
				assertMetadataStateUnchanged(t, home, before)
			}
		})
	}
}

func runMetadata(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertMetadataUsageError(t *testing.T, err error) {
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
		strings.Contains(msg, "command is required") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid metadata")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func readMetadataState(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "metadata", "state.json"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertMetadataStateUnchanged(t *testing.T, home, before string) {
	t.Helper()
	if got := readMetadataState(t, home); got != before {
		t.Fatalf("metadata/state.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
	}
}
