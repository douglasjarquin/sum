package execution

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/review"
)

const reviewerShell = 5151

// TestEndpointShells_onlyTheObservedIdleShellOfTheRecordedReviewerIsExempt
// records a reviewer endpoint whose shell sits in the task checkout after the
// worker pane closed. The reviewer carries the cwd production records, the
// installation root the review command ran from. Only an idle known shell of a
// reviewer pane on this machine and session, distinct from the worker pane,
// observable now, and whose live cwd is the task checkout is exempt; in every
// other case the shell still binds the checkout and the worker reservation
// stays held.
func TestEndpointShells_onlyTheObservedIdleShellOfTheRecordedReviewerIsExempt(t *testing.T) {
	cases := []struct {
		name       string
		paneID     string
		noWorktree bool
		edit       func(l *lab, reviewer *ordjson.Object, pane map[string]any)
		exempt     bool
	}{
		{name: "idle shell of the recorded reviewer", exempt: true},
		{name: "reviewer on another machine", edit: func(_ *lab, r *ordjson.Object, _ map[string]any) { r.Set("machine", "elsewhere") }},
		{name: "reviewer in another session", edit: func(_ *lab, r *ordjson.Object, _ map[string]any) { r.Set("session", "sum-other") }},
		{name: "reviewer pane is the worker pane", paneID: "w-worker:p1"},
		{name: "reviewer pane unobservable", edit: func(_ *lab, _ *ordjson.Object, p map[string]any) { clear(p) }},
		{name: "process-info missing", edit: func(_ *lab, _ *ordjson.Object, p map[string]any) { p["process_info_error"] = "server_unavailable" }},
		{name: "shell argv0 is not a known shell", edit: func(l *lab, _ *ordjson.Object, p map[string]any) {
			p["processes"] = []map[string]any{{"pid": reviewerShell, "name": "python3", "argv0": "python3", "argv": []any{"python3"}, "cwd": l.checkout}}
		}},
		{name: "shell is not in the foreground", edit: func(l *lab, _ *ordjson.Object, p map[string]any) {
			p["processes"] = []map[string]any{{"pid": reviewerShell + 1, "name": "sleep", "argv0": "sleep", "argv": []any{"sleep", "120"}, "cwd": l.checkout}}
		}},
		{name: "a nested shell holds the foreground", edit: func(l *lab, _ *ordjson.Object, p map[string]any) {
			p["processes"] = []map[string]any{{"pid": reviewerShell + 1, "name": "bash", "argv0": "bash", "argv": []any{"bash"}, "cwd": l.checkout}}
		}},
		{name: "live pane cwd is the recorded cwd, not the task checkout", edit: func(l *lab, r *ordjson.Object, p map[string]any) {
			p["cwd"] = stringField(r, "cwd")
		}},
		{name: "task records no worktree", noWorktree: true, edit: func(_ *lab, _ *ordjson.Object, p map[string]any) { p["cwd"] = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := newLab(t)
			l.writeSettings(1, 1)
			paneID := tc.paneID
			if paneID == "" {
				paneID = "w-rev:p1"
			}
			reviewer := ordjson.NewObject()
			reviewer.Set("machine", l.host)
			reviewer.Set("session", "sum-test")
			reviewer.Set("pane", paneID)
			reviewer.Set("cwd", stringField(l.ctx, "cwd"))
			pane := map[string]any{
				"pane_id": paneID, "cwd": l.checkout, "workspace_id": "w-rev",
				"agent_status": "unknown", "agent": nil, "shell_pid": reviewerShell,
				"processes": []map[string]any{
					{"pid": reviewerShell, "name": "bash", "argv0": "bash", "argv": []any{"/bin/bash"}, "cwd": l.checkout},
				},
			}
			if tc.edit != nil {
				tc.edit(l, reviewer, pane)
			}
			l.writeReviewerOnly(paneID, pane)
			l.plantLsof([]map[string]any{{"pid": reviewerShell, "cwd": l.checkout}})
			task, err := decodeObject(l.workerRunningJSON(""))
			if err != nil {
				t.Fatal(err)
			}
			task.Set("reviewer", reviewer)
			if tc.noWorktree {
				task.Set("worktree", "")
			}
			if err := l.store.SaveTask(task); err != nil {
				t.Fatal(err)
			}

			shells := EndpointShells(l.identity(), l.runtime, task)
			if got := shells[reviewerShell]; got != tc.exempt || len(shells) > 1 {
				t.Fatalf("EndpointShells = %v, want shell %d exempt=%v", shells, reviewerShell, tc.exempt)
			}
			inside, err := proc.ProcessesBoundTo(l.checkout, shells)
			if err != nil {
				t.Fatal(err)
			}
			if blocks := len(inside) == 1 && inside[0].PID == reviewerShell; blocks == tc.exempt {
				t.Fatalf("checkout-bound processes = %v, want reviewer shell blocking=%v", inside, !tc.exempt)
			}
			if paneID == stringField(task, "pane") {
				// A live pane with the worker's own id is the worker's pane;
				// its shell is excluded on the worker path, not as a reviewer.
				return
			}
			_, parkErr := l.park(workerID)
			if released := l.attemptState(workerID) == "released"; released != tc.exempt || (parkErr == nil) != tc.exempt {
				t.Fatalf("park err=%v state=%s, want released=%v", parkErr, l.attemptState(workerID), tc.exempt)
			}
		})
	}
}

