package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var readAtPattern = regexp.MustCompile(`"read_at": "[^"]*"`)

func normalizeReadAt(s string) string {
	return readAtPattern.ReplaceAllString(s, `"read_at": "<at>"`)
}

func TestContext_matchesThePythonReferenceAcrossScenarios(t *testing.T) {
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

	t.Run("minimal task: no worktree, evidence, notes, or environment record", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-aaaaaaaaaaaa", fmt.Sprintf(`{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repoA",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00"}`, baseSha))
		assertContextMatches(t, reference, home, "t-aaaaaaaaaaaa")
	})

	t.Run("real worktree with a current handoff and a passing root verification", func(t *testing.T) {
		home := t.TempDir()
		root := t.TempDir()
		head := initGitRepoWithCommit(t, filepath.Join(root, "checkout"))
		taskJSON := fmt.Sprintf(`{"schema": 1, "id": "t-bbbbbbbbbbbb", "status": "reported", "repository": "owner/repoB",
"questions": [], "notice": null, "attention": [], "brief": "do thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"report": null, "worktree": %q, "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:10:00+00:00",
"evidence": [
  {"schema": 1, "id": "e-0000000001", "kind": "handoff", "source": "worker", "at": "2026-01-01T00:05:00+00:00",
   "candidate": %q, "brief_revision": null, "sum_version": "0.1.0", "endpoint": null, "handoff": {"outcome": "done", "next_action": null}},
  {"schema": 1, "id": "e-0000000002", "kind": "verification", "source": "coordinator", "at": "2026-01-01T00:08:00+00:00",
   "candidate": %q, "brief_revision": null, "sum_version": "0.1.0", "endpoint": null, "run_id": "run-root-1", "result": "pass", "outcome": "all green"}
]}`, baseSha, filepath.Join(root, "checkout"), head, head)
		writeTaskFixture(t, home, "t-bbbbbbbbbbbb", taskJSON)
		assertContextMatches(t, reference, home, "t-bbbbbbbbbbbb")
	})

	t.Run("open and applied decisions, notes, and an environment record", func(t *testing.T) {
		home := t.TempDir()
		taskJSON := fmt.Sprintf(`{"schema": 1, "id": "t-cccccccccccc", "status": "running", "repository": "owner/repoC",
"notice": null, "attention": [{"id": "a1", "kind": "blocked", "status": "open", "at": "2026-01-01T00:01:00+00:00", "observed": null}],
"brief": "do thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md", "report": null, "evidence": [],
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:02:00+00:00",
"questions": [
  {"id": "q1", "key": "approach", "status": "open", "created_at": "2026-01-01T00:00:30+00:00", "text": "which way?", "answer": null},
  {"id": "q2", "key": "scope", "status": "applied", "created_at": "2026-01-01T00:00:10+00:00", "answered_at": "2026-01-01T00:00:20+00:00",
   "applied_at": "2026-01-01T00:00:25+00:00", "text": "in scope?", "answer": "yes"}
]}`, baseSha)
		writeTaskFixture(t, home, "t-cccccccccccc", taskJSON)
		if err := os.WriteFile(filepath.Join(home, "tasks", "t-cccccccccccc", "notes.md"),
			[]byte("# Notes for t-cccccccccccc\n\n## 2026-01-01T00:00:15+00:00 worker p1\n\nfound the thing\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		writeEnvironmentFixture(t, home, "t-cccccccccccc", `{
  "schema": 1, "task": "t-cccccccccccc", "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:01:00+00:00",
  "discovery": {"observed_at": "2026-01-01T00:00:00+00:00", "head": "abc", "config_revision": "r1", "current_revision": "r1",
    "stale": true, "stale_reason": "config changed", "checked_at": "2026-01-01T00:00:00+00:00", "summary": [], "problems": [],
    "task_origins": [], "verification_contract": null, "sources": [], "commands": []},
  "endpoints": [{"id": "e1", "url": "http://127.0.0.1:3000", "port": 3000, "local": true, "label": null, "ownership": "owned",
    "claimed_ownership": null, "state": "observed", "stale_reason": null, "config_stale": false, "observed_at": "2026-01-01T00:00:30+00:00",
    "recorded_by": "worker", "observation": null, "conflicts": []}],
  "logs": [], "resources": [],
  "services": [{"id": "svc1", "name": "dev", "source": "mise.toml", "kind": "service", "state": "running", "url": null, "port": null,
    "pane": null, "workspace": null, "label": null, "intent_at": "2026-01-01T00:00:00+00:00", "launched_at": null, "stopped_at": null,
    "exit_verified": false, "by": "worker", "launch": null, "process": null, "readiness": null, "stop": null}],
  "history": []
}`)
		assertContextMatches(t, reference, home, "t-cccccccccccc")
	})
}

func TestContextSections_matchThePythonReferenceAcrossScenarios(t *testing.T) {
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

	t.Run("decisions and returns together, with open and applied questions", func(t *testing.T) {
		home := t.TempDir()
		taskJSON := fmt.Sprintf(`{"schema": 1, "id": "t-eeeeeeeeeeee", "status": "running", "repository": "owner/repoE",
"notice": null, "attention": [], "brief": "do thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md", "report": null, "evidence": [],
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:02:00+00:00",
"questions": [
  {"id": "q1", "key": "approach", "status": "open", "created_at": "2026-01-01T00:00:30+00:00", "text": "which way?", "answer": null},
  {"id": "q2", "key": "scope", "status": "applied", "created_at": "2026-01-01T00:00:10+00:00", "answered_at": "2026-01-01T00:00:20+00:00",
   "applied_at": "2026-01-01T00:00:25+00:00", "text": "in scope?", "answer": "yes"}
]}`, baseSha)
		writeTaskFixture(t, home, "t-eeeeeeeeeeee", taskJSON)
		assertContextSectionsMatch(t, reference, home, "t-eeeeeeeeeeee", "decisions", "returns")
	})

	t.Run("handoff and evidence together, real worktree, multiple evidence kinds", func(t *testing.T) {
		home := t.TempDir()
		root := t.TempDir()
		head := initGitRepoWithCommit(t, filepath.Join(root, "checkout"))
		taskJSON := fmt.Sprintf(`{"schema": 1, "id": "t-ffffffffffff", "status": "reported", "repository": "owner/repoF",
"questions": [], "notice": null, "attention": [], "brief": "do thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"report": {"text": "done", "submitted_at": "2026-01-01T00:20:00+00:00", "brief_revision": "legacy", "candidate": %q, "sum_version": "0.1.0"},
"worktree": %q, "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:10:00+00:00",
"evidence": [
  {"schema": 1, "id": "e-0000000001", "kind": "handoff", "source": "worker", "at": "2026-01-01T00:05:00+00:00",
   "candidate": %q, "brief_revision": null, "sum_version": "0.1.0", "endpoint": null,
   "handoff": {"outcome": "done", "next_action": "review it", "files": ["a.go", "b.go"], "checks": [{"command": "go test ./...", "exit": 0, "note": "all green"}]}},
  {"schema": 1, "id": "e-0000000002", "kind": "verification", "source": "coordinator", "at": "2026-01-01T00:08:00+00:00",
   "candidate": %q, "brief_revision": null, "sum_version": "0.1.0", "endpoint": null, "run_id": "run-root-1", "result": "pass", "outcome": "all green"},
  {"schema": 1, "id": "e-0000000003", "kind": "review", "source": "reviewer", "at": "2026-01-01T00:09:00+00:00",
   "candidate": %q, "brief_revision": null, "sum_version": "0.1.0", "endpoint": null, "verdict": "approve", "tool": "made"}
]}`, baseSha, head, filepath.Join(root, "checkout"), head, head, head)
		writeTaskFixture(t, home, "t-ffffffffffff", taskJSON)
		assertContextSectionsMatch(t, reference, home, "t-ffffffffffff", "handoff", "evidence")
	})

	t.Run("brief section, legacy task with no versions.json", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-aaaaaaaaaaaa", fmt.Sprintf(`{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repoA",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00"}`, baseSha))
		assertContextSectionsMatch(t, reference, home, "t-aaaaaaaaaaaa", "brief")
	})

	t.Run("notes section, a present notes.md with two entries", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-0a0a0a0a0a0a", fmt.Sprintf(`{"schema": 1, "id": "t-0a0a0a0a0a0a", "status": "running", "repository": "owner/repoG",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00"}`, baseSha))
		if err := os.WriteFile(filepath.Join(home, "tasks", "t-0a0a0a0a0a0a", "notes.md"),
			[]byte("# Notes for t-0a0a0a0a0a0a\n\n## 2026-01-01T00:00:15+00:00 worker p1\n\nfound the thing\n\n## 2026-01-01T00:05:00+00:00 coordinator p2\n\nlooks fine\n"),
			0o600); err != nil {
			t.Fatal(err)
		}
		assertContextSectionsMatch(t, reference, home, "t-0a0a0a0a0a0a", "notes")
	})

	t.Run("notes section, no notes.md present", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-0b0b0b0b0b0b", fmt.Sprintf(`{"schema": 1, "id": "t-0b0b0b0b0b0b", "status": "running", "repository": "owner/repoH",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00"}`, baseSha))
		assertContextSectionsMatch(t, reference, home, "t-0b0b0b0b0b0b", "notes")
	})

	t.Run("execution section, no graph record, no launch/parent/reviewer", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-0c0c0c0c0c0c", fmt.Sprintf(`{"schema": 1, "id": "t-0c0c0c0c0c0c", "status": "running", "repository": "owner/repoI",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "branch": "task-branch", "harness": "claude",
"machine": "m1", "session": "s1", "pane": "p1"}`, baseSha))
		assertContextSectionsMatch(t, reference, home, "t-0c0c0c0c0c0c", "execution")
	})

	t.Run("execution section, a ready graph record, launch, parent, and reviewer", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-0d0d0d0d0d0d", fmt.Sprintf(`{"schema": 1, "id": "t-0d0d0d0d0d0d", "status": "running", "repository": "owner/repoJ",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "branch": "task-branch", "harness": "claude", "started_at": "2026-01-01T00:01:00+00:00",
"machine": "m1", "session": "s1", "pane": "p1",
"launch": {"harness": "claude", "model": "opus", "reasoning": "high", "preset": null, "argv": ["claude"], "observed": {"status": "running"}},
"admission": {"decision": "admitted", "at": "2026-01-01T00:00:30+00:00"},
"parent": {"machine": "m1", "session": "s1", "pane": "p0"},
"reviewer": {"machine": "m1", "session": "s1", "pane": "p2"}}`, baseSha))
		graphJSON := `{"schema": 1, "state": "ready", "index_path": "/tmp/checkout/.codegraph", "indexed_head": "abc123",
"tool": {"version": "1.5.0"}, "index": {"fileCount": 42, "nodeCount": 1000, "edgeCount": 2000},
"attempts": [{"action": "init", "ok": true, "seconds": 3.5}], "commands": {"explore": "codegraph explore", "sync": "codegraph sync"},
"freshness": {"pendingChanges": false}, "updated_at": "2026-01-01T00:00:45+00:00"}`
		if err := os.MkdirAll(filepath.Join(home, "tasks", "t-0d0d0d0d0d0d"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "tasks", "t-0d0d0d0d0d0d", "graph.json"), []byte(graphJSON), 0o600); err != nil {
			t.Fatal(err)
		}
		assertContextSectionsMatch(t, reference, home, "t-0d0d0d0d0d0d", "execution")
	})
}

func TestContextEnvironmentAndUpdateSections_matchThePythonReferenceAcrossScenarios(t *testing.T) {
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

	t.Run("environment section, legacy task, no environment.json", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-0e0e0e0e0e0e", fmt.Sprintf(`{"schema": 1, "id": "t-0e0e0e0e0e0e", "status": "running", "repository": "owner/repoK",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00"}`, baseSha))
		assertContextSectionsMatch(t, reference, home, "t-0e0e0e0e0e0e", "environment")
	})

	t.Run("environment section, a present environment.json record", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-0f0f0f0f0f0f", fmt.Sprintf(`{"schema": 1, "id": "t-0f0f0f0f0f0f", "status": "running", "repository": "owner/repoL",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00"}`, baseSha))
		writeEnvironmentFixture(t, home, "t-0f0f0f0f0f0f", `{
  "schema": 1, "task": "t-0f0f0f0f0f0f", "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:01:00+00:00",
  "discovery": {"observed_at": "2026-01-01T00:00:00+00:00", "head": "abc", "config_revision": "r1", "current_revision": "r1",
    "stale": false, "stale_reason": null, "checked_at": "2026-01-01T00:00:00+00:00", "summary": [], "problems": [],
    "task_origins": [], "verification_contract": null, "sources": [], "commands": []},
  "endpoints": [], "logs": [], "resources": [], "services": [], "history": []
}`)
		assertContextSectionsMatch(t, reference, home, "t-0f0f0f0f0f0f", "environment")
	})

	t.Run("update section, legacy task with no versions.json, outside an installation", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-1a1a1a1a1a1a", fmt.Sprintf(`{"schema": 1, "id": "t-1a1a1a1a1a1a", "status": "running", "repository": "owner/repoM",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00"}`, baseSha))
		assertContextSectionsMatch(t, reference, home, "t-1a1a1a1a1a1a", "update")
	})
}

