package ask

import (
	"bytes"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestLogicalAnswerReadReceiptRetainsOwnerWithoutClaim(t *testing.T) {
	s := testStore(t)
	session, err := s.ClaimCoordinator("", "answer conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := app.RequireClaim(s, session.Token())
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	task.Set("owner", session.Owner())
	task.Set("parent", nil)
	q := ordjson.NewObject()
	q.Set("id", "q-owner")
	q.Set("status", "open")
	q.Set("text", "Continue?")
	task.Set("questions", []any{q})
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err = AnswerExpected(s, "t-aaaaaaaaaaaa", "q-owner", "yes", "", "", ctx, func() (*ordjson.Object, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	saved, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	value, _ := findQuestion(saved, "q-owner").Get("read_receipt")
	receipt, _ := value.(*ordjson.Object)
	value, _ = receipt.Get("endpoint")
	owner, _ := value.(*ordjson.Object)
	if store.ValidateLogicalOwner(owner) != nil || !store.LogicalOwnersMatch(owner, session.Owner()) {
		t.Fatalf("read receipt lost logical owner: %#v", owner)
	}
	raw, _ := ordjson.MarshalCompact(receipt)
	if bytes.Contains(raw, []byte(session.Token())) || bytes.Contains(raw, []byte("generation")) {
		t.Fatal("read receipt persisted conversation authority")
	}
}
