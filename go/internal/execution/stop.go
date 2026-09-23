package execution

import (
	"fmt"
	"os"
	"time"

	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

const (
	OutcomeStopped = "stopped"
	OutcomeUnknown = "unknown"
)

type StopProof struct {
	Outcome     string
	Reason      string
	Observation *ordjson.Object
	// Orphans lists surviving processes provably bound to the task checkout
	// (cwd inside it or argv naming it, as with a detached
	// `serve --mcp --path <checkout>` daemon), populated only when the worker
	// pane is verified absent (pane_not_found or agent_not_found). The attempt
	// outcome stays unknown for callers like park; cleanup may stop orphans
	// once pane death is proven.
	Orphans []proc.BoundProcess
}

func ObserveStop(s *store.Store, runtimeRoot string, task, attempt *ordjson.Object) StopProof {
	if stringField(attempt, "state") == "released" {
		return StopProof{Outcome: OutcomeStopped, Observation: lastObservation(attempt)}
	}
	kind := stringField(attempt, "kind")
	if kind == "worker" {
		return observeWorker(s, runtimeRoot, task, attempt)
	}
	return observeVerifier(attempt)
}

func observeVerifier(attempt *ordjson.Object) StopProof {
	checkout := stringField(attempt, "checkout")
	if checkout == "" {
		return unknown("Verifier checkout identity is missing; reservation remains held.")
	}
	opPID, hasOp := pidField(attempt, "operation_pid")
	if !hasOp || opPID == 0 {
		return unknown("Verifier process identity is missing; reservation remains held.")
	}
	occupant := asObject(func() any { v, _ := attempt.Get("occupant"); return v }())
	occPID, hasOcc := pidField(occupant, "pid")
	if occupant == nil || !hasOcc || occPID == 0 {
		return unknown("Verifier occupant identity is missing; reservation remains held.")
	}
	argv, _ := occupant.Get("argv")
	if normalizeRecordedArgv(argv) == nil {
		return unknown("Verifier occupant argv is missing; reservation remains held.")
	}
	host, err := os.Hostname()
	if err != nil {
		return unknown("Verifier instance cannot be inspected; reservation remains held.")
	}
	if stringField(occupant, "machine") != host {
		return unknown("Verifier occupant instance does not match this machine; reservation remains held.")
	}
	occCheckout := stringField(occupant, "checkout")
	if occCheckout == "" || resolve(occCheckout) != resolve(checkout) {
		return unknown("Verifier occupant checkout does not match the attempt; reservation remains held.")
	}
	if proof := inspectRecordedPIDs([]int{opPID, occPID}, occupant, occPID); proof.Outcome != OutcomeStopped {
		return proof
	}
	inside, err := proc.ProcessesBoundTo(checkout, nil)
	if err != nil {
		return unknown("Verifier checkout processes cannot be inspected; reservation remains held. " + err.Error())
	}
	if len(inside) > 0 {
		return unknown(fmt.Sprintf("Verifier still has an owned process bound to its checkout (pid %d); reservation remains held.", inside[0].PID))
	}
	return stopped(func(obs *ordjson.Object) {
		obs.Set("pid", jsonNumber(occPID))
		obs.Set("checkout", checkout)
		_, statErr := os.Stat(checkout)
		obs.Set("checkout_present", statErr == nil)
	})
}

func observeWorker(s *store.Store, runtimeRoot string, task, attempt *ordjson.Object) StopProof {
	session := stringField(task, "session")
	envSession, err := store.SessionFromEnv()
	if err != nil {
		return unknown(err.Error())
	}
	if session != envSession {
		return unknown(fmt.Sprintf("Task lives in Herdr session %s; observation from another session cannot release it.", session))
	}
	paneID := stringField(task, "pane")
	workspace := stringField(task, "workspace")
	worktree := stringField(task, "worktree")
	if paneID == "" || workspace == "" || worktree == "" {
		return unknown("Worker pane, workspace, or checkout identity is missing; reservation remains held.")
	}
	host, err := os.Hostname()
	if err != nil {
		return unknown("Worker instance cannot be inspected; reservation remains held.")
	}
	if stringField(task, "machine") != host {
		return unknown("Worker instance does not match this machine; reservation remains held.")
	}
	occupant := asObject(func() any { v, _ := attempt.Get("occupant"); return v }())
	if occupant != nil {
		occCheckout := stringField(occupant, "checkout")
		if occCheckout != "" && resolve(occCheckout) != resolve(worktree) {
			return unknown("Worker occupant checkout does not match the task checkout; reservation remains held.")
		}
		if machine := stringField(occupant, "machine"); machine != "" && machine != host {
			return unknown("Worker occupant instance does not match this machine; reservation remains held.")
		}
	}
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	if err != nil {
		return unknown(err.Error())
	}
	pane, code, err := herdrclient.Observe(herdrPath, session, 5*time.Second, "pane", "get", paneID)
	if err != nil {
		return unknown(err.Error())
	}
	if pane == nil {
		if !herdrclient.IsAbsent(code) {
			return unknown(fmt.Sprintf("Worker pane cannot prove exit (%s); an unobservable pane does not release capacity.", code))
		}
	} else {
		paneObj := asObject(pane)
		if nested, ok := paneObj.Get("pane"); ok {
			if inner := asObject(nested); inner != nil {
				paneObj = inner
			}
		}
		ws, _ := paneObj.Get("workspace_id")
		cwd, _ := paneObj.Get("cwd")
		if fmt.Sprint(ws) != workspace || resolve(asString(cwd)) != resolve(worktree) {
			return unknown("Worker pane identity changed; reservation remains held.")
		}
		agent, agentCode, err := herdrclient.Observe(herdrPath, session, 5*time.Second, "agent", "get", paneID)
		if err != nil {
			return unknown(err.Error())
		}
		if agent != nil {
			agentObj := asObject(agent)
			if nested, ok := agentObj.Get("agent"); ok {
				if inner := asObject(nested); inner != nil {
					agentObj = inner
				}
			}
			status, _ := agentObj.Get("agent_status")
			return unknown(fmt.Sprintf("Worker is still observable (%v); idle or done status is not exit proof.", status))
		}
		if agentCode != "agent_not_found" {
			return unknown(fmt.Sprintf("Worker agent cannot be observed conclusively (%s); reservation remains held.", agentCode))
		}
		info, procCode, procErr := environment.PaneProcesses(runtimeRoot, session, paneID)
		if procErr != nil {
			return unknown(procErr.Error())
		}
		if info == nil {
			return unknown(fmt.Sprintf("Worker pane processes cannot be observed conclusively (%s); reservation remains held.", procCode))
		}
		processes, _ := info.Get("processes")
		list, _ := processes.([]any)
		if len(list) > 0 {
			return unknown("Worker pane still has foreground processes besides its shell; reservation remains held.")
		}
		exclude := EndpointShells(runtimeRoot, task)
		if n, ok := pidField(info, "shell_pid"); ok {
			exclude[n] = true
		}
		if occupant != nil {
			if n, ok := pidField(occupant, "shell_pid"); ok {
				exclude[n] = true
			}
			if n, has := pidField(occupant, "pid"); has && n != 0 {
				if proof := inspectRecordedPIDs([]int{n}, occupant, n); proof.Outcome != OutcomeStopped {
					return proof
				}
			}
		}
		inside, cwdErr := proc.ProcessesBoundTo(worktree, exclude)
		if cwdErr != nil {
			return unknown("Worker checkout processes cannot be inspected; reservation remains held. " + cwdErr.Error())
		}
		if len(inside) > 0 {
			return unknown(fmt.Sprintf("Worker still has an owned process bound to its checkout (pid %d); reservation remains held.", inside[0].PID))
		}
	}
	if pane == nil {
		inside, cwdErr := proc.ProcessesBoundTo(worktree, EndpointShells(runtimeRoot, task))
		if cwdErr != nil {
			return unknown("Worker checkout processes cannot be inspected; reservation remains held. " + cwdErr.Error())
		}
		if len(inside) > 0 {
			proof := unknown(fmt.Sprintf("Worker still has an owned process bound to its checkout (pid %d); reservation remains held.", inside[0].PID))
			proof.Orphans = inside
			return proof
		}
	}
	env, err := environment.Read(s, stringField(task, "id"))
	if err != nil {
		return unknown(err.Error())
	}
	if env != nil {
		services, _ := env.Get("services")
		list, _ := services.([]any)
		var active []any
		for _, raw := range list {
			row := asObject(raw)
			state := stringField(row, "state")
			for _, a := range append(environment.ServiceActive, "failed") {
				if state == a {
					id, _ := row.Get("id")
					active = append(active, id)
				}
			}
		}
		if len(active) > 0 {
			return unknown(fmt.Sprintf("Owned service reservations remain unresolved: %v; stop or reconcile them before parking the worker.", active))
		}
	}
	return stopped(func(obs *ordjson.Object) {
		obs.Set("pane", paneID)
		obs.Set("workspace", workspace)
		obs.Set("checkout", worktree)
	})
}

// EndpointShells returns the shell pid Herdr currently reports for each other
// pane the task records as its own endpoint (the reviewer pane). That shell
// is accounted for by the endpoint's own close path, so it is neither a
// worker occupant nor an orphan. Only the exact observed shell pid qualifies,
// and only while it sits in the pane's foreground as a known shell and the
// live pane's cwd is the task checkout: anything it runs is a separate pid and
// still binds the checkout. The reviewer's recorded cwd is the installation
// root the review command ran from, not the pane's, so it is not compared. An
// endpoint on another machine or session, one Herdr cannot report, or one
// whose identity changed yields nothing, so its processes keep blocking.
func EndpointShells(runtimeRoot string, task *ordjson.Object) map[int]bool {
	shells := map[int]bool{}
	reviewer := asObject(func() any { v, _ := task.Get("reviewer"); return v }())
	paneID := stringField(reviewer, "pane")
	session := stringField(task, "session")
	worktree := stringField(task, "worktree")
	if paneID == "" || paneID == stringField(task, "pane") || stringField(reviewer, "session") != session || worktree == "" {
		return shells
	}
	if host, err := os.Hostname(); err != nil || stringField(reviewer, "machine") != host {
		return shells
	}
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	if err != nil {
		return shells
	}
	pane, _, err := herdrclient.Observe(herdrPath, session, 5*time.Second, "pane", "get", paneID)
	if err != nil || pane == nil {
		return shells
	}
	paneObj := unwrapField(asObject(pane), "pane")
	if resolve(stringField(paneObj, "cwd")) != resolve(worktree) {
		return shells
	}
	info, _, err := herdrclient.Observe(herdrPath, session, 5*time.Second, "pane", "process-info", "--pane", paneID)
	if err != nil || info == nil {
		return shells
	}
	infoObj := unwrapField(asObject(info), "process_info")
	shellPID, ok := pidField(infoObj, "shell_pid")
	if !ok {
		return shells
	}
	fg, _ := infoObj.Get("foreground_processes")
	list, _ := fg.([]any)
	for _, raw := range list {
		p := asObject(raw)
		argv0 := stringField(p, "argv0")
		if argv0 == "" {
			argv0 = stringField(p, "name")
		}
		if n, has := pidField(p, "pid"); has && n == shellPID && environment.IsShell(argv0) {
			shells[shellPID] = true
		}
	}
	return shells
}

func unwrapField(o *ordjson.Object, key string) *ordjson.Object {
	if o == nil {
		return nil
	}
	if inner := asObject(func() any { v, _ := o.Get(key); return v }()); inner != nil {
		return inner
	}
	return o
}

func inspectRecordedPIDs(pids []int, occupant *ordjson.Object, occupantPID int) StopProof {
	seen := map[int]bool{}
	for _, pid := range pids {
		if pid == 0 || seen[pid] {
			continue
		}
		seen[pid] = true
		running, err := proc.PIDRunning(pid)
		if err != nil {
			return unknown(err.Error())
		}
		if running {
			if occupant != nil && pid == occupantPID {
				live, argvErr := proc.ProcessArgv(pid)
				if argvErr != nil {
					return unknown("Recorded pid cannot be inspected; reservation remains held. " + argvErr.Error())
				}
				argv, _ := occupant.Get("argv")
				if !proc.ArgvEqual(argv, live) {
					return unknown(fmt.Sprintf("Recorded pid %d was reused or does not match recorded argv; reservation remains held.", pid))
				}
			}
			return unknown(fmt.Sprintf("Recorded pid %d still exists; reservation remains held.", pid))
		}
		kids, err := proc.Descendants(pid)
		if err != nil {
			return unknown("Owned child processes cannot be inspected; reservation remains held. " + err.Error())
		}
		for _, child := range kids {
			childRunning, childErr := proc.PIDRunning(child)
			if childErr != nil {
				return unknown(childErr.Error())
			}
			if childRunning {
				return unknown(fmt.Sprintf("Owned child pid %d still exists; reservation remains held.", child))
			}
		}
	}
	return StopProof{Outcome: OutcomeStopped}
}

func unknown(reason string) StopProof {
	obs := ordjson.NewObject()
	obs.Set("at", store.Now())
	obs.Set("outcome", OutcomeUnknown)
	obs.Set("reason", reason)
	return StopProof{Outcome: OutcomeUnknown, Reason: reason, Observation: obs}
}

func stopped(extra func(*ordjson.Object)) StopProof {
	obs := ordjson.NewObject()
	obs.Set("at", store.Now())
	obs.Set("outcome", OutcomeStopped)
	if extra != nil {
		extra(obs)
	}
	return StopProof{Outcome: OutcomeStopped, Observation: obs}
}

func stringField(o *ordjson.Object, key string) string {
	if o == nil {
		return ""
	}
	v, _ := o.Get(key)
	s, _ := v.(string)
	return s
}

func pidField(o *ordjson.Object, key string) (int, bool) {
	if o == nil {
		return 0, false
	}
	if _, has := o.Get(key); !has {
		return 0, false
	}
	n := intFrom(o, key)
	return n, n > 0
}

func normalizeRecordedArgv(argv any) []string {
	list, ok := argv.([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok || s == "" {
			return nil
		}
		out = append(out, s)
	}
	return out
}
