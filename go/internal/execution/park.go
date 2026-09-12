package execution

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/prepare"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

func asString(v any) string { s, _ := v.(string); return s }
func asObject(v any) *ordjson.Object { o, _ := v.(*ordjson.Object); return o }

func Park(s *store.Store, ctx *ordjson.Object, runtimeRoot, taskID, attemptID string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		unlock()
		return nil, err
	}
	if err := s.CheckMachine(task); err != nil {
		unlock()
		return nil, err
	}
	if err := repair.RefuseDuringCleanup(task, "Execution park"); err != nil {
		unlock()
		return nil, err
	}
	value, err := reservations.GetExecution(task)
	if err != nil {
		unlock()
		return nil, fmt.Errorf("Malformed execution reservation for %s: %s. Release is refused.", taskID, err)
	}
	var attempt *ordjson.Object
	if value == nil {
		if attemptID != "legacy:"+taskID {
			unlock()
			return nil, fmt.Errorf("Legacy task requires attempt legacy:%s; no reservation changed.", taskID)
		}
		owner := ordjson.NewObject()
		machine, _ := task.Get("machine")
		session, _ := task.Get("session")
		pane, _ := task.Get("pane")
		owner.Set("machine", machine)
		owner.Set("session", session)
		owner.Set("pane", pane)
		worktree, _ := task.Get("worktree")
		created, err := reservations.NewAttempt("worker", owner, worktree, store.Now(), "", nil)
		if err != nil {
			unlock()
			return nil, err
		}
		task.Set("execution", reservations.NewExecution(created))
		attempt = created
	} else {
		rows := append([]*ordjson.Object{value.Worker}, value.Verifiers...)
		var matches []*ordjson.Object
		for _, row := range rows {
			id, _ := row.Get("id")
			if id == attemptID {
				matches = append(matches, row)
			}
		}
		if len(matches) != 1 {
			unlock()
			return nil, fmt.Errorf("Execution attempt %s is stale or unknown; no reservation changed.", attemptID)
		}
		attempt = matches[0]
	}
	state := asString(func() any { v, _ := attempt.Get("state"); return v }())
	kind := asString(func() any { v, _ := attempt.Get("kind"); return v }())
	if state == "observing" {
		pidN := intFrom(attempt, "observer_pid")
		if pidN == 0 {
			unlock()
			return nil, fmt.Errorf("Reservation observation is active or its owner is unknown; reservation remains held.")
		}
		running, runErr := proc.PIDRunning(pidN)
		if runErr != nil {
			unlock()
			return nil, runErr
		}
		if running {
			unlock()
			return nil, fmt.Errorf("Reservation observation is active or its owner is unknown; reservation remains held.")
		}
	}
	allowed := map[string]bool{"held": true, "running": true, "uncertain": true, "observing": true}
	if kind != "worker" {
		allowed = map[string]bool{"starting": true, "running": true, "uncertain": true, "observing": true}
	}
	if kind == "verifier" && state == "starting" {
		if _, has := attempt.Get("operation_pid"); !has {
			unlock()
			return nil, fmt.Errorf("Verifier launch ownership is unknown; reservation remains held.")
		}
	}
	if !allowed[state] {
		unlock()
		return nil, fmt.Errorf("Execution attempt %s is %s and not parkable; no reservation changed.", attemptID, state)
	}
	recordedID := asString(func() any { v, _ := attempt.Get("id"); return v }())
	if _, err := reservations.Transition(task, recordedID, "observing", store.Now(), nil, nil); err != nil {
		unlock()
		return nil, err
	}
	attempt.Set("observer_pid", jsonNumber(os.Getpid()))
	generation := int64From(attempt, "generation")
	if err := s.SaveTask(task); err != nil {
		unlock()
		return nil, err
	}
	unlock()

	var observation *ordjson.Object
	var observeErr error
	if kind == "worker" {
		observation, observeErr = parkWorker(s, runtimeRoot, task)
	} else {
		observation, observeErr = parkVerifier(attempt)
	}
	if observeErr != nil {
		unlock, err = s.Lock()
		if err == nil {
			current, readErr := s.ReadTask(taskID)
			if readErr == nil {
				if val, gerr := reservations.GetExecution(current); gerr == nil && val != nil {
					row := matchAttempt(val, recordedID)
					if row != nil && asString(func() any { v, _ := row.Get("state"); return v }()) == "observing" && int64From(row, "generation") == generation {
						obs := ordjson.NewObject()
						obs.Set("at", store.Now())
						obs.Set("outcome", "uncertain")
						reason := observeErr.Error()
						if len(reason) > 500 {
							reason = reason[:500]
						}
						obs.Set("reason", reason)
						_, _ = reservations.Transition(current, recordedID, "uncertain", store.Now(), obs, &generation)
						row.Delete("observer_pid")
						_ = s.SaveTask(current)
					}
				}
			}
			unlock()
		}
		return nil, observeErr
	}
	unlock, err = s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	row, err := reservations.Transition(current, recordedID, "released", store.Now(), observation, &generation)
	if err != nil {
		return nil, fmt.Errorf("Worker attempt %s changed during stop observation; reservation remains held.", attemptID)
	}
	row.Delete("observer_pid")
	if kind == "worker" {
		if report, ok := current.Get("report"); ok && report != nil {
			current.Set("status", "reported")
		} else {
			current.Set("status", "waiting")
		}
	}
	if err := s.SaveTask(current); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", taskID)
	result.Set("attempt", row)
	result.Set("released", true)
	result.Set("observation", observation)
	return result, nil
}

