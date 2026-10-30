package panes

import (
	"reflect"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestClosedEndpointRecordsOnceAndMatchesCurrentBinding(t *testing.T) {
	t.Setenv("SUM_NOW", "2026-01-02T00:00:00Z")
	worker := store.Endpoint{Schema: 2, Backend: "hc", Machine: "m-lab", CoreID: "core-worker", Session: "lab", Socket: "/lab/worker.sock", Address: "t1", Task: "t-aaaaaaaaaaaa", Role: "worker", Attempt: "worker-1", BindingGeneration: 1, CreatedMS: 100}
	reviewer := worker
	reviewer.CoreID = "core-reviewer"
	reviewer.Role = "reviewer"
	reviewer.Attempt = "reviewer-1"
	task := ordjson.NewObject()
	task.Set("id", worker.Task)
	task.Set("worker_endpoint", worker.Object())
	task.Set("reviewer", reviewer.Object())
	if WorkerIsClosed(task) || ReviewerIsClosed(task) {
		t.Fatal("empty closure history is closed")
	}
	first := AppendEndpoint(task, worker, "report-submitted")
	if first == nil {
		t.Fatal("first closure ignored")
	}
	if !WorkerIsClosed(task) || ReviewerIsClosed(task) {
		t.Fatal("worker closure leaked to reviewer at same address on another core")
	}
	if !reflect.DeepEqual(store.EndpointFromContext(asObject(field(first, "endpoint"))), worker) {
		t.Fatal("closure lost exact native binding")
	}
	if field(first, "at") != store.Now() {
		t.Fatalf("closure time = %v", field(first, "at"))
	}
	before, _ := ordjson.MarshalCompact(task)
	if AppendEndpoint(task, worker, "other-reason") != nil {
		t.Fatal("duplicate closure rewrote history")
	}
	if after, _ := ordjson.MarshalCompact(task); string(before) != string(after) {
		t.Fatal("duplicate closure changed saved history")
	}
	for name, change := range map[string]func(*store.Endpoint){
		"core":       func(e *store.Endpoint) { e.CoreID = "new-core" },
		"address":    func(e *store.Endpoint) { e.Address = "t2" },
		"task":       func(e *store.Endpoint) { e.Task = "t-bbbbbbbbbbbb" },
		"role":       func(e *store.Endpoint) { e.Role = "reviewer" },
		"attempt":    func(e *store.Endpoint) { e.Attempt = "worker-2" },
		"generation": func(e *store.Endpoint) { e.BindingGeneration++ },
		"creation":   func(e *store.Endpoint) { e.CreatedMS++ },
	} {
		t.Run(name, func(t *testing.T) {
			successor := worker
			change(&successor)
			task.Set("worker_endpoint", successor.Object())
			if WorkerIsClosed(task) {
				t.Fatal("successor inherited another binding's closure")
			}
		})
	}
	relocated := worker
	relocated.Socket = "/lab/relocated.sock"
	task.Set("worker_endpoint", relocated.Object())
	if !WorkerIsClosed(task) {
		t.Fatal("socket route change changed permanent identity")
	}
	if AppendEndpoint(task, reviewer, "review-submitted") == nil || !ReviewerIsClosed(task) {
		t.Fatal("independent reviewer closure not recorded")
	}
	if n := len(asList(field(task, "closed_terminals"))); n != 2 {
		t.Fatalf("closure entries = %d", n)
	}
	historical := ordjson.NewObject()
	historical.Set("pane", "t1")
	task.Set("closed_panes", []any{historical})
	invalid := worker
	invalid.CreatedMS = 0
	if ClosedEndpoint(task, invalid) || AppendEndpoint(task, invalid, "unknown") != nil {
		t.Fatal("historical address or incomplete native binding granted closure authority")
	}
	if note := ResumeNote(worker.Task); note != "pane closed, resume via execution resume "+worker.Task {
		t.Fatalf("resume note = %q", note)
	}
}
