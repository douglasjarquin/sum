package cleanup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/execution"
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
	savedIntent := asObject(func() any { v, _ := record.Get("intent"); return v }())
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
	var orphansStopped []any
	if len(asList(func() any { v, _ := plan.Get("blockers"); return v }())) > 0 && orphansAreOnlyBlockers(plan) {
		// The only thing holding this cleanup is a set of processes whose owner
		// pane and workspace are both verified dead. Stop each one explicitly,
		// verify exit, and take one fresh inspection; a survivor leaves the named
		// blockers exactly where they were.
		stopped, stopErr := stopOrphans(task, plan)
		if stopErr != nil {
			return nil, stopErr
		}
		orphansStopped = stopped
		plan, err = inspectTask(s, task, ctx, runtimeRoot, args.Number, scope)
		if err != nil {
			return nil, err
		}
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
	execRow := asObject(func() any { v, _ := plan.Get("execution"); return v }())
	var attempts any
	if execRow != nil {
		attempts, _ = execRow.Get("attempts")
	}
	intentReconciled := "fresh"
	if savedIntent != nil {
		if intentMatches(savedIntent, task, plan, mergedHead, attempts) {
			intentReconciled = "continued"
		} else {
			intentReconciled = "re-derived"
		}
	}
	if settle := asList(func() any { v, _ := plan.Get("settled_questions"); return v }()); len(settle) > 0 {
		if err := settleAnsweredQuestions(s, args.Task, settle, ctx); err != nil {
			return nil, err
		}
	}
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
	if err := saveCleanupIntent(s, args.Task, intent, attempts, asObject(func() any { v, _ := plan.Get("resources"); return v }())); err != nil {
		return nil, err
	}
	task, err = s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	removed := ordjson.NewObject()
	removed.Set("performed", false)
	if len(orphansStopped) > 0 {
		removed.Set("orphans_stopped", orphansStopped)
	}
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
		// Pane-closed-then-cleanup is the normal end state: a checkout whose
		// recorded path still verifies as this task's worktree (clean, registered,
		// branch-matched, no live processes — all blocker-checked above) is
		// adopted and removed through git without force once the workspace and
		// pane are both proven absent.
		if asString(func() any { v, _ := resources.Get("workspace"); return v }()) != "absent" ||
			asString(func() any { v, _ := resources.Get("pane"); return v }()) != "absent" {
			return nil, fmt.Errorf("Cleanup of %s refused: no Herdr workspace owns the remaining checkout %s.", args.Task, stringField(task, "worktree"))
		}
		if err := adoptCheckout(task); err != nil {
			return nil, err
		}
		removed.Set("performed", true)
		removed.Set("path", stringField(task, "worktree"))
		removed.Set("adopted", true)
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
	if err := releaseCleanupReservations(s, args.Task, runtimeRoot, detail); err != nil {
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
	result.Set("intent", intentReconciled)
	result.Set("settled_questions", func() any { v, _ := plan.Get("settled_questions"); return v }())
	result.Set("orphans", func() any { v, _ := plan.Get("orphans"); return v }())
	kept := ordjson.NewObject()
	kept.Set("branch", func() any { v, _ := task.Get("branch"); return v }())
	path, _ := s.TaskPath(args.Task)
	kept.Set("records", path)
	result.Set("kept", kept)
	result.Set("note", "Only the verified task workspace and its clean checkout were removed, through native Herdr without force. The branch, brief revisions, decisions, reports, and PR evidence stay.")
	return result, nil
}

// intentMatches reports whether a saved cleanup intent still describes the same
// workspace, pane, worktree, branch, merge evidence, and recorded execution
// attempts that fresh inspection just produced. When it does, a previously
// interrupted apply may continue; when it does not, the fresh plan replaces the
// saved intent so a stale snapshot can never wedge the task or authorize removal
// of resources it no longer describes.
func intentMatches(saved *ordjson.Object, task, plan *ordjson.Object, mergedHead string, attempts any) bool {
	for _, key := range []string{"workspace", "pane", "worktree", "branch"} {
		want, _ := task.Get(key)
		got, _ := saved.Get(key)
		if fmt.Sprint(want) != fmt.Sprint(got) {
			return false
		}
	}
	pr := asObject(func() any { v, _ := plan.Get("pr"); return v }())
	if stringField(saved, "head") != stringField(plan, "head") ||
		stringField(saved, "merged_head") != mergedHead ||
		stringField(saved, "merge_commit") != stringField(pr, "merge_commit") ||
		fmt.Sprint(func() any { v, _ := saved.Get("pr"); return v }()) != fmt.Sprint(func() any { v, _ := pr.Get("number"); return v }()) {
		return false
	}
	savedAttempts := asList(func() any { v, _ := saved.Get("attempts"); return v }())
	freshAttempts := asList(attempts)
	if len(savedAttempts) != len(freshAttempts) {
		return false
	}
	freshByID := map[string]*ordjson.Object{}
	for _, raw := range freshAttempts {
		if row := asObject(raw); row != nil {
			freshByID[stringField(row, "id")] = row
		}
	}
	for _, raw := range savedAttempts {
		row := asObject(raw)
		fresh := freshByID[stringField(row, "id")]
		if fresh == nil ||
			stringField(row, "outcome") != stringField(fresh, "outcome") ||
			fmt.Sprint(func() any { v, _ := row.Get("generation"); return v }()) != fmt.Sprint(func() any { v, _ := fresh.Get("generation"); return v }()) {
			return false
		}
	}
	return true
}

// settleAnsweredQuestions marks answered questions settled once every attempt
// that could apply them has verified stop evidence. It uses one conditional
// write inside the existing task lock so a concurrent `resolve` can never lose
// its applied status to a settle that raced it.
func settleAnsweredQuestions(s *store.Store, taskID string, ids []any, ctx *ordjson.Object) error {
	settle := map[string]bool{}
	for _, raw := range ids {
		if id, ok := raw.(string); ok && id != "" {
			settle[id] = true
		}
	}
	if len(settle) == 0 {
		return nil
	}
	actor := "sum:" + stringField(ctx, "session") + ":cleanup"
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	t, err := s.ReadTask(taskID)
	if err != nil {
		return err
	}
	raw, _ := t.Get("questions")
	rows, _ := raw.([]any)
	changed := false
	for _, qraw := range rows {
		q, _ := qraw.(*ordjson.Object)
		if q == nil {
			continue
		}
		qid, _ := q.Get("id")
		status, _ := q.Get("status")
		if id, _ := qid.(string); settle[id] && status == "answered" {
			q.Set("status", "settled")
			q.Set("settled_at", store.Now())
			q.Set("settled_by", actor)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	t.Set("updated_at", store.Now())
	return s.SaveTask(t)
}

// orphansAreOnlyBlockers reports whether every remaining blocker is a live
// process bound to the checkout whose owner pane is verified absent. That is
// the one case where apply may act on a blocker itself: each named orphan is
// terminated and the task is re-inspected once. A present workspace is fine —
// it persists until this apply removes it, and any live pane inside it is
// already a blocker of another code. An unproven pane or workspace, or any
// other blocker code, keeps the refuse path.
func orphansAreOnlyBlockers(plan *ordjson.Object) bool {
	orphans := asList(func() any { v, _ := plan.Get("orphans"); return v }())
	if len(orphans) == 0 {
		return false
	}
	resources := asObject(func() any { v, _ := plan.Get("resources"); return v }())
	if asString(func() any { v, _ := resources.Get("pane"); return v }()) != "absent" {
		return false
	}
	if ws := asString(func() any { v, _ := resources.Get("workspace"); return v }()); ws != "present" && ws != "absent" {
		return false
	}
	for _, raw := range asList(func() any { v, _ := plan.Get("blockers"); return v }()) {
		code := stringField(asObject(raw), "code")
		if code != "occupant" && code != "execution" {
			return false
		}
	}
	return true
}

// stopOrphans terminates processes the inspection plan proved are orphans:
// bound to the checkout while every recorded pane that could own them is dead.
// One SIGTERM per process, then a re-scan; anything still alive (sum never
// force-kills) keeps its reservation and leaves a fresh intent for the next
// bounded pass.
func stopOrphans(task, plan *ordjson.Object) ([]any, error) {
	worktree := stringField(task, "worktree")
	if worktree == "" {
		return nil, nil
	}
	var stopped []any
	for _, raw := range asList(func() any { v, _ := plan.Get("orphans"); return v }()) {
		row := asObject(raw)
		if row == nil {
			continue
		}
		pid, _ := row.Get("pid")
		n, _ := pid.(json.Number)
		i, _ := n.Int64()
		p := int(i)
		if p <= 0 {
			continue
		}
		if err := proc.Terminate(p); err != nil {
			return nil, fmt.Errorf("Cleanup could not stop orphan pid %d in %s: %s", p, worktree, err)
		}
		stopped = append(stopped, fmt.Sprint(p))
	}
	if len(stopped) == 0 {
		return nil, nil
	}
	var inside []any
	var errorText string
	for i := 0; i < 40; i++ {
		inside, errorText = processesBoundTo(worktree, map[int]bool{})
		if errorText != "" {
			return nil, fmt.Errorf("Cleanup could not verify orphan exit in %s: %s", worktree, errorText)
		}
		if len(inside) == 0 {
			return stopped, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	p := asObject(inside[0])
	pid, _ := p.Get("pid")
	return nil, fmt.Errorf("Cleanup terminated orphan processes in %s but pid %v is still alive; the reservation stays held and the intent is refreshed for the next pass", worktree, pid)
}

// adoptCheckout removes a checkout that inspection proved safe when no Herdr
// workspace remains to perform the removal. It re-verifies registration and
// cleanliness at removal time, then asks git to remove the worktree without
// force; the branch is kept like every other cleanup, and every failure leaves
// the checkout untouched and the intent saved for the next bounded pass.
func adoptCheckout(task *ordjson.Object) error {
	worktree := stringField(task, "worktree")
	repository := stringField(task, "repository")
	branch := stringField(task, "branch")
	registered, err := worktreePaths(repository)
	if err != nil {
		return fmt.Errorf("Cleanup could not adopt %s: %s", worktree, err)
	}
	found := false
	for _, p := range registered {
		if samePath(p, worktree) {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("Cleanup refused to adopt %s: the checkout is not a registered worktree of %s", worktree, repository)
	}
	branchShow, err := proc.Run([]string{"git", "-C", worktree, "branch", "--show-current"}, "", 20*time.Second, true, nil)
	if err != nil {
		return fmt.Errorf("Cleanup could not adopt %s: %s", worktree, err)
	}
	if strings.TrimSpace(branchShow.Stdout) != branch {
		return fmt.Errorf("Cleanup refused to adopt %s: the checkout is on %q, not %q", worktree, strings.TrimSpace(branchShow.Stdout), branch)
	}
	statusOut, err := proc.Run([]string{"git", "-C", worktree, "status", "--porcelain", "--untracked-files=all"}, "", 30*time.Second, true, nil)
	if err != nil {
		return fmt.Errorf("Cleanup could not adopt %s: %s", worktree, err)
	}
	if strings.TrimSpace(statusOut.Stdout) != "" {
		return fmt.Errorf("Cleanup refused to adopt %s: the checkout is not clean", worktree)
	}
	if _, err := proc.Run([]string{"git", "-C", repository, "worktree", "remove", worktree}, "", 30*time.Second, true, nil); err != nil {
		return fmt.Errorf("Cleanup could not remove adopted checkout %s: %s", worktree, err)
	}
	return nil
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
	record := cleanupRecord(task)
	record.Set("schema", json.Number("1"))
	record.Set("at", store.Now())
	record.Set("state", "ready")
	record.Set("step", "intent")
	intent.Set("attempts", expectedAttempts)
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
	return s.SaveTask(task)
}

func releaseCleanupReservations(s *store.Store, taskID, runtimeRoot string, detail *ordjson.Object) error {
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
			proof := execution.ObserveStop(s, runtimeRoot, task, row)
			if proof.Outcome != execution.OutcomeStopped {
				return fmt.Errorf("Cleanup cannot archive %s: %s", taskID, proof.Reason)
			}
			id := asString(func() any { v, _ := row.Get("id"); return v }())
			obs := proof.Observation
			if obs == nil {
				obs = ordjson.NewObject()
				obs.Set("at", stamp)
				obs.Set("outcome", execution.OutcomeStopped)
			}
			obs.Set("resources", detail)
			if _, tErr := reservations.Transition(task, id, "released", stamp, obs, nil); tErr != nil {
				return tErr
			}
		}
	}
	return s.SaveTask(task)
}
