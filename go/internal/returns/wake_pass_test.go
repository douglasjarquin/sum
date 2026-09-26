package returns

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// Coordinator wake episodes (#240a): one outstanding routine prompt per adopted coordinator, persisted before any
// prompt, with every later arrival coalesced behind it in its real attempt state.

// adopt marks the lab coordinator's owner record as having adopted the wake protocol, as init does.
func (l *passLab) adopt() {
	l.t.Helper()
	l.setOwner(func(owner *ordjson.Object) { owner.Set("wake_protocol", json.Number("1")) })
}

func (l *passLab) setOwner(change func(owner *ordjson.Object)) {
	l.t.Helper()
	owner, err := l.s.Owner()
	if err != nil || owner == nil {
		l.t.Fatalf("owner: %v %v", owner, err)
	}
	change(owner)
	if err := ordjson.WriteFile(filepath.Join(l.s.Home, "context.json"), owner); err != nil {
		l.t.Fatal(err)
	}
}

// reporting records a task whose worker submitted a report the lab coordinator has not verified: one routine return.
func (l *passLab) reporting() string {
	l.t.Helper()
	l.tasks++
	id := fmt.Sprintf("t-%012x", l.tasks)
	if err := os.MkdirAll(filepath.Join(l.s.Tasks, id), 0o700); err != nil {
		l.t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", json.Number("1"))
	task.Set("id", id)
	task.Set("status", "running")
	task.Set("machine", l.host)
	task.Set("session", "lab")
	task.Set("pane", fmt.Sprintf("w-reporter%d:p1", l.tasks))
	task.Set("worktree", filepath.Join(l.root, "wt", id))
	task.Set("parent", l.ctx)
	report := ordjson.NewObject()
	report.Set("kind", "report")
	report.Set("id", "r1")
	report.Set("at", "2026-09-24T00:00:00+00:00")
	task.Set("evidence", []any{report})
	if err := l.s.SaveTask(task); err != nil {
		l.t.Fatal(err)
	}
	return id
}

func (l *passLab) coordinatorIdle() {
	l.session("lab", map[string]any{"panes": map[string]any{"w-root:p1": l.pane("idle", l.root)}})
}

func (l *passLab) endpoint() [3]string { return [3]string{l.host, "lab", "w-root:p1"} }

func (l *passLab) wake() *Wake {
	l.t.Helper()
	w, status := ReadWake(l.s, l.endpoint())
	if status.State == WakeBlocked {
		l.t.Fatalf("wake sidecar blocked: %s", status.Diagnostic)
	}
	return w
}

func (l *passLab) wakePhase() string {
	l.t.Helper()
	w := l.wake()
	if w == nil || w.Episode == nil {
		return "absent"
	}
	return w.Episode.Phase
}

func row(t *testing.T, result *ordjson.Object, pane string) *ordjson.Object {
	t.Helper()
	rows, _ := result.Get("recipients")
	for _, r := range rows.([]any) {
		obj := r.(*ordjson.Object)
		rec, _ := obj.Get("recipient")
		if p, _ := rec.(*ordjson.Object).Get("pane"); fmt.Sprint(p) == pane {
			return obj
		}
	}
	t.Fatalf("no row for %s in %v", pane, result)
	return nil
}

func rowField(t *testing.T, result *ordjson.Object, pane, key string) string {
	t.Helper()
	v, _ := row(t, result, pane).Get(key)
	return fmt.Sprint(v)
}

// notificationStates maps each listed obligation of pane's row to its notification state.
func notificationStates(t *testing.T, result *ordjson.Object, pane string) map[string]string {
	t.Helper()
	out := map[string]string{}
	listing, _ := row(t, result, pane).Get("obligations")
	for _, item := range listing.([]any) {
		obj := item.(*ordjson.Object)
		task, _ := obj.Get("task")
		note, _ := obj.Get("notification")
		state, _ := note.(*ordjson.Object).Get("state")
		out[fmt.Sprint(task)] = fmt.Sprint(state)
	}
	return out
}

func openCount(t *testing.T, s *store.Store, ids []string) int {
	t.Helper()
	n := 0
	for _, id := range ids {
		task, err := s.ReadTask(id)
		if err != nil {
			t.Fatal(err)
		}
		obligations, err := OpenObligations(s, task)
		if err != nil {
			t.Fatal(err)
		}
		n += len(obligations)
	}
	return n
}

// AE1: twelve routine arrivals across two processes and three passes make exactly one prompt; the six that arrived
// behind it are coalesced in their real (pending) state and all twelve stay open and retrievable.
func TestWakeTwelveArrivalsMakeOnePromptAcrossProcessesAndPasses(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	var first []string
	for i := 0; i < 6; i++ {
		first = append(first, l.reporting())
	}
	l.session("lab", map[string]any{"hold": map[string]any{"w-root:p1": "prompt"}, "panes": map[string]any{"w-root:p1": l.pane("idle", l.root)}})
	h := l.startHelper("first", first, "parent")
	l.awaitHeld(h, "prompt", "w-root:p1")
	if got := l.wakePhase(); got != WakeIntent {
		t.Fatalf("while the prompt is held, sidecar phase = %s, want intent", got)
	}
	var later []string
	for i := 0; i < 6; i++ {
		later = append(later, l.reporting())
	}
	during, _ := l.pumpFor(later, "parent", 3*time.Second)
	if got := rowField(t, during, "w-root:p1", "state"); got != "deferred" {
		t.Fatalf("pass during the held prompt = %s, want deferred behind the recipient lock", got)
	}
	l.release("w-root:p1")
	if got := states(h.result(t))["w-root:p1"]; got != "submitted" {
		t.Fatalf("first process = %s, want submitted", got)
	}
	if got := l.wakePhase(); got != WakeSubmitted {
		t.Fatalf("after the prompt, sidecar phase = %s", got)
	}
	l.coordinatorIdle()
	all := append(append([]string{}, first...), later...)
	after, _ := l.pumpFor(all, "parent", 8*time.Second)
	r := row(t, after, "w-root:p1")
	if state, _ := r.Get("state"); state != "coalesced" {
		t.Fatalf("pass after the wake = %v, want coalesced", r)
	}
	if reason, _ := r.Get("reason"); !strings.Contains(fmt.Sprint(reason), l.wake().Episode.ID) || !strings.Contains(fmt.Sprint(reason), "sumctl wake show") {
		t.Fatalf("coalesced reason = %v, want the episode id and the inspection command", reason)
	}
	wake, _ := r.Get("wake")
	if phase, _ := wake.(*ordjson.Object).Get("phase"); phase != WakeSubmitted {
		t.Fatalf("row wake = %v", wake)
	}
	if prompts, _ := after.Get("prompts"); fmt.Sprint(prompts) != "0" {
		t.Fatalf("a coalesced row counted as a prompt: %v", prompts)
	}
	notes := notificationStates(t, after, "w-root:p1")
	for _, id := range first {
		if notes[id] != "submitted" {
			t.Fatalf("%s = %s, want submitted", id, notes[id])
		}
	}
	for _, id := range later {
		if notes[id] != "pending" {
			t.Fatalf("%s = %s, want pending (nothing stamped behind the wake)", id, notes[id])
		}
		if d := deliveries(t, l.s, id); len(d) != 0 {
			t.Fatalf("%s gained a delivery record while coalesced: %v", id, d)
		}
	}
	if n := openCount(t, l.s, all); n != 12 {
		t.Fatalf("open obligations = %d, want all twelve retrievable", n)
	}
	if n := len(prompts(l.calls(), "w-root:p1")); n != 1 {
		t.Fatalf("prompts = %d, want exactly one for the episode", n)
	}
}

// Crash after prepare (before the claim): nothing was stamped or sent; the next pass reconciles nothing by itself and
// prompts nothing until `sumctl wake reconcile`.
func TestWakeCrashAfterPrepareBlocksTheNextPassUntilReconciled(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.session("lab", map[string]any{"hold": map[string]any{"w-root:p1": "get"}, "panes": map[string]any{"w-root:p1": l.pane("idle", l.root)}})
	h := l.startHelper("a", []string{a}, "parent")
	l.awaitHeld(h, "get", "w-root:p1")
	h.kill(t)
	l.release("w-root:p1")
	if got := l.wakePhase(); got != WakePrepared {
		t.Fatalf("after the crash, sidecar phase = %s, want prepared", got)
	}
	if d := deliveries(t, l.s, a); len(d) != 0 {
		t.Fatalf("deliveries = %v, want nothing stamped before the claim", d)
	}
	l.coordinatorIdle()
	result, _ := l.pumpFor([]string{a}, "parent", 8*time.Second)
	r := row(t, result, "w-root:p1")
	state, _ := r.Get("state")
	reason, _ := r.Get("reason")
	if state != "deferred" || !strings.Contains(fmt.Sprint(reason), "sumctl wake reconcile") {
		t.Fatalf("row = %v / %v, want deferred naming the reconcile command", state, reason)
	}
	if n := countCalls(l.calls(), "prompt"); n != 0 {
		t.Fatalf("prompts = %d, want none over an unreconciled preparation", n)
	}
	if l.wakePhase() != WakePrepared || len(deliveries(t, l.s, a)) != 0 {
		t.Fatalf("the pass changed the preparation or stamped an attempt")
	}
}

// Crash with the prompt held (intent persisted): the attempt stays in-flight/uncertain, the episode stays outstanding,
// and nothing is resent.
func TestWakeCrashDuringThePromptStaysOutstandingAndIsNeverResent(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.session("lab", map[string]any{"hold": map[string]any{"w-root:p1": "prompt"}, "panes": map[string]any{"w-root:p1": l.pane("idle", l.root)}})
	h := l.startHelper("a", []string{a}, "parent")
	l.awaitHeld(h, "prompt", "w-root:p1")
	h.kill(t)
	l.release("w-root:p1")
	if got := l.wakePhase(); got != WakeIntent {
		t.Fatalf("after the crash, sidecar phase = %s, want intent", got)
	}
	b := l.reporting()
	l.coordinatorIdle()
	result, _ := l.pumpFor([]string{a, b}, "parent", 8*time.Second)
	if got := rowField(t, result, "w-root:p1", "state"); got != "coalesced" {
		t.Fatalf("next pass = %s, want coalesced behind the uncertain episode", got)
	}
	notes := notificationStates(t, result, "w-root:p1")
	if notes[a] != "uncertain" || notes[b] != "pending" {
		t.Fatalf("notification states = %v, want the interrupted attempt uncertain and the arrival pending", notes)
	}
	if n := len(prompts(l.calls(), "w-root:p1")); n != 1 || l.wakePhase() != WakeIntent {
		t.Fatalf("prompts = %d, phase = %s; want the one interrupted prompt and the intent kept", n, l.wakePhase())
	}
}

// A persisted claimed phase (crash between the in-flight stamp and the intent write) is outstanding too.
func TestWakeClaimedPhaseIsOutstanding(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	owner, _ := l.s.Owner()
	recorded, _ := incarnation.CoordinatorRecord(owner)
	w, err := NewWake(l.s, l.endpoint(), IncarnationJSON(recorded))
	if err != nil {
		t.Fatal(err)
	}
	w.Prepare("w-crashed", []WakeRef{{Task: a, ID: "report:r1"}})
	w.Episode.Phase = WakeClaimed
	if err := WriteWake(l.s, w); err != nil {
		t.Fatal(err)
	}
	l.coordinatorIdle()
	result, _ := l.pumpFor([]string{a}, "parent", 8*time.Second)
	if got := rowField(t, result, "w-root:p1", "state"); got != "coalesced" || countCalls(l.calls(), "prompt") != 0 {
		t.Fatalf("state = %s, prompts = %d; want coalesced and no prompt", got, countCalls(l.calls(), "prompt"))
	}
}

// A not-delivered outcome closes the episode: the next pass may prompt again, with the next generation.
func TestWakeNotDeliveredClosesTheEpisodeSoTheNextPassMayPrompt(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.session("lab", map[string]any{"panes": map[string]any{"w-root:p1": l.pane("working", l.root)}})
	busy, _ := l.pumpFor([]string{a}, "parent", 8*time.Second)
	if got := rowField(t, busy, "w-root:p1", "state"); got != "not-delivered" {
		t.Fatalf("busy coordinator = %s, want not-delivered", got)
	}
	w := l.wake()
	if w == nil || w.IsOutstanding() || w.NeedsReconcile() || w.Generation != 1 {
		t.Fatalf("after a busy recipient the episode must be closed: %+v", w)
	}
	l.coordinatorIdle()
	result, _ := l.pumpFor([]string{a}, "parent", 8*time.Second)
	if got := rowField(t, result, "w-root:p1", "state"); got != "submitted" || len(prompts(l.calls(), "w-root:p1")) != 1 {
		t.Fatalf("idle coordinator = %s with %d prompts, want submitted once", got, len(prompts(l.calls(), "w-root:p1")))
	}
	if w := l.wake(); w.Generation != 2 || w.Episode.Phase != WakeSubmitted || w.Episode.Delivery == "" || len(w.Episode.Claims) != 1 {
		t.Fatalf("second episode = %d %+v", w.Generation, w.Episode)
	}
}

// A directory sync failure after each sidecar rename does not abort the pass or double-send: the writer reloads the
// persisted phase and continues from it.
func TestWakeSyncFailuresAreReloadedDuringThePass(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.coordinatorIdle()
	old := wakeSyncDir
	wakeSyncDir = func(string) error { return errors.New("injected: directory sync failed") }
	t.Cleanup(func() { wakeSyncDir = old })
	result, _ := l.pumpFor([]string{a}, "parent", 8*time.Second)
	if got := rowField(t, result, "w-root:p1", "state"); got != "submitted" || len(prompts(l.calls(), "w-root:p1")) != 1 {
		t.Fatalf("state = %s, prompts = %d", got, len(prompts(l.calls(), "w-root:p1")))
	}
	wakeSyncDir = old
	if got := l.wakePhase(); got != WakeSubmitted {
		t.Fatalf("persisted phase = %s, want submitted", got)
	}
}

// An invalid sidecar of any kind blocks admission: zero prompt submissions, a deferred row with the diagnostic, and
// no attempt stamped.
func TestWakeInvalidSidecarBlocksAdmissionWithoutTouchingAttempts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		plant func(l *passLab)
	}{
		{"corrupt", func(l *passLab) { writeRaw(l.t, l.s.WakePath(l.endpoint()), "{broken") }},
		{"directory", func(l *passLab) {
			if err := os.MkdirAll(l.s.WakePath(l.endpoint()), 0o700); err != nil {
				l.t.Fatal(err)
			}
		}},
		{"symlink", func(l *passLab) {
			target := filepath.Join(l.root, "elsewhere.json")
			writeRaw(l.t, target, "{}")
			if err := os.MkdirAll(filepath.Dir(l.s.WakePath(l.endpoint())), 0o700); err != nil {
				l.t.Fatal(err)
			}
			if err := os.Symlink(target, l.s.WakePath(l.endpoint())); err != nil {
				l.t.Fatal(err)
			}
		}},
		{"unsupported", func(l *passLab) {
			w, _ := NewWake(l.s, l.endpoint(), nil)
			w.Schema = 2
			if err := WriteWake(l.s, w); err != nil {
				l.t.Fatal(err)
			}
		}},
		{"mismatched", func(l *passLab) {
			w, _ := NewWake(l.s, l.endpoint(), nil)
			w.Installation = "someone-else"
			if err := WriteWake(l.s, w); err != nil {
				l.t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newPassLab(t)
			l.adopt()
			a := l.reporting()
			l.coordinatorIdle()
			tc.plant(l)
			result, _ := l.pumpFor([]string{a}, "parent", 8*time.Second)
			r := row(t, result, "w-root:p1")
			state, _ := r.Get("state")
			reason, _ := r.Get("reason")
			if state != "deferred" || !strings.Contains(fmt.Sprint(reason), "sumctl wake show") {
				t.Fatalf("row = %v / %v, want deferred with the inspection command", state, reason)
			}
			if countCalls(l.calls(), "prompt") != 0 || len(deliveries(t, l.s, a)) != 0 {
				t.Fatalf("calls = %v, deliveries = %v; want nothing sent or stamped", l.calls(), deliveries(t, l.s, a))
			}
			if notes := notificationStates(t, result, "w-root:p1"); notes[a] != "pending" {
				t.Fatalf("attempt state = %v, want unchanged pending", notes)
			}
		})
	}
}

