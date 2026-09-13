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
	clearHerdrEnv(t)
	home := writeDesignatedHome(t)
	stdout, stderr, err := runProject(t, home, "project", "enroll", "owner/repo")
	if err == nil {
		t.Fatalf("expected coordinator requirement, stdout=%s stderr=%s", stdout, stderr)
	}
	if !strings.Contains(err.Error(), "not the registered coordinator") && !strings.Contains(err.Error(), "Herdr pane") {
		t.Fatalf("err = %v", err)
	}
	assertProjectRegistryUnchanged(t, home, "")
}

func TestProjectEnrollMigrate_unknownFlagOrExtraPositionalDoesNotRunDomain(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
		migrate bool
	}{
		{name: "enroll unknown flag", args: []string{"project", "enroll", "owner/repo", "--unexpected"}, unknown: true},
		{name: "enroll extra positional", args: []string{"project", "enroll", "owner/repo", "extra"}},
		{name: "enroll extra after dashdash", args: []string{"project", "enroll", "owner/repo", "--", "extra"}},
		{name: "enroll unknown flag before spec", args: []string{"project", "enroll", "--unexpected", "owner/repo"}, unknown: true},
		{name: "migrate unknown flag", args: []string{"project", "migrate", "owner/repo", "--unexpected"}, unknown: true, migrate: true},
		{name: "migrate extra positional", args: []string{"project", "migrate", "owner/repo", "extra"}, migrate: true},
		{name: "migrate extra after dashdash", args: []string{"project", "migrate", "owner/repo", "--", "extra"}, migrate: true},
		{name: "migrate unknown flag before name", args: []string{"project", "migrate", "--unexpected", "owner/repo"}, unknown: true, migrate: true},
		{name: "migrate extra after --apply", args: []string{"project", "migrate", "owner/repo", "--apply", "extra"}, migrate: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home := writeDesignatedHome(t)
			before := ""
			if tc.migrate {
				before = writeProjectRegistry(t, home)
			}
			stdout, stderr, err := runProject(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertProjectUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
			assertProjectRegistryUnchanged(t, home, before)
		})
	}
}

func TestProjectCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		lab       string
		herdr     bool
		success   bool
		usage     bool
		unknown   bool
		domainErr string
	}{
		{name: "valid list", args: []string{"project", "list"}, lab: "list", success: true},
		{name: "valid list after dashdash", args: []string{"project", "list", "--"}, lab: "list", success: true},
		{name: "valid show", args: []string{"project", "show", "owner/clean-proj"}, lab: "show", success: true},
		{name: "valid show after dashdash", args: []string{"project", "show", "--", "owner/clean-proj"}, lab: "show", success: true},
		{name: "valid migrate inspect", args: []string{"project", "migrate", "owner/clean-proj"}, lab: "show", success: true},
		{name: "valid migrate --apply", args: []string{"project", "migrate", "owner/clean-proj", "--apply"}, lab: "show", herdr: true, domainErr: "registered coordinator"},
		{name: "valid enroll --host", args: []string{"project", "enroll", "owner/repo", "--host", "github.com"}, herdr: true, domainErr: "registered coordinator"},
		{name: "valid enroll --host=", args: []string{"project", "enroll", "owner/repo", "--host=github.com"}, herdr: true, domainErr: "registered coordinator"},
		{name: "valid enroll --remote", args: []string{"project", "enroll", "owner/repo", "--remote", "https://github.com/owner/repo.git"}, herdr: true, domainErr: "registered coordinator"},
		{name: "valid enroll --path", args: []string{"project", "enroll", "owner/repo", "--path", "/tmp/no-such-clone"}, herdr: true, domainErr: "registered coordinator"},
		{name: "project missing subcommand", args: []string{"project"}, usage: true},
		{name: "show missing name", args: []string{"project", "show"}, usage: true},
		{name: "enroll missing spec", args: []string{"project", "enroll"}, usage: true},
		{name: "migrate missing name", args: []string{"project", "migrate"}, usage: true},
		{name: "list extra positional", args: []string{"project", "list", "extra"}, lab: "list", usage: true},
		{name: "list unknown flag", args: []string{"project", "list", "--unexpected"}, lab: "list", usage: true, unknown: true},
		{name: "show extra positional", args: []string{"project", "show", "owner/clean-proj", "extra"}, lab: "show", usage: true},
		{name: "show unknown flag", args: []string{"project", "show", "owner/clean-proj", "--unexpected"}, lab: "show", usage: true, unknown: true},
		{name: "enroll extra positional", args: []string{"project", "enroll", "owner/repo", "extra"}, usage: true},
		{name: "enroll unknown flag", args: []string{"project", "enroll", "owner/repo", "--unexpected"}, usage: true, unknown: true},
		{name: "enroll extra after dashdash", args: []string{"project", "enroll", "owner/repo", "--", "extra"}, usage: true},
		{name: "enroll missing --host value", args: []string{"project", "enroll", "owner/repo", "--host"}, usage: true},
		{name: "migrate extra positional", args: []string{"project", "migrate", "owner/clean-proj", "extra"}, lab: "show", usage: true},
		{name: "migrate unknown flag", args: []string{"project", "migrate", "owner/clean-proj", "--unexpected"}, lab: "show", usage: true, unknown: true},
		{name: "migrate extra after --apply", args: []string{"project", "migrate", "owner/clean-proj", "--apply", "extra"}, lab: "show", usage: true},
		{name: "enroll requires coordinator", args: []string{"project", "enroll", "owner/repo"}, herdr: true, domainErr: "registered coordinator"},
		{name: "enroll requires pane without herdr", args: []string{"project", "enroll", "owner/repo"}, domainErr: "Herdr pane"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, before := projectUsageLab(t, tc.lab)
			if tc.herdr {
				herdrEnv(t, home)
				t.Setenv("HERDR_PANE_ID", "w-other:p1")
			} else {
				clearHerdrEnv(t)
			}
			stdout, stderr, err := runProject(t, home, tc.args...)
			switch {
			case tc.success:
				if err != nil {
					t.Fatalf("err = %v stderr=%s stdout=%s", err, stderr, stdout)
				}
				if !strings.Contains(stdout, "projects") && !strings.Contains(stdout, "owner/clean-proj") {
					t.Fatalf("stdout missing project payload:\n%s", stdout)
				}
			case tc.usage:
				assertProjectUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				assertProjectRegistryUnchanged(t, home, before)
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s", stdout)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.domainErr)
				}
				assertProjectRegistryUnchanged(t, home, before)
			}
		})
	}
}

func runProject(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertProjectUsageError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected usage failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "registered coordinator") || strings.Contains(msg, "Herdr pane") || strings.Contains(msg, "No enrolled project") {
		t.Fatalf("err = %v, want a usage failure", err)
	}
	usage := strings.Contains(msg, "unknown flag") ||
		strings.Contains(msg, "accepts 0 arg") ||
		strings.Contains(msg, "accepts 1 arg") ||
		strings.Contains(msg, "flag needs an argument") ||
		strings.Contains(msg, "command is required") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid project")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func projectUsageLab(t *testing.T, lab string) (home, before string) {
	t.Helper()
	home = writeDesignatedHome(t)
	switch lab {
	case "list", "show":
		root := t.TempDir()
		cleanPath := filepath.Join(root, "clean-proj")
		initGitClone(t, cleanPath, "https://github.com/owner/clean-proj.git")
		writeProjectsRegistry(t, home, fmt.Sprintf(`{"schema": 1, "projects": {"owner/clean-proj": %s}}`,
			projectRecordJSON("owner/clean-proj", "github.com", "owner", "clean-proj", "managed", cleanPath, "https://github.com/owner/clean-proj.git")))
		before = readProjectRegistry(t, home)
	}
	return home, before
}

func writeProjectRegistry(t *testing.T, home string) string {
	t.Helper()
	root := t.TempDir()
	cleanPath := filepath.Join(root, "clean-proj")
	initGitClone(t, cleanPath, "https://github.com/owner/repo.git")
	writeProjectsRegistry(t, home, fmt.Sprintf(`{"schema": 1, "projects": {"owner/repo": %s}}`,
		projectRecordJSON("owner/repo", "github.com", "owner", "repo", "managed", cleanPath, "https://github.com/owner/repo.git")))
	return readProjectRegistry(t, home)
}

func readProjectRegistry(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "projects.json"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertProjectRegistryUnchanged(t *testing.T, home, before string) {
	t.Helper()
	if got := readProjectRegistry(t, home); got != before {
		t.Fatalf("projects.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
	}
}
