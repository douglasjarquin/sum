package returns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// Exact wake consumption and conservative reconciliation (#240a, U6): the coordinator records that it rendered one
// exact snapshot of its outstanding work; nothing else changes, and everything outside that snapshot stays eligible.

func (l *passLab) wakeBytes() string {
	l.t.Helper()
	raw, err := os.ReadFile(l.s.WakePath(l.endpoint()))
	if err != nil {
		return ""
	}
	return string(raw)
}

// boundary builds the token for the current outstanding episode from the lab's own choice of coverage.
func (l *passLab) boundary(included, omitted []WakeCovered) string {
	l.t.Helper()
	w := l.wake()
	if w == nil {
		l.t.Fatal("no wake sidecar to bound")
	}
	b, err := BuildBoundary(l.s, w, included, omitted)
	if err != nil {
		l.t.Fatal(err)
	}
	token, err := b.Token()
	if err != nil {
		l.t.Fatal(err)
	}
	return token
}

func report(task string) WakeCovered { return WakeCovered{Task: task, ID: "report:r1", Revision: "r1"} }

func (l *passLab) consume(token string) (*ordjson.Object, error) {
	l.t.Helper()
	return Consume(l.s, l.ctx, token)
}

func field(t *testing.T, view *ordjson.Object, key string) string {
	t.Helper()
	v, _ := view.Get(key)
	return fmt.Sprint(v)
}

// showEntry is the `wake show` row for the lab coordinator.
func (l *passLab) showEntry() *ordjson.Object {
	l.t.Helper()
	view, err := Show(l.s, "")
	if err != nil {
		l.t.Fatal(err)
	}
	rows, _ := view.Get("recipients")
	for _, r := range rows.([]any) {
		obj := r.(*ordjson.Object)
		rec, _ := obj.Get("recipient")
		if pane, _ := rec.(*ordjson.Object).Get("pane"); pane == "w-root:p1" {
			return obj
		}
	}
	l.t.Fatalf("no wake row for the lab coordinator: %v", view)
	return nil
}

// stampInFlight records an in-flight attempt with deliveryID for task's open obligations, as a claim does right
// before it records itself.
func (l *passLab) stampInFlight(task, deliveryID string) {
	l.t.Helper()
	record, err := l.s.ReadTask(task)
	if err != nil {
		l.t.Fatal(err)
	}
	obligations, err := OpenObligations(l.s, record)
	if err != nil {
		l.t.Fatal(err)
	}
	var items [][2]*ordjson.Object
	for _, o := range obligations {
		items = append(items, [2]*ordjson.Object{record, o})
	}
	delivery := ordjson.NewObject()
	delivery.Set("id", deliveryID)
	delivery.Set("at", store.Now())
	delivery.Set("state", "in-flight")
	unlock, err := l.s.Lock()
	if err != nil {
		l.t.Fatal(err)
	}
	defer unlock()
	if err := stampDeliveryLocked(l.s, items, delivery, nil); err != nil {
		l.t.Fatal(err)
	}
}

func (l *passLab) submittedFor(tasks ...string) {
	l.t.Helper()
	l.coordinatorIdle()
	r, _ := l.pumpFor(tasks, "parent", 8*time.Second)
	if got := rowField(l.t, r, "w-root:p1", "state"); got != "submitted" {
		l.t.Fatalf("pass = %s, want submitted: %v", got, row(l.t, r, "w-root:p1"))
	}
}

