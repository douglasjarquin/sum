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
	cleanup := d.ctl(true, "cleanup", id)
	if got := asString(asMap(cleanup["resources"])["workspace"]); got != "present" {
		t.Fatalf("cleanup workspace resource after rebind = %v, want present", got)
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

func TestReportedClosedWorkspaceRecoversThroughRebindAndResume(t *testing.T) {
	d := newPolicyLab(t)
	d.setEnv("FAKE_CLOSE_LAST_WORKSPACE", "1")
	d.setEnv("FAKE_AGENT_CHILDREN", "2")
	repo := policyProject(t, d.base, "reported-workspace-recovery", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "claude", "--approved")
	id := asString(task["id"])
	worktree := asString(task["worktree"])
	oldPane := asString(task["pane"])
	oldAttempt := asString(asMap(asMap(task["execution"])["worker"])["id"])
	handoff, err := json.Marshal(map[string]any{"outcome": "completed", "candidate": task["base_sha"]})
	if err != nil {
		t.Fatal(err)
	}
	handoffPath := filepath.Join(d.base, "handoff.json")
	if err := os.WriteFile(handoffPath, handoff, 0o600); err != nil {
		t.Fatal(err)
	}
	d.ctlPane(oldPane, true, "report", id, "--text", "Candidate ready.", "--handoff", handoffPath)
	settleFakeWorker(t, d.base, oldPane)
	swept := d.ctl(true, "sweep")
	var closed bool
	for _, raw := range asSlice(swept["rows"]) {
		row := asMap(raw)
		if asString(row["action"]) == "pane-close" && asString(row["pane"]) == oldPane && asString(row["state"]) == "closed" {
			closed = true
		}
	}
	if !closed {
		t.Fatalf("sweep did not close the reported worker pane: %v", swept)
	}
	state := readFakeHerdrState(t, filepath.Join(d.base, "fake", "state.json"))
	oldWorkspace := asString(task["workspace"])
	if asMap(state["workspaces"])[oldWorkspace] != nil || asMap(state["panes"])[oldPane] != nil {
		t.Fatal("closing the last reported worker pane did not remove its workspace")
	}

	failedResume := d.ctl(false, "execution", "resume", id, "--attempt", oldAttempt)
	if !strings.Contains(asString(failedResume["error"]), "herdr worktree open --path "+worktree+" --no-focus") {
		t.Fatalf("resume with removed workspace = %v", failedResume)
	}
	failedTask := readTaskObject(t, d.home, id)
	uncertainAttempt := asMap(asMap(failedTask["execution"])["worker"])
	uncertainID := asString(uncertainAttempt["id"])
	if asString(uncertainAttempt["state"]) != "uncertain" {
		t.Fatalf("attempt after failed resume = %v, want uncertain", uncertainAttempt)
	}

	const reboundWorkspace = "w-reopened"
	const reboundPane = reboundWorkspace + ":p1"
	writeReopenedWorkspace(t, d, worktree, reboundWorkspace, repo)
	bound := d.ctl(true, "bind", id, "--worker-pane", reboundPane)
	if asString(bound["workspace"]) != reboundWorkspace {
		t.Fatalf("rebind result = %v", bound)
	}
	state = readFakeHerdrState(t, filepath.Join(d.base, "fake", "state.json"))
	rebound := asMap(asMap(state["panes"])[reboundPane])
	rebound["agent"] = nil
	delete(rebound, "processes")
	writeFakeHerdrState(t, filepath.Join(d.base, "fake", "state.json"), state)
	parked := d.ctl(true, "execution", "park", id, "--attempt", uncertainID)
	if parked["released"] != true {
		t.Fatalf("park uncertain attempt after rebind = %v", parked)
	}
	refused := d.ctl(false, "repair", "send", id, "--attempt", uncertainID, "--key", "before-resume", "--text", "Apply the correction.")
	if errText := asString(refused["error"]); !strings.Contains(errText, "exact running worker attempt") {
		t.Fatalf("repair send to released attempt = %v", refused)
	}
	resumed := d.ctl(true, "execution", "resume", id, "--attempt", uncertainID)
	resumedAttempt := asMap(asMap(readTaskObject(t, d.home, id)["execution"])["worker"])
	resumedID := asString(resumedAttempt["id"])
	if asString(resumedAttempt["state"]) != "running" || resumedID == uncertainID {
		t.Fatalf("attempt after successful resume = %v; command result %v", resumedAttempt, resumed)
	}
	settleFakeWorker(t, d.base, reboundPane)
	sent := d.ctl(false, "repair", "send", id, "--attempt", resumedID, "--key", "after-resume", "--text", "Apply the correction.")
	if errText := asString(sent["error"]); errText != "" {
		t.Fatalf("repair send after resume = %v", sent)
	}

	d.ctlPane(reboundPane, true, "report", id, "--text", "Corrected candidate ready.", "--handoff", handoffPath)
	settleFakeWorker(t, d.base, reboundPane)
	finalSweep := d.ctl(true, "sweep")
	closed = false
	for _, raw := range asSlice(finalSweep["rows"]) {
		row := asMap(raw)
		if asString(row["action"]) == "pane-close" && asString(row["pane"]) == reboundPane && asString(row["state"]) == "closed" {
			closed = true
		}
	}
	if !closed {
		t.Fatalf("sweep after rebind did not close the rebound worker pane: %v", finalSweep)
	}
	state = readFakeHerdrState(t, filepath.Join(d.base, "fake", "state.json"))
	if asMap(state["workspaces"])[reboundWorkspace] != nil {
		t.Fatal("sweep did not remove the rebound workspace after closing its last pane")
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