func assertContextSectionsMatch(t *testing.T, reference, home, taskID string, sections ...string) {
	t.Helper()
	args := []string{"--home", home, "context", taskID}
	for _, sec := range sections {
		args = append(args, "--section", sec)
	}
	want, err := exec.Command(reference, args...).Output()
	if err != nil {
		t.Fatalf("python reference failed: %v", err)
	}
	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs(args)
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("go command failed: %v (stderr=%s)", err, stderr.String())
	}
	gotNorm := normalizeReadAt(stdout.String())
	wantNorm := normalizeReadAt(string(want))
	if gotNorm != wantNorm {
		t.Fatalf("go output =\n%s\nwant (python reference)\n%s", gotNorm, wantNorm)
	}
}

func TestContextRole_matchesThePythonReferenceAcrossScenarios(t *testing.T) {
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

	t.Run("worker role, default sections, decisions filtered to answered", func(t *testing.T) {
		home := t.TempDir()
		taskJSON := fmt.Sprintf(`{"schema": 1, "id": "t-1b1b1b1b1b1b", "status": "running", "repository": "owner/repoN",
"notice": null, "attention": [], "brief": "do thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md", "report": null, "evidence": [],
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:02:00+00:00",
"questions": [
  {"id": "q1", "key": "approach", "status": "open", "created_at": "2026-01-01T00:00:30+00:00", "text": "which way?", "answer": null},
  {"id": "q2", "key": "scope", "status": "answered", "created_at": "2026-01-01T00:00:10+00:00", "answered_at": "2026-01-01T00:00:20+00:00", "text": "in scope?", "answer": "yes"}
]}`, baseSha)
		writeTaskFixture(t, home, "t-1b1b1b1b1b1b", taskJSON)
		assertContextRoleMatches(t, reference, home, "t-1b1b1b1b1b1b", "worker", nil)
	})

	t.Run("coordinator role, default sections, decisions filtered to open", func(t *testing.T) {
		home := t.TempDir()
		taskJSON := fmt.Sprintf(`{"schema": 1, "id": "t-1c1c1c1c1c1c", "status": "running", "repository": "owner/repoO",
"notice": null, "attention": [], "brief": "do thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md", "report": null, "evidence": [],
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:02:00+00:00",
"questions": [
  {"id": "q1", "key": "approach", "status": "open", "created_at": "2026-01-01T00:00:30+00:00", "text": "which way?", "answer": null},
  {"id": "q2", "key": "scope", "status": "answered", "created_at": "2026-01-01T00:00:10+00:00", "answered_at": "2026-01-01T00:00:20+00:00", "text": "in scope?", "answer": "yes"}
]}`, baseSha)
		writeTaskFixture(t, home, "t-1c1c1c1c1c1c", taskJSON)
		assertContextRoleMatches(t, reference, home, "t-1c1c1c1c1c1c", "coordinator", nil)
	})

	t.Run("reviewer role, explicit environment section, adds artifact references", func(t *testing.T) {
		home := t.TempDir()
		root := t.TempDir()
		checkout := filepath.Join(root, "checkout")
		head := initGitRepoWithCommit(t, checkout)
		taskJSON := fmt.Sprintf(`{"schema": 1, "id": "t-1d1d1d1d1d1d", "status": "reported", "repository": "owner/repoP",
"questions": [], "notice": null, "attention": [], "brief": "do thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"report": null, "worktree": %q, "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:10:00+00:00",
"evidence": [
  {"schema": 1, "id": "e-0000000001", "kind": "handoff", "source": "worker", "at": "2026-01-01T00:05:00+00:00",
   "candidate": %q, "brief_revision": null, "sum_version": "0.1.0", "endpoint": null,
   "handoff": {"outcome": "done", "next_action": null, "artifacts": ["README.md", "../outside.md", "/etc/passwd", "~/secret"]}}
]}`, baseSha, checkout, head)
		writeTaskFixture(t, home, "t-1d1d1d1d1d1d", taskJSON)
		assertContextRoleMatches(t, reference, home, "t-1d1d1d1d1d1d", "reviewer", []string{"environment"})
	})
}

