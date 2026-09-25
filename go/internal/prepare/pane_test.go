package prepare

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func TestEnsureWorkerPane_createsFreshTabWhenPaneIsGone(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	herdrRoot := filepath.Join(home, "fake-herdr")
	if err := os.MkdirAll(herdrRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_HERDR_BIN", filepath.Join(root, "tests", "fixtures", "herdr.py"))
	t.Setenv("FAKE_HERDR_ROOT", herdrRoot)
	t.Setenv("FAKE_SESSION", "sum-test")
	t.Setenv("HERDR_SESSION", "sum-test")
	checkout := t.TempDir()
	state := map[string]any{
		"panes": map[string]any{
			"w-parent:p1": map[string]any{
				"pane_id": "w-parent:p1", "cwd": home, "workspace_id": "w-parent",
				"agent_status": "idle", "agent": "claude",
			},
		},
		"workspaces": map[string]any{
			"w-worker": map[string]any{
				"workspace_id": "w-worker", "label": "task",
				"worktree": map[string]any{"checkout_path": checkout},
			},
		},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(herdrRoot, "state.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("pane", "w-worker:p1")
	task.Set("workspace", "w-worker")
	task.Set("worktree", checkout)
	pane, err := EnsureWorkerPane(root, "sum-test", task)
	if err != nil {
		t.Fatalf("EnsureWorkerPane: %v", err)
	}
	if pane == "" || pane == "w-worker:p1" {
		t.Fatalf("pane = %q, want a new pane in the existing workspace", pane)
	}
	if got := asString(func() any { v, _ := task.Get("pane"); return v }()); got != pane {
		t.Fatalf("task.pane = %q, want %q", got, pane)
	}
}

func TestEnsureWorkerPane_reusesLivePane(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	herdrRoot := filepath.Join(home, "fake-herdr")
	if err := os.MkdirAll(herdrRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_HERDR_BIN", filepath.Join(root, "tests", "fixtures", "herdr.py"))
	t.Setenv("FAKE_HERDR_ROOT", herdrRoot)
	t.Setenv("FAKE_SESSION", "sum-test")
	checkout := t.TempDir()
	state := map[string]any{
		"panes": map[string]any{
			"w-worker:p1": map[string]any{
				"pane_id": "w-worker:p1", "cwd": checkout, "workspace_id": "w-worker",
				"agent_status": "unknown", "agent": nil, "created": true,
			},
		},
		"workspaces": map[string]any{
			"w-worker": map[string]any{"workspace_id": "w-worker"},
		},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(herdrRoot, "state.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("pane", "w-worker:p1")
	task.Set("workspace", "w-worker")
	task.Set("worktree", checkout)
	pane, err := EnsureWorkerPane(root, "sum-test", task)
	if err != nil {
		t.Fatalf("EnsureWorkerPane: %v", err)
	}
	if pane != "w-worker:p1" {
		t.Fatalf("pane = %q, want the live pane", pane)
	}
}
