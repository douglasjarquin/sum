package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const unregisteredWorkerReason = "Recipient pane is not registered as this task's worker in this instance"

// A worker pane whose registration is missing (for example a launch
// interrupted before the harness came up) gets a not-delivered notice, never a
// crash, from every command that delivers returns to it.
func TestNoticeToUnregisteredWorkerIsNotDelivered(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "unregistered", map[string]string{"README.md": "A project.\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	taskID, pane := asString(task["id"]), asString(task["pane"])
	d.ctlPane(pane, true, "ask", taskID, "--key", "choice", "--text", "Which way?")
	removeWorkerRegistration(t, d.home, taskID)
	settleFakePane(t, d.base, pane)

	var questionID string
	for _, item := range asSlice(d.ctl(true, "show", taskID)["questions"]) {
		questionID = asString(asMap(item)["id"])
	}
	answered := d.ctl(true, "answer", taskID, questionID, "--text", "This way.")
	assertUnregisteredNotice(t, "answer", answered)

	noticed := d.ctl(true, "notice", taskID, "--to", "worker")
	assertUnregisteredNotice(t, "notice", noticed)

	d.ctl(true, "pump", "--task", taskID, "--force")
	d.ctl(true, "inbox", "--live")
}

func removeWorkerRegistration(t *testing.T, home, taskID string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(home, "sessions", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	removed := 0
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), `"worker"`) && strings.Contains(string(raw), taskID) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			removed++
		}
	}
	if removed != 1 {
		t.Fatalf("removed %d worker registrations from %v", removed, paths)
	}
}

func settleFakePane(t *testing.T, base, pane string) {
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
	asMap(asMap(state["panes"])[pane])["agent_status"] = "idle"
	raw, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertUnregisteredNotice(t *testing.T, command string, view map[string]any) {
	t.Helper()
	notice := asMap(view["notice"])
	if notice == nil {
		notice = view
	}
	rows := asSlice(asMap(notice["returns"])["recipients"])
	if len(rows) != 1 {
		t.Fatalf("%s: want one recipient row, got %v", command, view)
	}
	row := asMap(rows[0])
	if asString(row["state"]) != "not-delivered" || !strings.HasPrefix(asString(row["reason"]), unregisteredWorkerReason) {
		t.Fatalf("%s: row %v", command, row)
	}
	if !strings.HasPrefix(asString(notice["error"]), unregisteredWorkerReason) {
		t.Fatalf("%s: notice %v", command, notice)
	}
}
