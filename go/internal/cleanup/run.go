package cleanup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

type Args struct {
	Task         string
	Apply        bool
	ReviewerOnly bool
	Number       int
}

func cleanupRecord(task *ordjson.Object) *ordjson.Object {
	record := asObject(func() any { v, _ := task.Get("cleanup"); return v }())
	if record == nil {
		record = ordjson.NewObject()
		record.Set("schema", jsonNumber(1))
		record.Set("state", nil)
		record.Set("history", []any{})
	}
	return record
}

func jsonNumber(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

func saveCleanup(s *store.Store, taskID string, changes *ordjson.Object) (*ordjson.Object, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	record := cleanupRecord(task)
	record.Set("schema", json.Number("1"))
	record.Set("at", store.Now())
	for _, k := range changes.Keys() {
		v, _ := changes.Get(k)
		record.Set(k, v)
	}
	history := asList(func() any { v, _ := record.Get("history"); return v }())
	entry := ordjson.NewObject()
	entry.Set("at", func() any { v, _ := record.Get("at"); return v }())
	entry.Set("state", func() any { v, _ := record.Get("state"); return v }())
	entry.Set("step", func() any { v, _ := changes.Get("step"); return v }())
	history = append(history, entry)
	if len(history) > 40 {
		history = history[len(history)-40:]
	}
	record.Set("history", history)
	task.Set("cleanup", record)
	if asString(func() any { v, _ := changes.Get("state"); return v }()) == "complete" {
		task.Set("status", "archived")
	}
	if err := s.SaveTask(task); err != nil {
		return nil, err
	}
	return task, nil
}

func cleanupLock(s *store.Store, taskID string) (func() error, error) {
	path, err := s.TaskPath(taskID)
	if err != nil {
		return nil, err
	}
	lockPath := filepath.Join(path, ".cleanup.lock")
	handle, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(handle.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		handle.Close()
		return nil, fmt.Errorf("Cleanup for %s is already in progress; its intent is unchanged.", taskID)
	}
	return func() error {
		_ = syscall.Flock(int(handle.Fd()), syscall.LOCK_UN)
		return handle.Close()
	}, nil
}

func resourcesAbsent(task *ordjson.Object, runtimeRoot, session string) (bool, *ordjson.Object, error) {
	workspace, code, err := observe(runtimeRoot, session, 5*time.Second, "workspace", "get", stringField(task, "workspace"))
	if err != nil {
		return false, nil, err
	}
	detail := ordjson.NewObject()
	if workspace == nil && code == "workspace_not_found" {
		detail.Set("workspace", "absent")
	} else if workspace != nil {
		detail.Set("workspace", "present")
	} else {
		detail.Set("workspace", fmt.Sprintf("uncertain (%s)", code))
	}
	worktree := stringField(task, "worktree")
	if _, statErr := os.Stat(worktree); statErr == nil {
		detail.Set("worktree", "present")
	} else {
		detail.Set("worktree", "absent")
	}
	registered := false
	if paths, wErr := worktreePaths(stringField(task, "repository")); wErr == nil {
		resolved, _ := filepath.EvalSymlinks(worktree)
		for _, p := range paths {
			if p == resolved {
				registered = true
				break
			}
		}
	}
	detail.Set("registered", registered)
	branchOut, _ := proc.Run([]string{"git", "-C", stringField(task, "repository"), "show-ref", "--verify", "--quiet", "refs/heads/" + stringField(task, "branch")}, "", 20*time.Second, false, nil)
	if branchOut.Code == 0 {
		detail.Set("branch", "present")
	} else {
		detail.Set("branch", "absent")
	}
	gone := asString(func() any { v, _ := detail.Get("workspace"); return v }()) == "absent" &&
		asString(func() any { v, _ := detail.Get("worktree"); return v }()) == "absent" && !registered
	return gone, detail, nil
}

func Run(s *store.Store, ctx *ordjson.Object, runtimeRoot string, args Args) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	unlock, err := cleanupLock(s, args.Task)
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err := s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	if err := s.CheckMachine(task); err != nil {
		return nil, err
	}
	record := cleanupRecord(task)
	if asString(func() any { v, _ := record.Get("state"); return v }()) == "complete" {
		result := ordjson.NewObject()
		result.Set("task", args.Task)
		result.Set("state", "complete")
		result.Set("already", true)
		result.Set("resources", func() any { v, _ := record.Get("removed"); return v }())
		status, _ := task.Get("status")
		result.Set("archived", status == "archived")
		result.Set("note", "Cleanup already completed; nothing was observed or changed.")
		return result, nil
	}
	if err := repair.RefuseDuringCleanup(task, "Cleanup"); err != nil && args.Apply {
		return nil, err
	}
	scope := "task"
	if args.ReviewerOnly {
		scope = "reviewer"
	}
	plan, err := inspectTask(s, task, ctx, runtimeRoot, args.Number, scope)
	if err != nil {
		return nil, err
	}
	if !args.Apply {
		changes := ordjson.NewObject()
		changes.Set("step", "inspected")
		changes.Set("state", func() any { v, _ := plan.Get("state"); return v }())
		changes.Set("blockers", func() any { v, _ := plan.Get("blockers"); return v }())
		changes.Set("resources", func() any { v, _ := plan.Get("resources"); return v }())
		if _, err := saveCleanup(s, args.Task, changes); err != nil {
			return nil, err
		}
		plan.Set("apply", false)
		plan.Set("note", "Inspection only; nothing was removed. `cleanup TASK --apply` removes the verified workspace with native Herdr operations and archives the record only when no blocker remains.")
		return plan, nil
	}
	blockers := asList(func() any { v, _ := plan.Get("blockers"); return v }())
	if len(blockers) > 0 {
		changes := ordjson.NewObject()
		changes.Set("step", "apply-refused")
		changes.Set("state", "blocked")
		changes.Set("blockers", blockers)
		changes.Set("resources", func() any { v, _ := plan.Get("resources"); return v }())
		_, _ = saveCleanup(s, args.Task, changes)
		var parts []string
		for _, raw := range blockers {
			b := asObject(raw)
			parts = append(parts, fmt.Sprintf("[%s] %s", stringField(b, "code"), stringField(b, "detail")))
		}
		return nil, fmt.Errorf("Cleanup of %s refused; the task stays cleanup-pending. Blockers: %s", args.Task, stringsJoin(parts, "; "))
	}
	pr := asObject(func() any { v, _ := plan.Get("pr"); return v }())
	mergedHead := stringField(pr, "head_sha")
	intent := ordjson.NewObject()
	intent.Set("workspace", func() any { v, _ := task.Get("workspace"); return v }())
	intent.Set("pane", func() any { v, _ := task.Get("pane"); return v }())
	intent.Set("worktree", func() any { v, _ := task.Get("worktree"); return v }())
	intent.Set("branch", func() any { v, _ := task.Get("branch"); return v }())
	intent.Set("repository", func() any { v, _ := task.Get("repository"); return v }())
	intent.Set("head", func() any { v, _ := plan.Get("head"); return v }())
	intent.Set("merged_head", mergedHead)
	intent.Set("merge_commit", func() any { v, _ := pr.Get("merge_commit"); return v }())
	intent.Set("pr", func() any { v, _ := pr.Get("number"); return v }())
	intent.Set("at", store.Now())
	execRow := asObject(func() any { v, _ := plan.Get("execution"); return v }())
	var attempts any
	if execRow != nil {
		attempts, _ = execRow.Get("attempts")
	}
	if err := saveCleanupIntent(s, args.Task, intent, attempts, asObject(func() any { v, _ := plan.Get("resources"); return v }())); err != nil {
		return nil, err
	}
	task, err = s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	removed := ordjson.NewObject()
	removed.Set("performed", false)
	resources := asObject(func() any { v, _ := plan.Get("resources"); return v }())
	if asString(func() any { v, _ := resources.Get("workspace"); return v }()) == "present" {
		changes := ordjson.NewObject()
		changes.Set("step", "removing")
		changes.Set("state", "removing")
		if _, err := saveCleanup(s, args.Task, changes); err != nil {
			return nil, err
		}
		result, code, obsErr := observe(runtimeRoot, stringField(ctx, "session"), 60*time.Second, "worktree", "remove", "--workspace", stringField(task, "workspace"))
		if obsErr != nil {
			return nil, obsErr
		}
		if result == nil {
			if code == "workspace_not_found" {
				// already absent
			} else if code == "dirty_worktree_requires_force" || code == "worktree_requires_force" {
				return nil, fmt.Errorf("Herdr refused the non-forced removal (%s); nothing was removed and the task stays cleanup-pending.", code)
			} else {
				return nil, fmt.Errorf("worktree remove for workspace %s returned %s; the outcome is uncertain, run cleanup again to reconcile from observation.", stringField(task, "workspace"), code)
			}
		} else {
			resObj := asObject(result)
			removed.Set("performed", true)
			removed.Set("path", func() any { v, _ := resObj.Get("path"); return v }())
			forced, _ := resObj.Get("forced")
			removed.Set("forced", forced == true)
			if forced == true {
				return nil, fmt.Errorf("Herdr reports a forced removal; sum never requested force. Inspect the Herdr build before trusting this cleanup.")
			}
		}
	} else if asString(func() any { v, _ := resources.Get("worktree"); return v }()) == "present" {
		return nil, fmt.Errorf("Cleanup of %s refused: no Herdr workspace owns the remaining checkout %s.", args.Task, stringField(task, "worktree"))
	}
	gone, detail, err := resourcesAbsent(task, runtimeRoot, stringField(ctx, "session"))
	if err != nil {
		return nil, err
	}
	if !gone {
		return nil, fmt.Errorf("After removal the task resources are not all absent (%v); nothing further was changed. Inspect, then run cleanup again to reconcile.", detail)
	}
	if asString(func() any { v, _ := detail.Get("branch"); return v }()) != "present" {
		detail.Set("warning", fmt.Sprintf("branch %s is missing; sum never deletes branches, inspect the repository", stringField(task, "branch")))
	}
	if err := releaseCleanupReservations(s, args.Task, detail); err != nil {
		return nil, err
	}
	complete := ordjson.NewObject()
	complete.Set("state", "complete")
	complete.Set("step", "archived")
	removedOut := ordjson.NewObject()
	for _, k := range detail.Keys() {
		v, _ := detail.Get(k)
		removedOut.Set(k, v)
	}
	for _, k := range removed.Keys() {
		v, _ := removed.Get(k)
		removedOut.Set(k, v)
	}
	complete.Set("removed", removedOut)
	complete.Set("blockers", []any{})
	task, err = saveCleanup(s, args.Task, complete)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", args.Task)
	result.Set("state", "complete")
	result.Set("archived", true)
	result.Set("removed", removedOut)
	kept := ordjson.NewObject()
	kept.Set("branch", func() any { v, _ := task.Get("branch"); return v }())
	path, _ := s.TaskPath(args.Task)
	kept.Set("records", path)
	result.Set("kept", kept)
	result.Set("note", "Only the verified task workspace and its clean checkout were removed, through native Herdr without force. The branch, brief revisions, decisions, reports, and PR evidence stay.")
	return result, nil
}

