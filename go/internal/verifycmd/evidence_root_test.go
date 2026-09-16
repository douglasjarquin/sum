package verifycmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func TestWorkerEvidenceRootRefusesAbsolutePathOutsideWorktree(t *testing.T) {
	worktree := t.TempDir()
	outside := t.TempDir()
	cmp := filepath.Join(outside, "run-1", "scenario", "comparison.json")
	if err := os.MkdirAll(filepath.Dir(cmp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cmp, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	handoff := ordjson.NewObject()
	handoff.Set("artifacts", []any{cmp})
	record := ordjson.NewObject()
	record.Set("kind", "handoff")
	record.Set("handoff", handoff)
	task := ordjson.NewObject()
	task.Set("worktree", worktree)
	task.Set("evidence", []any{record})
	if got := workerEvidenceRoot(task); got != "" {
		t.Fatalf("workerEvidenceRoot = %q, want empty for a comparison outside the worktree", got)
	}
}

func TestWorkerEvidenceRootAcceptsAPathInsideTheWorktree(t *testing.T) {
	worktree := t.TempDir()
	cmp := filepath.Join(worktree, ".artifacts", "evidence", "run-1", "scenario", "comparison.json")
	if err := os.MkdirAll(filepath.Dir(cmp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cmp, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	handoff := ordjson.NewObject()
	handoff.Set("artifacts", []any{cmp})
	record := ordjson.NewObject()
	record.Set("kind", "handoff")
	record.Set("handoff", handoff)
	task := ordjson.NewObject()
	task.Set("worktree", worktree)
	task.Set("evidence", []any{record})
	got := workerEvidenceRoot(task)
	want := filepath.Join(worktree, ".artifacts", "evidence")
	if got != want {
		t.Fatalf("workerEvidenceRoot = %q, want %q", got, want)
	}
}
