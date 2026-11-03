package verifycmd

import (
	"testing"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestVerificationWaiverRetainsLogicalActorWithoutClaim(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "verification conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := app.RequireClaim(s, session.Token())
	if err != nil {
		t.Fatal(err)
	}
	actor := endpoint(ctx)
	if !store.LogicalOwnersMatch(actor, session.Owner()) || len(actor.Keys()) != 4 {
		t.Fatalf("invalid logical waiver actor: %v", actor)
	}
}
