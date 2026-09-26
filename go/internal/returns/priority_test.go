package returns

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

// Decision priority without repeated reminders (#240b, U7): an open question owed to an adopted coordinator is a
// decision. It bypasses only the routine wake latch, at the next eligible pass, through the same observation,
// occupant, identity, budget and claim rules as a routine prompt; a busy coordinator receives nothing. Its identity
// (task, obligation, question key) is recorded on the sidecar so unchanged decisions are never re-prompted.

// question appends an open question to task, keyed by key ("" records a null key as `ask` does without --key).
func (l *passLab) question(task, qid, key string) {
	l.t.Helper()
	l.mutate(task, func(record *ordjson.Object) {
		q := ordjson.NewObject()
		q.Set("id", qid)
		if key == "" {
			q.Set("key", nil)
		} else {
			q.Set("key", key)
		}
		q.Set("status", "open")
		q.Set("created_at", "2026-09-24T00:03:00+00:00")
		existing, _ := record.Get("questions")
		list, _ := existing.([]any)
		record.Set("questions", append(list, q))
	})
}

func (l *passLab) setQuestion(task, qid string, change func(q *ordjson.Object)) {
	l.t.Helper()
	l.mutate(task, func(record *ordjson.Object) {
		existing, _ := record.Get("questions")
		for _, raw := range existing.([]any) {
			q := raw.(*ordjson.Object)
			if id, _ := q.Get("id"); id == qid {
				change(q)
			}
		}
	})
}

func (l *passLab) rekey(task, qid, key string) {
	l.setQuestion(task, qid, func(q *ordjson.Object) { q.Set("key", key) })
}

func (l *passLab) answer(task, qid string) {
	l.setQuestion(task, qid, func(q *ordjson.Object) {
		q.Set("status", "answered")
		q.Set("answered_at", "2026-09-24T00:04:00+00:00")
	})
}

// decisionKeys maps each recorded decision on the lab coordinator's sidecar to its attempt state, by task/id/revision.
func (l *passLab) decisionKeys() map[string]string {
	l.t.Helper()
	out := map[string]string{}
	w := l.wake()
	if w == nil {
		return out
	}
	for _, d := range w.Decisions {
		out[d.Task+"/"+d.ID+"/"+d.Revision] = d.State
	}
	return out
}

// priorityRow asserts the coordinator row is a priority prompt attempt in state naming exactly decisions (task/id).
func priorityRow(t *testing.T, result *ordjson.Object, state string, decisions ...string) *ordjson.Object {
	t.Helper()
	r := row(t, result, "w-root:p1")
	got, _ := r.Get("state")
	via, _ := r.Get("via")
	priority, _ := r.Get("priority")
	if got != state || via != "prompt" || priority != true {
		t.Fatalf("row = %s via %v priority %v, want a %s priority prompt: %v", got, via, priority, state, r)
	}
	listed, _ := r.Get("decisions")
	var named []string
	for _, raw := range listed.([]any) {
		obj := raw.(*ordjson.Object)
		named = append(named, field(t, obj, "task")+"/"+field(t, obj, "id"))
	}
	if strings.Join(named, " ") != strings.Join(decisions, " ") {
		t.Fatalf("row decisions = %v, want %v", named, decisions)
	}
	wake, _ := r.Get("wake")
	if admission, _ := wake.(*ordjson.Object).Get("admission"); admission != wakeDecisionCoalesced {
		t.Fatalf("row wake = %v, want the coalesced view of the untouched routine episode", wake)
	}
	return r
}

