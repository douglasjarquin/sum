package cli

import (
	"bytes"
	"context"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateUnknownFlagsAreUsageErrorsBeforeApply(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "status unknown flag", args: []string{"update", "status", "--unexpected"}, unknown: true},
		{name: "status extra positional", args: []string{"update", "status", "extra"}},
		{name: "status extra after dashdash", args: []string{"update", "status", "--", "extra"}},
		{name: "apply unknown flag", args: []string{"update", "apply", "--unexpected"}, unknown: true},
		{name: "apply unknown flag after --no-fetch", args: []string{"update", "apply", "--no-fetch", "--unexpected"}, unknown: true},
		{name: "apply extra positional", args: []string{"update", "apply", "extra"}},
		{name: "apply extra after --ref", args: []string{"update", "apply", "--ref", "HEAD", "extra"}},
		{name: "apply missing --ref value", args: []string{"update", "apply", "--ref"}},
		{name: "check unknown flag", args: []string{"update", "check", "--unexpected"}, unknown: true},
		{name: "stage unknown flag", args: []string{"update", "stage", "--no-fetch", "--unexpected"}, unknown: true},
		{name: "rollback unknown flag", args: []string{"update", "rollback", "--unexpected"}, unknown: true},
		{name: "rollback extra after --to", args: []string{"update", "rollback", "--to", "checkout", "extra"}},
		{name: "rollback missing --to value", args: []string{"update", "rollback", "--to"}},
		{name: "recover unknown flag", args: []string{"update", "recover", "--generation", "g1", "--unexpected"}, unknown: true},
		{name: "recover extra positional", args: []string{"update", "recover", "--generation", "g1", "extra"}},
		{name: "recover missing --generation value", args: []string{"update", "recover", "--generation"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearNativeUpdateEnv(t)
			home, before := updateUsageLab(t)
			stdout, stderr, err := runUpdateCLI(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertUpdateUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
			assertUpdateStateUnchanged(t, home, before)
		})
	}
}

func TestUpdateCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		hc        bool
		usage     bool
		unknown   bool
		domainErr string
	}{
		{name: "valid status", args: []string{"update", "status"}, domainErr: "is not a sum installation's state home"},
		{name: "valid status after dashdash", args: []string{"update", "status", "--"}, domainErr: "is not a sum installation's state home"},
		{name: "valid check --ref --no-fetch", args: []string{"update", "check", "--ref", "HEAD", "--no-fetch"}, domainErr: "is not a sum installation's state home"},
		{name: "valid check --ref= --no-fetch=", args: []string{"update", "check", "--ref=HEAD", "--no-fetch=true"}, domainErr: "is not a sum installation's state home"},
		{name: "valid stage --no-fetch", args: []string{"update", "stage", "--no-fetch"}, domainErr: "is not a sum installation's state home"},
		{name: "valid apply --ref --no-fetch with HC", args: []string{"update", "apply", "--ref", "HEAD", "--no-fetch"}, hc: true, domainErr: "registered coordinator"},
		{name: "valid apply --ref= --no-fetch= with HC", args: []string{"update", "apply", "--ref=-main", "--no-fetch=true"}, hc: true, domainErr: "registered coordinator"},
		{name: "valid rollback", args: []string{"update", "rollback"}, domainErr: "explicit HC session"},
		{name: "valid rollback --to checkout", args: []string{"update", "rollback", "--to", "checkout"}, domainErr: "explicit HC session"},
		{name: "valid rollback --to=", args: []string{"update", "rollback", "--to=checkout"}, domainErr: "explicit HC session"},
		{name: "valid recover --generation", args: []string{"update", "recover", "--generation", "g1"}, domainErr: "explicit HC session"},
		{name: "valid recover --generation=", args: []string{"update", "recover", "--generation=g1"}, domainErr: "explicit HC session"},
		{name: "valid recover with HC", args: []string{"update", "recover", "--generation", "g1"}, hc: true, domainErr: "registered coordinator"},
		{name: "update missing subcommand", args: []string{"update"}, usage: true},
		{name: "status extra positional", args: []string{"update", "status", "extra"}, usage: true},
		{name: "status unknown flag", args: []string{"update", "status", "--unexpected"}, usage: true, unknown: true},
		{name: "apply unknown flag", args: []string{"update", "apply", "--unexpected"}, usage: true, unknown: true},
		{name: "apply extra after --no-fetch", args: []string{"update", "apply", "--no-fetch", "extra"}, usage: true},
		{name: "apply missing --ref value", args: []string{"update", "apply", "--ref"}, usage: true},
		{name: "rollback unknown flag", args: []string{"update", "rollback", "--unexpected"}, usage: true, unknown: true},
		{name: "rollback missing --to value", args: []string{"update", "rollback", "--to"}, usage: true},
		{name: "recover missing --generation", args: []string{"update", "recover"}, usage: true},
		{name: "recover missing --generation value", args: []string{"update", "recover", "--generation"}, usage: true},
		{name: "recover unknown flag", args: []string{"update", "recover", "--generation", "g1", "--unexpected"}, usage: true, unknown: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, before := updateUsageLab(t)
			clearNativeUpdateEnv(t)
			if tc.hc {
				nativeUpdateEnv(t)
			}
			stdout, stderr, err := runUpdateCLI(t, home, tc.args...)
			switch {
			case tc.usage:
				assertUpdateUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				assertUpdateStateUnchanged(t, home, before)
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s stderr=%s", stdout, stderr)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.domainErr)
				}
				assertUpdateStateUnchanged(t, home, before)
			}
		})
	}
}

