package launch

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrustGrokRepositoryRecordsTheCommonGitParent(t *testing.T) {
	repo, worktree := grokTrustFixture(t)
	grokHome := t.TempDir()
	if err := TrustGrokRepository(t.Context(), grokHome, worktree); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(grokHome, "trusted_folders.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	folder := grokTrustFolder(t, worktree)
	if same, err := samePath(folder, repo); err != nil || !same {
		t.Fatalf("common git parent = %s, repository = %s (%v)", folder, repo, err)
	}
	text := string(data)
	if !strings.Contains(text, folder) || strings.Contains(text, worktree) {
		t.Fatalf("trusted the task worktree instead of the repository grok asks about: %s", text)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("trust file mode = %o, want 0600", info.Mode().Perm())
	}
	before := append([]byte(nil), data...)
	if err := TrustGrokRepository(t.Context(), grokHome, worktree); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("rewrote an already trusted repository:\n%s", after)
	}
}

func TestTrustGrokRepositoryKeepsAnExplicitRefusal(t *testing.T) {
	_, worktree := grokTrustFixture(t)
	folder := grokTrustFolder(t, worktree)
	grokHome := t.TempDir()
	kept := "[folders.\"" + folder + "\"]\ntrusted = false\ndecided_at = 10\n"
	path := filepath.Join(grokHome, "trusted_folders.toml")
	if err := os.WriteFile(path, []byte(kept), 0o600); err != nil {
		t.Fatal(err)
	}
	err := TrustGrokRepository(t.Context(), grokHome, worktree)
	if err == nil || !strings.Contains(err.Error(), "is false") {
		t.Fatalf("flipped an explicit grok refusal: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != kept {
		t.Fatalf("changed a refused folder: %s", data)
	}
}

func grokTrustFixture(t *testing.T) (repo, worktree string) {
	t.Helper()
	repo = t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-b", "main")
	git("config", "user.email", "test@example.invalid")
	git("config", "user.name", "sum test")
	git("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "README.md")
	git("commit", "-q", "-m", "fixture")
	worktree = filepath.Join(t.TempDir(), "wt")
	git("worktree", "add", "--detach", worktree)
	return repo, worktree
}

func grokTrustFolder(t *testing.T, worktree string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", worktree, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	common := strings.TrimSpace(string(out))
	if filepath.Base(common) != ".git" {
		t.Fatalf("common dir = %s", common)
	}
	return filepath.Dir(common)
}

func samePath(left, right string) (bool, error) {
	a, err := filepath.EvalSymlinks(left)
	if err != nil {
		return false, err
	}
	b, err := filepath.EvalSymlinks(right)
	if err != nil {
		return false, err
	}
	return a == b, nil
}
