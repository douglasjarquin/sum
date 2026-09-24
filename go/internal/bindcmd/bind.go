package bindcmd

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
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

func Run(s *store.Store, ctx *ordjson.Object, taskID, workerPane string, parentOnly bool, pump returns.PumpOpts) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	if !parentOnly && workerPane == "" {
		return nil, fmt.Errorf("Specify --parent-only or --worker-pane. Binding never creates a replacement.")
	}
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	// A worker pane is observed before the state lock (no Herdr call runs under it): its cwd must be the task
	// checkout, and its occupant is what the worker registration records.
	var bound incarnation.Evidence
	var observedCwd string
	session, _ := ctx.Get("session")
	sessionStr, _ := session.(string)
	if workerPane != "" {
		herdrPath, err := toolpath.Find(pump.RuntimeRoot, "herdr")
		if err != nil {
			return nil, err
		}
		agent, err := herdrclient.Call(herdrPath, sessionStr, 5*time.Second, "agent", "get", workerPane)
		if err != nil {
			return nil, err
		}
		agentObj := herdrclient.UnwrapAgent(agent)
		observedCwd = herdrclient.AgentCwd(agentObj)
		bound = incarnation.FromInfo(agentObj)
		observeCtx, cancel := context.WithTimeout(context.Background(), 2*incarnation.ObserveTimeout)
		if shell, err := incarnation.ProbeShell(incarnation.SessionCall(observeCtx, herdrPath, sessionStr), workerPane); err == nil {
			bound.Shell = shell
		}
		cancel()
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
	// The record's own `notice` is history; the view derives it from the returns sidecar.
	notice := returns.NoticeOf(s, task)
	if _, recorded := task.Get("notice"); recorded || notice != nil {
		result.Set("notice", notice)
	}
	result.Set("returns", pumped)
	return result, nil
}