// AE3: a question arriving behind a submitted routine episode gets exactly one priority prompt at the next eligible
// pass; the routine episode is untouched; the same question is never repeated; once answered it is worker work and
// the coordinator's decision count drops.
func TestPriorityQuestionBehindARoutineWakeIsPromptedOnce(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.submittedFor(a)
	episode := l.wake().Episode.ID
	q := l.asking(l.host)
	l.coordinatorIdle()
	result, _ := l.pumpFor([]string{a, q}, "parent", 8*time.Second)
	r := priorityRow(t, result, "submitted", q+"/question:q1")
	if coalesced, _ := r.Get("coalesced_obligations"); !strings.Contains(text(coalesced), "report:r1") {
		t.Fatalf("routine obligations in the bucket are not reported as coalesced: %v", r)
	}
	if prompts, _ := result.Get("prompts"); fmt.Sprint(prompts) != "1" {
		t.Fatalf("prompts counted = %v", prompts)
	}
	sent := prompts(l.calls(), "w-root:p1")
	if len(sent) != 2 {
		t.Fatalf("prompts = %d, want the routine one and one priority prompt", len(sent))
	}
	msg := sent[1]
	// a's report is submitted, not pending; the priority prompt names only the decision.
	if !strings.Contains(msg, q) || !strings.Contains(msg, "question q1 is open") || strings.Contains(msg, a) {
		t.Fatalf("priority prompt = %q, want only the question named", msg)
	}
	if !strings.Contains(msg, "routine wake "+episode+" stays outstanding") || !strings.Contains(msg, "wake consume --boundary TOKEN") || strings.Contains(msg, "Wake episode ") {
		t.Fatalf("priority prompt = %q, want the decision marker naming the outstanding episode and no new episode", msg)
	}
	d := deliveries(t, l.s, q)
	if len(d) != 1 || field(t, d[0], "state") != "submitted" || text(func() any { v, _ := d[0].Get("obligations"); return v }()) != text([]any{"question:q1"}) {
		t.Fatalf("question attempt = %s, want one submitted attempt for exactly the question", text(d))
	}
	w := l.wake()
	if w.Episode.ID != episode || w.Episode.Phase != WakeSubmitted || w.Generation != 1 || len(w.Episode.Claims) != 1 {
		t.Fatalf("the priority prompt touched the routine episode: gen %d %+v", w.Generation, w.Episode)
	}
	if got := l.decisionKeys(); len(got) != 1 || got[q+"/question:q1/"] != "submitted" {
		t.Fatalf("recorded decisions = %v, want the question recorded as submitted", got)
	}
	if len(w.Decisions) != 1 || w.Decisions[0].Delivery != field(t, d[0], "id") {
		t.Fatalf("decision record = %+v, want the priority delivery id", w.Decisions)
	}
	// The same decision again, behind a new routine arrival: nothing is sent; the row says it was already prompted.
	c := l.reporting()
	again, _ := l.pumpFor([]string{a, q, c}, "parent", 8*time.Second)
	r = row(t, again, "w-root:p1")
	if state, _ := r.Get("state"); state != "coalesced" {
		t.Fatalf("second pass = %v, want coalesced with nothing sent", r)
	}
	if prompted, _ := r.Get("decisions_prompted"); !strings.Contains(text(prompted), "question:q1") {
		t.Fatalf("second pass does not list the decision as already prompted: %v", r)
	}
	if n := len(prompts(l.calls(), "w-root:p1")); n != 2 || len(deliveries(t, l.s, q)) != 1 {
		t.Fatalf("prompts = %d, attempts = %d; the decision was repeated", n, len(deliveries(t, l.s, q)))
	}
	// Answered: the obligation is the worker's, the coordinator gets no priority prompt, and the record is pruned.
	l.answer(q, "q1")
	task, _ := l.s.ReadTask(q)
	open, _ := OpenObligations(l.s, task)
	if len(open) != 1 || field(t, open[0], "kind") != "answer" || field(t, open[0], "recipient") != "worker" {
		t.Fatalf("obligations after the answer = %s, want one answer owed to the worker", text(open))
	}
	after, _ := l.pumpFor([]string{a, q, c}, "parent", 8*time.Second)
	if state, _ := row(t, after, "w-root:p1").Get("state"); state != "coalesced" {
		t.Fatalf("pass after the answer = %v", row(t, after, "w-root:p1"))
	}
	if n := len(prompts(l.calls(), "w-root:p1")); n != 2 {
		t.Fatalf("prompts = %d after the answer, want no coordinator prompt", n)
	}
	if got := l.decisionKeys(); len(got) != 0 {
		t.Fatalf("a settled decision stayed recorded: %v", got)
	}
}