// Busy, replaced, or unobservable coordinators get no notice and leave no outstanding episode; a worker recipient in
// the same pass still progresses.
func TestWakeUnsafeRecipientsLeaveNoOutstandingEpisode(t *testing.T) {
	for _, tc := range []struct {
		name  string
		pane  func(l *passLab) map[string]any
		state string
	}{
		{"busy", func(l *passLab) map[string]any { return l.pane("working", l.root) }, "not-delivered"},
		{"replaced", func(l *passLab) map[string]any { return l.restarted(l.root) }, "refused"},
		{"missing", func(l *passLab) map[string]any { return nil }, "not-delivered"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			psFixture(t, `{"5151": "2030-01-01T00:00:00Z"}`)
			l := newPassLab(t)
			l.adopt()
			a := l.reporting()
			worker := l.worker("lab", "w1:p1")
			panes := map[string]any{"w1:p1": l.pane("idle", l.worktree(worker))}
			if p := tc.pane(l); p != nil {
				panes["w-root:p1"] = p
			}
			l.session("lab", map[string]any{"panes": panes})
			result, _ := l.pumpFor([]string{a, worker}, "", 8*time.Second)
			got := states(result)
			if got["w-root:p1"] != tc.state || got["w1:p1"] != "submitted" {
				t.Fatalf("states = %v, want the coordinator %s and the worker submitted", got, tc.state)
			}
			if n := len(prompts(l.calls(), "w-root:p1")); n != 0 {
				t.Fatalf("coordinator prompts = %d, want none", n)
			}
			w := l.wake()
			if w == nil || w.IsOutstanding() || w.NeedsReconcile() || w.Episode.Phase != WakeNotSubmitted || w.Episode.Reason == "" {
				t.Fatalf("episode = %+v, want closed as not-submitted with a reason", w.Episode)
			}
		})
	}
}

