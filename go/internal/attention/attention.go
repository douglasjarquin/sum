package attention

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func Seen(s *store.Store, ctx *ordjson.Object, taskID, attentionID string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
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
	list, _ := task.Get("attention")
	items, _ := list.([]any)
	var record *ordjson.Object
	for _, raw := range items {
		item, _ := raw.(*ordjson.Object)
		if item == nil {
			continue
		}
		id, _ := item.Get("id")
		if id == attentionID {
			record = item
			break
		}
	}
	if record == nil {
		return nil, fmt.Errorf("Attention record not found.")
	}
	status, _ := record.Get("status")
	if status == "open" {
		record.Set("status", "seen")
		record.Set("closed_at", store.Now())
		record.Set("closed_by", "coordinator")
		if err := s.SaveTask(task); err != nil {
			return nil, err
		}
	}
	return record, nil
}