func assertContextRoleMatches(t *testing.T, reference, home, taskID, role string, sections []string) {
	t.Helper()
	args := []string{"--home", home, "context", taskID, "--role", role}
	for _, sec := range sections {
		args = append(args, "--section", sec)
	}
	want, err := exec.Command(reference, args...).Output()
	if err != nil {
		t.Fatalf("python reference failed: %v", err)
	}
	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs(args)
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("go command failed: %v (stderr=%s)", err, stderr.String())
	}
	gotNorm := normalizeReadAt(stdout.String())
	wantNorm := normalizeReadAt(string(want))
	if gotNorm != wantNorm {
		t.Fatalf("go output =\n%s\nwant (python reference)\n%s", gotNorm, wantNorm)
	}
}

func assertContextMatches(t *testing.T, reference, home, taskID string) {
	t.Helper()
	args := []string{"--home", home, "context", taskID}
	want, err := exec.Command(reference, args...).Output()
	if err != nil {
		t.Fatalf("python reference failed: %v", err)
	}
	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs(args)
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("go command failed: %v (stderr=%s)", err, stderr.String())
	}
	gotNorm := normalizeReadAt(stdout.String())
	wantNorm := normalizeReadAt(string(want))
	if gotNorm != wantNorm {
		t.Fatalf("go output =\n%s\nwant (python reference)\n%s", gotNorm, wantNorm)
	}
}

