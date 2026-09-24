package incarnation

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

const recordedAt = "2026-09-24T10:00:00+00:00"

func record(t *testing.T, e Evidence) any {
	t.Helper()
	// Round-trip through JSON like a record read back from disk.
	data, err := ordjson.MarshalCompact(e.Record(recordedAt))
	if err != nil {
		t.Fatal(err)
	}
	return obj(string(data))
}

func obj(text string) *ordjson.Object {
	value, err := ordjson.Decode([]byte(text))
	if err != nil {
		panic(err)
	}
	return value.(*ordjson.Object)
}

func osPID() int { return os.Getpid() }

func probe(s *Shell) ShellProbe {
	return func() (*Shell, error) { return s, nil }
}

func failingProbe() ShellProbe {
	return func() (*Shell, error) { return nil, errors.New("pane process-info timed out") }
}

func noProbe(t *testing.T) ShellProbe {
	return func() (*Shell, error) {
		t.Fatal("the shell probe must not run when the terminal already decides")
		return nil, nil
	}
}

var (
	claude    = &Session{Agent: "claude", Kind: "id", Value: "conv-A"}
	claudeB   = &Session{Agent: "claude", Kind: "id", Value: "conv-B"}
	shell     = &Shell{PID: 4242, Started: "2026-09-24T09:00:00Z"}
	newShell  = &Shell{PID: 5151, Started: "2026-09-24T11:00:00Z"}
	reusedPID = &Shell{PID: 4242, Started: "2026-09-24T11:00:00Z"}
)

func TestJudge(t *testing.T) {
	bound := Evidence{Terminal: "term_A", Session: claude, Agent: "claude", Shell: shell}
	cases := []struct {
		name     string
		recorded Evidence
		observed Evidence
		probe    ShellProbe
		outcome  string
		verified bool
	}{
		{"same terminal", bound, Evidence{Terminal: "term_A", Session: claude, Agent: "claude"}, nil, Same, true},
		{"same terminal, agent not reported now", bound, Evidence{Terminal: "term_A"}, nil, Same, true},
		{"same terminal, session appears", Evidence{Terminal: "term_A"}, Evidence{Terminal: "term_A", Session: claude}, nil, Same, true},
		{"new conversation in the same terminal", bound, Evidence{Terminal: "term_A", Session: claudeB, Agent: "claude"}, nil, NewConversation, true},
		{"native restore", bound, Evidence{Terminal: "term_B", Session: claude, Agent: "claude"}, nil, Restored, true},
		{"live handoff keeps the shell", bound, Evidence{Terminal: "term_B"}, probe(shell), Handoff, true},
		{"restart with a new shell", bound, Evidence{Terminal: "term_B"}, probe(newShell), Replaced, false},
		{"start time read a second apart is the same shell", bound, Evidence{Terminal: "term_B"}, probe(&Shell{PID: 4242, Started: "2026-09-24T09:00:01Z"}), Handoff, true},
		{"reused pid is not the same shell", bound, Evidence{Terminal: "term_B"}, probe(reusedPID), Replaced, false},
		{"session kept but no agent detected", bound, Evidence{Terminal: "term_B", Session: claude}, probe(newShell), Replaced, false},
		{"different conversation after restart", bound, Evidence{Terminal: "term_B", Session: claudeB, Agent: "claude"}, probe(newShell), Replaced, false},
		{"shell unobservable after terminal change", bound, Evidence{Terminal: "term_B"}, failingProbe(), Unobservable, false},
		{"no terminal reported now", bound, Evidence{}, nil, Unobservable, false},
		{"recorded without shell, shell predates record", Evidence{Terminal: "term_A"}, Evidence{Terminal: "term_B"}, probe(shell), Handoff, true},
		{"recorded without shell, shell started later", Evidence{Terminal: "term_A"}, Evidence{Terminal: "term_B"}, probe(newShell), Replaced, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.probe
			if p == nil {
				p = noProbe(t)
			}
			v := Judge(record(t, tc.recorded), recordedAt, tc.observed, p)
			if v.Outcome != tc.outcome || v.Verified != tc.verified {
				t.Fatalf("Judge = %s verified=%v (%s), want %s verified=%v", v.Outcome, v.Verified, v.Reason, tc.outcome, tc.verified)
			}
			if v.Reason == "" {
				t.Fatal("every verdict names its evidence")
			}
		})
	}
}

