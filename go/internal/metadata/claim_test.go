package metadata

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestMetadataFinalWriteRefusesTransferredClaim(t *testing.T) {
	l := newMetadataLab(t)
	session, err := l.s.RetireNativeCoordinator("", "metadata conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.RequireClaim(l.s, session.Token()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(Path(l.s))
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := store.Open(l.s.Root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replacement.ClaimCoordinator("", "new conversation", true); err != nil {
		t.Fatal(err)
	}
	l.meta.Set("enabled", false)
	if err := writeMetadata(l.s, l.meta); !errors.Is(err, store.ErrStaleClaim) {
		t.Fatalf("metadata write survived transfer: %v", err)
	}
	after, err := os.ReadFile(Path(l.s))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("stale metadata write changed the ledger")
	}
}