// A busy coordinator receives no priority prompt: the attempt is not-delivered exactly as a routine attempt would
// be, the identity stays unrecorded, and the next eligible pass prompts once.
func TestPriorityNeverInterruptsABusyCoordinator(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.submittedFor(a)
	q := l.asking(l.host)
	l.session("lab", map[string]any{"panes": map[string]any{"w-root:p1": l.pane("working", l.root)}})
	busy, _ := l.pumpFor([]string{a, q}, "parent", 8*time.Second)
	r := priorityRow(t, busy, "not-delivered", q+"/question:q1")
	if reason, _ := r.Get("reason"); !strings.Contains(fmt.Sprint(reason), "Recipient is working") {
		t.Fatalf("reason = %v", reason)
	}
	if d := deliveries(t, l.s, q); len(d) != 1 || field(t, d[0], "state") != "not-delivered" {
		t.Fatalf("attempt = %s, want one not-delivered record", text(d))
	}
	if got := l.decisionKeys(); len(got) != 0 {
		t.Fatalf("a not-delivered decision was recorded: %v", got)
	}
	if n := len(prompts(l.calls(), "w-root:p1")); n != 1 || l.wakePhase() != WakeSubmitted {
		t.Fatalf("prompts = %d, phase = %s; want no prompt to a busy pane and the routine episode kept", n, l.wakePhase())
	}
	l.coordinatorIdle()
	idle, _ := l.pumpFor([]string{a, q}, "parent", 8*time.Second)
	priorityRow(t, idle, "submitted", q+"/question:q1")
	if n := len(prompts(l.calls(), "w-root:p1")); n != 2 {
		t.Fatalf("prompts = %d, want exactly one priority prompt once the pane is idle", n)
	}
	if got := l.decisionKeys(); got[q+"/question:q1/"] != "submitted" {
		t.Fatalf("recorded decisions = %v", got)
	}
}

// Several new decisions in one pass make one coalesced priority prompt; a newly keyed question is a new identity and
// is eligible once more, on its own.
func TestPriorityDecisionsCoalesceAndARekeyedQuestionIsEligibleOnce(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.submittedFor(a)
	q1 := l.asking(l.host)
	q2 := l.asking(l.host)
	l.question(q2, "q7", "k1")
	l.coordinatorIdle()
	result, _ := l.pumpFor([]string{a, q1, q2}, "parent", 8*time.Second)
	priorityRow(t, result, "submitted", q1+"/question:q1", q2+"/question:q1", q2+"/question:q7")
	sent := prompts(l.calls(), "w-root:p1")
	if len(sent) != 2 || !strings.Contains(sent[1], q1) || !strings.Contains(sent[1], q2) || !strings.Contains(sent[1], "question q7 is open") {
		t.Fatalf("prompts = %d, last = %q; want one prompt naming every new decision", len(sent), sent[len(sent)-1])
	}
	if got := l.decisionKeys(); len(got) != 3 || got[q1+"/question:q1/"] != "submitted" || got[q2+"/question:q7/k1"] != "submitted" {
		t.Fatalf("recorded decisions = %v", got)
	}
	l.rekey(q2, "q7", "k2")
	rekeyed, _ := l.pumpFor([]string{a, q1, q2}, "parent", 8*time.Second)
	priorityRow(t, rekeyed, "submitted", q2+"/question:q7")
	sent = prompts(l.calls(), "w-root:p1")
	if len(sent) != 3 || strings.Contains(sent[2], q1) || !strings.Contains(sent[2], "question q7 is open") {
		t.Fatalf("prompts = %d, last = %q; want the re-keyed question alone", len(sent), sent[len(sent)-1])
	}
	got := l.decisionKeys()
	if len(got) != 3 || got[q2+"/question:q7/k2"] != "submitted" || got[q2+"/question:q7/k1"] != "" {
		t.Fatalf("recorded decisions after the rekey = %v, want the identity refreshed to the new key", got)
	}
	quiet, _ := l.pumpFor([]string{a, q1, q2}, "parent", 8*time.Second)
	if state, _ := row(t, quiet, "w-root:p1").Get("state"); state != "quiet" || len(prompts(l.calls(), "w-root:p1")) != 3 {
		t.Fatalf("pass after the rekeyed prompt = %v with %d prompts", row(t, quiet, "w-root:p1"), len(prompts(l.calls(), "w-root:p1")))
	}
}