// AE2: the episode mentions A; the snapshot includes A+B and omits C. Consuming covers A+B only, leaves B's attempt
// unchanged, and leaves C eligible: the next pass opens a new episode for C alone.
func TestConsumeCoversExactlyTheIncludedSnapshot(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.submittedFor(a)
	b, c := l.reporting(), l.reporting()
	if r, _ := l.pumpFor([]string{a, b, c}, "parent", 8*time.Second); rowField(t, r, "w-root:p1", "state") != "coalesced" {
		t.Fatalf("arrivals behind the episode = %v", row(t, r, "w-root:p1"))
	}
	first := l.wake().Episode.ID
	view, err := l.consume(l.boundary([]WakeCovered{report(a), report(b)}, []WakeCovered{report(c)}))
	if err != nil {
		t.Fatal(err)
	}
	if field(t, view, "result") != "consumed" || field(t, view, "covered_count") != "2" || field(t, view, "episode") != first {
		t.Fatalf("consume = %v", view)
	}
	w := l.wake()
	if w.Episode.Phase != WakeConsumed || w.Episode.ConsumedAt == "" || len(w.Receipts) != 1 || w.Receipts[0].Result != "consumed" || w.Receipts[0].Generation != 1 {
		t.Fatalf("sidecar after consume = %+v receipts %+v", w.Episode, w.Receipts)
	}
	for _, id := range []string{b, c} {
		if d := deliveries(t, l.s, id); len(d) != 0 {
			t.Fatalf("%s gained an attempt through consumption: %v", id, d)
		}
	}
	if n := openCount(t, l.s, []string{a, b, c}); n != 3 {
		t.Fatalf("consumption closed an obligation: open = %d", n)
	}
	l.coordinatorIdle()
	next, _ := l.pumpFor([]string{a, b, c}, "parent", 8*time.Second)
	if got := rowField(t, next, "w-root:p1", "state"); got != "submitted" {
		t.Fatalf("pass after consumption = %v", row(t, next, "w-root:p1"))
	}
	w = l.wake()
	if w.Generation != 2 || w.Episode.ID == first || len(w.Episode.Claims) != 1 || w.Episode.Claims[0].Task != c {
		t.Fatalf("second episode = gen %d %+v, want a new episode claiming only %s", w.Generation, w.Episode, c)
	}
	if d := deliveries(t, l.s, b); len(d) != 0 {
		t.Fatalf("the covered %s was stamped by the second episode: %v", b, d)
	}
	if d := deliveries(t, l.s, c); len(d) != 1 || field(t, d[0], "state") != "submitted" {
		t.Fatalf("%s after the second episode = %v, want one submitted attempt", c, d)
	}
	withheld, _ := row(t, next, "w-root:p1").Get("withheld")
	if !strings.Contains(text(withheld), b) || !strings.Contains(text(withheld), "covered") {
		t.Fatalf("withheld = %s, want %s named as covered", text(withheld), b)
	}
	sent := prompts(l.calls(), "w-root:p1")
	if len(sent) != 2 {
		t.Fatalf("prompts = %d", len(sent))
	}
	if strings.Contains(sent[1], b) || !strings.Contains(sent[1], c) {
		t.Fatalf("second prompt = %q, want %s omitted as covered and %s named", sent[1], b, c)
	}
}

