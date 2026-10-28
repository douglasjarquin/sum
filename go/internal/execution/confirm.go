package execution

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/prepare"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

func sameRoute(a, b *ordjson.Object) bool {
	if a == nil || b == nil {
		return a == b
	}
	for _, key := range []string{"machine", "session", "pane", "cwd", "recipient", "role"} {
		av, _ := a.Get(key)
		bv, _ := b.Get(key)
		if fmt.Sprint(av) != fmt.Sprint(bv) {
			return false
		}
	}
	return true
}

func Confirm(s *store.Store, ctx *ordjson.Object, runtimeRoot, taskID, attemptID string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	if err != nil {
		return nil, err
	}
	if _, err := herdrclient.EnsureVersion(herdrPath, contract.HerdrCLI); err != nil {
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
	if err := repair.RefuseDuringCleanup(task, "Execution confirm"); err != nil {
		unlock()
		return nil, err
	}
	worker, err := reservations.Worker(task)
	if err != nil {
		unlock()
		return nil, err
	}
	wid, _ := worker.Get("id")
	_, uncertainAt, uncertain := reservations.UncertainLaunch(worker)
	if fmt.Sprint(wid) != attemptID || !uncertain {
		unlock()
		return nil, fmt.Errorf("Worker attempt %s is not an uncertain launch; nothing was recorded.", attemptID)
	}
	host, err := s.Machine()
	if err != nil {
		unlock()
		return nil, err
	}
	receipt := reservations.LaunchReceipt(task, worker, host.SameEndpoint)
	pane := asString(func() any { v, _ := task.Get("pane"); return v }())
	if receipt == nil {
		unlock()
		return nil, fmt.Errorf("Worker pane %s records no receipt after the uncertain launch at %s; the worker records one by running the init command from its brief in that pane (or by filing a report). Nothing was recorded.", pane, uncertainAt)
	}
	generation := int64From(worker, "generation")
	route := returns.ReturnRoute(task, "worker")
	worktree := asString(func() any { v, _ := task.Get("worktree"); return v }())
	session := asString(func() any { v, _ := task.Get("session"); return v }())
	launchSpec := launch.TaskLaunch(task)
	harness := asString(func() any { v, _ := launchSpec.Get("harness"); return v }())
	unlock()

	occupant, err := returns.ObserveWorker(s, runtimeRoot, task, route, worktree)
	if err != nil {
		return nil, err
	}
	agent := occupant.Agent()
	observedKind := asString(func() any { v, _ := agent.Get("agent"); return v }())
	if observedKind != harness {
		return nil, fmt.Errorf("Worker pane %s holds agent %s, not the recorded harness %s; nothing was recorded.", pane, observedKind, harness)
	}
	bound := prepare.ObserveBound(herdrPath, session, pane, agent)

	unlock, err = s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	currentWorker, err := reservations.Worker(current)
	if err != nil {
		return nil, err
	}
	currentID, _ := currentWorker.Get("id")
	_, _, stillUncertain := reservations.UncertainLaunch(currentWorker)
	if fmt.Sprint(currentID) != attemptID || int64From(currentWorker, "generation") != generation || !stillUncertain || !sameRoute(returns.ReturnRoute(current, "worker"), route) {
		return nil, fmt.Errorf("Worker identity changed during observation; nothing was recorded.")
	}
	if message := occupant.Check(s, route); message != "" {
		return nil, fmt.Errorf("%s", message)
	}
	receiptView := ordjson.NewObject()
	for _, key := range []string{"id", "kind", "at"} {
		v, _ := receipt.Get(key)
		receiptView.Set(key, v)
	}
	body := ordjson.NewObject()
	body.Set("action", "confirm")
	body.Set("attempt", attemptID)
	body.Set("receipt", receiptView)
	body.Set("agent_status", herdrclient.AgentStatus(agent))
	if _, err := evidence.Append(current, "execution", "coordinator", body, nil, ctx); err != nil {
		return nil, err
	}
	result, err := prepare.RecordRunning(s, current, runtimeRoot, session, pane, harness, agent, bound)
	if err != nil {
		return nil, err
	}
	result.Set("confirmed", true)
	result.Set("receipt", receiptView)
	return result, nil
}

func Redeliver(s *store.Store, ctx *ordjson.Object, runtimeRoot, taskID, attemptID string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	if err != nil {
		return nil, err
	}
	if _, err := herdrclient.EnsureVersion(herdrPath, contract.HerdrCLI); err != nil {
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
	if err := repair.RefuseDuringCleanup(task, "Execution redeliver"); err != nil {
		unlock()
		return nil, err
	}
	worker, err := reservations.Worker(task)
	if err != nil {
		unlock()
		return nil, err
	}
	wid, _ := worker.Get("id")
	outcome, _, uncertain := reservations.UncertainLaunch(worker)
	pane := asString(func() any { v, _ := task.Get("pane"); return v }())
	if fmt.Sprint(wid) != attemptID || !uncertain {
		unlock()
		return nil, fmt.Errorf("Worker attempt %s is not an uncertain launch; nothing was sent.", attemptID)
	}
	if outcome != "launch-uncertain" {
		unlock()
		return nil, fmt.Errorf("Worker attempt %s already had its brief confirmed delivered (%s); use `execution confirm %s --attempt %s` once the worker records a receipt. Nothing was sent.", attemptID, outcome, taskID, attemptID)
	}
	host, err := s.Machine()
	if err != nil {
		unlock()
		return nil, err
	}
	if reservations.LaunchReceipt(task, worker, host.SameEndpoint) != nil {
		unlock()
		return nil, fmt.Errorf("Worker pane %s already received a brief: the worker recorded a receipt after the uncertain launch. Use `execution confirm %s --attempt %s`; nothing was sent.", pane, taskID, attemptID)
	}
	launchSpec := launch.TaskLaunch(task)
	if err := launch.CheckRecorded(launchSpec); err != nil {
		unlock()
		return nil, err
	}
	briefFile, err := versions.ActiveBrief(s, task)
	if err != nil {
		unlock()
		return nil, err
	}
	generation := int64From(worker, "generation")
	route := returns.ReturnRoute(task, "worker")
	worktree := asString(func() any { v, _ := task.Get("worktree"); return v }())
	session := asString(func() any { v, _ := task.Get("session"); return v }())
	harness := asString(func() any { v, _ := launchSpec.Get("harness"); return v }())
	unlock()

	occupant, err := returns.ObserveWorker(s, runtimeRoot, task, route, worktree)
	if err != nil {
		return nil, err
	}
	agent := occupant.Agent()
	observedKind := asString(func() any { v, _ := agent.Get("agent"); return v }())
	if observedKind != harness {
		return nil, fmt.Errorf("Worker pane %s holds agent %s, not the recorded harness %s; nothing was sent.", pane, observedKind, harness)
	}

	unlock, err = s.Lock()
	if err != nil {
		return nil, err
	}
	current, err := s.ReadTask(taskID)
	if err != nil {
		unlock()
		return nil, err
	}
	currentWorker, err := reservations.Worker(current)
	if err != nil {
		unlock()
		return nil, err
	}
	currentID, _ := currentWorker.Get("id")
	_, _, stillUncertain := reservations.UncertainLaunch(currentWorker)
	if fmt.Sprint(currentID) != attemptID || int64From(currentWorker, "generation") != generation || !stillUncertain || !sameRoute(returns.ReturnRoute(current, "worker"), route) {
		unlock()
		return nil, fmt.Errorf("Worker identity changed during observation; nothing was sent.")
	}
	if message := occupant.Check(s, route); message != "" {
		unlock()
		return nil, fmt.Errorf("%s", message)
	}
	if _, err := reservations.Transition(current, attemptID, "starting", store.Now(), nil, &generation); err != nil {
		unlock()
		return nil, err
	}
	if st, _ := current.Get("status"); st == "needs-attention" {
		current.Set("status", "starting")
	}
	if err := s.SaveTask(current); err != nil {
		unlock()
		return nil, err
	}
	unlock()

	prompt := prepare.WorkerPrompt(s, runtimeRoot, taskID, briefFile, current)
	if err := prepare.DeliverBrief(herdrPath, session, pane, prompt); err != nil {
		return prepare.FailStart(s, taskID, err)
	}
	bound := prepare.ObserveBound(herdrPath, session, pane, agent)

	unlock, err = s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err = s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	finalWorker, err := reservations.Worker(current)
	if err != nil {
		return nil, err
	}
	finalID, _ := finalWorker.Get("id")
	finalState, _ := finalWorker.Get("state")
	if fmt.Sprint(finalID) != attemptID || finalState != "starting" || int64From(finalWorker, "generation") != generation+1 {
		return nil, fmt.Errorf("Worker identity changed after delivery; the recorded launch was not confirmed.")
	}
	body := ordjson.NewObject()
	body.Set("action", "redeliver")
	body.Set("attempt", attemptID)
	if _, err := evidence.Append(current, "execution", "coordinator", body, nil, ctx); err != nil {
		return nil, err
	}
	return prepare.RecordRunning(s, current, runtimeRoot, session, pane, harness, agent, bound)
}