// A decision that rode the routine episode is recorded too; once consumed it is covered: not re-prompted even when
// re-keyed, while it stays an open obligation in every view.
func TestPriorityConsumedUnansweredDecisionIsNotRepeatedAndStaysVisible(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	q := l.asking(l.host)
	l.submittedFor(q)
	if got := l.decisionKeys(); got[q+"/question:q1/"] != "submitted" {
		t.Fatalf("a decision sent by the routine episode was not recorded: %v", got)
	}
	question := WakeCovered{Task: q, ID: "question:q1", Revision: "q1"}
	if view, err := l.consume(l.boundary([]WakeCovered{question}, nil)); err != nil || field(t, view, "result") != "consumed" {
		t.Fatalf("consume = %v %v", view, err)
	}
	l.rekey(q, "q1", "k9")
	c := l.reporting()
	l.coordinatorIdle()
	result, _ := l.pumpFor([]string{q, c}, "parent", 8*time.Second)
	r := row(t, result, "w-root:p1")
	if state, _ := r.Get("state"); state != "submitted" {
		t.Fatalf("pass after consumption = %v", r)
	}
	if withheld, _ := r.Get("withheld"); !strings.Contains(text(withheld), "question:q1") {
		t.Fatalf("the covered decision is not withheld: %v", r)
	}
	sent := prompts(l.calls(), "w-root:p1")
	if len(sent) != 2 || strings.Contains(sent[1], q) {
		t.Fatalf("prompts = %d, last = %q; want the covered decision not repeated", len(sent), sent[len(sent)-1])
	}
	if got := l.decisionKeys(); len(got) != 1 {
		t.Fatalf("recorded decisions = %v, want the consumed decision retained", got)
	}
	task, _ := l.s.ReadTask(q)
	open, _ := OpenObligations(l.s, task)
	if len(open) != 1 || field(t, open[0], "kind") != "question" {
		t.Fatalf("the consumed decision left the open obligations: %s", text(open))
	}
	if included, _ := l.showEntry().Get("included"); !strings.Contains(text(included), "question:q1") {
		t.Fatalf("wake show no longer lists the open decision: %s", text(included))
	}
}