// One wake file serves every machine spelling of the coordinator; a legacy-spelled arrival is admitted behind the
// stable-spelled episode (here a question, so it takes the decision priority path under that same episode).
func TestWakeMachineAliasesShareOneEpisode(t *testing.T) {
	l := newPassLab(t)
	identity, err := machine.Local(l.s.Home)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Hostname == "" || identity.Hostname == l.host || !identity.Is(identity.Hostname) {
		t.Skip("this host has no legacy hostname alias")
	}
	l.adopt()
	stable := l.asking(l.host)
	l.coordinatorIdle()
	if r, _ := l.pumpFor([]string{stable}, "parent", 8*time.Second); rowField(t, r, "w-root:p1", "state") != "submitted" {
		t.Fatalf("stable-spelled pass = %v", r)
	}
	episode := l.wake().Episode.ID
	legacy := l.asking(identity.Hostname)
	result, _ := l.pumpFor([]string{legacy}, "parent", 8*time.Second)
	r := row(t, result, "w-root:p1")
	wake, _ := r.Get("wake")
	admission, _ := wake.(*ordjson.Object).Get("admission")
	if priority, _ := r.Get("priority"); admission != wakeDecisionCoalesced || priority != true {
		t.Fatalf("legacy-spelled pass = %v, want admitted coalesced behind the same episode with the decision on the priority path", r)
	}
	if w := l.wake(); w.Episode.ID != episode || w.Generation != 1 {
		t.Fatalf("the legacy-spelled arrival changed the episode: gen %d %+v", w.Generation, w.Episode)
	}
	entries, err := ListWakes(l.s)
	if err != nil || len(entries) != 1 {
		t.Fatalf("wake files = %v, want exactly one for both spellings", entries)
	}
}

