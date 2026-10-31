package cleanup

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/douglasjarquin/sum/go/internal/execution"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/panes"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/usage"
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
		task.Set("terminal_maintenance", nil)
	}
	if err := s.SaveTask(task); err != nil {
		return nil, err
	}
	return task, nil
}

func cleanupLock(s *store.Store, taskID string) (func() error, error) {
	if err := s.CheckWritable(); err != nil {
		return nil, err
	}
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
	detail := ordjson.NewObject()
	endpoint := taskWorker(task)
	_, err := nativeTerminal(endpoint)
	terminalAbsent := nativeAbsent(err)
	if terminalAbsent {
		detail.Set("terminal", "absent")
	} else if err != nil {
		return false, nil, err
	} else {
		detail.Set("terminal", "present")
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
	gone := terminalAbsent &&
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
		artifacts := asObject(func() any { v, _ := plan.Get("artifacts"); return v }())
		if artifacts != nil {
			for _, key := range []string{"ignored_paths", "ignored_count", "ignored_omitted"} {
				if value, ok := artifacts.Get(key); ok {
					changes.Set(key, value)
				}
			}
		}
		if _, err := saveCleanup(s, args.Task, changes); err != nil {
			return nil, err
		}
		plan.Set("apply", false)
		plan.Set("note", "Inspection only; cleanup --apply removes exact finished HC terminals and the verified Git worktree when no blocker remains.")
		return plan, nil
	}
	var orphansStopped []any
	if len(asList(func() any { v, _ := plan.Get("blockers"); return v }())) > 0 && orphansAreOnlyBlockers(plan) {

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
	if args.ReviewerOnly {
		return closeReviewerOnly(s, task, ctx, runtimeRoot, plan)
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
		task, err = settleAnsweredQuestions(s, args.Task, settle, ctx, task)
		if err != nil {
			return nil, err
		}
	}
	intent := ordjson.NewObject()
	intent.Set("worker_endpoint", taskWorker(task).Object())
	intent.Set("worktree", func() any { v, _ := task.Get("worktree"); return v }())
	intent.Set("branch", func() any { v, _ := task.Get("branch"); return v }())
	intent.Set("repository", func() any { v, _ := task.Get("repository"); return v }())
	intent.Set("head", func() any { v, _ := plan.Get("head"); return v }())
	intent.Set("merged_head", mergedHead)
	intent.Set("merge_commit", func() any { v, _ := pr.Get("merge_commit"); return v }())
	intent.Set("pr", func() any { v, _ := pr.Get("number"); return v }())
	intent.Set("at", store.Now())
	if err := saveCleanupIntent(s, args.Task, intent, attempts, asObject(func() any { v, _ := plan.Get("resources"); return v }()), task); err != nil {
		return nil, err
	}
	task, err = s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	removed := ordjson.NewObject()
	removed.Set("performed", false)
	artifacts := asObject(func() any { v, _ := plan.Get("artifacts"); return v }())
	if artifacts != nil {
		for _, key := range []string{"ignored_paths", "ignored_count", "ignored_omitted"} {
			if value, ok := artifacts.Get(key); ok {
				removed.Set(key, value)
			}
		}
	}
	if len(orphansStopped) > 0 {
		removed.Set("orphans_stopped", orphansStopped)
	}
	resources := asObject(func() any { v, _ := plan.Get("resources"); return v }())

	fresh, err := s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	freshPlan, err := inspectTask(s, fresh, ctx, runtimeRoot, args.Number, scope)
	if err != nil {
		return nil, err
	}
	if len(asList(func() any { v, _ := freshPlan.Get("blockers"); return v }())) > 0 {
		return nil, fmt.Errorf("cleanup preconditions changed after intent; inspect again")
	}
	if err = claimCleanupEffects(s, fresh, freshPlan, intent, mergedHead); err != nil {
		return nil, err
	}

	if err = removeTaskTerminals(s, fresh); err != nil {
		return nil, err
	}
	if stringField(resources, "worktree") == "present" {
		if err = s.CheckWritable(); err != nil {
			return nil, err
		}
		if err = adoptCheckout(fresh); err != nil {
			return nil, err
		}
		removed.Set("performed", true)
		removed.Set("path", stringField(fresh, "worktree"))
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
	if err := recordReviewerClosed(s, args.Task); err != nil {
		return nil, err
	}
	usageResult := usage.BestEffort(s, args.Task)
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
	result.Set("usage", usageResult)
	result.Set("note", "Exact finished HC terminals and the verified Git worktree were removed without force. The branch and durable task evidence remain.")
	return result, nil
}

func recordReviewerClosed(s *store.Store, taskID string) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return err
	}
	reviewer := asObject(func() any { v, _ := task.Get("reviewer"); return v }())
	panes.AppendEndpoint(task, taskWorker(task), "cleanup")
	if reviewer == nil {
		return s.SaveTask(task)
	}
	endpoint := store.EndpointFromContext(reviewer)
	panes.AppendEndpoint(task, endpoint, "cleanup")
	return s.SaveTask(task)
}
func removeNative(s *store.Store, endpoint store.Endpoint) error {
	if err := s.CheckWritable(); err != nil {
		return err
	}
	t, err := nativeTerminal(endpoint)
	if nativeAbsent(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if t.State != "finished" || t.FinishedMS == nil || (t.ExitCode == nil && t.Signal == nil) {
		return fmt.Errorf("terminal %s is not proven finished", endpoint.Address)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID}
	_, err = client.Remove(ctx, endpoint.Address)
	return err
}
func removeTaskTerminals(s *store.Store, task *ordjson.Object) error {
	endpoints := []store.Endpoint{taskWorker(task)}
	if reviewer := asObject(func() any { v, _ := task.Get("reviewer"); return v }()); reviewer != nil {
		endpoints = append(endpoints, store.EndpointFromContext(reviewer))
	}
	record, err := environment.Read(s, stringField(task, "id"))
	if err != nil {
		return err
	}
	for _, raw := range asList(func() any {
		if record == nil {
			return nil
		}
		v, _ := record.Get("services")
		return v
	}()) {
		service := asObject(raw)
		e := store.EndpointFromContext(asObject(func() any { v, _ := service.Get("endpoint"); return v }()))
		if e.Validate() != nil {
			return fmt.Errorf("service %s has unresolved launch identity", stringField(service, "id"))
		}
		view, err := environment.ObserveService(task, service, "")
		if err != nil {
			return err
		}
		running, _ := view.Get("running")
		if stringField(view, "ownership") != "owned" || running != false {
			return fmt.Errorf("service %s exit or ownership unresolved", stringField(service, "id"))
		}
		endpoints = append(endpoints, e)
	}
	for _, e := range endpoints {
		if err := removeNative(s, e); err != nil {
			return err
		}
	}
	return nil
}
func closeReviewerOnly(s *store.Store, task, ctx *ordjson.Object, runtimeRoot string, plan *ordjson.Object) (*ordjson.Object, error) {
	reviewer := asObject(func() any { v, _ := task.Get("reviewer"); return v }())
	if reviewer == nil {
		return nil, fmt.Errorf("no reviewer endpoint")
	}
	endpoint := store.EndpointFromContext(reviewer)
	already := panes.ReviewerIsClosed(task)
	if !already {
		if err := claimReviewerRemoval(s, task, endpoint); err != nil {
			return nil, err
		}
		if err := removeNative(s, endpoint); err != nil {
			_ = uncertainReviewerRemoval(s, stringField(task, "id"), endpoint)
			return nil, err
		}

		unlock, err := s.Lock()
		if err != nil {
			return nil, err
		}
		current, err := s.ReadTask(stringField(task, "id"))
		if err == nil {
			now := store.EndpointFromContext(asObject(func() any { v, _ := current.Get("reviewer"); return v }()))
			if !endpoint.Native().SameAuthority(now.Native()) {
				err = fmt.Errorf("reviewer binding changed during cleanup")
			} else {
				claim := asObject(func() any { v, _ := current.Get("terminal_maintenance"); return v }())
				claimEndpoint := store.EndpointFromContext(asObject(func() any { v, _ := claim.Get("endpoint"); return v }()))
				if !claimEndpoint.Native().SameAuthority(endpoint.Native()) {
					err = fmt.Errorf("reviewer removal claim changed")
				} else {
					panes.AppendEndpoint(current, endpoint, "reviewer-only cleanup")
					current.Set("terminal_maintenance", nil)
				}
				if err == nil {
					err = s.SaveTask(current)
				}
			}
		}
		unlock()
		if err != nil {
			return nil, err
		}
	}
	out := ordjson.NewObject()
	out.Set("task", stringField(task, "id"))
	out.Set("state", "reviewer-closed")
	out.Set("endpoint", endpoint.Object())
	out.Set("already", already)
	out.Set("checkout", stringField(task, "worktree"))
	return out, nil
}

func intentMatches(saved *ordjson.Object, task, plan *ordjson.Object, mergedHead string, attempts any) bool {
	if !taskWorker(task).Native().SameAuthority(store.EndpointFromContext(asObject(func() any { v, _ := saved.Get("worker_endpoint"); return v }())).Native()) {
		return false
	}
	for _, key := range []string{"worktree", "branch"} {
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
func settleAnsweredQuestions(s *store.Store, taskID string, ids []any, ctx *ordjson.Object, expectedTask *ordjson.Object) (*ordjson.Object, error) {
	settle := map[string]bool{}
	for _, raw := range ids {
		if id, ok := raw.(string); ok && id != "" {
			settle[id] = true
		}
	}
	if len(settle) == 0 {
		return expectedTask, nil
	}
	actor := "sum:" + stringField(ctx, "session") + ":cleanup"
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	t, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	if !sameTaskSnapshot(t, expectedTask) {
		return nil, fmt.Errorf("task changed before settling answered questions; no cleanup intent created")
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
		return expectedTask, nil
	}
	t.Set("updated_at", store.Now())
	if err = s.SaveTask(t); err != nil {
		return nil, err
	}
	return t, nil
}

func orphansAreOnlyBlockers(plan *ordjson.Object) bool {
	orphans := asList(func() any { v, _ := plan.Get("orphans"); return v }())
	if len(orphans) == 0 {
		return false
	}
	resources := asObject(func() any { v, _ := plan.Get("resources"); return v }())
	if asString(func() any { v, _ := resources.Get("worker_terminal"); return v }()) != "absent" {
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

func saveCleanupIntent(s *store.Store, taskID string, intent *ordjson.Object, expectedAttempts any, resources *ordjson.Object, expectedTask *ordjson.Object) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return err
	}
	if !sameTaskSnapshot(task, expectedTask) {
		return fmt.Errorf("task changed before cleanup intent; inspect again")
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
	task, err := s.ReadTask(taskID)
	if err != nil {
		return err
	}
	execVal, err := reservations.GetExecution(task)
	if err != nil {
		return err
	}
	if execVal == nil {
		return nil
	}
	type release struct {
		id          string
		generation  int64
		observation *ordjson.Object
	}
	var releases []release
	stamp := store.Now()

	for _, row := range append([]*ordjson.Object{execVal.Worker}, execVal.Verifiers...) {
		state := stringField(row, "state")
		if state == "released" {
			continue
		}
		proof := execution.ObserveStop(s, runtimeRoot, task, row)
		if proof.Outcome != execution.OutcomeStopped {
			return fmt.Errorf("cleanup cannot archive %s: %s", taskID, proof.Reason)
		}
		generationNumber, ok := func() any { v, _ := row.Get("generation"); return v }().(json.Number)
		if !ok {
			return fmt.Errorf("reservation generation missing")
		}
		generation, err := generationNumber.Int64()
		if err != nil {
			return err
		}
		obs := proof.Observation
		if obs == nil {
			obs = ordjson.NewObject()
			obs.Set("at", stamp)
			obs.Set("outcome", execution.OutcomeStopped)
		}
		obs.Set("resources", detail)
		releases = append(releases, release{stringField(row, "id"), generation, obs})
	}
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	current, err := s.ReadTask(taskID)
	if err != nil {
		return err
	}
	if !sameTaskSnapshot(task, current) {
		return fmt.Errorf("task changed during cleanup reservation proof; archive remains pending")
	}
	for _, release := range releases {
		generation := release.generation
		if _, err = reservations.Transition(current, release.id, "released", stamp, release.observation, &generation); err != nil {
			return err
		}
	}
	return s.SaveTask(current)
}

func MaintenanceLock(s *store.Store, taskID string) (func() error, error) {
	return cleanupLock(s, taskID)
}
func sameTaskSnapshot(a, b *ordjson.Object) bool {
	if a == nil || b == nil {
		return false
	}
	aa, e1 := ordjson.MarshalSortedCompact(a)
	bb, e2 := ordjson.MarshalSortedCompact(b)
	return e1 == nil && e2 == nil && string(aa) == string(bb)
}
func claimCleanupEffects(s *store.Store, observed, plan, intent *ordjson.Object, mergedHead string) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	current, err := s.ReadTask(stringField(observed, "id"))
	if err != nil {
		return err
	}
	if !sameTaskSnapshot(current, observed) {
		return fmt.Errorf("task changed after cleanup observation; no removal submitted")
	}
	if err = RefusePendingEffects(s, stringField(current, "id")); err != nil {
		return err
	}
	execution := asObject(func() any { v, _ := plan.Get("execution"); return v }())
	attempts, _ := execution.Get("attempts")
	if !intentMatches(intent, current, plan, mergedHead, attempts) {
		return fmt.Errorf("cleanup candidate or current attempts changed")
	}
	record := cleanupRecord(current)
	record.Set("state", "removing")
	record.Set("step", "effects-claimed")
	record.Set("effect_claimed_at", store.Now())
	current.Set("cleanup", record)
	return s.SaveTask(current)
}

func claimReviewerRemoval(s *store.Store, observed *ordjson.Object, endpoint store.Endpoint) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	task, err := s.ReadTask(stringField(observed, "id"))
	if err != nil {
		return err
	}
	if !sameTaskSnapshot(task, observed) {
		return fmt.Errorf("task changed before reviewer removal claim")
	}
	if err = RefusePendingEffects(s, stringField(task, "id")); err != nil {
		return err
	}
	for _, raw := range asList(func() any { v, _ := task.Get("questions"); return v }()) {
		q := asObject(raw)
		state := stringField(q, "status")
		if state == "open" || state == "answered" {
			return fmt.Errorf("pending question retains reviewer history")
		}
	}
	if prior := asObject(func() any { v, _ := task.Get("terminal_maintenance"); return v }()); prior != nil {
		old := store.EndpointFromContext(asObject(func() any { v, _ := prior.Get("endpoint"); return v }()))
		if !old.Native().SameAuthority(endpoint.Native()) || stringField(prior, "reason") != "reviewer-only cleanup" {
			return fmt.Errorf("another terminal maintenance claim is unresolved")
		}
	}
	claim := ordjson.NewObject()
	claim.Set("state", "claimed")
	claim.Set("endpoint", endpoint.Object())
	claim.Set("role", "reviewer")
	claim.Set("reason", "reviewer-only cleanup")
	claim.Set("at", store.Now())
	task.Set("terminal_maintenance", claim)
	return s.SaveTask(task)
}
func uncertainReviewerRemoval(s *store.Store, taskID string, endpoint store.Endpoint) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return err
	}
	claim := asObject(func() any { v, _ := task.Get("terminal_maintenance"); return v }())
	if !store.EndpointFromContext(asObject(func() any { v, _ := claim.Get("endpoint"); return v }())).Native().SameAuthority(endpoint.Native()) {
		return fmt.Errorf("reviewer removal claim changed")
	}
	claim.Set("state", "uncertain")
	task.Set("terminal_maintenance", claim)
	return s.SaveTask(task)
}
