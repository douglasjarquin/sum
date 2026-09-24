package herdrclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/proc"
)

// countingHerdr logs every invocation's arguments to a file and answers `agent list` with two agents.
func countingHerdr(t *testing.T) (string, func() []string) {
	t.Helper()
	log := filepath.Join(t.TempDir(), "calls")
	herdr := fakeHerdr(t, `echo "$*" >> '`+log+`'
case "$*" in
  *"agent list"*) echo '{"result":{"agents":[{"pane_id":"w1:p1","cwd":"/work/a","agent_status":"idle"},{"pane_id":"w1:p2","agent_status":"working"}]}}';;
  *"agent get w1:p2"*) echo '{"result":{"agent":{"pane_id":"w1:p2","cwd":"/work/b","agent_status":"working"}}}';;
  *) echo '{"result":{}}';;
esac`)
	return herdr, func() []string {
		raw, err := os.ReadFile(log)
		if err != nil {
			return nil
		}
		return strings.Split(strings.TrimSpace(string(raw)), "\n")
	}
}

func TestSnapshotListsEachSessionOnce(t *testing.T) {
	herdr, calls := countingHerdr(t)
	sn := NewSnapshot(context.Background(), func() (string, error) { return herdr, nil }, 5*time.Second)
	first, err := sn.Agent("lab", "w1:p1")
	if err != nil || AgentCwd(first) != "/work/a" || AgentStatus(first) != "idle" {
		t.Fatalf("first agent = %v, %v", first, err)
	}
	if _, err := sn.Agent("lab", "w1:p1"); err != nil {
		t.Fatal(err)
	}
	if got := calls(); len(got) != 1 || !strings.Contains(got[0], "agent list") {
		t.Fatalf("calls = %v, want one agent list", got)
	}
	// Listed without a cwd: one agent get fills it in.
	second, err := sn.Agent("lab", "w1:p2")
	if err != nil || AgentCwd(second) != "/work/b" {
		t.Fatalf("second agent = %v, %v", second, err)
	}
	if _, err := sn.Agent("lab", "w1:p9"); err == nil || !ErrorIsAbsent(err) {
		t.Fatalf("absent agent err = %v, want agent_not_found", err)
	}
	if got := calls(); len(got) != 2 || sn.Calls() != 2 || sn.Sessions() != 1 {
		t.Fatalf("calls = %v (counted %d, sessions %d), want list + one get", got, sn.Calls(), sn.Sessions())
	}
}

func TestSnapshotKeepsAListFailureForTheOperation(t *testing.T) {
	log := filepath.Join(t.TempDir(), "calls")
	herdr := fakeHerdr(t, `echo x >> '`+log+`'; echo 'server down' >&2; exit 3`)
	sn := NewSnapshot(context.Background(), func() (string, error) { return herdr, nil }, 5*time.Second)
	for i := 0; i < 3; i++ {
		if _, err := sn.Agent("lab", "w1:p1"); err == nil || !strings.Contains(err.Error(), "agent list for session lab failed") {
			t.Fatalf("err = %v", err)
		}
	}
	raw, _ := os.ReadFile(log)
	if n := strings.Count(string(raw), "x"); n != 1 {
		t.Fatalf("herdr ran %d times, want 1", n)
	}
}

func TestCallContextHonorsTheCallersDeadline(t *testing.T) {
	herdr := fakeHerdr(t, `sleep 5`)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := CallContext(ctx, herdr, "lab", 5*time.Second, "agent", "prompt", "p1", "hi")
	if err == nil || !errors.Is(err, proc.ErrUncertain) {
		t.Fatalf("err = %v, want an uncertain stop at the caller's deadline", err)
	}
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("call ran %s past a 200ms caller deadline", elapsed)
	}
	if _, err := CallContext(ctx, herdr, "lab", 5*time.Second, "agent", "list"); err == nil || !errors.Is(err, proc.ErrNotStarted) {
		t.Fatalf("err after deadline = %v, want not started", err)
	}
}

func TestSnapshotTripIsSticky(t *testing.T) {
	sn := NewSnapshot(context.Background(), func() (string, error) { return "", errors.New("unused") }, time.Second)
	if _, ok := sn.Tripped("lab"); ok {
		t.Fatal("fresh snapshot is tripped")
	}
	sn.Trip("lab", "first")
	sn.Trip("lab", "second")
	if reason, ok := sn.Tripped("lab"); !ok || reason != "first" {
		t.Fatalf("tripped = %q %v", reason, ok)
	}
}