func extractCursor(t *testing.T, output []byte) string {
	t.Helper()
	var parsed struct {
		Cursor string `json:"cursor"`
	}
	if err := json.Unmarshal(output, &parsed); err != nil {
		t.Fatalf("failed to parse cursor from output: %v (output=%s)", err, output)
	}
	if parsed.Cursor == "" {
		t.Fatalf("no cursor found in output: %s", output)
	}
	return parsed.Cursor
}

func TestContextSince_matchesThePythonReferenceAcrossScenarios(t *testing.T) {
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

	t.Run("unchanged cursor: early return with just a note, no sections", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-1e1e1e1e1e1e", fmt.Sprintf(`{"schema": 1, "id": "t-1e1e1e1e1e1e", "status": "running", "repository": "owner/repoQ",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00"}`, baseSha))
		initial, err := exec.Command(reference, "--home", home, "context", "t-1e1e1e1e1e1e").Output()
		if err != nil {
			t.Fatalf("python reference failed: %v", err)
		}
		cursor := extractCursor(t, initial)
		assertContextSinceMatches(t, reference, home, "t-1e1e1e1e1e1e", cursor, nil, "")
	})

	t.Run("changed since cursor: a new question, no section requested", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-1f1f1f1f1f1f", fmt.Sprintf(`{"schema": 1, "id": "t-1f1f1f1f1f1f", "status": "running", "repository": "owner/repoR",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00"}`, baseSha))
		initial, err := exec.Command(reference, "--home", home, "context", "t-1f1f1f1f1f1f").Output()
		if err != nil {
			t.Fatalf("python reference failed: %v", err)
		}
		cursor := extractCursor(t, initial)
		writeTaskFixture(t, home, "t-1f1f1f1f1f1f", fmt.Sprintf(`{"schema": 1, "id": "t-1f1f1f1f1f1f", "status": "running", "repository": "owner/repoR",
"questions": [{"id": "q1", "key": "approach", "status": "open", "created_at": "2026-01-01T00:00:30+00:00", "text": "which way?", "answer": null}],
"evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:01:00+00:00"}`, baseSha))
		assertContextSinceMatches(t, reference, home, "t-1f1f1f1f1f1f", cursor, nil, "")
	})

	t.Run("changed since cursor, with an explicit --section: changes and the section both render", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-2a2a2a2a2a2a", fmt.Sprintf(`{"schema": 1, "id": "t-2a2a2a2a2a2a", "status": "running", "repository": "owner/repoS",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00"}`, baseSha))
		initial, err := exec.Command(reference, "--home", home, "context", "t-2a2a2a2a2a2a").Output()
		if err != nil {
			t.Fatalf("python reference failed: %v", err)
		}
		cursor := extractCursor(t, initial)
		writeTaskFixture(t, home, "t-2a2a2a2a2a2a", fmt.Sprintf(`{"schema": 1, "id": "t-2a2a2a2a2a2a", "status": "running", "repository": "owner/repoS",
"questions": [{"id": "q1", "key": "approach", "status": "open", "created_at": "2026-01-01T00:00:30+00:00", "text": "which way?", "answer": null}],
"evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:01:00+00:00"}`, baseSha))
		assertContextSinceMatches(t, reference, home, "t-2a2a2a2a2a2a", cursor, []string{"decisions"}, "")
	})

	t.Run("malformed cursor is a command-level failure", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-2b2b2b2b2b2b", fmt.Sprintf(`{"schema": 1, "id": "t-2b2b2b2b2b2b", "status": "running", "repository": "owner/repoT",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00"}`, baseSha))
		assertContextSinceFailureMatches(t, reference, home, "t-2b2b2b2b2b2b", "not-a-real-cursor")
	})
}

