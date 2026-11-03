package statuscmd

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestStatusShowsLogicalSessionRouteAndOutstandingWork(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "ordinary conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	route := ordjson.NewObject()
	route.Set("socket", "/tmp/sum-test-status.sock")
	route.Set("core_id", "saved-core")
	if err := ordjson.WriteFile(filepath.Join(s.Root, "runtime-route.json"), route); err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", 2)
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("status", "waiting")
	task.Set("owner", session.Owner())
	task.Set("parent", nil)
	question := ordjson.NewObject()
	question.Set("id", "q-one")
	question.Set("text", "Continue?")
	question.Set("status", "open")
	task.Set("questions", []any{question})
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	for _, compact := range []bool{false, true} {
		view, err := Status(s, Options{Compact: compact, Limit: 20, MaxChars: 200})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := view.Get("session")
		owner, _ := raw.(*ordjson.Object)
		if !store.LogicalOwnersMatch(owner, session.Owner()) {
			t.Fatalf("session missing: %v", view)
		}
		if generation, _ := view.Get("claim_generation"); fmt.Sprint(generation) != "1" {
			t.Fatalf("claim generation missing: %v", view)
		}
		raw, _ = view.Get("runtime_route")
		saved, _ := raw.(*ordjson.Object)
		if saved == nil {
			t.Fatal("saved route missing")
		}
		raw, _ = view.Get("outstanding")
		outstanding, _ := raw.(*ordjson.Object)
		if outstanding == nil {
			t.Fatal("outstanding work missing")
		}
		if total, _ := outstanding.Get("total"); fmt.Sprint(total) != "1" {
			t.Fatalf("outstanding total=%v", total)
		}
	}
}
