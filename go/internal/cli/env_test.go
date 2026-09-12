package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeEnvironmentFixture(t *testing.T, home, taskID, environmentJSON string) {
	t.Helper()
	dir := filepath.Join(home, "tasks", taskID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "environment.json"), []byte(environmentJSON), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEnvShow_matchesThePythonReferenceAcrossScenarios(t *testing.T) {
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

	baseSha := "0123456789abcdef0123456789abcdef01234567"
	taskFixture := fmt.Sprintf(`{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repoA", "questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md"}`, baseSha)

	t.Run("no environment record", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-aaaaaaaaaaaa", taskFixture)
		assertEnvShowMatches(t, reference, home, []string{"env", "show", "t-aaaaaaaaaaaa"})
	})

	t.Run("present record with discovery, secret-shaped command, endpoints, logs, resources, services", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-aaaaaaaaaaaa", taskFixture)
		environmentJSON := `{
  "schema": 1, "task": "t-aaaaaaaaaaaa", "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:05:00+00:00",
  "discovery": {
    "observed_at": "2026-01-01T00:00:00+00:00", "head": "abc123", "config_revision": "r1", "current_revision": "r1",
    "stale": false, "stale_reason": null, "checked_at": "2026-01-01T00:00:00+00:00",
    "summary": ["mise.toml declares dev, test"], "problems": [], "task_origins": [], "verification_contract": "VERIFY.md",
    "sources": [{"path": "mise.toml", "bytes": 120, "sha256": "deadbeef", "skipped": false}],
    "commands": [
      {"name": "dev", "kind": "service", "source": "mise.toml", "description": "Start the dev server", "image": null, "declared_ports": [3000],
       "command": "PORT=3000 API_KEY=AKIAABCDEFGHIJ123456 npm run dev"},
      {"name": "test", "kind": "verification", "source": "mise.toml", "description": null, "image": null, "declared_ports": [],
       "command": "npm test", "redactions": 0}
    ]
  },
  "endpoints": [
    {"id": "e1", "url": "http://127.0.0.1:3000", "port": 3000, "local": true, "label": "dev server", "ownership": "owned",
     "claimed_ownership": null, "state": "observed", "stale_reason": null, "config_stale": false, "observed_at": "2026-01-01T00:05:00+00:00",
     "recorded_by": "worker", "observation": {"listeners": [{"pid": 1234, "owner": "node"}]}, "conflicts": []},
    {"id": "e2", "url": "http://127.0.0.1:9999", "port": 9999, "local": true, "label": "stale one", "ownership": "shared",
     "claimed_ownership": "owned", "state": "stale", "stale_reason": "config changed", "config_stale": true, "observed_at": "2026-01-01T00:01:00+00:00",
     "recorded_by": "worker", "observation": null, "conflicts": [{"task": "t-other"}]}
  ],
  "logs": [
    {"id": "l1", "path": "/tmp/dev.log", "scope": "checkout", "label": "dev log", "ownership": "owned", "state": "present", "bytes": 512, "modified_at": "2026-01-01T00:04:00+00:00", "observed_at": "2026-01-01T00:05:00+00:00"}
  ],
  "resources": [
    {"kind": "pane", "id": "p1", "session": "s1", "label": "dev pane", "ownership": "owned", "state": "present", "cwd": "/tmp/checkout", "note": null, "service": "dev", "observed_at": "2026-01-01T00:05:00+00:00"}
  ],
  "services": [
    {"id": "svc1", "name": "dev", "source": "mise.toml", "kind": "service", "state": "running", "url": "http://127.0.0.1:3000", "port": 3000,
     "pane": "p1", "workspace": "w1", "label": "dev server", "intent_at": "2026-01-01T00:04:00+00:00", "launched_at": "2026-01-01T00:04:30+00:00",
     "stopped_at": null, "exit_verified": false, "by": "worker",
     "launch": {"via": "runner", "runner": "mise", "project": "owner/repoA", "pane_created": true},
     "process": {"pid": 1234, "name": "node", "shell_pid": 1200, "observed_at": "2026-01-01T00:04:30+00:00"},
     "readiness": {"ready": true, "checked": true, "waited_s": 2.5, "reason": null},
     "stop": null},
    {"id": "svc2", "name": "worker2", "source": "mise.toml", "kind": "service", "state": "unknown", "url": null, "port": null,
     "pane": "p2", "workspace": "w1", "label": null, "intent_at": "2026-01-01T00:00:00+00:00", "launched_at": null,
     "stopped_at": null, "exit_verified": false, "by": "worker",
     "launch": {"via": "runner", "runner": "mise", "project": "owner/repoA", "pane_created": false},
     "process": {}, "readiness": {}, "stop": {}}
  ],
  "history": [
    {"at": "2026-01-01T00:00:00+00:00", "kind": "discover"},
    {"at": "2026-01-01T00:01:00+00:00", "kind": "record"},
    {"at": "2026-01-01T00:02:00+00:00", "kind": "record"},
    {"at": "2026-01-01T00:03:00+00:00", "kind": "record"},
    {"at": "2026-01-01T00:04:00+00:00", "kind": "start"},
    {"at": "2026-01-01T00:05:00+00:00", "kind": "record"}
  ]
}`
		writeEnvironmentFixture(t, home, "t-aaaaaaaaaaaa", environmentJSON)
		assertEnvShowMatches(t, reference, home, []string{"env", "show", "t-aaaaaaaaaaaa"})
	})

	t.Run("--max-chars truncates a long discovered command", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-aaaaaaaaaaaa", taskFixture)
		longCommand := "npm run dev -- " + strings.Repeat("x", 200)
		environmentJSON := fmt.Sprintf(`{
  "schema": 1, "task": "t-aaaaaaaaaaaa", "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:05:00+00:00",
  "discovery": {
    "observed_at": "2026-01-01T00:00:00+00:00", "head": "abc123", "config_revision": "r1", "current_revision": "r1",
    "stale": false, "stale_reason": null, "checked_at": "2026-01-01T00:00:00+00:00",
    "summary": [], "problems": [], "task_origins": [], "verification_contract": "VERIFY.md",
    "sources": [],
    "commands": [{"name": "dev", "kind": "service", "source": "mise.toml", "description": null, "image": null, "declared_ports": [], "command": %q}]
  },
  "endpoints": [], "logs": [], "resources": [], "services": [], "history": []
}`, longCommand)
		writeEnvironmentFixture(t, home, "t-aaaaaaaaaaaa", environmentJSON)
		assertEnvShowMatches(t, reference, home, []string{"env", "show", "t-aaaaaaaaaaaa", "--max-chars", "40"})
	})

	t.Run("--max-chars 0 is unbounded", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-aaaaaaaaaaaa", taskFixture)
		longCommand := "npm run dev -- " + strings.Repeat("x", 200)
		environmentJSON := fmt.Sprintf(`{
  "schema": 1, "task": "t-aaaaaaaaaaaa", "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:05:00+00:00",
  "discovery": {
    "observed_at": "2026-01-01T00:00:00+00:00", "head": "abc123", "config_revision": "r1", "current_revision": "r1",
    "stale": false, "stale_reason": null, "checked_at": "2026-01-01T00:00:00+00:00",
    "summary": [], "problems": [], "task_origins": [], "verification_contract": "VERIFY.md",
    "sources": [],
    "commands": [{"name": "dev", "kind": "service", "source": "mise.toml", "description": null, "image": null, "declared_ports": [], "command": %q}]
  },
  "endpoints": [], "logs": [], "resources": [], "services": [], "history": []
}`, longCommand)
		writeEnvironmentFixture(t, home, "t-aaaaaaaaaaaa", environmentJSON)
		assertEnvShowMatches(t, reference, home, []string{"env", "show", "t-aaaaaaaaaaaa", "--max-chars", "0"})
	})

	t.Run("malformed sidecar: schema mismatch surfaces as present:false, ok:false", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-aaaaaaaaaaaa", taskFixture)
		writeEnvironmentFixture(t, home, "t-aaaaaaaaaaaa", `{"schema": 2, "task": "t-aaaaaaaaaaaa"}`)
		assertEnvShowMatches(t, reference, home, []string{"env", "show", "t-aaaaaaaaaaaa"})
	})
}

func assertEnvShowMatches(t *testing.T, reference, home string, args []string) {
	t.Helper()
	fullArgs := append([]string{"--home", home}, args...)
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

func TestEnvDiscover_requiresRecordedTask(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	root := NewRoot("", &stdout, &stderr)
	root.SetArgs([]string{"--home", home, "env", "discover", "t-aaaaaaaaaaaa"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected missing task to fail")
	}
	if !strings.Contains(err.Error(), "t-aaaaaaaaaaaa") {
		t.Fatalf("err = %v", err)
	}
}
