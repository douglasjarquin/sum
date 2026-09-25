package lifecycle

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/douglasjarquin/sum/go/internal/evidenceview"
	"github.com/douglasjarquin/sum/go/internal/execution"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/panes"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

// hasClosablePane is the records-only check sweep uses to queue a task: a worker
// or reviewer pane whose obligation is settled and not yet recorded closed.
func hasClosablePane(task *ordjson.Object) bool {
	if reason, _ := workerCloseReason(task); reason != "" && !panes.WorkerIsClosed(task) {
		return true
	}
	if reason, _ := reviewerCloseReason(task); reason != "" && !panes.ReviewerIsClosed(task) {
		return true
	}
	return false
}

func terminalState(task *ordjson.Object) string {
	if asString(task, "status") == "archived" {
		return "archived"
	}
	pr := asObject(field(task, "pr"))
	if pr == nil {
		return ""
	}
	if merged, _ := pr.Get("merged_for_task"); merged == true {
		return "merged"
	}
	if state, _ := pr.Get("state"); state == "closed" {
		return "pr-closed"
	}
	return ""
}

func currentSHA(task *ordjson.Object) string {
	return evidenceview.CurrentCandidate(task)
}

func reportMatchesCurrent(task *ordjson.Object) bool {
	report := asObject(field(task, "report"))
	if report == nil {
		return false
	}
	candidate := asString(report, "candidate")
	head := currentSHA(task)
	if candidate == "" {
		return true
	}
	return head != "" && candidate == head
}

func reviewMatchesCurrent(task *ordjson.Object) bool {
	head := currentSHA(task)
	for _, raw := range asList(field(task, "evidence")) {
		row := asObject(raw)
		if asString(row, "kind") != "review" {
			continue
		}
		candidate := asString(row, "candidate")
		if candidate == "" {
			return true
		}
		if head != "" && candidate == head {
			return true
		}
	}
	return false
}

func workerCloseReason(task *ordjson.Object) (string, string) {
	if pane := asString(task, "pane"); pane == "" {
		return "", ""
	}
	if term := terminalState(task); term != "" {
		return term, asString(task, "pane")
	}
	if reportMatchesCurrent(task) {
		return "report-submitted", asString(task, "pane")
	}
	return "", ""
}

func reviewerCloseReason(task *ordjson.Object) (string, string) {
	pane := asString(asObject(field(task, "reviewer")), "pane")
	if pane == "" {
		return "", ""
	}
	if term := terminalState(task); term != "" {
		return term, pane
	}
	if reviewMatchesCurrent(task) {
		return "review-recorded", pane
	}
	return "", ""
}