// A priority prompt interrupted in flight stays uncertain and is never resent; the routine episode is unaffected and
// the worker's answer delivery is intact.
func TestPriorityUncertainAttemptIsNeverResent(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.submittedFor(a)
	worker := l.worker("lab", "w1:p1")
	l.question(worker, "q2", "k")
	l.session("lab", map[string]any{"hold": map[string]any{"w-root:p1": "prompt"}, "panes": map[string]any{
		"w-root:p1": l.pane("idle", l.root), "w1:p1": l.pane("idle", l.worktree(worker))}})
	h := l.startHelper("priority", []string{a, worker}, "parent")
	l.awaitHeld(h, "prompt", "w-root:p1")
	h.kill(t)
	l.release("w-root:p1")
	if l.wakePhase() != WakeSubmitted || l.wake().Generation != 1 {
		t.Fatalf("the interrupted priority prompt changed the routine episode: %s gen %d", l.wakePhase(), l.wake().Generation)
	}
	if got := l.decisionKeys(); got[worker+"/question:q2/k"] != "in-flight" {
		t.Fatalf("recorded decisions = %v, want the claimed decision recorded before the prompt", got)
	}
	l.coordinatorIdle()
	c := l.reporting()
	result, _ := l.pumpFor([]string{a, worker, c}, "parent", 8*time.Second)
	if state, _ := row(t, result, "w-root:p1").Get("state"); state != "coalesced" {
		t.Fatalf("pass after the interrupted priority prompt = %v", row(t, result, "w-root:p1"))
	}
	if notes := notificationStates(t, result, "w-root:p1"); notes[worker] != "uncertain" {
		t.Fatalf("notification states = %v, want the interrupted attempt uncertain", notes)
	}
	if n := len(prompts(l.calls(), "w-root:p1")); n != 2 {
		t.Fatalf("prompts = %d, want the interrupted priority prompt not resent", n)
	}
	entry := l.showEntry()
	if uncoalesced, _ := entry.Get("uncoalesced_legacy_prompts"); len(uncoalesced.([]any)) != 0 {
		t.Fatalf("the interrupted priority prompt is reported as uncoalesced: %v", uncoalesced)
	}
	if listed, _ := entry.Get("priority_prompts"); !strings.Contains(text(listed), "question:q2") {
		t.Fatalf("wake show priority_prompts = %v", listed)
	}
	l.answer(worker, "q2")
	l.session("lab", map[string]any{"panes": map[string]any{"w-root:p1": l.pane("idle", l.root), "w1:p1": l.pane("idle", l.worktree(worker))}})
	full, _ := l.pumpFor(nil, "", 8*time.Second)
	if got := states(full)["w1:p1"]; got != "submitted" {
		t.Fatalf("worker answer delivery = %v", states(full))
	}
}

// A priority submission followed by consumption of routine episode A keeps both identities: the episode is consumed,
// the decision stays recorded, and `wake show` accounts for the priority delivery.
func TestPriorityDeliveryIsAccountedAfterTheRoutineEpisodeIsConsumed(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.submittedFor(a)
	q := l.asking(l.host)
	l.coordinatorIdle()
	result, _ := l.pumpFor([]string{a, q}, "parent", 8*time.Second)
	priorityRow(t, result, "submitted", q+"/question:q1")
	priorityDelivery := field(t, deliveries(t, l.s, q)[0], "id")
	question := WakeCovered{Task: q, ID: "question:q1", Revision: "q1"}
	if view, err := l.consume(l.boundary([]WakeCovered{report(a), question}, nil)); err != nil || field(t, view, "covered_count") != "2" {
		t.Fatalf("consume = %v %v", view, err)
	}
	w := l.wake()
	if w.Episode.Phase != WakeConsumed || len(w.Decisions) != 1 || w.Decisions[0].Delivery != priorityDelivery || w.Decisions[0].State != "submitted" {
		t.Fatalf("after consumption: %s decisions %+v", w.Episode.Phase, w.Decisions)
	}
	entry := l.showEntry()
	listed, _ := entry.Get("priority_prompts")
	if !strings.Contains(text(listed), priorityDelivery) || !strings.Contains(text(listed), "question:q1") {
		t.Fatalf("priority_prompts = %s", text(listed))
	}
	if uncoalesced, _ := entry.Get("uncoalesced_legacy_prompts"); len(uncoalesced.([]any)) != 0 {
		t.Fatalf("the priority delivery is reported as uncoalesced: %v", uncoalesced)
	}
	c := l.reporting()
	next, _ := l.pumpFor([]string{a, q, c}, "parent", 8*time.Second)
	r := row(t, next, "w-root:p1")
	if state, _ := r.Get("state"); state != "submitted" || l.wake().Generation != 2 {
		t.Fatalf("pass after consumption = %v gen %d", r, l.wake().Generation)
	}
	if sent := prompts(l.calls(), "w-root:p1"); len(sent) != 3 || strings.Contains(sent[2], q) {
		t.Fatalf("prompts = %d, last = %q; want the consumed decision not repeated", len(sent), sent[len(sent)-1])
	}
}

