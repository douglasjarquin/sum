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

func initGitRepoWithCommit(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitCmd := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	runGitCmd("init", "--quiet")
	runGitCmd("config", "user.email", "test@example.com")
	runGitCmd("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCmd("add", "README.md")
	runGitCmd("commit", "--quiet", "-m", "initial")
	head := runGitCmd("rev-parse", "HEAD")
	return head[:len(head)-1] // trim trailing newline
}

func TestShow_matchesThePythonReferenceAcrossScenarios(t *testing.T) {
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

	t.Run("minimal task: no worktree, evidence, pr, or reviewer", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-aaaaaaaaaaaa", fmt.Sprintf(`{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repoA",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md"}`, baseSha))
		assertShowMatches(t, reference, home, "t-aaaaaaaaaaaa")
	})

	t.Run("legacy report with no structured evidence synthesizes a report row", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-bbbbbbbbbbbb", fmt.Sprintf(`{"schema": 1, "id": "t-bbbbbbbbbbbb", "status": "reported", "repository": "owner/repoB",
"questions": [], "evidence": [], "notice": null, "attention": [], "brief": "do thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"report": {"text": "done", "submitted_at": "2026-01-01T00:30:00+00:00", "brief_revision": "legacy", "sum_version": "0.1.0"}}`, baseSha))
		assertShowMatches(t, reference, home, "t-bbbbbbbbbbbb")
	})

	t.Run("root run failed: names the failing run in the missing message", func(t *testing.T) {
		home := t.TempDir()
		root := t.TempDir()
		head := initGitRepoWithCommit(t, filepath.Join(root, "checkout"))
		writeTaskFixture(t, home, "t-cccccccccccc", fmt.Sprintf(`{"schema": 1, "id": "t-cccccccccccc", "status": "running", "repository": "owner/repoC",
"questions": [], "notice": null, "attention": [], "brief": "do thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"report": null, "worktree": %q,
"evidence": [
  {"schema": 1, "id": "e-0000000001", "kind": "verification", "source": "coordinator", "at": "2026-01-01T00:10:00+00:00",
   "candidate": %q, "brief_revision": null, "sum_version": "0.1.0", "endpoint": null, "run_id": "r1", "result": "fail", "outcome": "failing scenarios"}
]}`, baseSha, filepath.Join(root, "checkout"), head))
		assertShowMatches(t, reference, home, "t-cccccccccccc")
	})

	t.Run("standardized contract fully satisfied: prerequisites met", func(t *testing.T) {
		home := t.TempDir()
		root := t.TempDir()
		head := initGitRepoWithCommit(t, filepath.Join(root, "checkout"))
		taskJSON := fmt.Sprintf(`{"schema": 1, "id": "t-dddddddddddd", "status": "reported", "repository": "owner/repoD",
"questions": [], "notice": null, "attention": [], "brief": "do thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md",
"report": null, "worktree": %q, "verification_policy": {"status": "standardized"},
"pr": {"complete": true, "merged_for_task": false, "identity": {"repository": "owner/repoD", "number": 1, "head_sha": %q, "head_repository": "owner/repoD", "head_branch": "task-branch"}},
"reviewer": {"machine": "m1", "session": "s1", "pane": "p9"},
"evidence": [
  {"schema": 1, "id": "e-0000000001", "kind": "handoff", "source": "worker", "at": "2026-01-01T00:05:00+00:00",
   "candidate": %q, "brief_revision": null, "sum_version": "0.1.0", "endpoint": null, "verification": "run1"},
  {"schema": 1, "id": "e-0000000002", "kind": "verification", "source": "worker", "at": "2026-01-01T00:06:00+00:00",
   "candidate": %q, "brief_revision": null, "sum_version": "0.1.0", "endpoint": null, "run_id": "run-worker-1", "result": "pass", "outcome": "all green"},
  {"schema": 1, "id": "e-0000000003", "kind": "verification", "source": "coordinator", "at": "2026-01-01T00:10:00+00:00",
   "candidate": %q, "brief_revision": null, "sum_version": "0.1.0", "endpoint": null, "run_id": "run-root-1", "result": "pass", "outcome": "all green", "requires_root_review": false},
  {"schema": 1, "id": "e-0000000004", "kind": "review", "source": "reviewer", "at": "2026-01-01T00:12:00+00:00",
   "candidate": %q, "brief_revision": null, "sum_version": "0.1.0", "endpoint": null, "verdict": "approve", "tool": "made", "policy_reviewed": true}
]}`, baseSha, filepath.Join(root, "checkout"), head, head, head, head, head)
		writeTaskFixture(t, home, "t-dddddddddddd", taskJSON)
		assertShowMatches(t, reference, home, "t-dddddddddddd")
	})
}

func assertShowMatches(t *testing.T, reference, home, taskID string) {
	t.Helper()
	args := []string{"--home", home, "show", taskID}
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
	if stdout.String() != string(want) {
		t.Fatalf("go output =\n%s\nwant (python reference)\n%s", stdout.String(), want)
	}
}

func TestShow_extraArgsAreGoErrors(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRoot("", &bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"--home", home, "show", "t-aaaaaaaaaaaa", "--extra"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected unrecognized arguments")
	}
}

func TestShowCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		success   bool
		usage     bool
		unknown   bool
		domainErr string
	}{
		{name: "valid show", args: []string{"show", "t-aaaaaaaaaaaa"}, success: true},
		{name: "valid show after dashdash", args: []string{"show", "--", "t-aaaaaaaaaaaa"}, success: true},
		{name: "missing task", args: []string{"show"}, usage: true},
		{name: "extra positional", args: []string{"show", "t-aaaaaaaaaaaa", "extra"}, usage: true},
		{name: "unknown flag", args: []string{"show", "t-aaaaaaaaaaaa", "--unexpected"}, usage: true, unknown: true},
		{name: "unknown flag before task", args: []string{"show", "--unexpected", "t-aaaaaaaaaaaa"}, usage: true, unknown: true},
		{name: "extra after dashdash", args: []string{"show", "t-aaaaaaaaaaaa", "--", "extra"}, usage: true},
		{name: "unknown task", args: []string{"show", "t-bbbbbbbbbbbb"}, domainErr: "Cannot read"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home, taskPath, before := contextUsageLab(t)
			stdout, stderr, err := runShow(t, home, tc.args...)
			switch {
			case tc.success:
				if err != nil {
					t.Fatalf("err = %v stderr=%s stdout=%s", err, stderr, stdout)
				}
				if !strings.Contains(stdout, "t-aaaaaaaaaaaa") {
					t.Fatalf("stdout missing task id:\n%s", stdout)
				}
			case tc.usage:
				assertShowUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				assertFileUnchanged(t, taskPath, before)
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s", stdout)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err.Error(), tc.domainErr)
				}
				if stdout != "" {
					t.Fatalf("domain failure wrote stdout: %s", stdout)
				}
			}
		})
	}
}

func runShow(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertShowUsageError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected usage failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "Cannot read") {
		t.Fatalf("err = %v, want a usage failure", err)
	}
	usage := strings.Contains(msg, "unknown flag") ||
		strings.Contains(msg, "accepts 1 arg") ||
		strings.Contains(msg, "unrecognized arguments")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}
