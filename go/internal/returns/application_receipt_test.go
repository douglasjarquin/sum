package returns

import (
	"bytes"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestNativeApplicationReceiptPreservesProofEncoding(t *testing.T) {
	t.Setenv("SUM_NOW", "2026-11-02T18:00:00+00:00")
	task := ordjson.NewObject()
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: "machine", CoreID: "core", Session: "lab", Socket: "/tmp/lab.sock", Address: "t1", Task: "t-aaaaaaaaaaaa", Role: "worker", Attempt: "x-aaaaaaaaaaaa", BindingGeneration: 1, CreatedMS: 123, Cwd: "/tmp/work"}
	previous, err := ordjson.FromValue(applicationReceipt{Kind: "answer-applied", Obligation: "answer:q1", Revision: ObligationRevision(task, "answer:q1"), Endpoint: endpoint, At: store.Now()})
	if err != nil {
		t.Fatal(err)
	}
	want, err := ordjson.MarshalCompact(previous)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ordjson.MarshalCompact(ApplicationReceipt(task, "answer:q1", "answer-applied", endpoint.Object()))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("native receipt encoding changed:\ngot %s\nwant %s", got, want)
	}
}