// TestEndpointShells_reviewerRecordedByReviewRunIsExempt records the reviewer
// endpoint the way production does, through review.Run from a context whose cwd
// is the installation root, while the reviewer pane's live cwd is the task
// checkout. Its idle shell is the reviewer's, so the closed worker parks.
func TestEndpointShells_reviewerRecordedByReviewRunIsExempt(t *testing.T) {
	l := newLab(t)
	l.writeSettings(1, 1)
	l.writeReviewerOnly("w-rev:p1", map[string]any{
		"pane_id": "w-rev:p1", "cwd": l.checkout, "workspace_id": "w-rev",
		"agent_status": "unknown", "agent": nil, "shell_pid": reviewerShell,
		"processes": []map[string]any{
			{"pid": reviewerShell, "name": "bash", "argv0": "bash", "argv": []any{"/bin/bash"}, "cwd": l.checkout},
		},
	})
	l.plantLsof([]map[string]any{{"pid": reviewerShell, "cwd": l.checkout}})
	task, err := decodeObject(l.workerRunningJSON(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.store.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	endpoint := ordjson.NewObject()
	for _, key := range []string{"session", "machine", "cwd"} {
		v, _ := l.ctx.Get(key)
		endpoint.Set(key, v)
	}
	endpoint.Set("pane", "w-rev:p1")
	if _, err := review.Run(l.store, taskID, "approve", "", "", "no findings", "", false, endpoint); err != nil {
		t.Fatal(err)
	}
	task, err = l.store.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	reviewer := asObject(func() any { v, _ := task.Get("reviewer"); return v }())
	if cwd := stringField(reviewer, "cwd"); cwd != l.home {
		t.Fatalf("review.Run recorded reviewer cwd %q, want the installation root %q", cwd, l.home)
	}
	if shells := EndpointShells(l.identity(), l.runtime, task); !shells[reviewerShell] || len(shells) != 1 {
		t.Fatalf("EndpointShells = %v, want only the reviewer shell %d", shells, reviewerShell)
	}
	if _, err := l.park(workerID); err != nil {
		t.Fatalf("park held the closed worker on the reviewer shell: %v", err)
	}
	if got := l.attemptState(workerID); got != "released" {
		t.Fatalf("worker attempt state = %s, want released", got)
	}
}

// writeReviewerOnly writes fake Herdr state in which the worker pane has
// closed and only the reviewer pane (when pane is non-empty) remains.
func (l *lab) writeReviewerOnly(paneID string, pane map[string]any) {
	l.t.Helper()
	panes := map[string]any{}
	if len(pane) > 0 {
		panes[paneID] = pane
	}
	state := map[string]any{
		"panes": panes,
		"workspaces": map[string]any{
			"w-rev": map[string]any{"workspace_id": "w-rev", "label": "review", "worktree": nil},
			"w-worker": map[string]any{
				"workspace_id": "w-worker", "label": "task",
				"worktree": map[string]any{"checkout_path": l.checkout},
			},
		},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.herdr, "state.json"), raw, 0o600); err != nil {
		l.t.Fatal(err)
	}
}
