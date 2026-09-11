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

func TestBrief_fallsBackToReferenceForNonListSubcommands(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	reference := filepath.Join(dir, "reference.sh")
	if err := os.WriteFile(reference, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$SUM_GO_ARGS_FILE\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_GO_ARGS_FILE", argsFile)

	home := filepath.Join(dir, "state")
	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs([]string{"--home", home, "brief", "regenerate", "t-aaaaaaaaaaaa"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "--home\n" + home + "\nbrief\nregenerate\nt-aaaaaaaaaaaa\n"
	if string(got) != want {
		t.Fatalf("reference argv = %q, want %q", got, want)
	}
}
