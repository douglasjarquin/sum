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
	stop := asMap(rebind["stop_evidence"])
	if asString(stop["agent_code"]) != "agent_not_found" || stop["pane_present"] != true || stop["old_shell_pid"] != float64(6124) || asString(stop["old_cwd"]) != worktree || len(asSlice(stop["excluded_pids"])) == 0 {
		t.Fatalf("reviewer stop observation = %v", stop)
	}
	setReviewerPane(t, d.base, "w-review2:p1", worktree, nil, 6126)
	cleanupBeforeNewReview := d.ctl(false, "cleanup", taskID, "--reviewer-only")
	if asMap(asMap(cleanupBeforeNewReview["reviewer"]))["closable"] == true || asString(cleanupBeforeNewReview["state"]) != "blocked" || asString(asMap(cleanupBeforeNewReview["reviewer"])["reason"]) != "no saved reviewer findings" {
		t.Fatalf("cleanup accepted only the old reviewer's findings: %v", cleanupBeforeNewReview)
	}
	setReviewerPane(t, d.base, "w-review2:p1", worktree, map[string]any{"agent": "codex", "agent_status": "idle", "agent_pid": 6125}, 6126)
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

func TestBindReviewerPaneRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*testing.T, *demoLab, string, string)
		pane  string
		want  string
	}{
		{name: "prior reviewer pane reused", pane: "w-review:p1", setup: func(t *testing.T, d *demoLab, _, worktree string) {
			setReviewerPane(t, d.base, "w-review:p1", worktree, map[string]any{"agent": "codex", "agent_status": "idle", "agent_pid": 6123}, 6124)
		}, want: "must differ from the recorded reviewer pane"},
		{name: "reviewer on another machine", setup: func(t *testing.T, d *demoLab, taskID, _ string) {
			editTaskObject(t, d.home, taskID, func(task map[string]any) { asMap(task["reviewer"])["machine"] = "m-other" })
		}, want: "another machine or session"},
		{name: "reviewer in another session", setup: func(t *testing.T, d *demoLab, taskID, _ string) {
			editTaskObject(t, d.home, taskID, func(task map[string]any) { asMap(task["reviewer"])["session"] = "other-session" })
		}, want: "another machine or session"},
		{name: "old pane has foreground process", setup: func(t *testing.T, d *demoLab, _, worktree string) {
			editFakePane(t, d, "w-review:p1", func(pane map[string]any) {
				pane["processes"] = []any{map[string]any{"pid": 6124, "name": "sleep", "argv": []string{"sleep", "120"}}}
			})
		}, want: "still has foreground processes"},
		{name: "another process is bound to checkout", setup: func(t *testing.T, d *demoLab, _, worktree string) {
			path := filepath.Join(d.base, "fake-lsof", "cwds.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(`{"processes":[{"pid":8123,"cwd":"`+worktree+`"}]}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "process 8123 remains bound"},
		{name: "process-info failure", setup: func(t *testing.T, d *demoLab, _, _ string) {
			editFakePane(t, d, "w-review:p1", func(pane map[string]any) { pane["process_info_error"] = "server_busy" })
		}, want: "server_busy"},
		{name: "unproven agent code", setup: func(t *testing.T, d *demoLab, _, _ string) {
			editFakePane(t, d, "w-review:p1", func(pane map[string]any) { pane["agent_get_error"] = "server_busy" })
		}, want: "server_busy"},
		{name: "replacement cwd mismatch", setup: func(t *testing.T, d *demoLab, _, _ string) {
			setReviewerPane(t, d.base, "w-review2:p1", "/tmp/not-the-checkout", map[string]any{"agent": "codex", "agent_status": "idle", "agent_pid": 6125}, 6126)
		}, want: "Reviewer cwd does not match"},
		{name: "old pane cwd changed", setup: func(t *testing.T, d *demoLab, _, _ string) {
			editFakePane(t, d, "w-review:p1", func(pane map[string]any) { pane["cwd"] = "/tmp/not-the-checkout" })
		}, want: "cwd changed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, taskID, worktree := reviewerRebindLab(t)
			if tc.setup != nil {
				tc.setup(t, d, taskID, worktree)
			}
			pane := tc.pane
			if pane == "" {
				pane = "w-review2:p1"
			}
			out := d.ctl(false, "bind", taskID, "--reviewer-pane", pane)
			if !strings.Contains(asString(out["error"]), tc.want) {
				t.Fatalf("bind output = %v, want refusal containing %q", out, tc.want)
			}
		})
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

func editTaskObject(t *testing.T, home, taskID string, edit func(map[string]any)) {
	t.Helper()
	task := readTaskObject(t, home, taskID)
	edit(task)
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "tasks", taskID, "task.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func editFakePane(t *testing.T, d *demoLab, pane string, edit func(map[string]any)) {
	t.Helper()
	path := filepath.Join(d.base, "fake", "state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	edit(asMap(asMap(state["panes"])[pane]))
	raw, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
