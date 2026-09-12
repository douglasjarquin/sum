package execution

import (
	"encoding/json"
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func Show(s *store.Store, taskID string) (*ordjson.Object, error) {
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	value, err := reservations.GetExecution(task)
	if err != nil {
		return nil, fmt.Errorf("Malformed execution reservation for %s: %s. Admission and release are refused.", taskID, err)
	}
	result := ordjson.NewObject()
	result.Set("task", taskID)
	if value == nil {
		worker := ordjson.NewObject()
		worker.Set("id", "legacy:"+taskID)
		worker.Set("kind", "worker")
		worker.Set("state", "held")
		record := ordjson.NewObject()
		record.Set("schema", json.Number("1"))
		record.Set("worker", worker)
		record.Set("verifiers", []any{})
		result.Set("execution", record)
		result.Set("legacy", true)
		result.Set("note", "This legacy task has no execution metadata and remains held while non-archived.")
		return result, nil
	}
	record := ordjson.NewObject()
	record.Set("schema", json.Number(fmt.Sprint(reservations.Schema)))
	record.Set("worker", value.Worker)
	verifiers := make([]any, len(value.Verifiers))
	for i, v := range value.Verifiers {
		verifiers[i] = v
	}
	record.Set("verifiers", verifiers)
	result.Set("execution", record)
	result.Set("legacy", false)
	return result, nil
}
