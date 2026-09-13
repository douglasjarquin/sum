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

const executionFixtures = `
mkdir -p "$1/tasks/t-aaaaaaaaaaaa" "$1/tasks/t-bbbbbbbbbbbb" "$1/tasks/t-cccccccccccc" "$1/tasks/t-dddddddddddd"
cat > "$1/tasks/t-aaaaaaaaaaaa/task.json" <<'EOF'
{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repoA", "questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do thing A", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "task"}
EOF
cat > "$1/tasks/t-bbbbbbbbbbbb/task.json" <<'EOF'
{"schema": 1, "id": "t-bbbbbbbbbbbb", "status": "archived", "repository": "owner/repoB", "questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do thing B", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "task"}
EOF
cat > "$1/tasks/t-cccccccccccc/task.json" <<'EOF'
{
  "schema": 1, "id": "t-cccccccccccc", "status": "running", "repository": "owner/repoC",
  "questions": [], "evidence": [], "report": null, "notice": null, "attention": [],
  "brief": "do thing C", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "task",
  "execution": {
    "schema": 1,
    "worker": {"id": "x-0123456789ab", "kind": "worker", "state": "held", "generation": 1,
               "owner": {"machine": "m1", "session": "s1", "pane": "p1"}, "checkout": "/tmp/x",
               "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "observations": []},
    "verifiers": [
      {"id": "x-0123456789cd", "kind": "verifier", "state": "released", "generation": 1,
       "owner": {"machine": "m1", "session": "s1", "pane": "p2"}, "checkout": "/tmp/y",
       "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "observations": []},
      {"id": "x-0123456789ef", "kind": "verifier", "state": "running", "generation": 1,
       "owner": {"machine": "m1", "session": "s1", "pane": "p3"}, "checkout": "/tmp/z",
       "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "observations": []}
    ]
  }
}
EOF
`

func writeExecutionFixtures(t *testing.T, home string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", executionFixtures, "sh", home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture setup failed: %v\n%s", err, out)
	}
}

func TestStatusAndInbox_matchThePythonReferenceAcrossOccupancyScenarios(t *testing.T) {
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
		name    string
		fixture bool
		args    []string
	}{
		{name: "status on empty store", args: []string{"status"}},
		{name: "inbox on empty store", args: []string{"inbox"}},
		{name: "status with legacy/reservation/archived tasks", fixture: true, args: []string{"status"}},
		{name: "inbox with legacy/reservation/archived tasks", fixture: true, args: []string{"inbox"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.fixture {
				writeExecutionFixtures(t, home)
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

func TestStatus_malformedReservationMatchesPythonReference(t *testing.T) {
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

	home := t.TempDir()
	taskDir := filepath.Join(home, "tasks", "t-dddddddddddd")
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatal(err)
	}
	malformed := `{"schema": 1, "id": "t-dddddddddddd", "status": "running", "repository": "owner/repoD",
 "questions": [], "evidence": [], "report": null, "notice": null, "attention": [],
 "execution": {"schema": 1, "worker": {"id": "not-x-prefixed", "kind": "worker", "state": "held", "generation": 1,
               "owner": {"machine": "m1", "session": "s1", "pane": "p1"}, "checkout": "/tmp/x",
               "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "observations": []},
               "verifiers": []}}`
	if err := os.WriteFile(filepath.Join(taskDir, "task.json"), []byte(malformed), 0o600); err != nil {
		t.Fatal(err)
	}

	fullArgs := []string{"--home", home, "settings", "show"}
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
}

func TestStatusUnknownFlagsAreUsageErrors(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "status unknown flag", args: []string{"status", "--unexpected"}, unknown: true},
		{name: "status extra positional", args: []string{"status", "extra"}},
		{name: "status extra after dashdash", args: []string{"status", "--", "extra"}},
		{name: "status extra after --live", args: []string{"status", "--live", "extra"}},
		{name: "inbox unknown flag", args: []string{"inbox", "--unexpected"}, unknown: true},
		{name: "inbox extra positional", args: []string{"inbox", "extra"}},
		{name: "inbox extra after --live", args: []string{"inbox", "--live", "extra"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home := writeDesignatedHome(t)
			stdout, stderr, err := runStatus(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertStatusUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
		})
	}
}

func TestStatusCommands(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		success bool
		live    bool
		usage   bool
		unknown bool
	}{
		{name: "valid status", args: []string{"status"}, success: true},
		{name: "valid status after dashdash", args: []string{"status", "--"}, success: true},
		{name: "valid status --live", args: []string{"status", "--live"}, success: true, live: true},
		{name: "valid status --live=", args: []string{"status", "--live=true"}, success: true, live: true},
		{name: "valid inbox", args: []string{"inbox"}, success: true},
		{name: "valid inbox after dashdash", args: []string{"inbox", "--"}, success: true},
		{name: "valid inbox --live", args: []string{"inbox", "--live"}, success: true, live: true},
		{name: "valid inbox --live=", args: []string{"inbox", "--live=true"}, success: true, live: true},
		{name: "status extra positional", args: []string{"status", "extra"}, usage: true},
		{name: "status unknown flag", args: []string{"status", "--unexpected"}, usage: true, unknown: true},
		{name: "status extra after --live", args: []string{"status", "--live", "extra"}, usage: true},
		{name: "inbox extra positional", args: []string{"inbox", "extra"}, usage: true},
		{name: "inbox unknown flag", args: []string{"inbox", "--unexpected"}, usage: true, unknown: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home := writeDesignatedHome(t)
			stdout, stderr, err := runStatus(t, home, tc.args...)
			switch {
			case tc.success:
				if err != nil {
					t.Fatalf("err = %v stderr=%s stdout=%s", err, stderr, stdout)
				}
				if stdout == "" {
					t.Fatal("expected status output")
				}
				value := decodeObject(t, stdout)
				if tc.live {
					if value["live"] != true {
						t.Fatalf("live = %v, want true", value["live"])
					}
				} else if value["live"] != false {
					t.Fatalf("live = %v, want false", value["live"])
				}
			default:
				assertStatusUsageError(t, err)
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

func runStatus(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertStatusUsageError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected usage failure")
	}
	msg := err.Error()
	usage := strings.Contains(msg, "unknown flag") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "accepts 0 arg") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid status") ||
		strings.Contains(msg, "invalid inbox")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}
