package bindcmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
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
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	host, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	if parentOnly {
		machine, _ := task.Get("machine")
		if machine != host {
			return nil, fmt.Errorf("Cross-machine restore needs explicit worktree recovery, not a parent-only rebind.")
		}
	}
	if workerPane != "" {
		herdrPath, err := toolpath.Find(pump.RuntimeRoot, "herdr")
		if err != nil {
			return nil, err
		}
		session, _ := ctx.Get("session")
		sessionStr, _ := session.(string)
		agent, err := herdrclient.Call(herdrPath, sessionStr, 5*time.Second, "agent", "get", workerPane)
		if err != nil {
			return nil, err
		}
		agentObj, _ := agent.(*ordjson.Object)
		if nested, ok := agentObj.Get("agent"); ok {
			if inner, is := nested.(*ordjson.Object); is {
				agentObj = inner
			}
		}
		cwd, _ := agentObj.Get("cwd")
		if cwd == nil {
			cwd, _ = agentObj.Get("working_directory")
		}
		cwdStr, _ := cwd.(string)
		worktree, _ := task.Get("worktree")
		worktreeStr, _ := worktree.(string)
		if cwdStr == "" || resolve(cwdStr) != resolve(worktreeStr) {
			return nil, fmt.Errorf("Worker cwd does not match the recorded worktree.")
		}
		task.Set("pane", workerPane)
		task.Set("session", session)
		task.Set("machine", host)
	}
	task.Set("parent", ctx)
	if err := s.SaveTask(task); err != nil {
		return nil, err
	}
	opts := pump
	opts.Ctx = ctx
	opts.Tasks = []string{taskID}
	opts.Reason = "saved task state needs attention"
	opts.Inline = true
	pumped, err := returns.Pump(s, opts)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	for _, key := range task.Keys() {
		v, _ := task.Get(key)
		result.Set(key, v)
	}
	result.Set("returns", pumped)
	return result, nil
}