// Arrivals on every side of the snapshot: before it (included by the reader), between show and consume (not
// covered, still eligible), and after the receipt (a new episode). A receipt for generation 1 cannot consume
// generation 2.
func TestConsumeArrivalsAroundTheSnapshotAreNeverLostOrMisattributed(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.session("lab", map[string]any{"hold": map[string]any{"w-root:p1": "prompt"}, "panes": map[string]any{"w-root:p1": l.pane("idle", l.root)}})
	h := l.startHelper("first", []string{a}, "parent")
	l.awaitHeld(h, "prompt", "w-root:p1")
	before := l.reporting() // arrives while the prompt is in flight, before any snapshot
	if r, _ := l.pumpFor([]string{before}, "parent", 3*time.Second); rowField(t, r, "w-root:p1", "state") != "deferred" {
		t.Fatalf("pass during the held prompt = %v", row(t, r, "w-root:p1"))
	}
	l.release("w-root:p1")
	h.result(t)
	l.coordinatorIdle()

	entry := l.showEntry()
	tokenValue, _ := entry.Get("boundary")
	token := fmt.Sprint(tokenValue)
	included, _ := entry.Get("included")
	if !strings.Contains(text(included), a) || !strings.Contains(text(included), before) {
		t.Fatalf("show included = %s, want both %s and %s", text(included), a, before)
	}
	between := l.reporting() // arrives after the snapshot was rendered and before it is consumed
	view, err := l.consume(token)
	if err != nil {
		t.Fatal(err)
	}
	if field(t, view, "result") != "consumed" || field(t, view, "covered_count") != "2" {
		t.Fatalf("consume = %v", view)
	}
	after := l.reporting() // arrives after the receipt
	l.coordinatorIdle()
	next, _ := l.pumpFor([]string{a, before, between, after}, "parent", 8*time.Second)
	if got := rowField(t, next, "w-root:p1", "state"); got != "submitted" {
		t.Fatalf("pass after consumption = %v", row(t, next, "w-root:p1"))
	}
	w := l.wake()
	claims := map[string]bool{}
	for _, c := range w.Episode.Claims {
		claims[c.Task] = true
	}
	if w.Generation != 2 || len(claims) != 2 || !claims[between] || !claims[after] {
		t.Fatalf("second episode claims = %+v, want exactly the two uncovered arrivals", w.Episode.Claims)
	}
	// The generation-1 receipt is an exact repeat, and a fresh generation-1 boundary over the current work is expired.
	repeat, err := l.consume(token)
	if err != nil || field(t, repeat, "result") != "repeated" {
		t.Fatalf("repeat = %v %v", repeat, err)
	}
	stale, err := ParseBoundary(token)
	if err != nil {
		t.Fatal(err)
	}
	stale.Included = append(stale.Included, report(between))
	staleToken, _ := stale.Token()
	bytesBefore := l.wakeBytes()
	expired, err := l.consume(staleToken)
	if err != nil || field(t, expired, "result") != "expired" {
		t.Fatalf("older-generation consume = %v %v", expired, err)
	}
	if l.wakeBytes() != bytesBefore {
		t.Fatal("an expired receipt wrote the sidecar")
	}
	if w := l.wake(); w.Episode.Phase != WakeSubmitted || w.Generation != 2 {
		t.Fatalf("an expired receipt changed the current episode: %+v", w.Episode)
	}
}

