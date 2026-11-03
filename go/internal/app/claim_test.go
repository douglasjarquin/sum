package app

import (
	"errors"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestLogicalTaskActorRequiresCurrentClaimAndMatchingOwner(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "conversation", false)
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
	check := func() error {
		unlock, err := s.Lock()
		if err != nil {
			return err
		}
		defer unlock()
		return CheckTaskRole(s, task, actor, "coordinator")
	}
	if err = check(); err != nil {
		t.Fatal(err)
	}
	other := session.Owner()
	other.Set("session_id", "another-session")
	task.Set("owner", other)
	if err = check(); err == nil {
		t.Fatal("accepted another session's task")
	}
	task.Set("owner", session.Owner())
	next, err := s.ClaimCoordinator("", "replacement", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = check(); !errors.Is(err, store.ErrStaleClaim) {
		t.Fatalf("observed actor survived transfer: %v", err)
	}
	if _, err = RequireClaim(s, session.Token()); !errors.Is(err, store.ErrStaleClaim) {
		t.Fatalf("stale claim: %v", err)
	}
	if _, err = RequireClaim(s, next.Token()); err != nil {
		t.Fatal(err)
	}
}

func TestLogicalTaskActorNeedsNoNativeEndpoint(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ObserveTaskActor(s, session.Owner()); !errors.Is(err, store.ErrClaimRequired) {
		t.Fatalf("owner without captured claim: %v", err)
	}
	ctx, err := RequireClaim(s, session.Token())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.EndpointFromContext(ctx).Validate(); err == nil {
		t.Fatal("logical actor became native endpoint")
	}
	if _, err = ObserveTaskActor(s, ctx); err != nil {
		t.Fatal(err)
	}
}