func stringsJoin(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

func saveCleanupIntent(s *store.Store, taskID string, intent *ordjson.Object, expectedAttempts any, resources *ordjson.Object) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return err
	}
	if err := repair.RefuseActive(task, false); err != nil {
		return err
	}
	execVal, err := reservations.GetExecution(task)
	if err != nil {
		return fmt.Errorf("Malformed execution reservation for %s: %s. Cleanup intent is refused.", taskID, err)
	}
	if execVal == nil {
		return fmt.Errorf("Legacy task has no exact execution owner to bind cleanup to.")
	}
	attempts := executionStopProof(execVal)
	record := cleanupRecord(task)
	record.Set("schema", json.Number("1"))
	record.Set("at", store.Now())
	record.Set("state", "ready")
	record.Set("step", "intent")
	intent.Set("attempts", attempts)
	record.Set("intent", intent)
	record.Set("blockers", []any{})
	record.Set("resources", resources)
	history := asList(func() any { v, _ := record.Get("history"); return v }())
	entry := ordjson.NewObject()
	entry.Set("at", func() any { v, _ := record.Get("at"); return v }())
	entry.Set("state", "ready")
	entry.Set("step", "intent")
	record.Set("history", append(history, entry))
	task.Set("cleanup", record)
	_ = expectedAttempts
	return s.SaveTask(task)
}

func releaseCleanupReservations(s *store.Store, taskID string, detail *ordjson.Object) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return err
	}
	execVal, err := reservations.GetExecution(task)
	if err != nil {
		return fmt.Errorf("Malformed execution reservation for %s: %s. Cleanup cannot archive it.", taskID, err)
	}
	if execVal == nil {
		return nil
	}
	stamp := store.Now()
	rows := append([]*ordjson.Object{execVal.Worker}, execVal.Verifiers...)
	for _, row := range rows {
		state := asString(func() any { v, _ := row.Get("state"); return v }())
		if state == "held" || state == "observing" || state == "starting" || state == "running" || state == "uncertain" {
			id := asString(func() any { v, _ := row.Get("id"); return v }())
			obs := ordjson.NewObject()
			obs.Set("at", stamp)
			obs.Set("outcome", "cleanup-stopped")
			obs.Set("resources", detail)
			if _, tErr := reservations.Transition(task, id, "released", stamp, obs, nil); tErr != nil {
				return tErr
			}
		}
	}
	return s.SaveTask(task)
}