func TestConsumeRefusals(t *testing.T) {
	setup := func(t *testing.T) (*passLab, string, string) {
		l := newPassLab(t)
		l.adopt()
		a := l.reporting()
		l.submittedFor(a)
		return l, a, l.boundary([]WakeCovered{report(a)}, nil)
	}
	t.Run("worker pane", func(t *testing.T) {
		l, _, token := setup(t)
		worker := l.worker("lab", "w1:p1")
		ctx := ordjson.NewObject()
		ctx.Set("machine", l.host)
		ctx.Set("session", "lab")
		ctx.Set("pane", "w1:p1")
		ctx.Set("cwd", l.worktree(worker))
		before := l.wakeBytes()
		if _, err := Consume(l.s, ctx, token); err == nil || !strings.Contains(err.Error(), "not the registered coordinator") {
			t.Fatalf("worker consume = %v", err)
		}
		if l.wakeBytes() != before {
			t.Fatal("a refused consume wrote the sidecar")
		}
	})
	t.Run("foreign recipient", func(t *testing.T) {
		l, _, token := setup(t)
		b, _ := ParseBoundary(token)
		b.Recipient.Pane = "w-other:p1"
		foreign, _ := b.Token()
		if _, err := l.consume(foreign); err == nil || !strings.Contains(err.Error(), "another recipient") {
			t.Fatalf("foreign consume = %v", err)
		}
	})
	t.Run("replaced occupant", func(t *testing.T) {
		l, _, token := setup(t)
		l.setOwner(func(owner *ordjson.Object) { owner.Set("incarnation", boundTo("w-root:p1-reborn")) })
		l.session("lab", map[string]any{"panes": map[string]any{"w-root:p1": map[string]any{"agent_status": "idle", "cwd": l.root, "agent": "claude", "terminal_id": "term-w-root:p1-reborn"}}})
		before := l.wakeBytes()
		_, err := l.consume(token)
		if err == nil || !strings.Contains(err.Error(), "new occupant does not inherit") {
			t.Fatalf("replaced-occupant consume = %v", err)
		}
		if l.wakeBytes() != before {
			t.Fatal("a refused consume wrote the sidecar")
		}
	})
	t.Run("unverifiable token", func(t *testing.T) {
		l, _, token := setup(t)
		for name, bad := range map[string]string{
			"garbage":  "not-a-token",
			"tampered": strings.Replace(token, token[:8], "AAAAAAAA", 1),
			"empty":    "",
		} {
			if _, err := l.consume(bad); err == nil || !strings.Contains(err.Error(), "boundary") {
				t.Fatalf("%s = %v", name, err)
			}
		}
	})
	t.Run("altered coverage under a consumed identity", func(t *testing.T) {
		l, a, token := setup(t)
		if _, err := l.consume(token); err != nil {
			t.Fatal(err)
		}
		b := l.reporting()
		altered := l.boundary([]WakeCovered{report(a), report(b)}, nil)
		before := l.wakeBytes()
		if _, err := l.consume(altered); err == nil || !strings.Contains(err.Error(), "altered coverage") {
			t.Fatalf("altered consume = %v", err)
		}
		if l.wakeBytes() != before {
			t.Fatal("a refused consume wrote the sidecar")
		}
	})
	t.Run("prepared episode", func(t *testing.T) {
		l := newPassLab(t)
		l.adopt()
		a := l.reporting()
		owner, _ := l.s.Owner()
		w, _ := NewWake(l.s, l.endpoint(), IncarnationJSON(func() any { v, _ := owner.Get("incarnation"); return v }()))
		w.Prepare("w-prepared", []WakeRef{{Task: a, ID: "report:r1"}})
		if err := WriteWake(l.s, w); err != nil {
			t.Fatal(err)
		}
		l.coordinatorIdle()
		if _, err := l.consume(l.boundary([]WakeCovered{report(a)}, nil)); err == nil || !strings.Contains(err.Error(), "sumctl wake reconcile") {
			t.Fatalf("prepared consume = %v", err)
		}
	})
	t.Run("unknown identities are rejected, not covered", func(t *testing.T) {
		l, a, _ := setup(t)
		token := l.boundary([]WakeCovered{report(a), {Task: "t-000000000000", ID: "report:r1"}, {Task: a, ID: "question:q9"}}, nil)
		view, err := l.consume(token)
		if err != nil {
			t.Fatal(err)
		}
		rejected, _ := view.Get("rejected")
		if field(t, view, "covered_count") != "1" || len(rejected.([]any)) != 2 {
			t.Fatalf("consume = %v", view)
		}
	})
}

// An exact repeat is idempotent after a later episode started and after coverage pruning; no write happens.
func TestConsumeExactRepeatIsIdempotentAcrossEpisodesAndPruning(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.submittedFor(a)
	token := l.boundary([]WakeCovered{report(a)}, nil)
	if view, err := l.consume(token); err != nil || field(t, view, "result") != "consumed" {
		t.Fatalf("first = %v %v", view, err)
	}
	afterFirst := l.wakeBytes()
	if view, err := l.consume(token); err != nil || field(t, view, "result") != "repeated" || l.wakeBytes() != afterFirst {
		t.Fatalf("immediate repeat = %v %v (changed=%v)", view, err, l.wakeBytes() != afterFirst)
	}
	b := l.reporting()
	l.submittedFor(b)
	if w := l.wake(); w.Generation != 2 {
		t.Fatalf("generation = %d", w.Generation)
	}
	afterSecond := l.wakeBytes()
	if view, err := l.consume(token); err != nil || field(t, view, "result") != "repeated" || l.wakeBytes() != afterSecond {
		t.Fatalf("repeat after a later episode = %v %v", view, err)
	}
	// Close A canonically; the next pass prunes its coverage. The old receipt still repeats.
	l.mutate(a, func(task *ordjson.Object) { task.Set("status", "archived") })
	c := l.reporting()
	l.coordinatorIdle()
	l.pumpFor([]string{b, c}, "parent", 8*time.Second)
	if w := l.wake(); len(w.Covered) != 0 {
		t.Fatalf("coverage after archiving %s = %+v, want pruned", a, w.Covered)
	}
	afterPrune := l.wakeBytes()
	if view, err := l.consume(token); err != nil || field(t, view, "result") != "repeated" || l.wakeBytes() != afterPrune {
		t.Fatalf("repeat after pruning = %v %v", view, err)
	}
}