// The coordinator's own inline listing follows the policy without consuming or creating an episode.
func TestWakeInlineListingNeitherConsumesNorCreatesAnEpisode(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.coordinatorIdle()
	before, _ := l.pump(0)
	if got := rowField(t, before, "w-root:p1", "via"); got != "inline" || l.wake() != nil {
		t.Fatalf("inline listing via %s created wake %+v", got, l.wake())
	}
	wake, _ := row(t, before, "w-root:p1").Get("wake")
	if admission, _ := wake.(*ordjson.Object).Get("admission"); admission != "permitted" {
		t.Fatalf("inline wake context = %v, want permitted with no episode", wake)
	}
	b := l.reporting()
	if r, _ := l.pumpFor([]string{b}, "parent", 8*time.Second); rowField(t, r, "w-root:p1", "state") != "submitted" {
		t.Fatalf("external pass = %v", r)
	}
	c := l.reporting()
	after, _ := l.pump(0)
	r := row(t, after, "w-root:p1")
	if via, _ := r.Get("via"); via != "inline" {
		t.Fatalf("inline row = %v", r)
	}
	wake, _ = r.Get("wake")
	if admission, _ := wake.(*ordjson.Object).Get("admission"); admission != "coalesced" {
		t.Fatalf("inline wake context = %v, want coalesced behind the outstanding episode", wake)
	}
	if l.wakePhase() != WakeSubmitted {
		t.Fatalf("inline listing changed the episode to %s", l.wakePhase())
	}
	if n := openCount(t, l.s, []string{a, b, c}); n != 3 {
		t.Fatalf("open = %d", n)
	}
}

