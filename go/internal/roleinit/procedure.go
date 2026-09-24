package roleinit

import (
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/procedure"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

// coordinatorCore validates the runtime's coordinator core before any coordinator role is granted or kept.
func coordinatorCore(runtimeRoot string) (*ordjson.Object, error) {
	return procedure.Describe(runtimeRoot, procedure.Coordinator, "no coordinator role was granted and nothing was recorded")
}

// roleProcedure is what init names for the returned role to read: the coordinator core or the developer
// procedure from the runtime, or a worker's pinned procedure from its task record. A developer or worker
// reference that fails validation is reported with `ok: false` rather than refusing the role, since those
// roles claim no coordination.
func roleProcedure(runtimeRoot string, s *store.Store, role string, task, coordinator *ordjson.Object) any {
	switch role {
	case "coordinator":
		if coordinator == nil {
			return nil
		}
		row := coordinator
		row.Set("ok", true)
		return []any{row}
	case "developer":
		row, err := procedure.Describe(runtimeRoot, procedure.Developer, "read it from a verified runtime before developing")
		if err != nil {
			row = ordjson.NewObject()
			row.Set("name", procedure.Developer.Name)
			row.Set("source", procedure.Developer.Path)
			row.Set("load", procedure.Developer.Load)
			row.Set("ok", false)
			row.Set("error", err.Error())
			return []any{row}
		}
		row.Set("ok", true)
		return []any{row}
	case "worker":
		if s == nil || task == nil {
			return nil
		}
		versionsObj, err := versions.ReadVersions(s, task)
		if err != nil {
			return nil
		}
		active := versions.ActiveRevision(versionsObj)
		if active == nil {
			return nil
		}
		policy, _ := active.Get("policy")
		policyObj, _ := policy.(*ordjson.Object)
		id, _ := task.Get("id")
		idStr, _ := id.(string)
		taskPath, err := s.TaskPath(idStr)
		if err != nil {
			return nil
		}
		refs := procedure.References(taskPath, procedure.Rows(policyObj))
		if refs == nil {
			return nil
		}
		return refs
	}
	return nil
}
