package app

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestRequireCoordinatorFencesCachedGenerationBeforeObservation(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	f := hctest.New(t)
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", CoreID: f.CoreID, Socket: f.Socket, Session: "cached-generation", Address: "t1", CreatedMS: 1, Role: "coordinator", Attempt: "coordinator-attempt", BindingGeneration: 1}
	stateValue, err := ordjson.ReadFile(filepath.Join(s.Root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	state := stateValue.(*ordjson.Object)
	state.Set("generation", "g-000000000000000000000000")
	if err = ordjson.WriteFile(filepath.Join(s.Root, "state.json"), state); err != nil {
		t.Fatal(err)
	}
	if err = RequireCoordinator(s, endpoint.Object()); !errors.Is(err, store.ErrInactiveGeneration) {
		t.Fatalf("cached authority error = %v", err)
	}
	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("fenced helper contacted runtime: %v", calls)
	}
}
func TestNativeReceiptNilReturnsError(t *testing.T) {
	if receipt, err := NativeReceipt(nil); err == nil || receipt != nil {
		t.Fatalf("receipt=%v err=%v", receipt, err)
	}
}