// An owner record without wake_protocol (an older helper wrote it) keeps legacy uncoalesced prompting, disclosed on
// the row, and writes no sidecar.
func TestWakeNotAdoptedOwnerKeepsLegacyPromptingDisclosed(t *testing.T) {
	l := newPassLab(t)
	a := l.reporting()
	l.coordinatorIdle()
	first, _ := l.pumpFor([]string{a}, "parent", 8*time.Second)
	if got := rowField(t, first, "w-root:p1", "state"); got != "submitted" || rowField(t, first, "w-root:p1", "wake") != WakeLegacy {
		t.Fatalf("first pass = %v", row(t, first, "w-root:p1"))
	}
	b := l.reporting()
	second, _ := l.pumpFor([]string{b}, "parent", 8*time.Second)
	if got := rowField(t, second, "w-root:p1", "state"); got != "submitted" || rowField(t, second, "w-root:p1", "wake") != WakeLegacy {
		t.Fatalf("second pass = %v", row(t, second, "w-root:p1"))
	}
	if n := len(prompts(l.calls(), "w-root:p1")); n != 2 {
		t.Fatalf("prompts = %d, want the legacy one per pass", n)
	}
	if l.wake() != nil {
		t.Fatalf("a legacy coordinator gained a wake sidecar: %+v", l.wake())
	}
}