// Partial coverage closes only the named episode: the uncovered arrival is prompted by the next pass, the covered
// one is not repeated, and nothing is presented as all clear.
func TestConsumePartialCoverageNeverClearsTheWholeLatch(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.submittedFor(a)
	b, c := l.reporting(), l.reporting()
	view, err := l.consume(l.boundary([]WakeCovered{report(b)}, []WakeCovered{report(a), report(c)}))
	if err != nil || field(t, view, "covered_count") != "1" {
		t.Fatalf("consume = %v %v", view, err)
	}
	if note := field(t, view, "note"); !strings.Contains(note, "presentation attestation") {
		t.Fatalf("note = %s", note)
	}
	l.coordinatorIdle()
	next, _ := l.pumpFor([]string{a, b, c}, "parent", 8*time.Second)
	if got := rowField(t, next, "w-root:p1", "state"); got != "submitted" {
		t.Fatalf("next pass = %v", row(t, next, "w-root:p1"))
	}
	if w := l.wake(); len(w.Episode.Claims) != 1 || w.Episode.Claims[0].Task != c {
		t.Fatalf("claims = %+v, want only %s", w.Episode.Claims, c)
	}
	if n := openCount(t, l.s, []string{a, b, c}); n != 3 {
		t.Fatalf("open = %d", n)
	}
}

// A directory sync failure after the receipt's rename: the consume reloads the persisted record, reports consumed,
// and an identical replay is repeated.
func TestConsumeSyncFailureAfterReceiptReplaysSafely(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.submittedFor(a)
	token := l.boundary([]WakeCovered{report(a)}, nil)
	old := wakeSyncDir
	wakeSyncDir = func(string) error { return errors.New("injected: directory sync failed") }
	t.Cleanup(func() { wakeSyncDir = old })
	view, err := l.consume(token)
	if err != nil || field(t, view, "result") != "consumed" {
		t.Fatalf("consume under sync failure = %v %v", view, err)
	}
	wakeSyncDir = old
	if w := l.wake(); w.Episode.Phase != WakeConsumed || len(w.Receipts) != 1 {
		t.Fatalf("persisted = %+v", w.Episode)
	}
	if view, err := l.consume(token); err != nil || field(t, view, "result") != "repeated" {
		t.Fatalf("replay = %v %v", view, err)
	}
}

