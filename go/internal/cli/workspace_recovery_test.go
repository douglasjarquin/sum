package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBindWorkerPaneRecoversGoneWorkspace(t *testing.T) {
	d, task, worktree, pane := workspaceRecoveryFixture(t, false)
	id := asString(task["id"])
	oldWorkspace := asString(task["workspace"])
	reopenedWorkspace := "w-reopened"

	bound := d.ctl(true, "bind", id, "--worker-pane", pane)
	if asString(bound["workspace"]) != reopenedWorkspace {
		t.Fatalf("bind workspace = %v, want %s", bound["workspace"], reopenedWorkspace)
	}
	saved := readTaskObject(t, d.home, id)
	if asString(saved["workspace"]) != reopenedWorkspace {
		t.Fatalf("saved task workspace = %v, want %s", saved["workspace"], reopenedWorkspace)
	}
	var rebind map[string]any
	for _, row := range asSlice(saved["evidence"]) {
		if asString(asMap(row)["kind"]) == "worker-workspace-rebind" {
			rebind = asMap(row)
		}
	}
	if asString(rebind["from_workspace"]) != oldWorkspace || asString(rebind["to_workspace"]) != reopenedWorkspace || asString(rebind["old_workspace_code"]) != "workspace_not_found" {
		t.Fatalf("workspace rebind evidence = %v", rebind)
	}
	attempt := asString(asMap(asMap(task["execution"])["worker"])["id"])
	setReviewerPane(t, d.base, pane, worktree, nil, 7101)
	parked := d.ctl(true, "execution", "park", id, "--attempt", attempt)
	if parked["released"] != true {
		t.Fatalf("park after workspace rebind = %v", parked)
	}
	setReviewerPane(t, d.base, pane, worktree, nil, 7101)
	editFakePane(t, d, pane, func(row map[string]any) { delete(row, "processes") })
	resumed := d.ctl(true, "execution", "resume", id, "--attempt", attempt)
	if asString(resumed["status"]) != "running" {
		t.Fatalf("resume after workspace rebind = %v", resumed)
	}
}

func TestBindWorkerPaneRefusesWorkspaceRebindWithoutIdentity(t *testing.T) {
	d, task, worktree, pane := workspaceRecoveryFixture(t, false)
	workspace := "w-reopened"
	writeReopenedWorkspace(t, d, worktree, workspace, filepath.Join(d.base, "other-repository"))
	out := d.ctl(false, "bind", asString(task["id"]), "--worker-pane", pane)
	if errText := asString(out["error"]); errText == "" || !strings.Contains(errText, "does not match the recorded checkout identity") {
		t.Fatalf("bind with mismatched replacement identity = %v", out)
	}
	if got := asString(readTaskObject(t, d.home, asString(task["id"]))["workspace"]); got != asString(task["workspace"]) {
		t.Fatalf("refused bind changed task workspace to %s", got)
	}
}

func TestBindWorkerPaneRefusesWorkspaceRebindWhenOldWorkspaceExists(t *testing.T) {
	d, task, _, pane := workspaceRecoveryFixture(t, true)
	out := d.ctl(false, "bind", asString(task["id"]), "--worker-pane", pane)
	if errText := asString(out["error"]); errText == "" || !strings.Contains(errText, "not proven gone") {
		t.Fatalf("bind with live recorded workspace = %v", out)
	}
}

func workspaceRecoveryFixture(t *testing.T, keepOld bool) (*demoLab, map[string]any, string, string) {
	t.Helper()
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "workspace-recovery", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "claude", "--approved")
	statePath := filepath.Join(d.base, "fake", "state.json")
	state := readFakeHerdrState(t, statePath)
	if !keepOld {
		pane := asString(task["pane"])
		d.setEnv("FAKE_CLOSE_LAST_WORKSPACE", "1")
		closeFakePane(t, d, pane)
		state = readFakeHerdrState(t, statePath)
		if asMap(state["workspaces"])[asString(task["workspace"])] != nil || asMap(state["panes"])[pane] != nil {
			t.Fatal("closing the last worker pane did not remove its Herdr workspace")
		}
	}
	const workspace = "w-reopened"
	writeState := state
	writeState["workspaces"].(map[string]any)[workspace] = map[string]any{
		"workspace_id": workspace,
		"worktree":     map[string]any{"checkout_path": task["worktree"], "repo_root": repo, "is_linked_worktree": true},
	}
	pane := workspace + ":p1"
	writeState["panes"].(map[string]any)[pane] = map[string]any{
		"pane_id": pane, "cwd": task["worktree"], "workspace_id": workspace,
		"agent_status": "idle", "agent": "claude", "agent_pid": 7100, "shell_pid": 7101,
		"processes": []any{}, "created": true, "terminal_id": "term-reopened",
	}
	writeFakeHerdrState(t, statePath, writeState)
	return d, task, asString(task["worktree"]), pane
}

func closeFakePane(t *testing.T, d *demoLab, pane string) {
	t.Helper()
	cmd := exec.Command(filepath.Join(d.root, "tests", "fixtures", "herdr.py"), "--session", "sum-test", "pane", "close", pane)
	cmd.Env = d.env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fake Herdr pane close: %v\n%s", err, out)
	}
}

func writeReopenedWorkspace(t *testing.T, d *demoLab, worktree, workspace, replacementRepo string) {
	t.Helper()
	statePath := filepath.Join(d.base, "fake", "state.json")
	state := readFakeHerdrState(t, statePath)
	state["workspaces"].(map[string]any)[workspace] = map[string]any{
		"workspace_id": workspace,
		"worktree":     map[string]any{"checkout_path": worktree, "repo_root": replacementRepo, "is_linked_worktree": true},
	}
	pane := workspace + ":p1"
	state["panes"].(map[string]any)[pane] = map[string]any{
		"pane_id": pane, "cwd": worktree, "workspace_id": workspace,
		"agent_status": "idle", "agent": "claude", "agent_pid": 7100, "shell_pid": 7101,
		"processes": []any{}, "created": true, "terminal_id": "term-reopened",
	}
	writeFakeHerdrState(t, statePath, state)
}

func readFakeHerdrState(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func writeFakeHerdrState(t *testing.T, path string, state map[string]any) {
	t.Helper()
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
