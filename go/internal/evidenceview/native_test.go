package evidenceview

import (
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"testing"
)

func TestHistoricalEvidenceProjectionDoesNotRewriteSealedRecord(t *testing.T) {
	record := ordjson.NewObject()
	endpoint := ordjson.NewObject()
	endpoint.Set("machine", "old")
	endpoint.Set("session", "old")
	endpoint.Set("pane", "old-pane")
	record.Set("endpoint", endpoint)
	record.Set("candidate", "base")
	before, _ := ordjson.MarshalCompact(record)
	view := evidenceRowCopy(record, "base")
	if authority, _ := view.Get("endpoint_authority"); authority != "historical" {
		t.Fatal("old endpoint presented without historical label")
	}
	after, _ := ordjson.MarshalCompact(record)
	if string(before) != string(after) {
		t.Fatal("historical evidence rewritten")
	}
}

func TestLogicalCoordinatorEvidenceProjectionPreservesActor(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	record := ordjson.NewObject()
	record.Set("endpoint", session.Owner())
	before, _ := ordjson.MarshalCompact(record)
	view := evidenceRowCopy(record, "")
	if authority, _ := view.Get("endpoint_authority"); authority != "coordinator-session" {
		t.Fatalf("logical actor shown as %v", authority)
	}
	actor, _ := getField(view, "endpoint").(*ordjson.Object)
	if !store.LogicalOwnersMatch(actor, session.Owner()) {
		t.Fatal("logical actor was lost")
	}
	after, _ := ordjson.MarshalCompact(record)
	if string(before) != string(after) {
		t.Fatal("evidence projection rewrote original actor")
	}
}
