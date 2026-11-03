package project

import (
	"testing"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestEnrollmentRetainsLogicalActorWithoutClaim(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "project conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := app.RequireClaim(s, session.Token())
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ProjectIdentity("github.com", "example", "project")
	if err != nil {
		t.Fatal(err)
	}
	record := enrollmentRecord(identity, "existing", "/tmp/project", "origin", "/tmp/project", ctx, nil)
	raw, _ := record.Get("enrolled_by")
	actor, _ := raw.(*ordjson.Object)
	if !store.LogicalOwnersMatch(actor, session.Owner()) || len(actor.Keys()) != 4 {
		t.Fatalf("invalid logical enrollment actor: %v", actor)
	}
}
