package returns

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// psFixture answers process start times from scenario data instead of the host's process table.
func psFixture(t *testing.T, starts string) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_PS_BIN", filepath.Join(root, "tests", "fixtures", "ps.py"))
	t.Setenv("FAKE_PS_STARTS", starts)
}

// restarted is a worker pane as Herdr restores it after a restart: the same pane ID, a new terminal, a new shell.
func (l *passLab) restarted(cwd string) map[string]any {
	pane := l.pane("idle", cwd)
	pane["terminal_id"] = "term-after-restart"
	pane["shell_pid"] = 5151
	return pane
}

func workerRecord(t *testing.T, l *passLab, pane string) *ordjson.Object {
	t.Helper()
	reg, err := l.s.Registration(store.Endpoint{Machine: l.host, Session: "lab", Pane: pane})
	if err != nil || reg == nil {
		t.Fatalf("registration %v: %v", reg, err)
	}
	inc, _ := reg.Get("incarnation")
	obj, _ := inc.(*ordjson.Object)
	return obj
}

// A worker pane Herdr restored under a new server is not the recorded worker: its answer is not prompted there, nothing
// is recorded, and the return stays pending for the recorded worker.
func TestPassReplacedWorkerIsNotPromptedAndItsReturnStaysPending(t *testing.T) {
	psFixture(t, `{"5151": "2030-01-01T00:00:00Z"}`)
	l := newPassLab(t)
	a := l.worker("lab", "w1:p1")
	l.session("lab", map[string]any{"panes": map[string]any{"w1:p1": l.restarted(l.worktree(a))}})
	result, _ := l.pump(0)
	if got := states(result)["w1:p1"]; got != "refused" {
		t.Fatalf("state = %s (%v), want refused", got, result)
	}
	if n := countCalls(l.calls(), "prompt"); n != 0 {
		t.Fatalf("prompts = %d, want none into the restored pane", n)
	}
	if d := deliveries(t, l.s, a); len(d) != 0 {
		t.Fatalf("deliveries = %v, want nothing recorded", d)
	}
	rows, _ := result.Get("recipients")
	reason, _ := rows.([]any)[0].(*ordjson.Object).Get("reason")
	if !strings.Contains(reason.(string), incarnation.Replaced) || !strings.Contains(reason.(string), "bind TASK --worker-pane") {
		t.Fatalf("reason = %v, want the replaced outcome and the rebind recovery", reason)
	}
}

// Herdr restarted between the pass's agent list and its fresh agent get: the claim judges the fresh observation.
func TestPassRestartBetweenSnapshotAndClaimSendsNothing(t *testing.T) {
	psFixture(t, `{"5151": "2030-01-01T00:00:00Z"}`)
	l := newPassLab(t)
	a := l.worker("lab", "w1:p1")
	pane := l.pane("idle", l.worktree(a))
	pane["get_terminal_id"] = "term-after-restart"
	pane["shell_pid"] = 5151
	l.session("lab", map[string]any{"panes": map[string]any{"w1:p1": pane}})
	result, _ := l.pump(0)
	if got := states(result)["w1:p1"]; got != "refused" || countCalls(l.calls(), "prompt") != 0 || len(deliveries(t, l.s, a)) != 0 {
		t.Fatalf("state = %s, prompts = %d, deliveries = %v; want refused with nothing sent or stamped", got, countCalls(l.calls(), "prompt"), deliveries(t, l.s, a))
	}
}

// A rebind recorded while the pass observed the recipient invalidates that observation before the in-flight stamp.
func TestIncarnationRecordedDuringObservationIsJudgedAtTheClaim(t *testing.T) {
	psFixture(t, `{}`)
	l := newPassLab(t)
	a := l.worker("lab", "w1:p1")
	l.session("lab", map[string]any{"hold": map[string]any{"w1:p1": "get"}, "panes": map[string]any{"w1:p1": l.pane("idle", l.worktree(a))}})
	h := l.startHelper("a", []string{a}, "worker")
	l.awaitHeld(h, "get", "w1:p1")
	other := incarnation.Evidence{Terminal: "term-someone-else", Shell: &incarnation.Shell{PID: 9999, Started: "2025-01-01T00:00:00Z"}}.Record(store.Now())
	if err := l.s.SetIncarnation(store.Endpoint{Machine: l.host, Session: "lab", Pane: "w1:p1"}, other); err != nil {
		t.Fatal(err)
	}
	l.release("w1:p1")
	result := h.result(t)
	if got := states(result)["w1:p1"]; got != "refused" || countCalls(l.calls(), "prompt") != 0 || len(deliveries(t, l.s, a)) != 0 {
		t.Fatalf("state = %s, prompts = %d; want refused with nothing sent after the record changed", got, countCalls(l.calls(), "prompt"))
	}
}

// An uncertain prompt stays uncertain when the recipient's pane ID is later recycled: no replay, no new prompt.
func TestPassUncertainDeliveryStaysUncertainAfterTheAddressIsRecycled(t *testing.T) {
	psFixture(t, `{"5151": "2030-01-01T00:00:00Z"}`)
	l := newPassLab(t)
	a := l.worker("lab", "w1:p1")
	l.session("lab", map[string]any{"prompt_hang": 3, "panes": map[string]any{"w1:p1": l.pane("idle", l.worktree(a))}})
	if result, _ := l.pump(0); states(result)["w1:p1"] != "uncertain" {
		t.Fatalf("first pass = %v, want an uncertain prompt", states(result))
	}
	l.session("lab", map[string]any{"panes": map[string]any{"w1:p1": l.restarted(l.worktree(a))}})
	result, _ := l.pump(0)
	if got := states(result)["w1:p1"]; got != "uncertain" || countCalls(l.calls(), "prompt") != 1 {
		t.Fatalf("second pass = %s with %d prompts, want uncertain and no replay", got, countCalls(l.calls(), "prompt"))
	}
	d := deliveries(t, l.s, a)
	if state, _ := d[len(d)-1].Get("state"); len(d) != 1 || state != "uncertain" {
		t.Fatalf("deliveries = %v, want the one uncertain attempt unchanged", d)
	}
}

