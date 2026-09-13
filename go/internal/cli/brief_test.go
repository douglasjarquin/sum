package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeTaskFixture(t *testing.T, home, taskID, taskJSON string) {
	t.Helper()
	dir := filepath.Join(home, "tasks", taskID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "task.json"), []byte(taskJSON), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeRevisionFile(t *testing.T, home, taskID, relPath, content string) string {
	t.Helper()
	full := filepath.Join(home, "tasks", taskID, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func TestBriefList_matchesThePythonReferenceAcrossScenarios(t *testing.T) {
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

	t.Run("legacy task, no versions.json", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-aaaaaaaaaaaa", fmt.Sprintf(`{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repoA", "questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md"}`, baseSha))
		assertBriefListMatches(t, reference, home, "t-aaaaaaaaaaaa")
	})

	t.Run("real versions.json with an intact revision", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-aaaaaaaaaaaa", fmt.Sprintf(`{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repoA", "questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md"}`, baseSha))
		sha := writeRevisionFile(t, home, "t-aaaaaaaaaaaa", "contracts/r1.md", "revision content")
		versionsJSON := fmt.Sprintf(`{"schema": 1, "task": "t-aaaaaaaaaaaa", "legacy": false, "runtime": {"sum_version": "0.1.0"}, "brief_schema": 1,
 "approved": {"sha256": "abc", "base_sha": %q, "repository": "owner/repoA", "kind": "task"},
 "revisions": [{"id": "r1", "path": "contracts/r1.md", "status": "active", "created_at": "2026-01-01T00:00:00+00:00", "sha256": %q, "policy": {"sum_version": "0.1.0", "brief_schema": 1}, "summary": ["initial brief"], "verification_affected": false}],
 "active": "r1", "requested": null, "refresh": []}`, baseSha, sha)
		if err := os.WriteFile(filepath.Join(home, "tasks", "t-aaaaaaaaaaaa", "versions.json"), []byte(versionsJSON), 0o600); err != nil {
			t.Fatal(err)
		}
		assertBriefListMatches(t, reference, home, "t-aaaaaaaaaaaa")
	})

	t.Run("report made under a superseded revision, later one verification-affecting", func(t *testing.T) {
		home := t.TempDir()
		writeTaskFixture(t, home, "t-bbbbbbbbbbbb", fmt.Sprintf(`{"schema": 1, "id": "t-bbbbbbbbbbbb", "status": "reported", "repository": "owner/repoB", "questions": [], "evidence": [], "attention": [], "brief": "do thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md", "notice": null,
 "report": {"text": "done", "submitted_at": "2026-01-01T00:30:00+00:00", "brief_revision": "r1", "sum_version": "0.1.0"}}`, baseSha))
		sha1 := writeRevisionFile(t, home, "t-bbbbbbbbbbbb", "contracts/r1.md", "revision r1 content")
		sha2 := writeRevisionFile(t, home, "t-bbbbbbbbbbbb", "contracts/r2.md", "revision r2 content")
		versionsJSON := fmt.Sprintf(`{"schema": 1, "task": "t-bbbbbbbbbbbb", "legacy": false, "runtime": {"sum_version": "0.1.0"}, "brief_schema": 1,
 "approved": {"sha256": "abc", "base_sha": %q, "repository": "owner/repoB", "kind": "task"},
 "revisions": [
   {"id": "r1", "path": "contracts/r1.md", "status": "superseded", "created_at": "2026-01-01T00:00:00+00:00", "sha256": %q, "policy": {"sum_version": "0.1.0", "brief_schema": 1}, "summary": ["initial brief"], "verification_affected": false},
   {"id": "r2", "path": "contracts/r2.md", "status": "active", "created_at": "2026-01-01T01:00:00+00:00", "sha256": %q, "policy": {"sum_version": "0.1.0", "brief_schema": 1}, "summary": ["revised"], "verification_affected": true}
 ],
 "active": "r2", "requested": null, "refresh": []}`, baseSha, sha1, sha2)
		if err := os.WriteFile(filepath.Join(home, "tasks", "t-bbbbbbbbbbbb", "versions.json"), []byte(versionsJSON), 0o600); err != nil {
			t.Fatal(err)
		}
		assertBriefListMatches(t, reference, home, "t-bbbbbbbbbbbb")
	})
}

func assertBriefListMatches(t *testing.T, reference, home, taskID string) {
	t.Helper()
	args := []string{"--home", home, "brief", "list", taskID}
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

func TestBriefRegenerate_requiresCoordinator(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	root := NewRoot("", &stdout, &stderr)
	root.SetArgs([]string{"--home", home, "brief", "regenerate", "t-aaaaaaaaaaaa"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected coordinator requirement")
	}
	if !strings.Contains(err.Error(), "not the registered coordinator") && !strings.Contains(err.Error(), "Herdr pane") {
		t.Fatalf("err = %v", err)
	}
}

func TestBriefUnknownFlagsAreUsageErrorsBeforeTaskWrite(t *testing.T) {
	cases := []struct {
		name string
		args []string
		lab  string
	}{
		{name: "list unknown flag", args: []string{"--unexpected"}, lab: "list"},
		{name: "request unknown flag", args: []string{"--unexpected"}, lab: "request"},
		{name: "adopt unknown flag", args: []string{"--unexpected"}, lab: "adopt"},
		{name: "regenerate unknown flag", args: []string{"--unexpected"}, lab: "regenerate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, taskID, before := briefUsageLab(t, tc.lab)
			var stdout, stderr bytes.Buffer
			root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &stdout, &stderr)
			fullArgs := append([]string{"--home", home, "brief"}, briefCommandArgs(tc.lab, taskID)...)
			fullArgs = append(fullArgs, tc.args...)
			root.SetArgs(fullArgs)
			err := root.ExecuteContext(context.Background())
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout.String(), stderr.String())
			}
			if !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout.String() != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout.String())
			}
			assertBriefUnchanged(t, home, taskID, before)
		})
	}
}

func TestBriefCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		lab       string
		success   bool
		usage     bool
		unknown   bool
		domainErr string
	}{
		{name: "valid list", args: []string{"brief", "list", "t-aaaaaaaaaaaa"}, lab: "list", success: true},
		{name: "valid list after dashdash", args: []string{"brief", "list", "--", "t-aaaaaaaaaaaa"}, lab: "list", success: true},
		{name: "valid request", args: []string{"brief", "request", "t-aaaaaaaaaaaa", "r2"}, lab: "request", success: true},
		{name: "valid request after dashdash", args: []string{"brief", "request", "--", "t-aaaaaaaaaaaa", "r2"}, lab: "request", success: true},
		{name: "valid adopt", args: []string{"brief", "adopt", "t-aaaaaaaaaaaa", "r2"}, lab: "adopt", success: true},
		{name: "valid adopt after dashdash", args: []string{"brief", "adopt", "--", "t-aaaaaaaaaaaa", "r2"}, lab: "adopt", success: true},
		{name: "valid regenerate", args: []string{"brief", "regenerate", "t-aaaaaaaaaaaa"}, lab: "regenerate", success: true},
		{name: "valid regenerate after dashdash", args: []string{"brief", "regenerate", "--", "t-aaaaaaaaaaaa"}, lab: "regenerate", success: true},
		{name: "brief missing subcommand", args: []string{"brief"}, usage: true},
		{name: "list missing task", args: []string{"brief", "list"}, usage: true},
		{name: "request missing args", args: []string{"brief", "request"}, usage: true},
		{name: "request missing revision", args: []string{"brief", "request", "t-aaaaaaaaaaaa"}, lab: "request", usage: true},
		{name: "adopt missing args", args: []string{"brief", "adopt"}, usage: true},
		{name: "adopt missing revision", args: []string{"brief", "adopt", "t-aaaaaaaaaaaa"}, lab: "adopt", usage: true},
		{name: "regenerate missing task", args: []string{"brief", "regenerate"}, usage: true},
		{name: "list extra positional", args: []string{"brief", "list", "t-aaaaaaaaaaaa", "extra"}, lab: "list", usage: true},
		{name: "list unknown flag", args: []string{"brief", "list", "t-aaaaaaaaaaaa", "--unexpected"}, lab: "list", usage: true, unknown: true},
		{name: "list extra after dashdash", args: []string{"brief", "list", "t-aaaaaaaaaaaa", "--", "extra"}, lab: "list", usage: true},
		{name: "request extra positional", args: []string{"brief", "request", "t-aaaaaaaaaaaa", "r2", "extra"}, lab: "request", usage: true},
		{name: "request unknown flag", args: []string{"brief", "request", "t-aaaaaaaaaaaa", "r2", "--unexpected"}, lab: "request", usage: true, unknown: true},
		{name: "adopt extra positional", args: []string{"brief", "adopt", "t-aaaaaaaaaaaa", "r2", "extra"}, lab: "adopt", usage: true},
		{name: "adopt unknown flag", args: []string{"brief", "adopt", "t-aaaaaaaaaaaa", "r2", "--unexpected"}, lab: "adopt", usage: true, unknown: true},
		{name: "regenerate extra positional", args: []string{"brief", "regenerate", "t-aaaaaaaaaaaa", "extra"}, lab: "regenerate", usage: true},
		{name: "regenerate unknown flag", args: []string{"brief", "regenerate", "t-aaaaaaaaaaaa", "--unexpected"}, lab: "regenerate", usage: true, unknown: true},
		{name: "request requires coordinator", args: []string{"brief", "request", "t-aaaaaaaaaaaa", "r2"}, lab: "request-plain", domainErr: "registered coordinator"},
		{name: "regenerate requires coordinator", args: []string{"brief", "regenerate", "t-aaaaaaaaaaaa"}, lab: "regenerate-plain", domainErr: "registered coordinator"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, taskID, before := briefUsageLab(t, tc.lab)
			stdout, stderr, err := runBrief(t, home, tc.args...)
			switch {
			case tc.success:
				if err != nil {
					t.Fatalf("err = %v stderr=%s stdout=%s", err, stderr, stdout)
				}
				if !strings.Contains(stdout, taskID) {
					t.Fatalf("stdout missing task id:\n%s", stdout)
				}
			case tc.usage:
				assertBriefUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				if taskID != "" {
					assertBriefUnchanged(t, home, taskID, before)
				}
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s", stdout)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.domainErr)
				}
			}
		})
	}
}

