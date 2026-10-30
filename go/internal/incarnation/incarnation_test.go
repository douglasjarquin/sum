package incarnation

import (
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"testing"
)

func obj(text string) *ordjson.Object {
	value, err := ordjson.Decode([]byte(text))
	if err != nil {
		panic(err)
	}
	return value.(*ordjson.Object)
}
func TestFromInfoReadsHerdrShapes(t *testing.T) {
	pane := obj(`{"pane": {"pane_id": "w1:p1", "terminal_id": "term_65c3", "agent": "claude",
		"agent_session": {"agent": "claude", "kind": "id", "source": "herdr:claude", "value": "cf2a"}}}`)
	e := FromInfo(pane)
	if e.Terminal != "term_65c3" || e.Agent != "claude" || e.Session == nil || *e.Session != (Session{Agent: "claude", Kind: "id", Value: "cf2a"}) {
		t.Fatalf("FromInfo = %+v", e)
	}
	if got := FromInfo(obj(`{"agent": {"terminal_id": "t2", "agent_session": null}}`)); got.Terminal != "t2" || got.Session != nil {
		t.Fatalf("agent envelope: %+v", got)
	}
	if got := FromInfo(nil); got.Terminal != "" {
		t.Fatalf("nil: %+v", got)
	}
}

func TestOccupancyTimeIsTheEarliestStampNoLaterOccupantRewrote(t *testing.T) {
	owner := obj(`{"claimed_at": "2026-09-01T00:00:00+00:00", "at": "2026-08-31T23:59:59+00:00", "upgraded_at": "2026-09-20T00:00:00+00:00"}`)
	if _, at := CoordinatorRecord(owner); at != "2026-08-31T23:59:59+00:00" {
		t.Fatalf("occupiedAt = %q", at)
	}
	// An older release's init by a new occupant restamps updated_at; registered_at survives re-registration.
	registration := obj(`{"role": "worker", "task": "t-1", "registered_at": "2026-09-01T00:00:00+00:00", "updated_at": "2026-09-24T12:00:00+00:00"}`)
	_, at, ok := WorkerRecord(registration, "t-1")
	if !ok || at != "2026-09-01T00:00:00+00:00" {
		t.Fatalf("worker occupiedAt = %q ok=%v", at, ok)
	}
	if _, _, ok := WorkerRecord(obj(`{"role": "worker", "task": "t-1"}`), "t-2"); ok {
		t.Fatal("another task's worker registration is not this task's record")
	}
}
