package bindcmd

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

func resolve(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

func stringField(obj *ordjson.Object, key string) string {
	if obj == nil {
		return ""
	}
	value, _ := obj.Get(key)
	text, _ := value.(string)
	return text
}

func Run(s *store.Store, ctx *ordjson.Object, taskID, workerPane, reviewerPane string, parentOnly bool, pump returns.PumpOpts) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	if !parentOnly && workerPane == "" && reviewerPane == "" {
		return nil, fmt.Errorf("Specify --parent-only, --worker-pane, or --reviewer-pane. Binding never creates a replacement.")
	}
	if (parentOnly && (workerPane != "" || reviewerPane != "")) || (workerPane != "" && reviewerPane != "") {
		return nil, fmt.Errorf("Choose exactly one of --parent-only, --worker-pane, or --reviewer-pane.")
	}
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	// A worker pane is observed before the state lock (no Herdr call runs under it): its cwd must be the task
	// checkout, and its occupant is what the worker registration records.
	var bound incarnation.Evidence
	var observedCwd string
	var reviewerAgent *ordjson.Object
	var reviewerShell int
	session, _ := ctx.Get("session")
	sessionStr, _ := session.(string)
	if workerPane != "" || reviewerPane != "" {
		herdrPath, err := toolpath.Find(pump.RuntimeRoot, "herdr")
		if err != nil {
			return nil, err
		}
		paneID := workerPane
		if reviewerPane != "" {
			paneID = reviewerPane
		}
		agent, err := herdrclient.Call(herdrPath, sessionStr, 5*time.Second, "agent", "get", paneID)
		if err != nil {
			return nil, err
		}
		agentObj := herdrclient.UnwrapAgent(agent)
		observedCwd = herdrclient.AgentCwd(agentObj)
		bound = incarnation.FromInfo(agentObj)
		if reviewerPane != "" {
			reviewerAgent = agentObj
		}
		observeCtx, cancel := context.WithTimeout(context.Background(), 2*incarnation.ObserveTimeout)
		if shell, err := incarnation.ProbeShell(incarnation.SessionCall(observeCtx, herdrPath, sessionStr), paneID); err == nil {
			bound.Shell = shell
			if reviewerPane != "" {
				reviewerShell = shell.PID
			}
		}
		cancel()
	}
	var reviewerBefore *ordjson.Object
	var reviewerStopObservation *ordjson.Object
	if reviewerPane != "" {
		preTask, err := s.ReadTask(taskID)
		if err != nil {
			return nil, err
		}
		oldValue, _ := preTask.Get("reviewer")
		reviewerBefore, _ = oldValue.(*ordjson.Object)
		if reviewerBefore != nil {
			oldPaneStr := stringField(reviewerBefore, "pane")
			oldSessionStr := stringField(reviewerBefore, "session")
			oldMachine := stringField(reviewerBefore, "machine")
			if host.Is(oldMachine) && oldSessionStr == sessionStr && oldPaneStr != reviewerPane {
				worktree, _ := preTask.Get("worktree")
				workerPane := stringField(preTask, "pane")
				reviewerStopObservation, err = reviewerStopped(pump.RuntimeRoot, sessionStr, oldPaneStr, fmt.Sprint(worktree), workerPane, reviewerPane, reviewerShell, reviewerAgent)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	released := false
	defer func() {
		if !released {
			_ = unlock()
		}
	}()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	if parentOnly {
		if recorded, _ := task.Get("machine"); !host.Is(recorded) {
			return nil, fmt.Errorf("Cross-machine restore needs explicit worktree recovery, not a parent-only rebind.")
		}
	}
	if workerPane != "" {
		worktree, _ := task.Get("worktree")
		worktreeStr, _ := worktree.(string)
		if observedCwd == "" || resolve(observedCwd) != resolve(worktreeStr) {
			return nil, fmt.Errorf("Worker cwd does not match the recorded worktree.")
		}
		task.Set("pane", workerPane)
		task.Set("session", session)
		task.Set("machine", host.ID)
	}
	if reviewerPane != "" {
		worktree, _ := task.Get("worktree")
		worktreeStr, _ := worktree.(string)
		if observedCwd == "" || resolve(observedCwd) != resolve(worktreeStr) {
			return nil, fmt.Errorf("Reviewer cwd does not match the recorded worktree.")
		}
		if reviewerPane == stringField(task, "pane") {
			return nil, fmt.Errorf("The worker pane cannot be adopted as the reviewer.")
		}
		oldValue, hasReviewer := task.Get("reviewer")
		old, _ := oldValue.(*ordjson.Object)
		if !hasReviewer || old == nil {
			return nil, fmt.Errorf("No recorded reviewer endpoint exists to rebind; the first reviewer is recorded by `sumctl review`.")
		}
		if !sameEndpoint(old, reviewerBefore) {
			return nil, fmt.Errorf("The recorded reviewer endpoint changed concurrently; inspect it again before rebinding.")
		}
		oldPane, _ := old.Get("pane")
		oldSession, _ := old.Get("session")
		oldPaneStr, _ := oldPane.(string)
		oldSessionStr, _ := oldSession.(string)
		if reviewerPane == oldPaneStr {
			return nil, fmt.Errorf("The replacement reviewer pane must differ from the recorded reviewer pane.")
		}
		if !host.Is(func() any { v, _ := old.Get("machine"); return v }()) || oldSessionStr != sessionStr {
			return nil, fmt.Errorf("The recorded reviewer pane is in another machine or session; its stop cannot be proved here.")
		}
		if reviewerStopObservation == nil {
			return nil, fmt.Errorf("Cannot prove the recorded reviewer endpoint is gone.")
		}
		newEndpoint := ordjson.NewObject()
		newEndpoint.Set("machine", host.ID)
		newEndpoint.Set("session", session)
		newEndpoint.Set("pane", reviewerPane)
		newEndpoint.Set("cwd", observedCwd)
		boundAt := store.Now()
		newEndpoint.Set("bound_at", boundAt)
		newEndpoint.Set("incarnation", bound.Record(boundAt))
		rebind := ordjson.NewObject()
		rebind.Set("from", endpointRecord(old))
		rebind.Set("to", endpointRecord(newEndpoint))
		rebind.Set("stop_evidence", reviewerStopObservation)
		record, err := evidence.Append(task, "reviewer-rebind", "coordinator", rebind, nil, ctx)
		if err != nil {
			return nil, err
		}
		record.Set("brief_revision", evidence.ActiveRevision(s, task))
		task.Set("reviewer", newEndpoint)
	}
	task.Set("parent", ctx)
	if err := s.SaveTask(task); err != nil {
		return nil, err
	}
	if workerPane != "" {
		// The explicit rebind records the worker registration for the bound pane, so the pane's own init and every
		// delivery to it are judged against the occupant the coordinator inspected.
		if _, err := s.Register(store.Endpoint{Machine: host.ID, Session: sessionStr, Pane: workerPane, Cwd: observedCwd}, "worker", taskID, bound.Record(store.Now())); err != nil {
			return nil, err
		}
	}
	if err := unlock(); err != nil {
		return nil, err
	}
	released = true
	opts := pump
	opts.Ctx = ctx
	opts.Tasks = []string{taskID}
	opts.Inline = true
	opts.CallerVerifiedRole = "coordinator" // RequireCoordinator judged this pane's occupant above.
	pumped, err := returns.Pump(s, opts)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	for _, key := range task.Keys() {
		v, _ := task.Get(key)
		result.Set(key, v)
	}
	returns.SetNotice(s, task, result)
	result.Set("returns", pumped)
	return result, nil
}

func reviewerStopped(runtimeRoot, session, oldPane, worktree, workerPane, newPane string, newShell int, newAgent *ordjson.Object) (*ordjson.Object, error) {
	observation := ordjson.NewObject()
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	if err != nil {
		return observation, err
	}
	oldAgent, agentCode, err := herdrclient.Observe(herdrPath, session, 5*time.Second, "agent", "get", oldPane)
	observation.Set("agent_code", agentCode)
	if err != nil {
		return observation, fmt.Errorf("Cannot prove the recorded reviewer endpoint is gone: %w", err)
	}
	if oldAgent != nil {
		return observation, fmt.Errorf("Recorded reviewer pane %s still holds a live reviewer agent; it cannot be replaced.", oldPane)
	}
	if agentCode != "agent_not_found" && agentCode != "pane_not_found" {
		return observation, fmt.Errorf("Cannot prove the recorded reviewer endpoint is gone (%s).", agentCode)
	}
	oldPaneInfo, paneCode, err := herdrclient.Observe(herdrPath, session, 5*time.Second, "pane", "get", oldPane)
	observation.Set("pane_code", paneCode)
	observation.Set("pane_present", oldPaneInfo != nil)
	if err != nil {
		return observation, fmt.Errorf("Cannot prove the recorded reviewer endpoint is gone: %w", err)
	}
	if oldPaneInfo == nil && paneCode != "pane_not_found" {
		return observation, fmt.Errorf("Cannot prove the recorded reviewer pane is gone (%s).", paneCode)
	}
	exclude := map[int]bool{}
	if newShell > 0 {
		exclude[newShell] = true
	}
	for _, key := range []string{"pid", "agent_pid"} {
		if pid := numericPID(newAgent, key); pid > 0 {
			exclude[pid] = true
		}
	}
	for _, paneID := range []string{workerPane, newPane} {
		info, code, err := environment.PaneProcesses(runtimeRoot, session, paneID)
		if err != nil {
			return observation, fmt.Errorf("Cannot inspect checkout endpoint %s: %w", paneID, err)
		}
		if info == nil && code != "pane_not_found" && code != "agent_not_found" {
			return observation, fmt.Errorf("Cannot inspect checkout endpoint %s (%s).", paneID, code)
		}
		if info != nil {
			if pid := numericPID(info, "shell_pid"); pid > 0 {
				exclude[pid] = true
			}
			processes, _ := info.Get("processes")
			for _, raw := range asList(processes) {
				if pid := numericPID(asObject(raw), "pid"); pid > 0 {
					exclude[pid] = true
				}
			}
		}
	}
	if oldPaneInfo != nil {
		paneObj, _ := oldPaneInfo.(*ordjson.Object)
		if nested, ok := paneObj.Get("pane"); ok {
			paneObj, _ = nested.(*ordjson.Object)
		}
		oldCwd, _ := paneObj.Get("cwd")
		observation.Set("old_cwd", oldCwd)
		if resolve(fmt.Sprint(oldCwd)) != resolve(worktree) {
			return observation, fmt.Errorf("Recorded reviewer pane cwd changed; its stop cannot be proved.")
		}
		info, code, err := environment.PaneProcesses(runtimeRoot, session, oldPane)
		if err != nil {
			return observation, fmt.Errorf("Cannot inspect the recorded reviewer pane: %w", err)
		}
		if info == nil {
			return observation, fmt.Errorf("Cannot prove the recorded reviewer pane is unoccupied (%s).", code)
		}
		if pid := numericPID(info, "shell_pid"); pid > 0 {
			observation.Set("old_shell_pid", pid)
			exclude[pid] = true
		}
		processes, _ := info.Get("processes")
		if rows, _ := processes.([]any); len(rows) > 0 {
			return observation, fmt.Errorf("Recorded reviewer pane %s still has foreground processes; its stop cannot be proved.", oldPane)
		}
	}
	for _, paneID := range []string{workerPane, newPane} {
		agent, code, err := herdrclient.Observe(herdrPath, session, 5*time.Second, "agent", "get", paneID)
		if err != nil {
			return observation, fmt.Errorf("Cannot inspect checkout endpoint %s: %w", paneID, err)
		}
		if agent != nil {
			agentObj := herdrclient.UnwrapAgent(agent)
			for _, key := range []string{"pid", "agent_pid"} {
				if pid := numericPID(agentObj, key); pid > 0 {
					exclude[pid] = true
				}
			}
		} else if code != "agent_not_found" && code != "pane_not_found" {
			return observation, fmt.Errorf("Cannot inspect checkout endpoint %s (%s).", paneID, code)
		}
	}
	inside, err := proc.ProcessesBoundTo(worktree, exclude)
	excluded := make([]any, 0, len(exclude))
	for pid := range exclude {
		excluded = append(excluded, pid)
	}
	sort.Slice(excluded, func(i, j int) bool { return excluded[i].(int) < excluded[j].(int) })
	observation.Set("excluded_pids", excluded)
	if err != nil {
		return observation, fmt.Errorf("Cannot prove there are no other processes in the task checkout: %w", err)
	}
	if len(inside) > 0 {
		return observation, fmt.Errorf("Cannot rebind reviewer while process %d remains bound to the task checkout.", inside[0].PID)
	}
	return observation, nil
}

func numericPID(obj *ordjson.Object, key string) int {
	if obj == nil {
		return 0
	}
	value, _ := obj.Get(key)
	pid, _ := strconv.Atoi(fmt.Sprint(value))
	return pid
}

func asObject(value any) *ordjson.Object {
	obj, _ := value.(*ordjson.Object)
	return obj
}

func asList(value any) []any {
	rows, _ := value.([]any)
	return rows
}

func endpointRecord(endpoint *ordjson.Object) *ordjson.Object {
	row := ordjson.NewObject()
	for _, key := range []string{"machine", "session", "pane", "incarnation"} {
		value, _ := endpoint.Get(key)
		row.Set(key, value)
	}
	return row
}

func sameEndpoint(a, b *ordjson.Object) bool {
	if a == nil || b == nil {
		return a == b
	}
	for _, key := range []string{"machine", "session", "pane"} {
		if stringField(a, key) != stringField(b, key) {
			return false
		}
	}
	return true
}
