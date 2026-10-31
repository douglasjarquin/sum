package contextview

import (
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"path/filepath"
	"testing"
)

func TestExecutionViewPreservesNativeIdentityAndLabelsHistory(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "home"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("status", "running")
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: "host", CoreID: "core", Session: "lab", Socket: "/tmp/hc.sock", Address: "t1", CreatedMS: 7, Task: "t-aaaaaaaaaaaa", Role: "worker", Attempt: "x-worker", BindingGeneration: 4}
	task.Set("worker_endpoint", endpoint.Object())
	view, err := sectionExecution(s, task)
	if err != nil {
		t.Fatal(err)
	}
	endpoints := asObject(getField(view, "endpoints"))
	worker := asObject(getField(endpoints, "worker"))
	got := store.EndpointFromContext(worker)
	if !got.Native().SameAuthority(endpoint.Native()) {
		t.Fatalf("execution loses identity: %#v", got)
	}
	task.Delete("worker_endpoint")
	task.Set("machine", "oldhost")
	task.Set("session", "old")
	task.Set("pane", "old-pane")
	before, _ := ordjson.MarshalCompact(task)
	view, err = sectionExecution(s, task)
	if err != nil {
		t.Fatal(err)
	}
	endpoints = asObject(getField(view, "endpoints"))
	historical := asObject(getField(endpoints, "historical_worker"))
	if getField(historical, "authority") != "historical" || getField(historical, "pane") != "old-pane" {
		t.Fatalf("historical endpoint not labelled: %#v", historical)
	}
	after, _ := ordjson.MarshalCompact(task)
	if string(before) != string(after) {
		t.Fatal("view rewrote historical ledger")
	}
}