// A replaced occupant inherits no decision record: reconcile resets it and the new occupant is prompted once.
func TestPriorityReplacedOccupantIsPromptedOnce(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.submittedFor(a)
	q := l.asking(l.host)
	l.coordinatorIdle()
	result, _ := l.pumpFor([]string{a, q}, "parent", 8*time.Second)
	priorityRow(t, result, "submitted", q+"/question:q1")
	l.setOwner(func(owner *ordjson.Object) { owner.Set("incarnation", boundTo("w-root:p1-reborn")) })
	l.session("lab", map[string]any{"panes": map[string]any{"w-root:p1": map[string]any{"agent_status": "idle", "cwd": l.root, "agent": "claude", "terminal_id": "term-w-root:p1-reborn"}}})
	q2 := l.asking(l.host)
	blocked, _ := l.pumpFor([]string{a, q, q2}, "parent", 8*time.Second)
	if state, _ := row(t, blocked, "w-root:p1").Get("state"); state != "deferred" || len(prompts(l.calls(), "w-root:p1")) != 2 {
		t.Fatalf("pass under a replaced occupant = %v", row(t, blocked, "w-root:p1"))
	}
	view, err := Reconcile(l.s, l.ctx, "")
	if err != nil || field(t, view, "after") != WakeReplaced {
		t.Fatalf("reconcile = %v %v", view, err)
	}
	if got := l.decisionKeys(); len(got) != 0 {
		t.Fatalf("the new occupant inherited decisions: %v", got)
	}
	next, _ := l.pumpFor([]string{a, q, q2}, "parent", 8*time.Second)
	r := row(t, next, "w-root:p1")
	if state, _ := r.Get("state"); state != "submitted" {
		t.Fatalf("pass for the new occupant = %v", r)
	}
	sent := prompts(l.calls(), "w-root:p1")
	if len(sent) != 3 || !strings.Contains(sent[2], q) || !strings.Contains(sent[2], q2) {
		t.Fatalf("prompt to the new occupant = %q, want both decisions named once", sent[len(sent)-1])
	}
	if got := l.decisionKeys(); got[q2+"/question:q1/"] != "submitted" {
		t.Fatalf("recorded decisions for the new occupant = %v", got)
	}
	again, _ := l.pumpFor([]string{a, q, q2}, "parent", 8*time.Second)
	if state, _ := row(t, again, "w-root:p1").Get("state"); state != "quiet" || len(prompts(l.calls(), "w-root:p1")) != 3 {
		t.Fatalf("the new occupant was prompted again: %v", row(t, again, "w-root:p1"))
	}
}

// No lost race: a question arriving while a priority prompt is held is not attributed to that delivery and is
// eligible at the next pass.
func TestPriorityArrivalDuringAHeldPromptIsEligibleNext(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.submittedFor(a)
	q1 := l.asking(l.host)
	l.session("lab", map[string]any{"hold": map[string]any{"w-root:p1": "prompt"}, "panes": map[string]any{"w-root:p1": l.pane("idle", l.root)}})
	h := l.startHelper("priority", []string{a, q1}, "parent")
	l.awaitHeld(h, "prompt", "w-root:p1")
	q2 := l.asking(l.host)
	if r, _ := l.pumpFor([]string{q2}, "parent", 3*time.Second); rowField(t, r, "w-root:p1", "state") != "deferred" {
		t.Fatalf("pass during the held priority prompt = %v", row(t, r, "w-root:p1"))
	}
	l.release("w-root:p1")
	priorityRow(t, h.result(t), "submitted", q1+"/question:q1")
	if d := deliveries(t, l.s, q2); len(d) != 0 {
		t.Fatalf("the arrival was attributed to the held delivery: %s", text(d))
	}
	if got := l.decisionKeys(); len(got) != 1 || got[q1+"/question:q1/"] != "submitted" {
		t.Fatalf("recorded decisions = %v", got)
	}
	l.coordinatorIdle()
	next, _ := l.pumpFor([]string{a, q1, q2}, "parent", 8*time.Second)
	priorityRow(t, next, "submitted", q2+"/question:q1")
	sent := prompts(l.calls(), "w-root:p1")
	if len(sent) != 3 || !strings.Contains(sent[2], q2) || strings.Contains(sent[2], q1) {
		t.Fatalf("prompt for the arrival = %q, want only %s", sent[len(sent)-1], q2)
	}
	if got := l.decisionKeys(); len(got) != 2 {
		t.Fatalf("recorded decisions = %v", got)
	}
}