// A live handoff (new terminal, same shell) is the same worker and is prompted once.
func TestPassHandoffRecipientIsPromptedOnce(t *testing.T) {
	psFixture(t, `{"4242": "2025-01-01T00:00:00Z"}`)
	l := newPassLab(t)
	a := l.worker("lab", "w1:p1")
	pane := l.pane("idle", l.worktree(a))
	pane["terminal_id"] = "term-after-handoff"
	l.session("lab", map[string]any{"panes": map[string]any{"w1:p1": pane}})
	result, _ := l.pump(0)
	if got := states(result)["w1:p1"]; got != "submitted" || countCalls(l.calls(), "prompt") != 1 {
		t.Fatalf("state = %s with %d prompts, want one submission", got, countCalls(l.calls(), "prompt"))
	}
	if inc := workerRecord(t, l, "w1:p1"); inc == nil || func() any { v, _ := inc.Get("terminal"); return v }() != "term-after-handoff" {
		t.Fatalf("worker record = %v, want the handoff terminal recorded", inc)
	}
}

// A worker whose native session is reported after launch has it recorded at the claim, so a later native restore
// is recognized and prompted instead of refused.
func TestPassRecordsALateNativeSessionSoARestoreIsRecognized(t *testing.T) {
	psFixture(t, `{"5151": "2030-01-01T00:00:00Z"}`)
	l := newPassLab(t)
	a := l.worker("lab", "w1:p1")
	pane := l.pane("idle", l.worktree(a))
	pane["agent_session"] = map[string]any{"agent": "claude", "kind": "id", "source": "herdr:claude", "value": "conv-W"}
	l.session("lab", map[string]any{"panes": map[string]any{"w1:p1": pane}})
	if result, _ := l.pump(0); states(result)["w1:p1"] != "submitted" {
		t.Fatalf("first pass = %v", states(result))
	}
	session, _ := workerRecord(t, l, "w1:p1").Get("agent_session")
	if value, _ := session.(*ordjson.Object).Get("value"); value != "conv-W" {
		t.Fatalf("recorded session = %v, want conv-W", session)
	}
	// The worker applies nothing; a second answer is owed after Herdr restores the conversation.
	l.mutate(a, func(task *ordjson.Object) {
		questions, _ := task.Get("questions")
		q := ordjson.NewObject()
		q.Set("id", "q2")
		q.Set("status", "answered")
		q.Set("created_at", "2026-09-24T00:02:00+00:00")
		q.Set("answered_at", "2026-09-24T00:03:00+00:00")
		task.Set("questions", append(questions.([]any), q))
	})
	restored := l.restarted(l.worktree(a))
	restored["agent_session"] = pane["agent_session"]
	l.session("lab", map[string]any{"panes": map[string]any{"w1:p1": restored}})
	if result, _ := l.pump(0); states(result)["w1:p1"] != "submitted" || countCalls(l.calls(), "prompt") != 2 {
		t.Fatalf("after restore = %v with %d prompts, want the restored worker prompted", states(result), countCalls(l.calls(), "prompt"))
	}
}

// An inline listing is presented only to the recorded occupant of the calling pane.
func TestPassInlineListingIsNotPresentedToAReplacedCaller(t *testing.T) {
	psFixture(t, `{"5151": "2030-01-01T00:00:00Z"}`)
	l := newPassLab(t)
	l.asking(l.host)
	l.session("lab", map[string]any{"panes": map[string]any{"w-root:p1": l.restarted(l.root)}})
	result, err := Pump(l.s, PumpOpts{RuntimeRoot: l.root, SumctlPath: "sumctl", Ctx: l.ctx, Inline: true, Budget: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if got := states(result)["w-root:p1"]; got != "refused" {
		t.Fatalf("inline state = %s (%v), want refused", got, result)
	}
}

// A caller verified as the coordinator does not skip the occupant check for a worker-role listing at its own pane.
func TestCallerVerifiedCoversOnlyItsOwnRoleAndTask(t *testing.T) {
	coordinator := ordjson.NewObject()
	coordinator.Set("role", "coordinator")
	worker := ordjson.NewObject()
	worker.Set("role", "worker")
	task := ordjson.NewObject()
	task.Set("id", "t-1")
	other := ordjson.NewObject()
	other.Set("id", "t-2")
	items := [][2]*ordjson.Object{{task, nil}}
	if !callerVerifiedFor(PumpOpts{CallerVerifiedRole: "coordinator"}, coordinator, items) {
		t.Fatal("a verified coordinator's own listing must not be re-observed")
	}
	if callerVerifiedFor(PumpOpts{CallerVerifiedRole: "coordinator"}, worker, items) {
		t.Fatal("a coordinator verification covered a worker listing")
	}
	if !callerVerifiedFor(PumpOpts{CallerVerifiedRole: "worker", CallerVerifiedTask: "t-1"}, worker, items) {
		t.Fatal("a verified worker's own task listing must not be re-observed")
	}
	if callerVerifiedFor(PumpOpts{CallerVerifiedRole: "worker", CallerVerifiedTask: "t-1"}, worker, append(items, [2]*ordjson.Object{other, nil})) {
		t.Fatal("a worker verification covered another task")
	}
	if callerVerifiedFor(PumpOpts{}, coordinator, items) {
		t.Fatal("an unverified caller skipped the check")
	}
}