// Adoption is decided from the owner record read under the compatibility lock: a helper that admitted a prompt as
// adopted re-reads the record at its claim, and a change refuses the send.
func TestWakeAdoptionIsRereadUnderTheCompatibilityLock(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.session("lab", map[string]any{"hold": map[string]any{"w-root:p1": "get"}, "panes": map[string]any{"w-root:p1": l.pane("idle", l.root)}})
	h := l.startHelper("a", []string{a}, "parent")
	l.awaitHeld(h, "get", "w-root:p1")
	if got := l.wakePhase(); got != WakePrepared {
		t.Fatalf("admitted as adopted: phase = %s", got)
	}
	l.setOwner(func(owner *ordjson.Object) { owner.Delete("wake_protocol") })
	l.release("w-root:p1")
	result := h.result(t)
	r := row(t, result, "w-root:p1")
	state, _ := r.Get("state")
	reason, _ := r.Get("reason")
	if state != "refused" || !strings.Contains(fmt.Sprint(reason), "adoption") {
		t.Fatalf("row = %v / %v, want refused because adoption changed", state, reason)
	}
	if countCalls(l.calls(), "prompt") != 0 || len(deliveries(t, l.s, a)) != 0 {
		t.Fatalf("calls = %v, deliveries = %v; want nothing sent or stamped", l.calls(), deliveries(t, l.s, a))
	}
	if w := l.wake(); w == nil || w.Episode.Phase != WakeNotSubmitted {
		t.Fatalf("episode = %+v, want closed as not-submitted", w)
	}
}