func runBrief(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertBriefUsageError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected usage failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "registered coordinator") || strings.Contains(msg, "This task has no") || strings.Contains(msg, "not the requested") {
		t.Fatalf("err = %v, want a usage failure", err)
	}
	usage := strings.Contains(msg, "unknown flag") ||
		strings.Contains(msg, "accepts 1 arg") ||
		strings.Contains(msg, "accepts 2 arg") ||
		strings.Contains(msg, "command is required") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid brief")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func briefCommandArgs(lab, taskID string) []string {
	switch lab {
	case "request", "request-plain":
		return []string{"request", taskID, "r2"}
	case "adopt":
		return []string{"adopt", taskID, "r2"}
	case "regenerate", "regenerate-plain":
		return []string{"regenerate", taskID}
	default:
		return []string{"list", taskID}
	}
}

func briefUsageLab(t *testing.T, lab string) (home, taskID, before string) {
	t.Helper()
	taskID = "t-aaaaaaaaaaaa"
	switch lab {
	case "request":
		home, _, taskID = briefCoordinatorLab(t)
		writeBriefRevisions(t, home, taskID, "")
	case "request-plain":
		home = writeDesignatedHome(t)
		herdrEnv(t, home)
		t.Setenv("HERDR_PANE_ID", "w-other:p1")
		writeBriefTask(t, home, taskID, "")
		writeBriefRevisions(t, home, taskID, "")
	case "adopt":
		home = writeDesignatedHome(t)
		writeBriefTask(t, home, taskID, "")
		writeBriefRevisions(t, home, taskID, "r2")
	case "regenerate":
		home, _, taskID = briefCoordinatorLab(t)
		writeBriefFile(t, home, taskID)
	case "regenerate-plain":
		home = writeDesignatedHome(t)
		herdrEnv(t, home)
		t.Setenv("HERDR_PANE_ID", "w-other:p1")
		writeBriefTask(t, home, taskID, "")
		writeBriefFile(t, home, taskID)
	case "list":
		home = writeDesignatedHome(t)
		writeBriefTask(t, home, taskID, "")
	default:
		home = writeDesignatedHome(t)
	}
	if taskID != "" {
		if _, err := os.Stat(filepath.Join(home, "tasks", taskID, "task.json")); err == nil {
			before = listRegularFiles(t, filepath.Join(home, "tasks", taskID))
		}
	}
	return home, taskID, before
}

