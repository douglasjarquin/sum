package store

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func TestCoordinatorClaimCreateResumeConflictAndTransfer(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.ClaimCoordinator("", "first conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Generation != 1 || first.SessionID == "" || first.Token() == "" {
		t.Fatalf("first = %+v", first)
	}
	path := filepath.Join(s.Home, "coordinator.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := s.ClaimCoordinator(first.Token(), "changed label", false)
	if err != nil || resumed.Token() != first.Token() {
		t.Fatalf("resume = %+v, %v", resumed, err)
	}
	if _, err = s.ClaimCoordinator("", "other conversation", false); !errors.Is(err, ErrClaimConflict) {
		t.Fatalf("conflict = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("resume/conflict changed record: %v", err)
	}
	next, err := s.ClaimCoordinator("", "new conversation", true)
	if err != nil {
		t.Fatal(err)
	}
	if next.Generation != 2 || next.SessionID != first.SessionID {
		t.Fatalf("transfer = %+v", next)
	}
	if _, err = s.CheckClaim(first.Token()); !errors.Is(err, ErrStaleClaim) {
		t.Fatalf("old claim = %v", err)
	}
	if _, err = s.CheckClaim(next.Token()); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorClaimIndependentProcesses(t *testing.T) {
	if home := os.Getenv("SUM_TEST_CLAIM_HOME"); home != "" {
		s, err := Open(home)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.ClaimCoordinator("", "racing conversation", false)
		if errors.Is(err, ErrClaimConflict) {
			os.Exit(3)
		}
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	home := t.TempDir()
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wg.Go(func() {
			cmd := exec.Command(os.Args[0], "-test.run=^TestCoordinatorClaimIndependentProcesses$")
			cmd.Env = append(os.Environ(), "SUM_TEST_CLAIM_HOME="+home)
			err := cmd.Run()
			if err == nil {
				codes <- 0
				return
			}
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				codes <- exit.ExitCode()
				return
			}
			codes <- -1
		})
	}
	wg.Wait()
	close(codes)
	var successes, conflicts int
	for code := range codes {
		if code == 0 {
			successes++
		}
		if code == 3 {
			conflicts++
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	s, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	record, err := s.CoordinatorSession()
	if err != nil || record.Generation != 1 {
		t.Fatalf("record=%+v err=%v", record, err)
	}
}
