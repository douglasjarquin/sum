package prepare

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestWorkspaceMatchesSnapshotRequiresCoordinatorRebindEvidence(t *testing.T) {
	checkout := t.TempDir()
	repository := t.TempDir()
	rebind := ordjson.NewObject()
	for key, value := range map[string]any{
		"kind": "worker-workspace-rebind", "source": "coordinator",
		"from_workspace": "w-old", "to_workspace": "w-new", "old_workspace_code": "workspace_not_found",
		"checkout": checkout, "git_root": checkout, "repository": repository,
		"git_common_dir": filepath.Join(repository, ".git"), "branch": "sum/task",
	} {
		rebind.Set(key, value)
	}
	snapshot := ordjson.NewObject()
	snapshot.Set("workspace", "w-old")
	snapshot.Set("path", checkout)
	snapshot.Set("git_root", checkout)
	snapshot.Set("branch", "sum/task")
	task := ordjson.NewObject()
	task.Set("workspace", "w-new")
	task.Set("repository", repository)
	task.Set("evidence", []any{rebind})
	common := filepath.Join(repository, ".git")
	if !workspaceMatchesSnapshot(task, snapshot, common) {
		t.Fatal("coordinator rebind evidence did not validate the replacement workspace")
	}
	rebind.Set("source", "worker")
	if workspaceMatchesSnapshot(task, snapshot, common) {
		t.Fatal("worker-authored rebind evidence validated the replacement workspace")
	}
}

func TestEnsureWorkerPaneReportsObservedPaneIdentity(t *testing.T) {
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
		"panes": map[string]any{"w-worker:p1": map[string]any{
			"pane_id": "w-worker:p1", "cwd": home, "workspace_id": "w-observed",
		}},
		"workspaces": map[string]any{},
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
	task.Set("workspace", "w-expected")
	task.Set("worktree", checkout)
	_, err = EnsureWorkerPane(root, "sum-test", task)
	if err == nil || !strings.Contains(err.Error(), "observed cwd "+home) || !strings.Contains(err.Error(), "pane workspace w-observed") || !strings.Contains(err.Error(), "expected checkout "+checkout) || !strings.Contains(err.Error(), "workspace w-expected") {
		t.Fatalf("EnsureWorkerPane mismatch error = %v", err)
	}
}

func TestEnsureWorkerPaneExplainsGoneWorkspaceRecovery(t *testing.T) {
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
	state, err := json.Marshal(map[string]any{"panes": map[string]any{}, "workspaces": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(herdrRoot, "state.json"), state, 0o600); err != nil {
		t.Fatal(err)
	}
	checkout := t.TempDir()
	task := ordjson.NewObject()
	task.Set("pane", "w-old:p1")
	task.Set("workspace", "w-old")
	task.Set("worktree", checkout)
	_, err = EnsureWorkerPane(root, "sum-test", task)
	if err == nil || !strings.Contains(err.Error(), "herdr worktree open --path "+checkout+" --no-focus") || !strings.Contains(err.Error(), "bind TASK_ID --worker-pane PANE_ID") || !strings.Contains(err.Error(), "docs/recovery.md") {
		t.Fatalf("EnsureWorkerPane missing-workspace error = %v", err)
	}
}