// `sumctl notice --to parent` is the explicit single retry: it is not latched behind an outstanding episode but
// supersedes it with the next generation, disclosed on the row.
func TestWakeExplicitNoticeSupersedesTheOutstandingEpisode(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.coordinatorIdle()
	if r, _ := l.pumpFor([]string{a}, "parent", 8*time.Second); rowField(t, r, "w-root:p1", "state") != "submitted" {
		t.Fatalf("first pass = %v", r)
	}
	first := l.wake().Episode.ID
	result, err := Pump(l.s, PumpOpts{RuntimeRoot: l.root, SumctlPath: "sumctl", Tasks: []string{a}, Recipient: "parent", Force: true, Budget: 8 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if got := rowField(t, result, "w-root:p1", "state"); got != "submitted" || len(prompts(l.calls(), "w-root:p1")) != 2 {
		t.Fatalf("forced notice = %s with %d prompts, want a second submitted prompt", got, len(prompts(l.calls(), "w-root:p1")))
	}
	wake, _ := row(t, result, "w-root:p1").Get("wake")
	if superseded, _ := wake.(*ordjson.Object).Get("superseded"); superseded != first {
		t.Fatalf("row wake = %v, want superseded %s", wake, first)
	}
	w := l.wake()
	if w.Generation != 2 || w.Episode.ID == first || w.Episode.Phase != WakeSubmitted {
		t.Fatalf("episode after the forced notice = %d %+v", w.Generation, w.Episode)
	}
	// The superseded episode leaves a receipt carrying its delivery (no coverage, no fingerprint), so `wake show`
	// keeps accounting for its prompt instead of reporting it as a legacy uncoalesced one.
	firstDelivery := field(t, deliveries(t, l.s, a)[0], "id")
	if len(w.Receipts) != 1 || w.Receipts[0].Result != "superseded" || w.Receipts[0].Fingerprint != "" || w.Receipts[0].Generation != 1 || w.Receipts[0].Delivery != firstDelivery {
		t.Fatalf("receipts after the supersede = %+v, want one superseded receipt for delivery %s", w.Receipts, firstDelivery)
	}
	if uncoalesced, _ := l.showEntry().Get("uncoalesced_legacy_prompts"); len(uncoalesced.([]any)) != 0 {
		t.Fatalf("show lists the superseded prompt as uncoalesced: %v", uncoalesced)
	}
	if msg := prompts(l.calls(), "w-root:p1")[1]; !strings.Contains(msg, "Wake episode "+w.Episode.ID) || !strings.Contains(msg, "wake consume --boundary TOKEN") || !strings.Contains(msg, "wake show") {
		t.Fatalf("prompt = %q, want the episode id and the show/consume commands", msg)
	}
}

// A forced notice while the coordinator is busy claims nothing: the outstanding episode is kept byte for byte, its
// boundary still consumes, and no replacement episode is opened that a routine pass could prompt over.
func TestWakeForcedNoticeAgainstABusyCoordinatorKeepsTheOutstandingEpisode(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.coordinatorIdle()
	if r, _ := l.pumpFor([]string{a}, "parent", 8*time.Second); rowField(t, r, "w-root:p1", "state") != "submitted" {
		t.Fatalf("first pass = %v", r)
	}
	before := l.wakeBytes()
	token := l.boundary([]WakeCovered{report(a)}, nil)
	l.session("lab", map[string]any{"panes": map[string]any{"w-root:p1": l.pane("working", l.root)}})
	result, err := Pump(l.s, PumpOpts{RuntimeRoot: l.root, SumctlPath: "sumctl", Tasks: []string{a}, Recipient: "parent", Force: true, Budget: 8 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	r := row(t, result, "w-root:p1")
	if state, _ := r.Get("state"); state != "not-delivered" {
		t.Fatalf("forced notice to a busy coordinator = %v", r)
	}
	wake, _ := r.Get("wake")
	if superseded, has := wake.(*ordjson.Object).Get("superseded"); has {
		t.Fatalf("row claims a supersede that never happened: %v", superseded)
	}
	if l.wakeBytes() != before {
		t.Fatalf("the sidecar changed although nothing was claimed:\n%s\n%s", before, l.wakeBytes())
	}
	// The busy attempt is recorded as not-delivered, as for any busy recipient; the original stays submitted.
	if d := deliveries(t, l.s, a); len(d) != 2 || field(t, d[0], "state") != "submitted" || field(t, d[1], "state") != "not-delivered" {
		t.Fatalf("deliveries = %s, want the original submitted attempt and a not-delivered one", text(d))
	}
	b := l.reporting()
	l.coordinatorIdle()
	next, _ := l.pumpFor([]string{a, b}, "parent", 8*time.Second)
	if got := rowField(t, next, "w-root:p1", "state"); got != "coalesced" || len(prompts(l.calls(), "w-root:p1")) != 1 {
		t.Fatalf("routine pass after the failed force = %s with %d prompts, want coalesced behind the kept episode", got, len(prompts(l.calls(), "w-root:p1")))
	}
	view, err := l.consume(token)
	if err != nil || field(t, view, "result") != "consumed" {
		t.Fatalf("the kept episode's boundary = %v %v, want still consumable", view, err)
	}
}

// Coverage is bound to the occupant that consumed it: a new occupant of the coordinator pane inherits no consumption
// authority, so a routine pass prompts it for the items the old occupant covered and withholds nothing as covered.
func TestWakeCoverageDoesNotSurviveAnOccupantChange(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.coordinatorIdle()
	if r, _ := l.pumpFor([]string{a}, "parent", 8*time.Second); rowField(t, r, "w-root:p1", "state") != "submitted" {
		t.Fatalf("first pass = %v", r)
	}
	if view, err := l.consume(l.boundary([]WakeCovered{report(a)}, nil)); err != nil || field(t, view, "covered_count") != "1" {
		t.Fatalf("consume = %v %v", view, err)
	}
	l.setOwner(func(owner *ordjson.Object) { owner.Set("incarnation", boundTo("w-root:p1-reborn")) })
	l.session("lab", map[string]any{"panes": map[string]any{"w-root:p1": map[string]any{"agent_status": "idle", "cwd": l.root, "agent": "claude", "terminal_id": "term-w-root:p1-reborn"}}})
	b := l.reporting()
	next, _ := l.pumpFor([]string{a, b}, "parent", 8*time.Second)
	r := row(t, next, "w-root:p1")
	if state, _ := r.Get("state"); state != "submitted" {
		t.Fatalf("pass for the new occupant = %v", r)
	}
	if withheld, _ := r.Get("withheld"); strings.Contains(text(withheld), "covered") {
		t.Fatalf("the new occupant had work withheld as covered: %s", text(withheld))
	}
	// a's attempt is already submitted, so it is named but not re-stamped; b is the new claim. Nothing is covered.
	w := l.wake()
	if len(w.Covered) != 0 || w.Generation != 2 || len(w.Episode.Claims) != 1 || w.Episode.Claims[0].Task != b {
		t.Fatalf("sidecar for the new occupant = covered %+v gen %d claims %+v; want no inherited coverage and %s claimed", w.Covered, w.Generation, w.Episode.Claims, b)
	}
	if msg := prompts(l.calls(), "w-root:p1")[1]; !strings.Contains(msg, a) || !strings.Contains(msg, b) {
		t.Fatalf("prompt to the new occupant = %q, want both %s and %s named", msg, a, b)
	}
}
