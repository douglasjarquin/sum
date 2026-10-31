package taskcheckout

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid", "GIT_CONFIG_NOSYSTEM=1")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}
func repo(t *testing.T) (string, string) {
	t.Helper()
	r := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(r, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, r, "init", "-q")
	os.WriteFile(filepath.Join(r, "file"), []byte("base\n"), 0600)
	git(t, r, "add", "file")
	git(t, r, "commit", "-qm", "base")
	return r, git(t, r, "rev-parse", "HEAD")
}
func TestCreateReconcilesGitAndPreservesWork(t *testing.T) {
	r, base := repo(t)
	intent, err := Plan(filepath.Join(t.TempDir(), "state"), r, "t-123abc", base)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Create(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if first.Path != intent.Path || first.Branch != "sum/t-123abc" || first.Head != base {
		t.Fatalf("%#v", first)
	}
	os.WriteFile(filepath.Join(intent.Path, "file"), []byte("unfinished\n"), 0600)
	again, err := Create(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if again.CommonDir != first.CommonDir {
		t.Fatal("common dir changed")
	}
	data, _ := os.ReadFile(filepath.Join(intent.Path, "file"))
	if string(data) != "unfinished\n" {
		t.Fatal("unfinished work reset")
	}
	git(t, intent.Path, "add", "file")
	git(t, intent.Path, "commit", "-qm", "advance")
	if _, err = Create(context.Background(), intent); err == nil {
		t.Fatal("changed HEAD silently adopted for initial creation")
	}
	if _, err = Verify(context.Background(), intent, false); err != nil {
		t.Fatal(err)
	}
}
func TestUnregisteredAndWrongBranchRefuse(t *testing.T) {
	r, base := repo(t)
	intent, err := Plan(filepath.Join(t.TempDir(), "state"), r, "t-cafe", base)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(intent.Path, 0700)
	os.WriteFile(filepath.Join(intent.Path, "keep"), []byte("retain"), 0600)
	if _, err = Create(context.Background(), intent); err == nil {
		t.Fatal("unregistered path overwritten")
	}
	if _, err = os.Stat(filepath.Join(intent.Path, "keep")); err != nil {
		t.Fatal(err)
	}
	bad := intent
	bad.Path = r
	if _, err = Verify(context.Background(), bad, false); err == nil {
		t.Fatal("main checkout accepted")
	}
}
func TestPlanRejectsEscapes(t *testing.T) {
	r, base := repo(t)
	for _, id := range []string{"../escape", "t-../../x", "", "name"} {
		if _, err := Plan(t.TempDir(), r, id, base); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
}
