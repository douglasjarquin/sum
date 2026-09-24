package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// The legacy `notice` field every command prints is derived from the returns sidecar: a question submitted to the
// coordinator reads as submitted-not-acknowledged from the asking command, a duplicate ask, status, show, and bind;
// no delivery rewrites the task record, whose own `notice` stays as dispatch wrote it.
func TestNoticeViewIsDerivedFromTheReturnsSidecar(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "notice-view", map[string]string{"README.md": "A project.\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	taskID, pane := asString(task["id"]), asString(task["pane"])
	d.ctlPane(pane, true, "init")
	record := filepath.Join(d.home, "tasks", taskID, "task.json")
	settleFakePane(t, d.base, "w-parent:p1")

	asked := d.ctlPane(pane, true, "ask", taskID, "--key", "choice", "--text", "Which way?")
	assertNoticeStatus(t, "ask", asMap(asked["notice"]), "submitted-not-acknowledged")
	before, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}

	duplicate := d.ctlPane(pane, true, "ask", taskID, "--key", "choice", "--text", "Which way?")
	if duplicate["duplicate"] != true {
		t.Fatalf("second ask = %v, want a duplicate", duplicate)
	}
	assertNoticeStatus(t, "duplicate ask", asMap(duplicate["notice"]), "submitted-not-acknowledged")

	var row map[string]any
	for _, item := range asSlice(d.ctl(true, "status")["tasks"]) {
		if asString(asMap(item)["id"]) == taskID {
			row = asMap(item)
		}
	}
	assertNoticeStatus(t, "status", asMap(row["notice"]), "submitted-not-acknowledged")
	shown := d.ctl(true, "show", taskID)
	assertNoticeStatus(t, "show", asMap(shown["notice"]), "submitted-not-acknowledged")

	noticed := d.ctl(true, "notice", taskID, "--to", "parent")
	if asString(noticed["status"]) != "pending" || noticed["delivery"] == nil {
		t.Fatalf("notice to the now-busy parent = %v, want pending with its delivery id", noticed)
	}
	after, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("delivery rewrote the task record:\nbefore %s\nafter  %s", before, after)
	}

	// bind rebinds the parent route (its own write) and presents the catch-up inline.
	bound := d.ctl(true, "bind", taskID, "--parent-only")
	// That inline presentation is itself the newest recorded attempt.
	assertNoticeStatus(t, "bind", asMap(bound["notice"]), "submitted-not-acknowledged")
	if asMap(bound["notice"])["delivery"] == noticed["delivery"] {
		t.Fatalf("bind notice = %v, want its own inline attempt, not the earlier %v", bound["notice"], noticed["delivery"])
	}

	if persisted := readJSON(t, record)["notice"]; persisted != nil {
		t.Fatalf("persisted notice = %v, want the dispatch-time null", persisted)
	}
}

func assertNoticeStatus(t *testing.T, command string, notice map[string]any, want string) {
	t.Helper()
	if asString(notice["status"]) != want || asString(notice["recipient"]) != "parent" || notice["delivery"] == nil {
		t.Fatalf("%s notice = %v, want %s to the parent with a delivery id", command, notice, want)
	}
}