func TestReconcileMatrix(t *testing.T) {
	plant := func(l *passLab, phase string, claims []WakeRef) {
		l.t.Helper()
		owner, _ := l.s.Owner()
		w, err := NewWake(l.s, l.endpoint(), IncarnationJSON(func() any { v, _ := owner.Get("incarnation"); return v }()))
		if err != nil {
			l.t.Fatal(err)
		}
		w.Prepare("w-planted", claims)
		w.Episode.Phase = phase
		w.Episode.Delivery = "d-planted"
		if err := WriteWake(l.s, w); err != nil {
			l.t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		before, after, action string
	}{
		{WakePrepared, WakeNotSubmitted, "closed"},
		{WakeClaimed, WakeUncertain, "held"},
		{WakeIntent, WakeUncertain, "held"},
		{WakeSubmitted, WakeSubmitted, "none"},
		{WakeUncertain, WakeUncertain, "none"},
		{WakeConsumed, WakeConsumed, "none"},
		{WakeNotDelivered, WakeNotDelivered, "none"},
	} {
		t.Run(tc.before, func(t *testing.T) {
			l := newPassLab(t)
			l.adopt()
			a := l.reporting()
			plant(l, tc.before, []WakeRef{{Task: a, ID: "report:r1"}})
			l.coordinatorIdle()
			view, err := Reconcile(l.s, l.ctx, "")
			if err != nil {
				t.Fatal(err)
			}
			if field(t, view, "before") != tc.before || field(t, view, "after") != tc.after || field(t, view, "action") != tc.action {
				t.Fatalf("reconcile %s = %v", tc.before, view)
			}
			if l.wakePhase() != tc.after {
				t.Fatalf("persisted phase = %s", l.wakePhase())
			}
			if tc.action != "none" && field(t, view, "delivery") != "d-planted" {
				t.Fatalf("view names no delivery: %v", view)
			}
			if d := deliveries(t, l.s, a); len(d) != 0 || countCalls(l.calls(), "prompt") != 0 {
				t.Fatalf("reconcile touched attempts (%v) or sent (%d)", d, countCalls(l.calls(), "prompt"))
			}
			if tc.after == WakeUncertain || tc.after == WakeSubmitted {
				if rem := field(t, view, "remaining"); !strings.Contains(rem, "sumctl wake consume") {
					t.Fatalf("remaining = %s", rem)
				}
			}
		})
	}
	t.Run("prepared then the next pass may prompt", func(t *testing.T) {
		l := newPassLab(t)
		l.adopt()
		a := l.reporting()
		plant(l, WakePrepared, []WakeRef{{Task: a, ID: "report:r1"}})
		l.coordinatorIdle()
		if _, err := Reconcile(l.s, l.ctx, ""); err != nil {
			t.Fatal(err)
		}
		r, _ := l.pumpFor([]string{a}, "parent", 8*time.Second)
		if got := rowField(t, r, "w-root:p1", "state"); got != "submitted" || l.wake().Generation != 2 {
			t.Fatalf("pass after reconcile = %s gen %d", got, l.wake().Generation)
		}
	})
	t.Run("replaced occupant closes the old episode without consuming it", func(t *testing.T) {
		l := newPassLab(t)
		l.adopt()
		a := l.reporting()
		l.submittedFor(a)
		l.setOwner(func(owner *ordjson.Object) { owner.Set("incarnation", boundTo("w-root:p1-reborn")) })
		l.session("lab", map[string]any{"panes": map[string]any{"w-root:p1": map[string]any{"agent_status": "idle", "cwd": l.root, "agent": "claude", "terminal_id": "term-w-root:p1-reborn"}}})
		b := l.reporting()
		blocked, _ := l.pumpFor([]string{a, b}, "parent", 8*time.Second)
		if got := rowField(t, blocked, "w-root:p1", "state"); got != "deferred" {
			t.Fatalf("pass under a replaced occupant = %v", row(t, blocked, "w-root:p1"))
		}
		view, err := Reconcile(l.s, l.ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		if field(t, view, "after") != WakeReplaced || field(t, view, "action") != "closed" {
			t.Fatalf("reconcile = %v", view)
		}
		d := deliveries(t, l.s, a)
		if len(d) != 1 || field(t, d[0], "state") != "submitted" {
			t.Fatalf("the old attempt changed: %v", d)
		}
		if w := l.wake(); len(w.Covered) != 0 || len(w.Receipts) != 1 || w.Receipts[0].Result != WakeReplaced || w.Receipts[0].Fingerprint != "" || w.Receipts[0].Delivery != field(t, d[0], "id") {
			t.Fatalf("reconcile granted coverage or left the old delivery unaccounted: %+v", w)
		}
		r, _ := l.pumpFor([]string{a, b}, "parent", 8*time.Second)
		if got := rowField(t, r, "w-root:p1", "state"); got != "submitted" {
			t.Fatalf("pass after reconcile = %v, want a new episode for the new occupant", row(t, r, "w-root:p1"))
		}
		owner, _ := l.s.Owner()
		reborn, _ := owner.Get("incarnation")
		if w := l.wake(); w.Generation != 2 || len(w.Episode.Claims) != 1 || w.Episode.Claims[0].Task != b || !IncarnationMatches(w.Incarnation, IncarnationJSON(reborn)) {
			t.Fatalf("new episode = gen %d %+v (incarnation %s); want only %s claimed and the new occupant recorded", w.Generation, w.Episode, w.Incarnation, b)
		}
		if d := deliveries(t, l.s, a); len(d) != 1 {
			t.Fatalf("the old submitted attempt was resent: %v", d)
		}
		if uncoalesced, _ := l.showEntry().Get("uncoalesced_legacy_prompts"); len(uncoalesced.([]any)) != 0 {
			t.Fatalf("show lists the replaced episode's prompt as uncoalesced: %v", uncoalesced)
		}
	})
	t.Run("prepared with a stamped attempt is uncertain, not unsubmitted", func(t *testing.T) {
		l := newPassLab(t)
		l.adopt()
		a := l.reporting()
		plant(l, WakePrepared, []WakeRef{{Task: a, ID: "report:r1"}})
		l.stampInFlight(a, "d-planted")
		l.coordinatorIdle()
		view, err := Reconcile(l.s, l.ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		if field(t, view, "after") != WakeUncertain || field(t, view, "action") != "held" || !strings.Contains(field(t, view, "remaining"), "sumctl wake consume") || !strings.Contains(field(t, view, "remaining"), "forced notice") {
			t.Fatalf("reconcile = %v", view)
		}
		if w := l.wake(); !w.IsOutstanding() || !strings.Contains(w.Episode.Reason, "between the attempt stamp and the claim") {
			t.Fatalf("episode = %+v, want outstanding as uncertain with the interruption named", w.Episode)
		}
		if d := deliveries(t, l.s, a); len(d) != 1 || field(t, d[0], "state") != "in-flight" {
			t.Fatalf("reconcile touched the attempt: %v", d)
		}
		next, _ := l.pumpFor([]string{a}, "parent", 8*time.Second)
		if got := rowField(t, next, "w-root:p1", "state"); got != "coalesced" || countCalls(l.calls(), "prompt") != 0 {
			t.Fatalf("pass after reconcile = %s, want coalesced behind the uncertain episode with nothing resent", got)
		}
		if _, has := l.showEntry().Get("boundary"); !has {
			t.Fatal("show offers no boundary for the uncertain episode")
		}
	})
	t.Run("absent", func(t *testing.T) {
		l := newPassLab(t)
		l.adopt()
		l.coordinatorIdle()
		view, err := Reconcile(l.s, l.ctx, "")
		if err != nil || field(t, view, "status") != WakeAbsent || field(t, view, "action") != "none" {
			t.Fatalf("reconcile absent = %v %v", view, err)
		}
	})
	t.Run("unreadable file is reported, never overwritten", func(t *testing.T) {
		l := newPassLab(t)
		l.adopt()
		l.coordinatorIdle()
		writeRaw(t, l.s.WakePath(l.endpoint()), "{broken")
		view, err := Reconcile(l.s, l.ctx, "")
		if err != nil || field(t, view, "status") != WakeBlocked || field(t, view, "action") != "none" || !strings.Contains(field(t, view, "diagnostic"), l.s.WakePath(l.endpoint())) {
			t.Fatalf("reconcile blocked = %v %v", view, err)
		}
		if l.wakeBytes() != "{broken" {
			t.Fatal("reconcile rewrote an unreadable sidecar")
		}
	})
	t.Run("worker pane is refused", func(t *testing.T) {
		l := newPassLab(t)
		l.adopt()
		worker := l.worker("lab", "w1:p1")
		ctx := ordjson.NewObject()
		ctx.Set("machine", l.host)
		ctx.Set("session", "lab")
		ctx.Set("pane", "w1:p1")
		ctx.Set("cwd", l.worktree(worker))
		if _, err := Reconcile(l.s, ctx, ""); err == nil {
			t.Fatal("a worker reconciled the coordinator's wake")
		}
	})
}

// `wake show` reads everything and writes nothing: the outstanding episode's boundary covers the current open
// obligations to the coordinator, an unreadable task is an explicit omission, and prompts a non-adopted (legacy)
// helper submitted are reported as uncoalesced.
func TestShowListsCoverageGapsAndUncoalescedLegacyPrompts(t *testing.T) {
	l := newPassLab(t)
	legacy := l.reporting()
	l.submittedFor(legacy)
	legacyDelivery := field(t, deliveries(t, l.s, legacy)[0], "id")
	l.adopt()
	a := l.reporting()
	l.submittedFor(a)
	broken := l.reporting()
	writeRaw(t, filepath.Join(l.s.Tasks, broken, "task.json"), "{not json")
	before := snapshotDir(t, l.s.Home)
	entry := l.showEntry()
	if snapshotDir(t, l.s.Home) != before {
		t.Fatal("wake show wrote under the state home")
	}
	if field(t, entry, "status") != WakeOK || field(t, entry, "generation") != "1" || field(t, entry, "outstanding") != "true" {
		t.Fatalf("entry = %v", entry)
	}
	included, _ := entry.Get("included")
	omitted, _ := entry.Get("omitted")
	if !strings.Contains(text(included), a) || !strings.Contains(text(included), legacy) || strings.Contains(text(included), broken) {
		t.Fatalf("included = %s", text(included))
	}
	if !strings.Contains(text(omitted), broken) {
		t.Fatalf("omitted = %s, want the unreadable task named", text(omitted))
	}
	uncoalesced, _ := entry.Get("uncoalesced_legacy_prompts")
	list := uncoalesced.([]any)
	if len(list) != 1 || field(t, list[0].(*ordjson.Object), "delivery") != legacyDelivery {
		t.Fatalf("uncoalesced = %v, want only the legacy delivery %s", uncoalesced, legacyDelivery)
	}
	b, err := ParseBoundary(field(t, entry, "boundary"))
	if err != nil {
		t.Fatal(err)
	}
	if b.Episode != l.wake().Episode.ID || b.Generation != 1 || len(b.Included) != 2 || len(b.Omitted) != 1 {
		t.Fatalf("boundary = %+v", b)
	}
	view, err := l.consume(field(t, entry, "boundary"))
	if err != nil || field(t, view, "covered_count") != "2" {
		t.Fatalf("consume of the shown boundary = %v %v", view, err)
	}
}

// The boundary token is opaque but self-checking: a payload edit is detected, and coverage order does not matter.
func TestBoundaryTokenRoundTrip(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.submittedFor(a)
	w := l.wake()
	one, _ := BuildBoundary(l.s, w, []WakeCovered{report("t-2"), report("t-1")}, nil)
	two, _ := BuildBoundary(l.s, w, []WakeCovered{report("t-1"), report("t-2")}, nil)
	if one.Fingerprint() != two.Fingerprint() {
		t.Fatal("coverage order changed the fingerprint")
	}
	three, _ := BuildBoundary(l.s, w, []WakeCovered{report("t-1")}, []WakeCovered{report("t-2")})
	if three.Fingerprint() == one.Fingerprint() {
		t.Fatal("moving an identity from included to omitted kept the fingerprint")
	}
	token, _ := one.Token()
	parsed, err := ParseBoundary(token)
	if err != nil || parsed.Fingerprint() != one.Fingerprint() || parsed.Recipient != w.Recipient || !IncarnationMatches(parsed.Incarnation, w.Incarnation) {
		t.Fatalf("round trip = %+v %v", parsed, err)
	}
	var raw map[string]any
	payload, _ := json.Marshal(one)
	if json.Unmarshal(payload, &raw) != nil || raw["installation"] == nil {
		t.Fatalf("payload = %s", payload)
	}
}

// text renders a view value as compact JSON for substring assertions.
func text(v any) string {
	raw, err := ordjson.MarshalCompact(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}

// snapshotDir fingerprints every file under dir (path, size, content) so a read can be proven write-free.
func snapshotDir(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s %d %x\n", path, info.Size(), raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// The consume takes the recipient lock: a pass holding it for this coordinator makes the consume wait, and the
// consume then decides from the sidecar that pass left.
func TestConsumeWaitsForTheRecipientLock(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.submittedFor(a)
	token := l.boundary([]WakeCovered{report(a)}, nil)
	other, err := store.Open(l.s.Home)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := lockRecipient(other, context.Background(), context.Background(), l.endpoint())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := l.consume(token)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("consume finished while the recipient lock was held: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("consume never finished after the lock was released")
	}
	if l.wakePhase() != WakeConsumed {
		t.Fatalf("phase = %s", l.wakePhase())
	}
}
