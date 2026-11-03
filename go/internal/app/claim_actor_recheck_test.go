package app

import (
	"errors"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestObservedLogicalActorRechecksGenerationOnFreshStore(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "first conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := RequireClaim(s, session.Token())
	if err != nil {
		t.Fatal(err)
	}
	actor, err := ObserveTaskActor(s, ctx)
	if err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("owner", session.Owner())
	if _, err = s.ClaimCoordinator("", "replacement", true); err != nil {
		t.Fatal(err)
	}
	// A fresh handle has no captured token, so its lock cannot mask the actor check.
	fresh, err := store.Open(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := fresh.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err = CheckTaskRole(fresh, task, actor, "coordinator"); !errors.Is(err, store.ErrStaleClaim) {
		t.Fatalf("observed actor survived transfer: %v", err)
	}
}
