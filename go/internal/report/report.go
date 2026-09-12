package report

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/ask"
	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func Run(s *store.Store, taskID, text string, handoff *ordjson.Object, endpoint *ordjson.Object, pump returns.PumpOpts) (*ordjson.Object, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		unlock()
		return nil, err
	}
	revision := evidence.ActiveRevision(s, task)
	stamp := store.Now()
	reportObj := ordjson.NewObject()
	reportObj.Set("text", text)
	reportObj.Set("submitted_at", stamp)
	reportObj.Set("brief_revision", revision)
	reportObj.Set("sum_version", contract.SumVersion)
	var candidate any
	if handoff != nil {
		candidate, _ = handoff.Get("candidate")
	}
	reportObj.Set("candidate", candidate)
	task.Set("report", reportObj)

	if handoff != nil {
		verification, _ := handoff.Get("verification")
		run, _ := verification.(*ordjson.Object)
		if run != nil {
			runID, _ := run.Get("run_id")
			if recorded := recordedRunIDs(task); runID != nil && recorded[fmt.Sprint(runID)] != "" {
				unlock()
				return nil, fmt.Errorf("Verification run id %v is already recorded on this task by the %s; every run has its own immutable id. Run the contract again for this candidate and attach that run.", runID, recorded[fmt.Sprint(runID)])
			}
		}
	}

	body := ordjson.NewObject()
	body.Set("text", text)
	record, err := evidence.Append(task, "report", "worker", body, candidate, endpoint)
	if err != nil {
		unlock()
		return nil, err
	}
	records := []*ordjson.Object{record}
	if handoff != nil {
		handoffBody := ordjson.NewObject()
		handoffBody.Set("handoff", handoff)
		hRecord, err := evidence.Append(task, "handoff", "worker", handoffBody, candidate, endpoint)
		if err != nil {
			unlock()
			return nil, err
		}
		records = append(records, hRecord)
		verification, _ := handoff.Get("verification")
		run, _ := verification.(*ordjson.Object)
		if run != nil {
			runBody := ordjson.NewObject()
			outcome, _ := run.Get("outcome")
			runID, _ := run.Get("run_id")
			runBody.Set("result", runOutcomeResult(fmt.Sprint(outcome)))
			runBody.Set("text", fmt.Sprintf("worker run %v (%v)", runID, outcome))
			for _, key := range run.Keys() {
				v, _ := run.Get(key)
				runBody.Set(key, v)
			}
			vRecord, err := evidence.Append(task, "verification", "worker", runBody, candidate, endpoint)
			if err != nil {
				unlock()
				return nil, err
			}
			records = append(records, vRecord)
		}
	}
	ids := make([]any, 0, len(records))
	for _, rec := range records {
		rec.Set("brief_revision", revision)
		id, _ := rec.Get("id")
		ids = append(ids, id)
	}
	task.Set("status", "reported")
	firstID, _ := records[0].Get("id")
	ask.SupersedeAttention(task, fmt.Sprintf("report %v", firstID))
	if err := s.SaveTask(task); err != nil {
		unlock()
		return nil, err
	}
	unlock()

	notice, err := returns.Notify(s, pump, taskID, "parent", "a worker report is available", false)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", taskID)
	result.Set("status", "reported-not-verified")
	result.Set("evidence", ids)
	result.Set("notice", notice)
	return result, nil
}

func recordedRunIDs(task *ordjson.Object) map[string]string {
	out := map[string]string{}
	list, _ := task.Get("evidence")
	items, _ := list.([]any)
	for _, raw := range items {
		rec, _ := raw.(*ordjson.Object)
		if rec == nil {
			continue
		}
		kind, _ := rec.Get("kind")
		if kind != "verification" {
			continue
		}
		runID, _ := rec.Get("run_id")
		if runID == nil {
			continue
		}
		source, _ := rec.Get("source")
		out[fmt.Sprint(runID)] = fmt.Sprint(source)
	}
	return out
}

func runOutcomeResult(outcome string) string {
	switch outcome {
	case "pass":
		return "pass"
	case "fail":
		return "fail"
	case "blocked":
		return "inconclusive"
	default:
		return outcome
	}
}