func resolvePath(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

func unwrapPane(value any) *ordjson.Object {
	obj := asObject(value)
	if obj == nil {
		return nil
	}
	if nested := asObject(field(obj, "pane")); nested != nil {
		return nested
	}
	return obj
}

func coordinatorPane(ctx *ordjson.Object) string {
	return asString(ctx, "pane")
}

func parentPane(task *ordjson.Object) string {
	return asString(asObject(field(task, "parent")), "pane")
}

// closeSettledPanes closes worker and reviewer panes whose records show the
// in-memory session is no longer needed. Unanswered questions do not keep a
// pane open: answer writes the decision, and execution resume launches a
// fresh worker that reads context --role worker --section decisions.
func closeSettledPanes(s *store.Store, ctx *ordjson.Object, runtimeRoot, taskID string) []any {
	task, err := s.ReadTask(taskID)
	if err != nil {
		row := ordjson.NewObject()
		row.Set("task", taskID)
		row.Set("action", "pane-close")
		row.Set("state", "error")
		row.Set("error", err.Error())
		return []any{row}
	}
	var rows []any
	if reason, pane := reviewerCloseReason(task); reason != "" && !panes.Closed(task, pane) {
		if row := closeOnePane(s, ctx, runtimeRoot, taskID, pane, "reviewer", reason, reviewerExpectedCwd(task)); row != nil {
			rows = append(rows, row)
		}
	}
	if reason, pane := workerCloseReason(task); reason != "" && !panes.Closed(task, pane) {
		if row := closeOnePane(s, ctx, runtimeRoot, taskID, pane, "worker", reason, asString(task, "worktree")); row != nil {
			rows = append(rows, row)
		}
	}
	return rows
}

func reviewerExpectedCwd(task *ordjson.Object) string {
	cwd := asString(asObject(field(task, "reviewer")), "cwd")
	if cwd != "" {
		return cwd
	}
	return asString(task, "worktree")
}

func closeOnePane(s *store.Store, ctx *ordjson.Object, runtimeRoot, taskID, paneID, role, reason, expectedCwd string) *ordjson.Object {
	row := ordjson.NewObject()
	row.Set("task", taskID)
	row.Set("action", "pane-close")
	row.Set("pane", paneID)
	row.Set("role", role)
	row.Set("reason", reason)
	if paneID == "" {
		row.Set("state", "skipped")
		row.Set("error", "no pane is recorded")
		return row
	}
	if paneID == coordinatorPane(ctx) {
		row.Set("state", "skipped")
		row.Set("error", "refusing to close the registered coordinator pane")
		return row
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		row.Set("state", "error")
		row.Set("error", err.Error())
		return row
	}
	if paneID == parentPane(task) {
		row.Set("state", "skipped")
		row.Set("error", "refusing to close the task's parent pane")
		return row
	}
	session := asString(task, "session")
	if session == "" {
		session = asString(ctx, "session")
	}
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	if err != nil {
		row.Set("state", "error")
		row.Set("error", err.Error())
		return row
	}
	observed, code, err := herdrclient.Observe(herdrPath, session, 5*time.Second, "pane", "get", paneID)
	if err != nil {
		row.Set("state", "error")
		row.Set("error", err.Error())
		return row
	}
	if observed == nil {
		if !herdrclient.IsAbsent(code) {
			row.Set("state", "error")
			row.Set("error", fmt.Sprintf("pane %s cannot be observed (%s)", paneID, code))
			return row
		}
		if err := recordClosed(s, taskID, paneID, role, "", reason); err != nil {
			row.Set("state", "error")
			row.Set("error", err.Error())
			return row
		}
		if role == "worker" {
			parkWorkerAfterClose(s, ctx, runtimeRoot, taskID, row)
		}
		row.Set("state", "already-absent")
		return row
	}
	paneObj := unwrapPane(observed)
	gotCwd := resolvePath(asString(paneObj, "cwd"))
	wantCwd := resolvePath(expectedCwd)
	if wantCwd == "" || gotCwd != wantCwd {
		row.Set("state", "skipped")
		row.Set("error", fmt.Sprintf("pane %s cwd %s does not match the recorded checkout %s; occupant does not match the record", paneID, gotCwd, wantCwd))
		return row
	}
	if role == "worker" {
		if ws := asString(paneObj, "workspace_id"); ws != "" && ws != asString(task, "workspace") {
			row.Set("state", "skipped")
			row.Set("error", fmt.Sprintf("pane %s workspace %s does not match the task workspace %s", paneID, ws, asString(task, "workspace")))
			return row
		}
	}
	agentName := asString(paneObj, "name")
	if agentName == "" {
		if kind := asString(paneObj, "agent"); kind != "" && kind != "<nil>" {
			agentName = kind
		}
	}
	closed, closeCode, closeErr := herdrclient.Observe(herdrPath, session, 10*time.Second, "pane", "close", paneID)
	if closeErr != nil {
		row.Set("state", "error")
		row.Set("error", closeErr.Error())
		return row
	}
	if closed == nil && !herdrclient.IsAbsent(closeCode) {
		row.Set("state", "error")
		row.Set("error", fmt.Sprintf("pane close returned %s", closeCode))
		return row
	}
	if role == "worker" {
		if err := reapCheckoutLeftovers(s, runtimeRoot, task); err != nil {
			row.Set("state", "error")
			row.Set("error", err.Error())
			_ = recordClosed(s, taskID, paneID, role, agentName, reason)
			return row
		}
	}
	if err := recordClosed(s, taskID, paneID, role, agentName, reason); err != nil {
		row.Set("state", "error")
		row.Set("error", err.Error())
		return row
	}
	if role == "worker" {
		parkWorkerAfterClose(s, ctx, runtimeRoot, taskID, row)
	}
	row.Set("state", "closed")
	if agentName != "" {
		row.Set("agent", agentName)
	}
	return row
}

func recordClosed(s *store.Store, taskID, paneID, role, agent, reason string) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return err
	}
	if panes.Append(task, paneID, role, agent, reason) == nil {
		return nil
	}
	return s.SaveTask(task)
}

func parkWorkerAfterClose(s *store.Store, ctx *ordjson.Object, runtimeRoot, taskID string, row *ordjson.Object) {
	task, err := s.ReadTask(taskID)
	if err != nil {
		row.Set("park_error", err.Error())
		return
	}
	worker, err := reservations.Worker(task)
	if err != nil || worker == nil {
		return
	}
	if asString(worker, "state") == "released" {
		return
	}
	attempt := asString(worker, "id")
	if attempt == "" {
		return
	}
	if _, err := execution.Park(s, ctx, runtimeRoot, taskID, attempt); err != nil {
		row.Set("park_error", err.Error())
	}
}

func reapCheckoutLeftovers(s *store.Store, runtimeRoot string, task *ordjson.Object) error {
	worktree := asString(task, "worktree")
	if worktree == "" {
		return nil
	}
	host, err := s.Machine()
	if err != nil {
		return err
	}
	exclude := execution.EndpointShells(host, runtimeRoot, task)
	inside, err := proc.ProcessesBoundTo(worktree, exclude)
	if err != nil {
		return fmt.Errorf("checkout leftovers cannot be inspected: %s", err)
	}
	var stopped []int
	for _, p := range inside {
		if err := proc.Terminate(p.PID); err != nil {
			return fmt.Errorf("could not stop leftover pid %d in %s: %s", p.PID, worktree, err)
		}
		stopped = append(stopped, p.PID)
	}
	if len(stopped) == 0 {
		return nil
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		left, err := proc.ProcessesBoundTo(worktree, exclude)
		if err != nil {
			return fmt.Errorf("could not verify leftover exit in %s: %s", worktree, err)
		}
		if len(left) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("terminated leftover processes in %s but pid %d is still alive", worktree, left[0].PID)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
