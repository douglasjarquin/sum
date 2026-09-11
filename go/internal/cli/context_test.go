package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

func TestContext_fallsBackToReferenceForUnsupportedFlags(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	reference := filepath.Join(dir, "reference.sh")
	if err := os.WriteFile(reference, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$SUM_GO_ARGS_FILE\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_GO_ARGS_FILE", argsFile)

	home := filepath.Join(dir, "state")

	t.Run("--role is not yet supported", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		root := NewRoot(reference, &stdout, &stderr)
		root.SetArgs([]string{"--home", home, "context", "t-aaaaaaaaaaaa", "--role", "worker"})
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
		}
		got, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatal(err)
		}
		want := "--home\n" + home + "\ncontext\nt-aaaaaaaaaaaa\n--role\nworker\n"
		if string(got) != want {
			t.Fatalf("reference argv = %q, want %q", got, want)
		}
	})

	t.Run("an unrecognized section name is not yet supported", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		root := NewRoot(reference, &stdout, &stderr)
		root.SetArgs([]string{"--home", home, "context", "t-aaaaaaaaaaaa", "--section", "not-a-real-section"})
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
		}
		got, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatal(err)
		}
		want := "--home\n" + home + "\ncontext\nt-aaaaaaaaaaaa\n--section\nnot-a-real-section\n"
		if string(got) != want {
			t.Fatalf("reference argv = %q, want %q", got, want)
		}
	})
}
