package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func requireLogicalActor(t *testing.T, home string, actor any) {
	t.Helper()
	s, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CoordinatorSession()
	if err != nil || session == nil {
		t.Fatalf("coordinator session: %v", err)
	}
	want, err := ordjson.MarshalSortedCompact(session.Owner())
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(actor)
	if err != nil {
		t.Fatal(err)
	}
	var normalized bytes.Buffer
	if err := json.Compact(&normalized, want); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, normalized.Bytes()) {
		t.Fatalf("logical actor = %s; want %s", got, want)
	}
}
