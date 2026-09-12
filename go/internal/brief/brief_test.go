package brief

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestWriteInitialNamesCanonicalWorkerSkill(t *testing.T) {
	home := t.TempDir()
	s, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}

	runtime := t.TempDir()
	canonical := filepath.Join(runtime, "skills", "sum-worker", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonical, []byte("# sum-worker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sum-worker", filepath.Join(runtime, "skills", "worker")); err != nil {
		t.Fatal(err)
	}

	task := ordjson.NewObject()
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("brief", "do the thing")
	task.Set("repository", "owner/repo")
	task.Set("worktree", "/tmp/wt")
	task.Set("base_sha", "0123456789abcdef0123456789abcdef01234567")
	task.Set("branch", "sum-dev/x")
	task.Set("kind", "ship")
	task.Set("harness", "codex")

	path, err := WriteInitial(s, runtime, filepath.Join(runtime, "bin", "sumctl"), task)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, canonical) {
		t.Fatalf("brief does not name %s:\n%s", canonical, text)
	}
	leftover := filepath.Join(runtime, "skills", "worker", "SKILL.md")
	if strings.Contains(text, leftover) {
		t.Fatalf("brief names leftover alias %s:\n%s", leftover, text)
	}
}

func TestWorkerSkillDoesNotReadLeftoverAlias(t *testing.T) {
	runtime := t.TempDir()
	leftover := filepath.Join(runtime, "skills", "worker", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(leftover), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leftover, []byte("# leftover worker alias\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := WorkerSkill(runtime); got != "" {
		t.Fatalf("WorkerSkill = %q, want empty when only skills/worker exists", got)
	}
}
