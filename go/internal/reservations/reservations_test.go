package reservations

import (
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestWorkerReservationAcceptsLogicalOwnerAndHistoricalOwners(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	legacy := ordjson.NewObject()
	legacy.Set("machine", "historical-host")
	legacy.Set("session", "historical-session")
	legacy.Set("pane", "historical-pane")
	native := (store.Endpoint{Schema: 2, Backend: "hc", Machine: "native-host", CoreID: "core", Socket: "/tmp/hc.sock", Session: "lab", Address: "t1", CreatedMS: 1, Role: "coordinator", Attempt: "attempt", BindingGeneration: 1}).Object()
	for name, owner := range map[string]*ordjson.Object{"logical": session.Owner(), "native": native, "historical": legacy} {
		t.Run(name, func(t *testing.T) {
			attempt, err := NewAttempt("worker", owner, nil, store.Now(), "running", nil)
			if err != nil {
				t.Fatal(err)
			}
			task := ordjson.NewObject()
			task.Set("execution", NewExecution(attempt))
			task.Set("parent", nil)
			if _, err = Worker(task); err != nil {
				t.Fatalf("owner rejected: %v", err)
			}
		})
	}
}
