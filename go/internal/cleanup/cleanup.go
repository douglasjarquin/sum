package cleanup

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func Pending(task *ordjson.Object) *ordjson.Object {
	recordValue, _ := task.Get("cleanup")
	record, _ := recordValue.(*ordjson.Object)

	statusValue, _ := task.Get("status")
	var state any
	if record != nil {
		state, _ = record.Get("state")
	}

	if statusValue == "archived" && (state == nil || state == "complete") {
		return nil
	}

	switch state {
	case "blocked", "removing", "ready", "pending":
		at, _ := record.Get("at")
		blockersValue, _ := record.Get("blockers")
		blockerList, _ := blockersValue.([]any)
		codes := make([]any, 0, len(blockerList))
		for _, b := range blockerList {
			blocker, _ := b.(*ordjson.Object)
			code, _ := blocker.Get("code")
			codes = append(codes, code)
		}
		result := ordjson.NewObject()
		result.Set("state", state)
		result.Set("at", at)
		result.Set("blockers", codes)
		return result
	}

	prValue, _ := task.Get("pr")
	pr, _ := prValue.(*ordjson.Object)
	if pr != nil {
		if merged, _ := pr.Get("merged_for_task"); merged == true {
			observedAt, _ := pr.Get("observed_at")
			idValue, _ := task.Get("id")
			result := ordjson.NewObject()
			result.Set("state", "pending")
			result.Set("at", observedAt)
			result.Set("blockers", []any{})
			result.Set("note", fmt.Sprintf("PR merged on record; run cleanup %v", idValue))
			return result
		}
	}
	return nil
}