func assertContextSinceMatches(t *testing.T, reference, home, taskID, cursor string, sections []string, role string) {
	t.Helper()
	args := []string{"--home", home, "context", taskID, "--since", cursor}
	for _, sec := range sections {
		args = append(args, "--section", sec)
	}
	if role != "" {
		args = append(args, "--role", role)
	}
	want, err := exec.Command(reference, args...).Output()
	if err != nil {
		t.Fatalf("python reference failed: %v", err)
	}
	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs(args)
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("go command failed: %v (stderr=%s)", err, stderr.String())
	}
	gotNorm := normalizeReadAt(stdout.String())
	wantNorm := normalizeReadAt(string(want))
	if gotNorm != wantNorm {
		t.Fatalf("go output =\n%s\nwant (python reference)\n%s", gotNorm, wantNorm)
	}
}

func assertContextSinceFailureMatches(t *testing.T, reference, home, taskID, cursor string) {
	t.Helper()
	args := []string{"--home", home, "context", taskID, "--since", cursor}
	cmd := exec.Command(reference, args...)
	var pyStderr bytes.Buffer
	cmd.Stderr = &pyStderr
	if err := cmd.Run(); err == nil {
		t.Fatalf("expected python reference to fail, got success with stderr=%s", pyStderr.String())
	}
	var pyPayload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(pyStderr.Bytes(), &pyPayload); err != nil {
		t.Fatalf("python stderr is not the expected error JSON: %v (stderr=%s)", err, pyStderr.String())
	}

	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected go command to fail, got success with stdout=%s", stdout.String())
	}
	if err.Error() != pyPayload.Error {
		t.Fatalf("go error = %q, want (python reference) %q", err.Error(), pyPayload.Error)
	}
}

