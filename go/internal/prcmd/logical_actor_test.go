package prcmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestPRObservationRetainsLogicalActorWithoutClaim(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "PR conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := app.RequireClaim(s, session.Token())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ordjson.Decode([]byte(observationTask(strings.Repeat("a", 40), "")))
	if err != nil {
		t.Fatal(err)
	}
	task := raw.(*ordjson.Object)
	task.Set("owner", session.Owner())
	task.Set("parent", nil)
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal([]byte(ghView("OPEN", strings.Repeat("a", 40), "")), &fields); err != nil {
		t.Fatal(err)
	}
	pr, _, err := recordObservation(s, ctx, "t-aaaaaaaaaaaa", fields)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = pr.Get("observed_by")
	actor, _ := raw.(*ordjson.Object)
	if !store.LogicalOwnersMatch(actor, session.Owner()) || len(actor.Keys()) != 4 {
		t.Fatalf("invalid logical observation actor: %v", actor)
	}
}
