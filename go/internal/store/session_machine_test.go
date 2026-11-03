package store

import (
	"github.com/douglasjarquin/sum/go/internal/machine"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoordinatorClaimCannotResumeOrTransferOnAnotherMachine(t *testing.T) {
	restore := machine.Pin("11111111111111111111111111111111", "first")
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "first", false)
	if err != nil {
		t.Fatal(err)
	}
	restore()
	defer machine.Pin("22222222222222222222222222222222", "second")()
	path := filepath.Join(s.Home, "coordinator.json")
	s, err = Open(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, transfer := range []bool{false, true} {
		if _, err = s.ClaimCoordinator(session.Token(), "second", transfer); err == nil || !strings.Contains(err.Error(), "another machine") {
			t.Fatalf("transfer=%v: %v", transfer, err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("foreign claim changed ownership")
	}
}
