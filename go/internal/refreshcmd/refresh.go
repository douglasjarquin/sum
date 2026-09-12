package refreshcmd

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

func Status(s *store.Store, taskIDs []string) (*ordjson.Object, error) {
	result := ordjson.NewObject()
	result.Set("coordinator", versions.ContractState(s))
	var rows []any
	tasks, err := s.AllTasks()
	if err != nil {
		return nil, err
	}
	filter := map[string]bool{}
	for _, id := range taskIDs {
		filter[id] = true
	}
	for _, task := range tasks {
		id, _ := task.Get("id")
		idStr, _ := id.(string)
		if len(filter) > 0 && !filter[idStr] {
			continue
		}
		versionsObj, vErr := versions.ReadVersions(s, task)
		row := ordjson.NewObject()
		row.Set("task", idStr)
		if vErr != nil {
			row.Set("error", vErr.Error())
		} else {
			row.Set("refresh", versions.RefreshState(versionsObj))
		}
		rows = append(rows, row)
	}
	result.Set("tasks", rows)
	return result, nil
}

func AdoptCoordinator(s *store.Store, ctx *ordjson.Object, revision string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	versionsObj, err := versions.ReadContractVersions(s)
	if err != nil {
		return nil, err
	}
	requested, _ := versionsObj.Get("requested")
	if requested != revision {
		return nil, fmt.Errorf("%s is not the requested revision (%v). Adopt only what was requested.", revision, requested)
	}
	revisionsValue, _ := versionsObj.Get("revisions")
	list, _ := revisionsValue.([]any)
	var target *ordjson.Object
	for _, raw := range list {
		rev, _ := raw.(*ordjson.Object)
		id, _ := rev.Get("id")
		if id == revision {
			target = rev
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("Unknown revision %s.", revision)
	}
	for _, raw := range list {
		rev, _ := raw.(*ordjson.Object)
		if st, _ := rev.Get("status"); st == "active" {
			rev.Set("status", "superseded")
		}
	}
	target.Set("status", "active")
	versionsObj.Set("active", revision)
	versionsObj.Set("requested", nil)
	refreshValue, _ := versionsObj.Get("refresh")
	refresh, _ := refreshValue.([]any)
	row := ordjson.NewObject()
	row.Set("at", store.Now())
	row.Set("event", "adopted")
	row.Set("revision", revision)
	versionsObj.Set("refresh", append(refresh, row))
	if err := versions.WriteContractVersions(s, versionsObj); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("active", revision)
	result.Set("note", "Receipt recorded: this contract revision was read and adopted. A receipt is evidence of reading, not proof it is followed.")
	return result, nil
}

func Request(s *store.Store, ctx *ordjson.Object, taskIDs []string, coordinator bool) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("coordinator", coordinator)
	result.Set("tasks", taskIDs)
	result.Set("note", "Refresh request is recorded as a bounded pass over saved targets; delivery is a separate explicit step.")
	return result, nil
}
