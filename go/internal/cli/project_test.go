package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func initGitClone(t *testing.T, dir, remoteURL string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitCmd := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runGitCmd("init", "--quiet")
	if remoteURL != "" {
		runGitCmd("remote", "add", "origin", remoteURL)
	}
}

func writeProjectsRegistry(t *testing.T, home, registryJSON string) {
	t.Helper()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "projects.json"), []byte(registryJSON), 0o600); err != nil {
		t.Fatal(err)
	}
}

func projectRecordJSON(name, host, owner, repo, kind, path, remote string) string {
	return fmt.Sprintf(`{"name": %q, "host": %q, "owner": %q, "repo": %q, "kind": %q, "path": %q, "remote": %q,
"enrolled_at": "2026-01-01T00:00:00+00:00", "enrolled_by": {"machine": "m1", "session": "s1", "pane": "p1"},
"canonical_path": %q, "note": null}`, name, host, owner, repo, kind, path, remote, path)
}

func TestProjectList_matchesThePythonReferenceAcrossScenarios(t *testing.T) {
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

	t.Run("no registry file", func(t *testing.T) {
		home := t.TempDir()
		assertProjectMatches(t, reference, home, []string{"project", "list"})
	})

	t.Run("clean, dirty, mismatched-remote, and missing clones", func(t *testing.T) {
		home := t.TempDir()
		root := t.TempDir()

		cleanPath := filepath.Join(root, "clean-proj")
		initGitClone(t, cleanPath, "https://github.com/owner/clean-proj.git")

		dirtyPath := filepath.Join(root, "dirty-proj")
		initGitClone(t, dirtyPath, "https://github.com/owner/dirty-proj.git")
		if err := os.WriteFile(filepath.Join(dirtyPath, "untracked.txt"), []byte("wip\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		mismatchedPath := filepath.Join(root, "mismatched-proj")
		initGitClone(t, mismatchedPath, "https://github.com/owner/actual-remote.git")

		missingPath := filepath.Join(root, "missing-proj")

		registry := fmt.Sprintf(`{"schema": 1, "projects": {
  "owner/clean-proj": %s,
  "owner/dirty-proj": %s,
  "owner/mismatched-proj": %s,
  "owner/missing-proj": %s
}}`,
			projectRecordJSON("owner/clean-proj", "github.com", "owner", "clean-proj", "managed", cleanPath, "https://github.com/owner/clean-proj.git"),
			projectRecordJSON("owner/dirty-proj", "github.com", "owner", "dirty-proj", "managed", dirtyPath, "https://github.com/owner/dirty-proj.git"),
			projectRecordJSON("owner/mismatched-proj", "github.com", "owner", "mismatched-proj", "managed", mismatchedPath, "https://github.com/owner/mismatched-proj.git"),
			projectRecordJSON("owner/missing-proj", "github.com", "owner", "missing-proj", "managed", missingPath, "https://github.com/owner/missing-proj.git"),
		)
		writeProjectsRegistry(t, home, registry)
		assertProjectMatches(t, reference, home, []string{"project", "list"})
	})
}

func TestProjectShow_matchesThePythonReferenceAcrossScenarios(t *testing.T) {
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

	t.Run("known project with active and archived tasks", func(t *testing.T) {
		home := t.TempDir()
		root := t.TempDir()
		cleanPath := filepath.Join(root, "clean-proj")
		initGitClone(t, cleanPath, "https://github.com/owner/clean-proj.git")

		registry := fmt.Sprintf(`{"schema": 1, "projects": {"owner/clean-proj": %s}}`,
			projectRecordJSON("owner/clean-proj", "github.com", "owner", "clean-proj", "managed", cleanPath, "https://github.com/owner/clean-proj.git"))
		writeProjectsRegistry(t, home, registry)

		baseSha := "0123456789abcdef0123456789abcdef01234567"
		writeTaskFixture(t, home, "t-aaaaaaaaaaaa", fmt.Sprintf(`{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": %q, "questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do thing", "base_sha": %q, "kind": "task", "worktree": "/tmp/worktree-a"}`, cleanPath, baseSha))
		writeTaskFixture(t, home, "t-bbbbbbbbbbbb", fmt.Sprintf(`{"schema": 1, "id": "t-bbbbbbbbbbbb", "status": "archived", "repository": %q, "questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do thing b", "base_sha": %q, "kind": "task"}`, cleanPath, baseSha))
		writeTaskFixture(t, home, "t-cccccccccccc", fmt.Sprintf(`{"schema": 1, "id": "t-cccccccccccc", "status": "running", "repository": "/tmp/unrelated", "questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "unrelated", "base_sha": %q, "kind": "task"}`, baseSha))

		assertProjectMatches(t, reference, home, []string{"project", "show", "owner/clean-proj"})
	})

	t.Run("unknown project is a command-level failure", func(t *testing.T) {
		home := t.TempDir()
		writeProjectsRegistry(t, home, `{"schema": 1, "projects": {}}`)
		assertProjectFailureMatches(t, reference, home, []string{"project", "show", "owner/nope"})
	})
}

func assertProjectMatches(t *testing.T, reference, home string, args []string) {
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

func assertProjectFailureMatches(t *testing.T, reference, home string, args []string) {
	t.Helper()
	fullArgs := append([]string{"--home", home}, args...)
	cmd := exec.Command(reference, fullArgs...)
	var pyStderr bytes.Buffer
	cmd.Stderr = &pyStderr
	if err := cmd.Run(); err == nil {
		t.Fatalf("expected python reference to fail, got success with stderr=%s", pyStderr.String())
	}
	want := decodeCLIError(t, pyStderr.Bytes())
	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs(fullArgs)
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected go command to fail, got success with stdout=%s", stdout.String())
	}
	if err.Error() != want {
		t.Fatalf("go error = %q, want (python reference) %q", err.Error(), want)
	}
}

func TestProjectEnroll_requiresCoordinator(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRoot("", &bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"--home", home, "project", "enroll", "owner/repo"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected coordinator requirement")
	}
}
