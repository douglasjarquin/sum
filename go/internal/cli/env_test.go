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

func TestEnvUnknownFlagsAreUsageErrorsBeforeDomain(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "show unknown flag", args: []string{"env", "show", "t-aaaaaaaaaaaa", "--unexpected"}, unknown: true},
		{name: "show unknown flag before task", args: []string{"env", "show", "--unexpected", "t-aaaaaaaaaaaa"}, unknown: true},
		{name: "show extra positional", args: []string{"env", "show", "t-aaaaaaaaaaaa", "extra"}},
		{name: "show extra after dashdash", args: []string{"env", "show", "t-aaaaaaaaaaaa", "--", "extra"}},
		{name: "discover unknown flag", args: []string{"env", "discover", "t-aaaaaaaaaaaa", "--unexpected"}, unknown: true},
		{name: "discover extra positional", args: []string{"env", "discover", "t-aaaaaaaaaaaa", "extra"}},
		{name: "inspect unknown flag", args: []string{"env", "inspect", "t-aaaaaaaaaaaa", "--unexpected"}, unknown: true},
		{name: "inspect extra positional", args: []string{"env", "inspect", "t-aaaaaaaaaaaa", "extra"}},
		{name: "record unknown flag", args: []string{"env", "record", "t-aaaaaaaaaaaa", "--url", "http://127.0.0.1:3000", "--unexpected"}, unknown: true},
		{name: "record unknown flag before task", args: []string{"env", "record", "--unexpected", "t-aaaaaaaaaaaa"}, unknown: true},
		{name: "record extra positional", args: []string{"env", "record", "t-aaaaaaaaaaaa", "extra"}},
		{name: "record extra after --url", args: []string{"env", "record", "t-aaaaaaaaaaaa", "--url", "http://127.0.0.1:3000", "extra"}},
		{name: "start unknown flag", args: []string{"env", "start", "t-aaaaaaaaaaaa", "--command", "dev", "--unexpected"}, unknown: true},
		{name: "start unknown flag before task", args: []string{"env", "start", "--unexpected", "t-aaaaaaaaaaaa", "--command", "dev"}, unknown: true},
		{name: "start extra positional", args: []string{"env", "start", "t-aaaaaaaaaaaa", "--command", "dev", "extra"}},
		{name: "stop unknown flag", args: []string{"env", "stop", "t-aaaaaaaaaaaa", "--unexpected"}, unknown: true},
		{name: "stop extra positional", args: []string{"env", "stop", "t-aaaaaaaaaaaa", "extra"}},
		{name: "stop extra after --timeout", args: []string{"env", "stop", "t-aaaaaaaaaaaa", "--timeout", "5", "extra"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home, taskID, before := envUsageLab(t)
			stdout, stderr, err := runEnv(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertEnvUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
			assertEnvSidecarUnchanged(t, home, taskID, before)
		})
	}
}

func TestEnvCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		success   bool
		usage     bool
		unknown   bool
		domainErr string
	}{
		{name: "valid show", args: []string{"env", "show", "t-aaaaaaaaaaaa"}, success: true},
		{name: "valid show after dashdash", args: []string{"env", "show", "--", "t-aaaaaaaaaaaa"}, success: true},
		{name: "valid show --max-chars", args: []string{"env", "show", "t-aaaaaaaaaaaa", "--max-chars", "40"}, success: true},
		{name: "valid show --max-chars=", args: []string{"env", "show", "t-aaaaaaaaaaaa", "--max-chars=0"}, success: true},
		{name: "valid discover", args: []string{"env", "discover", "t-aaaaaaaaaaaa"}, domainErr: "recorded worktree"},
		{name: "valid inspect", args: []string{"env", "inspect", "t-aaaaaaaaaaaa"}, domainErr: "recorded worktree"},
		{name: "valid record --url", args: []string{"env", "record", "t-aaaaaaaaaaaa", "--url", "http://127.0.0.1:3000"}, domainErr: "recorded worktree"},
		{name: "valid record --url=", args: []string{"env", "record", "t-aaaaaaaaaaaa", "--url=http://127.0.0.1:3000"}, domainErr: "recorded worktree"},
		{name: "valid start --command", args: []string{"env", "start", "t-aaaaaaaaaaaa", "--command", "dev"}, domainErr: "another machine"},
		{name: "valid start --command=", args: []string{"env", "start", "t-aaaaaaaaaaaa", "--command=dev", "--timeout=5"}, domainErr: "another machine"},
		{name: "valid stop --timeout", args: []string{"env", "stop", "t-aaaaaaaaaaaa", "--timeout", "5"}, domainErr: "another machine"},
		{name: "valid stop --service=", args: []string{"env", "stop", "t-aaaaaaaaaaaa", "--service=svc1"}, domainErr: "another machine"},
		{name: "env missing subcommand", args: []string{"env"}, usage: true},
		{name: "show missing task", args: []string{"env", "show"}, usage: true},
		{name: "discover missing task", args: []string{"env", "discover"}, usage: true},
		{name: "inspect missing task", args: []string{"env", "inspect"}, usage: true},
		{name: "record missing task", args: []string{"env", "record"}, usage: true},
		{name: "start missing task", args: []string{"env", "start", "--command", "dev"}, usage: true},
		{name: "start missing --command", args: []string{"env", "start", "t-aaaaaaaaaaaa"}, usage: true},
		{name: "stop missing task", args: []string{"env", "stop"}, usage: true},
		{name: "show extra positional", args: []string{"env", "show", "t-aaaaaaaaaaaa", "extra"}, usage: true},
		{name: "show unknown flag", args: []string{"env", "show", "t-aaaaaaaaaaaa", "--unexpected"}, usage: true, unknown: true},
		{name: "discover unknown flag", args: []string{"env", "discover", "t-aaaaaaaaaaaa", "--unexpected"}, usage: true, unknown: true},
		{name: "inspect unknown flag", args: []string{"env", "inspect", "t-aaaaaaaaaaaa", "--unexpected"}, usage: true, unknown: true},
		{name: "record unknown flag", args: []string{"env", "record", "t-aaaaaaaaaaaa", "--unexpected"}, usage: true, unknown: true},
		{name: "record missing --url value", args: []string{"env", "record", "t-aaaaaaaaaaaa", "--url"}, usage: true},
		{name: "start unknown flag", args: []string{"env", "start", "t-aaaaaaaaaaaa", "--command", "dev", "--unexpected"}, usage: true, unknown: true},
		{name: "start missing --command value", args: []string{"env", "start", "t-aaaaaaaaaaaa", "--command"}, usage: true},
		{name: "stop unknown flag", args: []string{"env", "stop", "t-aaaaaaaaaaaa", "--unexpected"}, usage: true, unknown: true},
		{name: "stop invalid --timeout", args: []string{"env", "stop", "t-aaaaaaaaaaaa", "--timeout", "nope"}, usage: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home, taskID, before := envUsageLab(t)
			stdout, stderr, err := runEnv(t, home, tc.args...)
			switch {
			case tc.success:
				if err != nil {
					t.Fatalf("err = %v stderr=%s stdout=%s", err, stderr, stdout)
				}
				if !strings.Contains(stdout, taskID) {
					t.Fatalf("stdout missing task id:\n%s", stdout)
				}
			case tc.usage:
				assertEnvUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				assertEnvSidecarUnchanged(t, home, taskID, before)
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s", stdout)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.domainErr)
				}
				assertEnvSidecarUnchanged(t, home, taskID, before)
			}
		})
	}
}

func runEnv(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertEnvUsageError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected usage failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "recorded worktree") || strings.Contains(msg, "another machine") || strings.Contains(msg, "Give exactly one") {
		t.Fatalf("err = %v, want a usage failure", err)
	}
	usage := strings.Contains(msg, "unknown flag") ||
		strings.Contains(msg, "accepts 1 arg") ||
		strings.Contains(msg, "accepts 0 arg") ||
		strings.Contains(msg, "flag needs an argument") ||
		strings.Contains(msg, "required flag") ||
		strings.Contains(msg, "invalid argument") ||
		strings.Contains(msg, "command is required") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid env")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func envUsageLab(t *testing.T) (home, taskID, before string) {
	t.Helper()
	home = writeDesignatedHome(t)
	taskID = "t-aaaaaaaaaaaa"
	writeTaskFixture(t, home, taskID, `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repoA", "questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "task", "brief_path": "brief.md"}`)
	writeEnvironmentFixture(t, home, taskID, `{"schema": 1, "task": "t-aaaaaaaaaaaa", "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "discovery": {"observed_at": "2026-01-01T00:00:00+00:00", "head": "abc123", "config_revision": "r1", "current_revision": "r1", "stale": false, "stale_reason": null, "checked_at": "2026-01-01T00:00:00+00:00", "summary": [], "problems": [], "task_origins": [], "verification_contract": null, "sources": [], "commands": []}, "endpoints": [], "logs": [], "resources": [], "services": [], "history": []}`)
	before = readEnvSidecar(t, home, taskID)
	return home, taskID, before
}

func readEnvSidecar(t *testing.T, home, taskID string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "tasks", taskID, "environment.json"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertEnvSidecarUnchanged(t *testing.T, home, taskID, before string) {
	t.Helper()
	if got := readEnvSidecar(t, home, taskID); got != before {
		t.Fatalf("environment.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
	}
}