// A re-keyed question is found by its own listing entry, not by its position in the bucket: an earlier obligation
// closed after the pass read its task drops out of the revalidated listing, and the re-keyed question behind it is
// still prompted.
func TestPriorityRekeyedQuestionIsPromptedAfterAnEarlierObligationCloses(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.submittedFor(a)
	q1 := l.asking(l.host)
	q2 := l.asking(l.host)
	l.question(q2, "q7", "k1")
	l.coordinatorIdle()
	result, _ := l.pumpFor([]string{a, q1, q2}, "parent", 8*time.Second)
	priorityRow(t, result, "submitted", q1+"/question:q1", q2+"/question:q1", q2+"/question:q7")
	l.rekey(q2, "q7", "k2")
	stale, err := l.s.AllTasks()
	if err != nil {
		t.Fatal(err)
	}
	// q1's question closes after the pass read its task, so revalidation drops it ahead of the re-keyed question.
	l.answer(q1, "q1")
	rekeyed, err := Pump(l.s, PumpOpts{RuntimeRoot: l.root, SumctlPath: "sumctl", Tasks: []string{a, q1, q2}, Recipient: "parent", Budget: 8 * time.Second, Snapshot: stale})
	if err != nil {
		t.Fatal(err)
	}
	r := priorityRow(t, rekeyed, "submitted", q2+"/question:q7")
	if revalidated, _ := r.Get("revalidated"); !strings.Contains(text(revalidated), q1) || !strings.Contains(text(revalidated), "question:q1") {
		t.Fatalf("the closed question was not dropped by revalidation: %v", r)
	}
	sent := prompts(l.calls(), "w-root:p1")
	if len(sent) != 3 || strings.Contains(sent[2], q1) || !strings.Contains(sent[2], "question q7 is open") {
		t.Fatalf("prompts = %d, last = %q; want the re-keyed question alone", len(sent), sent[len(sent)-1])
	}
	if got := l.decisionKeys(); got[q2+"/question:q7/k2"] != "submitted" || got[q2+"/question:q7/k1"] != "" {
		t.Fatalf("recorded decisions after the rekey = %v, want the identity refreshed to the new key", got)
	}
}

