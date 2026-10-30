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
