package archive

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func Run(s *store.Store, taskID string, acknowledge bool) (*ordjson.Object, error) {
	if !acknowledge {
		return nil, fmt.Errorf("Use --acknowledge only after inspecting/preserving the work. This command never stops an agent or deletes a checkout.")
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
	if questionsValue, ok := task.Get("questions"); ok {
		if list, ok := questionsValue.([]any); ok {
			for _, q := range list {
				question, _ := q.(*ordjson.Object)
				status, _ := question.Get("status")
				if status != "applied" {
					return nil, fmt.Errorf("Outstanding questions must be answered and applied before archiving.")
				}
			}
		}
	}
	held, err := reservations.Held(task)
	if err != nil {
		id, _ := task.Get("id")
		return nil, fmt.Errorf("Malformed execution reservation for %v: %s. Admission and release are refused.", id, err)
	}
	if len(held) > 0 {
		return nil, fmt.Errorf("Task has a held execution reservation. Use `execution park TASK --attempt ID` after proving the worker stopped; archive never releases capacity.")
	}
	task.Set("status", "archived")
	if err := s.SaveTask(task); err != nil {
		return nil, err
	}
	worktree, _ := task.Get("worktree")
	result := ordjson.NewObject()
	result.Set("archived", taskID)
	result.Set("worktree_preserved", worktree)
	result.Set("processes_untouched", true)
	return result, nil
}