// A decision whose attempts reached stalled while the coordinator stayed busy is not left waiting behind the routine
// latch: the pass that retries stalled attempts (the idle hook) sends it once, like a stalled routine return.
func TestPriorityStalledDecisionIsRetriedOnTheIdleHookPass(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.submittedFor(a)
	q := l.asking(l.host)
	l.session("lab", map[string]any{"panes": map[string]any{"w-root:p1": l.pane("working", l.root)}})
	for i := 0; i < AttemptsBound; i++ {
		busy, _ := l.pumpFor([]string{a, q}, "parent", 8*time.Second)
		priorityRow(t, busy, "not-delivered", q+"/question:q1")
	}
	d := deliveries(t, l.s, q)
	if len(d) != AttemptsBound {
		t.Fatalf("attempts = %d, want %d not-delivered ones", len(d), AttemptsBound)
	}
	// Still busy, then idle: an ordinary pass reads the bucket as stalled and stamps nothing more.
	for _, idle := range []bool{false, true} {
		if idle {
			l.coordinatorIdle()
		}
		stalled, _ := l.pumpFor([]string{a, q}, "parent", 8*time.Second)
		r := row(t, stalled, "w-root:p1")
		if state, _ := r.Get("state"); state != "stalled" || len(deliveries(t, l.s, q)) != AttemptsBound || len(prompts(l.calls(), "w-root:p1")) != 1 {
			t.Fatalf("ordinary pass (idle %v) on a stalled decision = %v (attempts %d, prompts %d), want stalled with nothing sent", idle, r, len(deliveries(t, l.s, q)), len(prompts(l.calls(), "w-root:p1")))
		}
	}
	retried, err := Pump(l.s, PumpOpts{RuntimeRoot: l.root, SumctlPath: "sumctl", Tasks: []string{a, q}, Recipient: "parent", Budget: 8 * time.Second, RetryStalled: true})
	if err != nil {
		t.Fatal(err)
	}
	priorityRow(t, retried, "submitted", q+"/question:q1")
	if n := len(prompts(l.calls(), "w-root:p1")); n != 2 {
		t.Fatalf("prompts = %d, want exactly one priority prompt from the retry pass", n)
	}
	if got := l.decisionKeys(); got[q+"/question:q1/"] != "submitted" {
		t.Fatalf("recorded decisions = %v", got)
	}
	if l.wakePhase() != WakeSubmitted || l.wake().Generation != 1 {
		t.Fatalf("the retry touched the routine episode: %s gen %d", l.wakePhase(), l.wake().Generation)
	}
}

// Once its question is answered, a recorded decision is pruned and its delivery stays accounted for by a
// decision-closed receipt: wake show lists it neither under priority_prompts nor as an uncoalesced legacy prompt.
func TestPriorityAnsweredDecisionLeavesAnAccountedDelivery(t *testing.T) {
	l := newPassLab(t)
	l.adopt()
	a := l.reporting()
	l.submittedFor(a)
	q := l.asking(l.host)
	l.coordinatorIdle()
	result, _ := l.pumpFor([]string{a, q}, "parent", 8*time.Second)
	priorityRow(t, result, "submitted", q+"/question:q1")
	priorityDelivery := field(t, deliveries(t, l.s, q)[0], "id")
	l.answer(q, "q1")
	// The next pass with something to consider (a new routine arrival) prunes the answered decision at admission.
	c := l.reporting()
	after, _ := l.pumpFor([]string{a, q, c}, "parent", 8*time.Second)
	if state, _ := row(t, after, "w-root:p1").Get("state"); state != "coalesced" {
		t.Fatalf("pass after the answer = %v", row(t, after, "w-root:p1"))
	}
	w := l.wake()
	if len(w.Decisions) != 0 {
		t.Fatalf("answered decision stayed recorded: %+v", w.Decisions)
	}
	closed := 0
	for _, r := range w.Receipts {
		if r.Result == "decision-closed" && r.Delivery == priorityDelivery && r.Fingerprint == "" && r.Generation == w.Generation {
			closed++
		}
	}
	if closed != 1 {
		t.Fatalf("receipts = %+v, want one decision-closed receipt for %s", w.Receipts, priorityDelivery)
	}
	entry := l.showEntry()
	if uncoalesced, _ := entry.Get("uncoalesced_legacy_prompts"); len(uncoalesced.([]any)) != 0 {
		t.Fatalf("the answered decision's delivery is reported as uncoalesced: %v", uncoalesced)
	}
	if listed, _ := entry.Get("priority_prompts"); strings.Contains(text(listed), priorityDelivery) {
		t.Fatalf("priority_prompts still lists the pruned decision: %s", text(listed))
	}
	if l.wakePhase() != WakeSubmitted || w.Generation != 1 {
		t.Fatalf("the prune touched the routine episode: %s gen %d", l.wakePhase(), w.Generation)
	}
}
