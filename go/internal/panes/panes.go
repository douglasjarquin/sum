// Package panes is the task-record of worker and reviewer panes that sweep closed
// because their recorded obligation is settled. Delivery and resume read this
// record so a closed pane is named pane-closed, never a mysterious unreachable.
package panes

import (
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func asObject(v any) *ordjson.Object {
	o, _ := v.(*ordjson.Object)
	return o
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asList(v any) []any {
	list, _ := v.([]any)
	return list
}

func field(obj *ordjson.Object, key string) any {
	if obj == nil {
		return nil
	}
	v, _ := obj.Get(key)
	return v
}

// Entries is the recorded closures on task, oldest first.
func Entries(task *ordjson.Object) []*ordjson.Object {
	var out []*ordjson.Object
	for _, raw := range asList(field(task, "closed_panes")) {
		if row := asObject(raw); row != nil {
			out = append(out, row)
		}
	}
	return out
}

// Closed reports whether paneID was recorded closed on task.
func Closed(task *ordjson.Object, paneID string) bool {
	if paneID == "" {
		return false
	}
	for _, row := range Entries(task) {
		if asString(field(row, "pane")) == paneID {
			return true
		}
	}
	return false
}

// WorkerIsClosed reports whether the task's current worker pane id is recorded closed.
func WorkerIsClosed(task *ordjson.Object) bool {
	return ClosedEndpoint(task, store.EndpointFromContext(asObject(field(task, "worker_endpoint"))))
}

// ReviewerIsClosed reports whether the task's recorded reviewer pane id is recorded closed.
func ReviewerIsClosed(task *ordjson.Object) bool {
	return ClosedEndpoint(task, store.EndpointFromContext(asObject(field(task, "reviewer"))))
}

// Append records one closure. Duplicate pane ids are ignored so a second sweep is a no-op.
func Append(task *ordjson.Object, paneID, role, agent, reason string) *ordjson.Object {
	if paneID == "" || Closed(task, paneID) {
		return nil
	}
	entry := ordjson.NewObject()
	entry.Set("pane", paneID)
	entry.Set("role", role)
	if agent == "" {
		entry.Set("agent", nil)
	} else {
		entry.Set("agent", agent)
	}
	entry.Set("reason", reason)
	entry.Set("at", store.Now())
	list := asList(field(task, "closed_panes"))
	task.Set("closed_panes", append(list, entry))
	return entry
}

// ResumeNote is the status/inbox/context line for a closed worker pane.
func ResumeNote(taskID string) string {
	if taskID == "" {
		return "pane closed, resume via execution resume"
	}
	return "pane closed, resume via execution resume " + taskID
}

func ClosedEndpoint(task *ordjson.Object, endpoint store.Endpoint) bool {
	if endpoint.Validate() != nil {
		return false
	}
	for _, raw := range asList(field(task, "closed_terminals")) {
		row := asObject(raw)
		if row == nil {
			continue
		}
		recorded := store.EndpointFromContext(asObject(field(row, "endpoint")))
		if recorded.Native().SameAuthority(endpoint.Native()) && recorded.CreatedMS == endpoint.CreatedMS {
			return true
		}
	}
	return false
}
func AppendEndpoint(task *ordjson.Object, endpoint store.Endpoint, reason string) *ordjson.Object {
	if endpoint.Validate() != nil || ClosedEndpoint(task, endpoint) {
		return nil
	}
	row := ordjson.NewObject()
	row.Set("endpoint", endpoint.Object())
	row.Set("role", endpoint.Role)
	row.Set("reason", reason)
	row.Set("at", store.Now())
	task.Set("closed_terminals", append(asList(field(task, "closed_terminals")), row))
	return row
}
