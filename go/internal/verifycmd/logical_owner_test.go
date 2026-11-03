package verifycmd

import (
	"testing"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestVerifierReservationKeepsLogicalCoordinatorOwner(t *testing.T) {
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
	worker, err := reservations.NewAttempt("worker", session.Owner(), "/tmp/sum-test-checkout", store.Now(), "released", nil)
	if err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", 2)
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("repository", "example/project")
	task.Set("owner", session.Owner())
	task.Set("execution", reservations.NewExecution(worker))
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := reserveVerifier(s, ctx, "t-aaaaaaaaaaaa", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "/tmp/sum-test-verifier"); err != nil {
		t.Fatal(err)
	}
	saved, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	execution, err := reservations.GetExecution(saved)
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := execution.Verifiers[0].Get("owner")
	if !store.LogicalOwnersMatch(owner.(*ordjson.Object), session.Owner()) {
		t.Fatalf("verifier owner = %v", owner)
	}
}
