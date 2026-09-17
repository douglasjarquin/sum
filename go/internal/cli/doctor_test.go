package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var isoTimestamp = regexp.MustCompile(`"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\+00:00"`)

func normalizeTimestamps(s string) string {
	return isoTimestamp.ReplaceAllString(s, `"<at>"`)
}

// Safe against the live installation: doctor is documented "never binds" and
// this port only reads (tool lookups, a herdr pane-get, file existence
// checks) — the same observation `./bin/sumctl doctor` already performs here.
func TestDoctor_extraArgsAreGoErrors(t *testing.T) {
	clearHerdrEnv(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRoot("", &bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"--home", home, "doctor", "--unexpected"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected unrecognized arguments")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("err = %v, want unknown flag", err)
	}
}

func TestDoctor_unknownFlagOrExtraPositionalDoesNotCallDoctor(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "unknown flag", args: []string{"doctor", "--unexpected"}, unknown: true},
		{name: "unknown --json", args: []string{"doctor", "--json"}, unknown: true},
		{name: "extra positional", args: []string{"doctor", "extra"}},
		{name: "extra after dashdash", args: []string{"doctor", "--", "extra"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home := writeDesignatedHome(t)
			stdout, stderr, err := runDoctor(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertDoctorUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("Doctor wrote stdout: %s", stdout)
			}
		})
	}
}

func TestDoctorCommands(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		herdr   bool
		success bool
		usage   bool
		unknown bool
	}{
		{name: "valid doctor", args: []string{"doctor"}, success: true},
		{name: "valid doctor after dashdash", args: []string{"doctor", "--"}, success: true},
		{name: "valid doctor with fake pane", args: []string{"doctor"}, herdr: true, success: true},
		{name: "extra positional", args: []string{"doctor", "extra"}, usage: true},
		{name: "unknown flag", args: []string{"doctor", "--unexpected"}, usage: true, unknown: true},
		{name: "unknown --json", args: []string{"doctor", "--json"}, usage: true, unknown: true},
		{name: "extra after dashdash", args: []string{"doctor", "--", "extra"}, usage: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := writeDesignatedHome(t)
			if tc.herdr {
				herdrEnv(t, home)
			} else {
				clearHerdrEnv(t)
			}
			stdout, stderr, err := runDoctor(t, home, tc.args...)
			switch {
			case tc.success:
				if !doctorRan(err) {
					t.Fatalf("err = %v stderr=%s stdout=%s", err, stderr, stdout)
				}
				if stdout == "" {
					t.Fatal("expected doctor output")
				}
				if !strings.Contains(stdout, "ok") {
					t.Fatalf("stdout missing ok:\n%s", stdout)
				}
			default:
				assertDoctorUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
			}
		})
	}
}

func runDoctor(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func doctorRan(err error) bool {
	if err == nil {
		return true
	}
	_, ok := err.(*ExitError)
	return ok
}

func assertDoctorUsageError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected usage failure")
	}
	if _, ok := err.(*ExitError); ok {
		t.Fatalf("err = %v, want a usage failure", err)
	}
	msg := err.Error()
	usage := strings.Contains(msg, "unknown flag") ||
		strings.Contains(msg, "accepts 0 arg") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid doctor")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}