func TestJudgeLegacyRecordNeedsProof(t *testing.T) {
	at := func(offset time.Duration) *Shell {
		base, _ := time.Parse(time.RFC3339, recordedAt)
		return &Shell{PID: 7, Started: base.Add(offset).UTC().Format(time.RFC3339)}
	}
	cases := []struct {
		name       string
		recorded   any
		occupiedAt string
		probe      ShellProbe
		outcome    string
	}{
		{"shell predates the record", nil, recordedAt, probe(at(-time.Minute)), LegacyVerified},
		{"shell started after the record", nil, recordedAt, probe(at(3 * time.Second)), Replaced},
		{"a second after the record", nil, recordedAt, probe(at(time.Second)), Replaced},
		{"same second", nil, recordedAt, probe(at(0)), Unrecorded},
		{"a second before is too close to prove", nil, recordedAt, probe(at(-time.Second)), Unrecorded},
		{"shell cannot be observed", nil, recordedAt, failingProbe(), Unrecorded},
		{"no occupancy time", nil, "", probe(at(-time.Minute)), Unrecorded},
		{"malformed record is legacy, not verified", obj(`{"terminal": 7}`), recordedAt, failingProbe(), Unrecorded},
		{"null record", obj(`{"terminal": null}`), recordedAt, probe(at(10 * time.Second)), Replaced},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := Judge(tc.recorded, tc.occupiedAt, Evidence{Terminal: "term_X"}, tc.probe)
			if v.Outcome != tc.outcome {
				t.Fatalf("Judge = %s (%s), want %s", v.Outcome, v.Reason, tc.outcome)
			}
			if v.Verified != (tc.outcome == LegacyVerified) {
				t.Fatalf("verified = %v for %s", v.Verified, v.Outcome)
			}
		})
	}
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

func TestPaneClassifiesHerdrFailures(t *testing.T) {
	gone := func(time.Duration, ...string) (any, string, error) { return nil, "pane_not_found", nil }
	if v := Pane(gone, "w1:p1", nil, recordedAt); v.Outcome != Absent || v.Verified {
		t.Fatalf("pane_not_found: %+v", v)
	}
	stale := func(time.Duration, ...string) (any, string, error) {
		return nil, "", errors.New("herdr pane get exited 1: connect: no such file or directory")
	}
	if v := Pane(stale, "w1:p1", nil, recordedAt); v.Outcome != Unobservable || v.Verified || !strings.Contains(v.Reason, "no such file") {
		t.Fatalf("stale socket: %+v", v)
	}
}

func TestProbeShellReadsTheStartTime(t *testing.T) {
	restore := StartTime
	defer func() { StartTime = restore }()
	StartTime = func(pid int) (string, error) {
		if pid != 99 {
			return "", errors.New("wrong pid")
		}
		return "2026-09-24T09:00:00Z", nil
	}
	call := func(_ time.Duration, args ...string) (any, string, error) {
		return obj(`{"process_info": {"pane_id": "w1:p1", "shell_pid": 99}}`), "", nil
	}
	got, err := ProbeShell(call, "w1:p1")
	if err != nil || *got != (Shell{PID: 99, Started: "2026-09-24T09:00:00Z"}) {
		t.Fatalf("ProbeShell = %+v, %v", got, err)
	}
}

func TestParseStart(t *testing.T) {
	got, err := ParseStart("Thu Sep  4 10:13:37 2026\n")
	if err != nil || got != "2026-09-04T10:13:37Z" {
		t.Fatalf("ParseStart = %q, %v", got, err)
	}
	if _, err := ParseStart("not a time"); err == nil {
		t.Fatal("garbage must not parse")
	}
}

func TestProcessStartReadsThisProcess(t *testing.T) {
	started, err := processStart(osPID())
	if err != nil {
		t.Skipf("ps unavailable: %v", err)
	}
	parsed, err := time.Parse(time.RFC3339, started)
	if err != nil || parsed.After(time.Now().Add(time.Second)) {
		t.Fatalf("start %q, %v", started, err)
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
	restored := &Shell{PID: 9, Started: "2026-09-24T11:00:00Z"}
	if v := Judge(nil, at, Evidence{Terminal: "t"}, probe(restored)); v.Outcome != Replaced {
		t.Fatalf("a shell restored after the first registration is %s, not replaced (%s)", v.Outcome, v.Reason)
	}
	if _, _, ok := WorkerRecord(obj(`{"role": "worker", "task": "t-1"}`), "t-2"); ok {
		t.Fatal("another task's worker registration is not this task's record")
	}
}
