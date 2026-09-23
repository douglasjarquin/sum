package execution

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
)

const reviewerShell = 5151

// TestEndpointShells_onlyTheObservedIdleShellOfTheRecordedReviewerIsExempt
// records a reviewer endpoint whose shell sits in the task checkout after the
// worker pane closed. Only an idle known shell of a reviewer pane on this
// machine and session, distinct from the worker pane, observable now, and in
// the recorded cwd is exempt; in every other case the shell still binds the
// checkout and the worker reservation stays held.
func TestEndpointShells_onlyTheObservedIdleShellOfTheRecordedReviewerIsExempt(t *testing.T) {
	cases := []struct {
		name   string
		paneID string
		edit   func(l *lab, reviewer *ordjson.Object, pane map[string]any)
		exempt bool
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
		{name: "live pane cwd is not the recorded cwd", edit: func(l *lab, _ *ordjson.Object, p map[string]any) { p["cwd"] = l.home }},
		{name: "reviewer endpoint recorded no cwd", edit: func(_ *lab, r *ordjson.Object, p map[string]any) {
			r.Delete("cwd")
			p["cwd"] = ""
		}},
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
			reviewer.Set("cwd", l.checkout)
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
			if err := l.store.SaveTask(task); err != nil {
				t.Fatal(err)
			}

			shells := EndpointShells(l.runtime, task)
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