func matchAttempt(value *reservations.Execution, id string) *ordjson.Object {
	rows := append([]*ordjson.Object{value.Worker}, value.Verifiers...)
	for _, row := range rows {
		rid, _ := row.Get("id")
		if rid == id {
			return row
		}
	}
	return nil
}

func jsonNumber(n int) json.Number { return json.Number(fmt.Sprint(n)) }

func intFrom(o *ordjson.Object, key string) int {
	return int(int64From(o, key))
}

func int64From(o *ordjson.Object, key string) int64 {
	v, _ := o.Get(key)
	switch t := v.(type) {
	case json.Number:
		n, _ := t.Int64()
		return n
	case int:
		return int64(t)
	case int64:
		return t
	}
	return 0
}

func parkWorker(s *store.Store, runtimeRoot string, task *ordjson.Object) (*ordjson.Object, error) {
	session := asString(func() any { v, _ := task.Get("session"); return v }())
	envSession, err := store.SessionFromEnv()
	if err != nil {
		return nil, err
	}
	if session != envSession {
		return nil, fmt.Errorf("Task lives in Herdr session %s; observation from another session cannot release it.", session)
	}
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	if err != nil {
		return nil, err
	}
	paneID := asString(func() any { v, _ := task.Get("pane"); return v }())
	pane, code, err := herdrclient.Observe(herdrPath, session, 5*time.Second, "pane", "get", paneID)
	if err != nil {
		return nil, err
	}
	if pane == nil {
		return nil, fmt.Errorf("Worker pane cannot prove exit (%s); a missing or unobservable pane does not release capacity.", code)
	}
	paneObj := asObject(pane)
	if nested, ok := paneObj.Get("pane"); ok {
		if inner := asObject(nested); inner != nil {
			paneObj = inner
		}
	}
	workspace, _ := task.Get("workspace")
	ws, _ := paneObj.Get("workspace_id")
	cwd, _ := paneObj.Get("cwd")
	worktree := asString(func() any { v, _ := task.Get("worktree"); return v }())
	if ws != workspace || resolve(asString(cwd)) != resolve(worktree) {
		return nil, fmt.Errorf("Worker pane identity changed; reservation remains held.")
	}
	agent, agentCode, err := herdrclient.Observe(herdrPath, session, 5*time.Second, "agent", "get", paneID)
	if err != nil {
		return nil, err
	}
	if agent != nil {
		agentObj := asObject(agent)
		if nested, ok := agentObj.Get("agent"); ok {
			if inner := asObject(nested); inner != nil {
				agentObj = inner
			}
		}
		status, _ := agentObj.Get("agent_status")
		return nil, fmt.Errorf("Worker is still observable (%v); idle or done status is not exit proof.", status)
	}
	if agentCode != "agent_not_found" {
		return nil, fmt.Errorf("Worker agent cannot be observed conclusively (%s); reservation remains held.", agentCode)
	}
	env, err := environment.Read(s, asString(func() any { v, _ := task.Get("id"); return v }()))
	if err != nil {
		return nil, err
	}
	if env != nil {
		services, _ := env.Get("services")
		list, _ := services.([]any)
		var active []any
		for _, raw := range list {
			row := asObject(raw)
			state := asString(func() any { v, _ := row.Get("state"); return v }())
			for _, a := range append(environment.ServiceActive, "failed") {
				if state == a {
					id, _ := row.Get("id")
					active = append(active, id)
				}
			}
		}
		if len(active) > 0 {
			return nil, fmt.Errorf("Owned service reservations remain unresolved: %v; stop or reconcile them before parking the worker.", active)
		}
	}
	obs := ordjson.NewObject()
	obs.Set("at", store.Now())
	obs.Set("outcome", "stopped")
	obs.Set("pane", paneID)
	obs.Set("workspace", workspace)
	obs.Set("checkout", worktree)
	return obs, nil
}