func TestContextPagingAndRevisionFlags_matchThePythonReferenceAcrossScenarios(t *testing.T) {
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

	t.Run("--after and --limit page the decisions section", func(t *testing.T) {
		home := t.TempDir()
		taskJSON := fmt.Sprintf(`{"schema": 1, "id": "t-2c2c2c2c2c2c", "status": "running", "repository": "owner/repoU",
"notice": null, "attention": [], "brief": "do thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md", "report": null, "evidence": [],
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:02:00+00:00",
"questions": [
  {"id": "q1", "key": "a", "status": "open", "created_at": "2026-01-01T00:00:10+00:00", "text": "one?", "answer": null},
  {"id": "q2", "key": "b", "status": "open", "created_at": "2026-01-01T00:00:20+00:00", "text": "two?", "answer": null},
  {"id": "q3", "key": "c", "status": "open", "created_at": "2026-01-01T00:00:30+00:00", "text": "three?", "answer": null}
]}`, baseSha)
		writeTaskFixture(t, home, "t-2c2c2c2c2c2c", taskJSON)
		assertContextArgsMatch(t, reference, home, "t-2c2c2c2c2c2c", "--section", "decisions", "--after", "1", "--limit", "1")
	})

	t.Run("--kind filters the evidence section", func(t *testing.T) {
		home := t.TempDir()
		root := t.TempDir()
		head := initGitRepoWithCommit(t, filepath.Join(root, "checkout"))
		taskJSON := fmt.Sprintf(`{"schema": 1, "id": "t-2d2d2d2d2d2d", "status": "reported", "repository": "owner/repoV",
"questions": [], "notice": null, "attention": [], "brief": "do thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"report": null, "worktree": %q, "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:10:00+00:00",
"evidence": [
  {"schema": 1, "id": "e-0000000001", "kind": "handoff", "source": "worker", "at": "2026-01-01T00:05:00+00:00",
   "candidate": %q, "brief_revision": null, "sum_version": "0.1.0", "endpoint": null, "handoff": {"outcome": "done", "next_action": null}},
  {"schema": 1, "id": "e-0000000002", "kind": "verification", "source": "coordinator", "at": "2026-01-01T00:08:00+00:00",
   "candidate": %q, "brief_revision": null, "sum_version": "0.1.0", "endpoint": null, "run_id": "run-root-1", "result": "pass", "outcome": "all green"},
  {"schema": 1, "id": "e-0000000003", "kind": "review", "source": "reviewer", "at": "2026-01-01T00:09:00+00:00",
   "candidate": %q, "brief_revision": null, "sum_version": "0.1.0", "endpoint": null, "verdict": "approve", "tool": "made"}
]}`, baseSha, filepath.Join(root, "checkout"), head, head, head)
		writeTaskFixture(t, home, "t-2d2d2d2d2d2d", taskJSON)
		assertContextArgsMatch(t, reference, home, "t-2d2d2d2d2d2d", "--section", "evidence", "--kind", "verification")
	})

	t.Run("--max-chars truncates the brief section's approved text", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-2e2e2e2e2e2e", fmt.Sprintf(`{"schema": 1, "id": "t-2e2e2e2e2e2e", "status": "running", "repository": "owner/repoW",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "a brief that is longer than ten characters", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00"}`, baseSha))
		assertContextArgsMatch(t, reference, home, "t-2e2e2e2e2e2e", "--section", "brief", "--max-chars", "10")
	})

	t.Run("--revision looks up a recorded brief revision by ID", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-2f2f2f2f2f2f", fmt.Sprintf(`{"schema": 1, "id": "t-2f2f2f2f2f2f", "status": "running", "repository": "owner/repoX", "questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00"}`, baseSha))
		sha := writeRevisionFile(t, home, "t-2f2f2f2f2f2f", "contracts/r1.md", "revision content")
		versionsJSON := fmt.Sprintf(`{"schema": 1, "task": "t-2f2f2f2f2f2f", "legacy": false, "runtime": {"sum_version": "0.1.0"}, "brief_schema": 1,
 "approved": {"sha256": "abc", "base_sha": %q, "repository": "owner/repoX", "kind": "task"},
 "revisions": [{"id": "r1", "path": "contracts/r1.md", "status": "active", "created_at": "2026-01-01T00:00:00+00:00", "sha256": %q, "policy": {"sum_version": "0.1.0", "brief_schema": 1}, "summary": ["initial brief"], "verification_affected": false}],
 "active": "r1", "requested": null, "refresh": []}`, baseSha, sha)
		if err := os.WriteFile(filepath.Join(home, "tasks", "t-2f2f2f2f2f2f", "versions.json"), []byte(versionsJSON), 0o600); err != nil {
			t.Fatal(err)
		}
		assertContextArgsMatch(t, reference, home, "t-2f2f2f2f2f2f", "--section", "brief", "--revision", "r1")
	})

	t.Run("--revision with an unknown ID is a command-level failure", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-3a3a3a3a3a3a", fmt.Sprintf(`{"schema": 1, "id": "t-3a3a3a3a3a3a", "status": "running", "repository": "owner/repoY", "questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00"}`, baseSha))
		sha := writeRevisionFile(t, home, "t-3a3a3a3a3a3a", "contracts/r1.md", "revision content")
		versionsJSON := fmt.Sprintf(`{"schema": 1, "task": "t-3a3a3a3a3a3a", "legacy": false, "runtime": {"sum_version": "0.1.0"}, "brief_schema": 1,
 "approved": {"sha256": "abc", "base_sha": %q, "repository": "owner/repoY", "kind": "task"},
 "revisions": [{"id": "r1", "path": "contracts/r1.md", "status": "active", "created_at": "2026-01-01T00:00:00+00:00", "sha256": %q, "policy": {"sum_version": "0.1.0", "brief_schema": 1}, "summary": ["initial brief"], "verification_affected": false}],
 "active": "r1", "requested": null, "refresh": []}`, baseSha, sha)
		if err := os.WriteFile(filepath.Join(home, "tasks", "t-3a3a3a3a3a3a", "versions.json"), []byte(versionsJSON), 0o600); err != nil {
			t.Fatal(err)
		}
		assertContextArgsFailureMatches(t, reference, home, "t-3a3a3a3a3a3a", "--section", "brief", "--revision", "no-such-id")
	})

	t.Run("--limit out of range is a command-level failure", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-3b3b3b3b3b3b", fmt.Sprintf(`{"schema": 1, "id": "t-3b3b3b3b3b3b", "status": "running", "repository": "owner/repoZ", "questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md"}`, baseSha))
		assertContextArgsFailureMatches(t, reference, home, "t-3b3b3b3b3b3b", "--limit", "0")
		assertContextArgsFailureMatches(t, reference, home, "t-3b3b3b3b3b3b", "--limit", "201")
	})

	t.Run("negative --after or --max-chars is a command-level failure", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-3c3c3c3c3c3c", fmt.Sprintf(`{"schema": 1, "id": "t-3c3c3c3c3c3c", "status": "running", "repository": "owner/repoAA", "questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md"}`, baseSha))
		assertContextArgsFailureMatches(t, reference, home, "t-3c3c3c3c3c3c", "--after", "-1")
		assertContextArgsFailureMatches(t, reference, home, "t-3c3c3c3c3c3c", "--max-chars", "-1")
	})
}

func assertContextArgsMatch(t *testing.T, reference, home, taskID string, extra ...string) {
	t.Helper()
	args := append([]string{"--home", home, "context", taskID}, extra...)
	want, err := exec.Command(reference, args...).Output()
	if err != nil {
		t.Fatalf("python reference failed: %v", err)
	}
	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs(args)
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("go command failed: %v (stderr=%s)", err, stderr.String())
	}
	gotNorm := normalizeReadAt(stdout.String())
	wantNorm := normalizeReadAt(string(want))
	if gotNorm != wantNorm {
		t.Fatalf("go output =\n%s\nwant (python reference)\n%s", gotNorm, wantNorm)
	}
}

func assertContextArgsFailureMatches(t *testing.T, reference, home, taskID string, extra ...string) {
	t.Helper()
	args := append([]string{"--home", home, "context", taskID}, extra...)
	cmd := exec.Command(reference, args...)
	var pyStderr bytes.Buffer
	cmd.Stderr = &pyStderr
	if err := cmd.Run(); err == nil {
		t.Fatalf("expected python reference to fail, got success with stderr=%s", pyStderr.String())
	}
	var pyPayload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(pyStderr.Bytes(), &pyPayload); err != nil {
		t.Fatalf("python stderr is not the expected error JSON: %v (stderr=%s)", err, pyStderr.String())
	}

	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected go command to fail, got success with stdout=%s", stdout.String())
	}
	if err.Error() != pyPayload.Error {
		t.Fatalf("go error = %q, want (python reference) %q", err.Error(), pyPayload.Error)
	}
}

func TestContext_unknownFlagsAreGoErrors(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRoot("", &bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"--home", home, "context", "t-aaaaaaaaaaaa", "--role", "not-a-real-role"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected unrecognized arguments")
	}
	if !strings.Contains(err.Error(), "unrecognized arguments") {
		t.Fatalf("err = %v", err)
	}
}
