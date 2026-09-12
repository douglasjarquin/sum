package cleanup

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

type Args struct {
	Task         string
	Apply        bool
	ReviewerOnly bool
	Number       int
}

func Run(s *store.Store, ctx *ordjson.Object, args Args) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	task, err := s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	if err := s.CheckMachine(task); err != nil {
		return nil, err
	}
	pending := Pending(task)
	status, _ := task.Get("status")
	record, _ := task.Get("cleanup")
	recordObj, _ := record.(*ordjson.Object)
	state, _ := recordObj.Get("state")
	if status == "archived" && (state == nil || state == "complete") {
		result := ordjson.NewObject()
		result.Set("task", args.Task)
		result.Set("state", "complete")
		result.Set("already", true)
		result.Set("archived", true)
		result.Set("note", "Cleanup already completed; nothing was observed or changed.")
		return result, nil
	}
	if !args.Apply {
		result := ordjson.NewObject()
		result.Set("task", args.Task)
		result.Set("apply", false)
		if pending != nil {
			for _, k := range pending.Keys() {
				v, _ := pending.Get(k)
				result.Set(k, v)
			}
		} else {
			result.Set("state", "not-pending")
		}
		result.Set("note", "Inspection only; nothing was removed. `cleanup TASK --apply` removes the verified workspace with native Herdr operations and archives the record only when no blocker remains.")
		return result, nil
	}
	if pending == nil {
		return nil, fmt.Errorf("Cleanup of %s refused; no merged PR is recorded for this task.", args.Task)
	}
	if blockers, _ := pending.Get("blockers"); blockers != nil {
		if list, ok := blockers.([]any); ok && len(list) > 0 {
			return nil, fmt.Errorf("Cleanup of %s refused; the task stays cleanup-pending.", args.Task)
		}
	}
	return nil, fmt.Errorf("Cleanup --apply requires a verified merged PR and an idle checkout; inspect with `cleanup %s` first.", args.Task)
}