func parkVerifier(attempt *ordjson.Object) (*ordjson.Object, error) {
	if pid := intFrom(attempt, "operation_pid"); pid != 0 {
		running, err := proc.PIDRunning(pid)
		if err != nil {
			return nil, err
		}
		if running {
			return nil, fmt.Errorf("Verifier operation is still in progress; reservation remains held.")
		}
	}
	occupant := asObject(func() any { v, _ := attempt.Get("occupant"); return v }())
	var pid any
	if occupant != nil {
		pid, _ = occupant.Get("pid")
		if n := intFrom(occupant, "pid"); n != 0 {
			running, err := proc.PIDRunning(n)
			if err != nil {
				return nil, fmt.Errorf("Verifier pid %d cannot be inspected; reservation remains held.", n)
			}
			if running {
				return nil, fmt.Errorf("Verifier pid %d still exists; reservation remains held.", n)
			}
		}
	}
	checkout := asString(func() any { v, _ := attempt.Get("checkout"); return v }())
	present := checkout != ""
	if present {
		if _, err := os.Stat(checkout); err != nil {
			present = false
		}
	}
	obs := ordjson.NewObject()
	obs.Set("at", store.Now())
	obs.Set("outcome", "stopped")
	obs.Set("pid", pid)
	obs.Set("checkout", checkout)
	obs.Set("checkout_present", present)
	return obs, nil
}

func resolve(path string) string {
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

func Resume(s *store.Store, ctx *ordjson.Object, runtimeRoot, taskID, attemptID string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		unlock()
		return nil, err
	}
	if err := s.CheckMachine(task); err != nil {
		unlock()
		return nil, err
	}
	if err := repair.RefuseDuringCleanup(task, "Worker resume"); err != nil {
		unlock()
		return nil, err
	}
	worker, err := reservations.Worker(task)
	if err != nil {
		unlock()
		return nil, fmt.Errorf("Malformed execution reservation for %s: %s. Resume is refused.", taskID, err)
	}
	id, _ := worker.Get("id")
	state, _ := worker.Get("state")
	if id != attemptID || state != "released" {
		unlock()
		return nil, fmt.Errorf("Worker attempt %s is stale or not released; nothing was launched.", attemptID)
	}
	if err := repair.CheckAllowance(s, task); err != nil {
		unlock()
		return nil, err
	}
	tasks, err := s.AllTasks()
	if err != nil {
		unlock()
		return nil, err
	}
	repo := asString(func() any { v, _ := task.Get("repository"); return v }())
	admission, err := launch.Admit(s, tasks, repo)
	if err != nil {
		unlock()
		return nil, err
	}
	owner := ordjson.NewObject()
	for _, key := range []string{"machine", "session", "pane"} {
		v, _ := task.Get(key)
		owner.Set(key, v)
	}
	worktree, _ := task.Get("worktree")
	successor, err := reservations.NewAttempt("worker", owner, worktree, store.Now(), "", nil)
	if err != nil {
		unlock()
		return nil, err
	}
	successor.Set("resumes", attemptID)
	if err := repair.RecordResume(task, successor); err != nil {
		unlock()
		return nil, err
	}
	body := ordjson.NewObject()
	body.Set("attempt", worker)
	sid, _ := successor.Get("id")
	body.Set("successor", sid)
	if _, err := evidence.Append(task, "execution", "coordinator", body, nil, ctx); err != nil {
		unlock()
		return nil, err
	}
	if err := reservations.ReplaceWorker(task, successor); err != nil {
		unlock()
		return nil, err
	}
	task.Set("admission", admission)
	task.Set("status", "prepared")
	task.Set("error", nil)
	if err := s.SaveTask(task); err != nil {
		unlock()
		return nil, err
	}
	unlock()
	started, err := prepare.Start(s, ctx, runtimeRoot, taskID, nil)
	if err != nil {
		return nil, err
	}
	started.Set("task", taskID)
	return started, nil
}
