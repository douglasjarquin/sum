package evidence

import (
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"testing"
)

func TestAppendPreservesNativeEndpointAndSealedHistory(t *testing.T) {
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: "host", CoreID: "core", Address: "t1", Socket: "/tmp/hc.sock", Session: "lab", Task: "task", Role: "worker", Attempt: "attempt", BindingGeneration: 1, CreatedMS: 5}.Object()
	task := ordjson.NewObject()
	old := ordjson.NewObject()
	old.Set("endpoint", map[string]any{"pane": "old"})
	task.Set("evidence", []any{old})
	before, _ := ordjson.MarshalCompact(old)
	row, err := Append(task, "claim", "test", nil, nil, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := row.Get("endpoint")
	got := store.EndpointFromContext(raw.(*ordjson.Object))
	if !got.Native().SameAuthority(store.EndpointFromContext(endpoint).Native()) {
		t.Fatalf("endpoint lost identity: %#v", got)
	}
	after, _ := ordjson.MarshalCompact(old)
	if string(before) != string(after) {
		t.Fatal("sealed history changed")
	}
}

func TestAppendPreservesLogicalActorWithoutClaimToken(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	actor := session.Owner()
	actor.Set("claim", session.Token())
	task := ordjson.NewObject()
	row, err := Append(task, "verification", "coordinator", nil, nil, actor)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := row.Get("endpoint")
	got, _ := raw.(*ordjson.Object)
	if !store.LogicalOwnersMatch(got, session.Owner()) {
		t.Fatalf("logical actor lost: %v", raw)
	}
	if _, exists := got.Get("claim"); exists {
		t.Fatal("conversation claim leaked into historical evidence")
	}
}
