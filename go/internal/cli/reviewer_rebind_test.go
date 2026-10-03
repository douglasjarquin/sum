package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reviewerRebindLab(t *testing.T) (*demoLab, string, string) {
	t.Helper()
	d, taskID := reviewLab(t)
	d.ctlPane("w-review:p1", true, "review", taskID, "--verdict", "changes-requested", "--candidate", reviewCandidate, "--text", "first review")
	worktree := asString(readTaskObject(t, d.home, taskID)["worktree"])
	setReviewerPane(t, d.base, "w-review:p1", worktree, nil, 6124)
	setReviewerPane(t, d.base, "w-review2:p1", worktree, map[string]any{"agent": "codex", "agent_status": "idle", "agent_pid": 6125}, 6126)
	return d, taskID, worktree
}

func setReviewerPane(t *testing.T, base, pane, cwd string, agent map[string]any, shell int) {
	t.Helper()
	path := filepath.Join(base, "fake", "state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	panes := asMap(state["panes"])
	row := asMap(panes[pane])
	if row == nil {
		row = map[string]any{"pane_id": pane, "workspace_id": strings.Split(pane, ":")[0], "terminal_id": "term-" + pane}
		panes[pane] = row
	}
	row["cwd"] = cwd
	row["shell_pid"] = shell
	row["processes"] = []any{}
	if agent == nil {
		row["agent"] = nil
		row["agent_status"] = "unknown"
		delete(row, "agent_pid")
	} else {
		for key, value := range agent {
			row[key] = value
		}
	}
	raw, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBindReviewerPaneRebindsAfterRecordedReviewerStops(t *testing.T) {
	d, taskID, worktree := reviewerRebindLab(t)
	out := d.ctl(true, "bind", taskID, "--reviewer-pane", "w-review2:p1")
	if asString(asMap(out["reviewer"])["pane"]) != "w-review2:p1" {
		t.Fatalf("reviewer = %v, want new pane", out["reviewer"])
	}
	if asMap(asMap(out["reviewer"])["incarnation"]) == nil {
		t.Fatalf("reviewer endpoint omitted inspected incarnation: %v", out["reviewer"])
	}
	task := readTaskObject(t, d.home, taskID)
	var rebind map[string]any
	for _, raw := range asSlice(task["evidence"]) {
		if asString(asMap(raw)["kind"]) == "reviewer-rebind" {
			rebind = asMap(raw)
		}
	}
	if asString(asMap(rebind["from"])["pane"]) != "w-review:p1" || asString(asMap(rebind["to"])["pane"]) != "w-review2:p1" || asString(rebind["source"]) != "coordinator" {
		t.Fatalf("reviewer rebind evidence = %v", rebind)
	}
	review := d.ctlPane("w-review2:p1", true, "review", taskID, "--verdict", "approve", "--candidate", reviewCandidate, "--text", "second review")
	if asString(asMap(asMap(review["evidence"])["endpoint"])["pane"]) != "w-review2:p1" {
		t.Fatalf("review evidence endpoint = %v", asMap(review["evidence"])["endpoint"])
	}
	context := d.ctl(true, "context", taskID, "--role", "coordinator", "--section", "execution")
	if asString(asMap(asMap(asMap(context["execution"])["endpoints"])["reviewer"])["pane"]) != "w-review2:p1" {
		t.Fatalf("context reviewer endpoint = %v", asMap(asMap(asMap(context["execution"])["endpoints"])["reviewer"]))
	}
	cleanup := d.ctl(false, "cleanup", taskID, "--reviewer-only")
	if asString(asMap(cleanup["reviewer"])["pane"]) != "w-review2:p1" {
		t.Fatalf("reviewer-only cleanup endpoint = %v", cleanup["reviewer"])
	}
	if !strings.Contains(worktree, "worktrees with spaces") {
		t.Fatalf("test task worktree = %q", worktree)
	}
}

func TestBindReviewerPaneRefusesLiveRecordedReviewer(t *testing.T) {
	d, taskID, worktree := reviewerRebindLab(t)
	setReviewerPane(t, d.base, "w-review:p1", worktree, map[string]any{"agent": "codex", "agent_status": "idle", "agent_pid": 6123}, 6124)
	out := d.ctl(false, "bind", taskID, "--reviewer-pane", "w-review2:p1")
	if !strings.Contains(asString(out["error"]), "still holds a live reviewer agent") {
		t.Fatalf("bind output = %v, want live reviewer refusal", out)
	}
}

func TestReviewRefusalNamesReviewerRebindRoute(t *testing.T) {
	d, taskID, worktree := reviewerRebindLab(t)
	setReviewerPane(t, d.base, "w-review2:p1", worktree, map[string]any{"agent": "codex", "agent_status": "idle", "agent_pid": 6125}, 6126)
	out := d.ctlPane("w-review2:p1", false, "review", taskID, "--verdict", "approve", "--candidate", reviewCandidate, "--text", "second review")
	if !strings.Contains(asString(out["error"]), "bind TASK --reviewer-pane PANE") {
		t.Fatalf("review output = %v, want supported reviewer rebind route", out)
	}
}

func TestBindReviewerPaneRefusesWorkerPane(t *testing.T) {
	d, taskID, _ := reviewerRebindLab(t)
	workerPane := asString(readTaskObject(t, d.home, taskID)["pane"])
	out := d.ctl(false, "bind", taskID, "--reviewer-pane", workerPane)
	if !strings.Contains(asString(out["error"]), "worker pane cannot be adopted") {
		t.Fatalf("bind output = %v, want worker-pane refusal", out)
	}
}

func readTaskObject(t *testing.T, home, taskID string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "tasks", taskID, "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	var task map[string]any
	if err := json.Unmarshal(raw, &task); err != nil {
		t.Fatal(err)
	}
	return task
}