func assertBriefUnchanged(t *testing.T, home, taskID, before string) {
	t.Helper()
	after := listRegularFiles(t, filepath.Join(home, "tasks", taskID))
	if after != before {
		t.Fatalf("task directory changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func writeBriefTask(t *testing.T, home, taskID, worktree string) {
	t.Helper()
	baseSha := "0123456789abcdef0123456789abcdef01234567"
	body := fmt.Sprintf(`{"schema": 1, "id": %q, "status": "running", "repository": "owner/repoA", "questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md"}`, taskID, baseSha)
	if worktree != "" {
		body = fmt.Sprintf(`{"schema": 1, "id": %q, "status": "running", "repository": "owner/repoA", "questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "task", "brief_path": "brief.md", "worktree": %q}`, taskID, baseSha, worktree)
	}
	writeTaskFixture(t, home, taskID, body)
}

func writeBriefFile(t *testing.T, home, taskID string) {
	t.Helper()
	path := filepath.Join(home, "tasks", taskID, "brief.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("do the thing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeBriefRevisions(t *testing.T, home, taskID, requested string) {
	t.Helper()
	sha1 := writeRevisionFile(t, home, taskID, "contracts/r1.md", "revision r1 content")
	sha2 := writeRevisionFile(t, home, taskID, "contracts/r2.md", "revision r2 content")
	requestedJSON := "null"
	r2Status := "staged"
	if requested != "" {
		requestedJSON = fmt.Sprintf("%q", requested)
		r2Status = "requested"
	}
	baseSha := "0123456789abcdef0123456789abcdef01234567"
	versionsJSON := fmt.Sprintf(`{"schema": 1, "task": %q, "legacy": false, "runtime": {"sum_version": "0.1.0"}, "brief_schema": 1,
 "approved": {"sha256": "abc", "base_sha": %q, "repository": "owner/repoA", "kind": "task"},
 "revisions": [
   {"id": "r1", "path": "contracts/r1.md", "status": "active", "created_at": "2026-01-01T00:00:00+00:00", "sha256": %q, "policy": {"sum_version": "0.1.0", "brief_schema": 1}, "summary": ["initial brief"], "verification_affected": false},
   {"id": "r2", "path": "contracts/r2.md", "status": %q, "created_at": "2026-01-01T01:00:00+00:00", "sha256": %q, "policy": {"sum_version": "0.1.0", "brief_schema": 1}, "summary": ["revised"], "verification_affected": false}
 ],
 "active": "r1", "requested": %s, "refresh": []}`, taskID, baseSha, sha1, r2Status, sha2, requestedJSON)
	if err := os.WriteFile(filepath.Join(home, "tasks", taskID, "versions.json"), []byte(versionsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
}

func briefCoordinatorLab(t *testing.T) (home, worktree, taskID string) {
	t.Helper()
	home = writeDesignatedHome(t)
	herdrEnv(t, home)
	if _, err := runCLI(t, home, "init"); err != nil {
		t.Fatalf("coordinator init: %v", err)
	}
	worktree = t.TempDir()
	initGitRepoWithCommit(t, worktree)
	taskID = "t-aaaaaaaaaaaa"
	writeBriefTask(t, home, taskID, worktree)
	return home, worktree, taskID
}
