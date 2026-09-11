package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
