package updatecmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/douglasjarquin/sum/go/internal/execution"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

type bootstrapPlan struct {
	tasks    []*ordjson.Object
	releases []any
	changed  []*ordjson.Object
}

func planBootstrapReleases(s *store.Store, root, operation string, tasks []*ordjson.Object) (*bootstrapPlan, []any) {
	plan := &bootstrapPlan{tasks: tasks, releases: []any{}}
	var blocked []any
	for _, task := range tasks {
		reservation, err := reservations.GetExecution(task)
		if err != nil {
			blocked = append(blocked, fmt.Sprintf("task %s execution cannot be validated: %v", effectField(task, "id"), err))
			continue
		}
		if reservation == nil || reservation.Worker == nil {
			continue
		}
		worker := reservation.Worker
		state := effectField(worker, "state")
		completed := false
		if state == "released" && operation != "" {
			raw, _ := worker.Get("observations")
			observations, _ := raw.([]any)
			if len(observations) != 0 {
				completed = effectField(asObject(observations[len(observations)-1]), "bootstrap_operation") == operation
			}
		}
		if state != "running" && !completed {
			continue
		}
		if err := repair.RefuseDuringCleanup(task, "Bootstrap reconciliation"); err != nil {
			blocked = append(blocked, err.Error())
			continue
		}
		if !nativeReceiptResolvesLaunch(s, task, worker) {
			continue
		}
		if err := s.CheckMachine(task); err != nil {
			blocked = append(blocked, err.Error())
			continue
		}
		if effectField(worker, "checkout") != effectField(task, "worktree") {
			blocked = append(blocked, "task "+effectField(task, "id")+" worker checkout differs from its reservation")
			continue
		}
		proof := execution.ObserveStopFresh(s, root, task, worker)
		if proof.Outcome != execution.OutcomeStopped {
			blocked = append(blocked, "task "+effectField(task, "id")+" stopped-worker reconciliation: "+proof.Reason)
			continue
		}
		raw, _ := worker.Get("generation")
		generation, err := strconv.ParseInt(fmt.Sprint(raw), 10, 64)
		if err != nil || generation < 1 {
			blocked = append(blocked, "invalid stopped-worker reservation generation")
			continue
		}
		originalGeneration := generation
		if completed {
			originalGeneration--
		}
		row := ordjson.NewObject()
		row.Set("task", effectField(task, "id"))
		row.Set("attempt", effectField(worker, "id"))
		row.Set("generation", json.Number(strconv.FormatInt(originalGeneration, 10)))
		plan.releases = append(plan.releases, row)
		if !completed {
			proof.Observation.Set("bootstrap_operation", operation)
			if _, err := execution.ReleaseStopped(task, effectField(worker, "id"), proof.Observation, generation); err != nil {
				blocked = append(blocked, err.Error())
				continue
			}
			plan.changed = append(plan.changed, task)
		}
	}
	return plan, blocked
}

func (p *bootstrapPlan) matches(journal *ordjson.Object) error {
	raw, present := journal.Get("reconciliation")
	if !present {
		return fmt.Errorf("bootstrap journal has no reconciliation plan")
	}
	expected, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("malformed bootstrap reconciliation plan")
	}
	want, e1 := ordjson.MarshalCompact(expected)
	got, e2 := ordjson.MarshalCompact(p.releases)
	if e1 != nil || e2 != nil || !bytes.Equal(want, got) {
		return fmt.Errorf("stopped-worker reconciliation plan changed; resume only the recorded task attempts and generations")
	}
	return nil
}
