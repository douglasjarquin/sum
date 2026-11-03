package store

import (
	"errors"
	"testing"
)

func TestCapturedClaimIsCheckedAfterAcquiringWriteLock(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.ClaimCoordinator("", "first", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CaptureClaim(first.Token()); err != nil {
		t.Fatal(err)
	}
	replacement, err := Open(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := replacement.ClaimCoordinator("", "replacement", true)
	if err != nil {
		t.Fatal(err)
	}
	if unlock, err := s.Lock(); !errors.Is(err, ErrStaleClaim) {
		if unlock != nil {
			unlock()
		}
		t.Fatalf("stale writer acquired the lock: %v", err)
	}
	if err := s.CaptureClaim(second.Token()); err != nil {
		t.Fatal(err)
	}
	unlock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}