func updateUsageLab(t *testing.T) (home, before string) {
	t.Helper()
	home = filepath.Join(t.TempDir(), "state")
	s, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Machine(); err != nil {
		t.Fatal(err)
	}
	before = readUpdateState(t, home)
	return home, before
}

func runUpdateCLI(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertUpdateUsageError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected usage failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "registered coordinator") || strings.Contains(msg, "explicit HC session") || strings.Contains(msg, "is not a sum installation's state home") {
		t.Fatalf("err = %v, want a usage failure", err)
	}
	usage := strings.Contains(msg, "unknown flag") ||
		strings.Contains(msg, "accepts 0 arg") ||
		strings.Contains(msg, "flag needs an argument") ||
		strings.Contains(msg, "command is required") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid update")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func readUpdateState(t *testing.T, home string) string {
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

func assertUpdateStateUnchanged(t *testing.T, home, before string) {
	t.Helper()
	if got := readUpdateState(t, home); got != before {
		t.Fatalf("state changed:\nbefore:\n%s\nafter:\n%s", before, got)
	}
}

func TestNativeUpdateHasNoLegacyCapabilityOverride(t *testing.T) {
	for _, command := range []string{"apply", "rollback", "recover"} {
		t.Run(command, func(t *testing.T) {
			root := NewRoot("", &bytes.Buffer{}, &bytes.Buffer{})
			root.SetArgs([]string{"update", command, "--allow-pre-machine-identity"})
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("legacy capability override remains exposed: %v", err)
			}
		})
	}
}

func clearNativeUpdateEnv(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "HERDR_") {
			t.Setenv(key, "")
		}
	}
	for _, key := range []string{"SUM_HOME", "SUM_SESSION", "SUM_CORE_ID", "SUM_ROLE", "SUM_TASK", "SUM_ATTEMPT", "SUM_BINDING_GENERATION", "HC_SESSION", "HC_SOCKET", "HC_TERMINAL_ID"} {
		t.Setenv(key, "")
	}
}
func nativeUpdateEnv(t *testing.T) {
	t.Helper()
	fixture := hctest.New(t)
	fixture.SetTerminal(hcclient.Terminal{Address: "t1", CreatedMS: 1, State: "live", Cwd: t.TempDir()})
	for key, value := range map[string]string{"SUM_SESSION": "update-lab", "SUM_CORE_ID": fixture.CoreID, "SUM_ROLE": "coordinator", "SUM_ATTEMPT": "coordinator-one", "SUM_BINDING_GENERATION": "1", "HC_SOCKET": fixture.Socket, "HC_TERMINAL_ID": "t1"} {
		t.Setenv(key, value)
	}
}
