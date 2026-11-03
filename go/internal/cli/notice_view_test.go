package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// Parent obligations stay pending for collection; only worker delivery produces a notice sidecar.
func TestNoticeViewIsDerivedFromTheReturnsSidecar(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "notice-view", map[string]string{"README.md": "A project.\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	taskID, pane := asString(task["id"]), workerAddress(task)
	d.ctlPane(pane, true, "init")
	record := filepath.Join(d.home, "tasks", taskID, "task.json")

	asked := d.ctlPane(pane, true, "ask", taskID, "--key", "choice", "--text", "Which way?")
	assertPendingParentNotice(t, "ask", asMap(asked["notice"]))
	before, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}

	duplicate := d.ctlPane(pane, true, "ask", taskID, "--key", "choice", "--text", "Which way?")
	if duplicate["duplicate"] != true {
		t.Fatalf("second ask = %v, want a duplicate", duplicate)
	}
	if duplicate["notice"] != nil {
		t.Fatalf("duplicate ask invented delivery: %v", duplicate["notice"])
	}

	for _, args := range [][]string{{"status"}, {"show", taskID}} {
		view := d.ctl(true, args...)
		if args[0] == "status" {
			for _, item := range asSlice(view["tasks"]) {
				if row := asMap(item); asString(row["id"]) == taskID && row["notice"] != nil {
					t.Fatalf("status invented parent delivery: %v", row["notice"])
				}
			}
		} else if view["notice"] != nil {
			t.Fatalf("show invented parent delivery: %v", view["notice"])
		}
	}
	calls := len(d.native.Calls())
	noticed := d.ctl(true, "notice", taskID, "--to", "parent")
	assertPendingParentNotice(t, "explicit parent notice", noticed)
	for _, call := range d.native.Calls()[calls:] {
		if call.Method == "terminal.send" {
			t.Fatalf("parent notice prompted a terminal: %v", call)
		}
	}
	after, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("parent notice rewrote task records")
	}
	for range 2 {
		collected := d.ctl(true, "collect", "--budget", "100ms")
		found := false
		for _, item := range asSlice(collected["items"]) {
			row := asMap(item)
			found = found || row["task"] == taskID && row["kind"] == "question"
		}
		if !found {
			t.Fatalf("reading settled the question: %v", collected)
		}
	}
	bound := d.ctl(true, "bind", taskID, "--parent-only")
	if bound["parent"] != nil || bound["notice"] != nil {
		t.Fatalf("bind recreated parent delivery: %v", bound)
	}
	requireLogicalActor(t, d.home, bound["owner"])
	settleNativeTerminal(t, d.base, pane)
	answered := d.ctl(true, "answer", taskID, asString(asMap(asked["question"])["id"]), "--text", "The approved route.")
	assertNoticeStatus(t, "worker answer", asMap(answered["notice"]), "worker", "submitted-not-acknowledged")
	shown := d.ctl(true, "show", taskID)
	assertNoticeStatus(t, "show after worker delivery", asMap(shown["notice"]), "worker", "submitted-not-acknowledged")

	if persisted := readJSON(t, record)["notice"]; persisted != nil {
		t.Fatalf("persisted notice = %v, want the dispatch-time null", persisted)
	}
}

func assertNoticeStatus(t *testing.T, command string, notice map[string]any, recipient, want string) {
	t.Helper()
	if asString(notice["status"]) != want || asString(notice["recipient"]) != recipient || notice["delivery"] == nil {
		t.Fatalf("%s notice = %v, want %s to %s with a delivery id", command, notice, want, recipient)
	}
}

func assertPendingParentNotice(t *testing.T, command string, notice map[string]any) {
	t.Helper()
	if notice["status"] != "pending" || notice["recipient"] != "parent" || notice["delivery"] != nil {
		t.Fatalf("%s created parent delivery: %v", command, notice)
	}
}
