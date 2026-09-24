package returns

import (
	"encoding/json"
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

// passFake is a scripted Herdr for one delivery pass: per-session panes and hangs from cfg.json, every call logged.
const passFake = `#!/usr/bin/env python3
import json, os, sys, time
root = os.environ["PASS_FAKE"]
args = sys.argv[1:]
session, rest = args[1], args[2:]
with open(os.path.join(root, "calls"), "a") as out:
    out.write(json.dumps([session] + rest) + "\n")
cfg = json.load(open(os.path.join(root, "cfg.json")))
sess = cfg.get(session, {})
panes = sess.get("panes", {})
def hold(verb, pane):
    # A held call marks held-<verb>-<pane> and waits for release-<pane>, standing in for a slow or unreachable pane.
    if sess.get("hold", {}).get(pane) != verb:
        return
    open(os.path.join(root, "held-%s-%s" % (verb, pane)), "w").close()
    release, deadline = os.path.join(root, "release-" + pane), time.time() + 60
    while not os.path.exists(release) and time.time() < deadline:
        time.sleep(0.02)
def row(pane):
    # Every pane reports Herdr's terminal_id; a scenario that restarts Herdr sets its own.
    return dict({"pane_id": pane, "terminal_id": "term-" + pane}, **panes.get(pane, {}))
if rest[:2] == ["agent", "list"]:
    time.sleep(sess.get("list_hang", 0))
    print(json.dumps({"result": {"agents": [row(k) for k in panes]}})); sys.exit(0)
if rest[:2] == ["agent", "get"]:
    if rest[2] not in panes:
        print(json.dumps({"error": {"code": "agent_not_found", "message": "gone"}}), file=sys.stderr); sys.exit(1)
    hold("get", rest[2])
    r = row(rest[2])
    if "get_terminal_id" in r: r["terminal_id"] = r.pop("get_terminal_id")  # Herdr restarted after the pass's agent list.
    print(json.dumps({"result": {"agent": r}})); sys.exit(0)
if rest[:2] == ["pane", "get"]:  # Any pane the scenario does not list is a plain shell, like the lab root.
    print(json.dumps({"result": {"pane": row(rest[2])}})); sys.exit(0)
if rest[:2] == ["pane", "process-info"]:
    pane = rest[3]
    print(json.dumps({"result": {"process_info": {"pane_id": pane, "shell_pid": panes.get(pane, {}).get("shell_pid", 4242)}}})); sys.exit(0)
if rest[:2] == ["agent", "prompt"]:
    hold("prompt", rest[2])
    time.sleep(sess.get("prompt_hang", 0))
    print(json.dumps({"result": {}})); sys.exit(0)
print("unsupported", file=sys.stderr); sys.exit(2)
`

type passLab struct {
	t     *testing.T
	s     *store.Store
	host  string
	root  string
	fake  string
	ctx   *ordjson.Object
	cfg   map[string]any
	tasks int
}

func newPassLab(t *testing.T) *passLab {
	t.Helper()
	for _, key := range []string{"SUM_HOME", "SUM_SESSION", "SUM_NOW", "HERDR_SESSION", "HERDR_PANE_ID", "HERDR_ENV"} {
		t.Setenv(key, "")
	}
	root := t.TempDir()
	fake := filepath.Join(root, "herdr")
	if err := os.WriteFile(fake, []byte(passFake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_HERDR_BIN", fake)
	t.Setenv("PASS_FAKE", root)
	s, err := store.Open(filepath.Join(root, "home"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	host, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	ctx := ordjson.NewObject()
	ctx.Set("machine", host)
	ctx.Set("session", "lab")
	ctx.Set("pane", "w-root:p1")
	ctx.Set("cwd", root)
	owner := ordjson.NewObject()
	for _, k := range ctx.Keys() {
		v, _ := ctx.Get(k)
		owner.Set(k, v)
	}
	owner.Set("role", "coordinator")
	owner.Set("incarnation", boundTo("w-root:p1"))
	if err := ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Register(store.EndpointFromContext(ctx), "coordinator", nil, boundTo("w-root:p1")); err != nil {
		t.Fatal(err)
	}
	old := [2]time.Duration{ObserveTimeout, PromptTimeout}
	ObserveTimeout, PromptTimeout = time.Second, time.Second
	t.Cleanup(func() { ObserveTimeout, PromptTimeout = old[0], old[1] })
	lab := &passLab{t: t, s: s, host: host, root: root, fake: fake, ctx: ctx, cfg: map[string]any{}}
	lab.writeCfg()
	return lab
}

func (l *passLab) writeCfg() {
	raw, err := json.Marshal(l.cfg)
	if err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.root, "cfg.json"), raw, 0o600); err != nil {
		l.t.Fatal(err)
	}
}

// session configures one fake Herdr session.
func (l *passLab) session(name string, conf map[string]any) {
	l.cfg[name] = conf
	l.writeCfg()
}

// worker records a task in session whose worker owes an unapplied answer, registered and parented to the lab root.
func (l *passLab) worker(session, pane string) string {
	l.t.Helper()
	l.tasks++
	id := fmt.Sprintf("t-%012x", l.tasks)
	worktree := filepath.Join(l.root, "wt", id)
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		l.t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", json.Number("1"))
	task.Set("id", id)
	task.Set("status", "running")
	task.Set("machine", l.host)
	task.Set("session", session)
	task.Set("pane", pane)
	task.Set("worktree", worktree)
	task.Set("parent", l.ctx)
	question := ordjson.NewObject()
	question.Set("id", "q1")
	question.Set("status", "answered")
	question.Set("created_at", "2026-09-24T00:00:00+00:00")
	question.Set("answered_at", "2026-09-24T00:01:00+00:00")
	task.Set("questions", []any{question})
	if err := os.MkdirAll(filepath.Join(l.s.Tasks, id), 0o700); err != nil {
		l.t.Fatal(err)
	}
	if err := l.s.SaveTask(task); err != nil {
		l.t.Fatal(err)
	}
	if _, err := l.s.Register(store.Endpoint{Machine: l.host, Session: session, Pane: pane, Cwd: worktree}, "worker", id, boundTo(pane)); err != nil {
		l.t.Fatal(err)
	}
	return id
}

// boundTo is the incarnation recorded when the fake's pane was bound: the terminal the fake reports for it.
func boundTo(pane string) *ordjson.Object {
	return incarnation.Evidence{Terminal: "term-" + pane}.Record(store.Now())
}

func (l *passLab) pane(status, cwd string) map[string]any {
	return map[string]any{"agent_status": status, "cwd": cwd, "agent": "claude"}
}

func (l *passLab) worktree(id string) string { return filepath.Join(l.root, "wt", id) }

func (l *passLab) pump(budget time.Duration) (*ordjson.Object, time.Duration) {
	l.t.Helper()
	started := time.Now()
	result, err := Pump(l.s, PumpOpts{RuntimeRoot: l.root, SumctlPath: "sumctl", Ctx: l.ctx, Inline: true, Budget: budget, CallerVerifiedRole: "coordinator"})
	if err != nil {
		l.t.Fatal(err)
	}
	return result, time.Since(started)
}

func (l *passLab) calls() [][]string {
	raw, err := os.ReadFile(filepath.Join(l.root, "calls"))
	if err != nil {
		return nil
	}
	var out [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var call []string
		if err := json.Unmarshal([]byte(line), &call); err == nil {
			out = append(out, call)
		}
	}
	return out
}

func countCalls(calls [][]string, verb string) int {
	n := 0
	for _, c := range calls {
		if len(c) > 2 && c[1] == "agent" && c[2] == verb {
			n++
		}
	}
	return n
}

// states maps each recipient pane to its row state.
func states(result *ordjson.Object) map[string]string {
	out := map[string]string{}
	rows, _ := result.Get("recipients")
	for _, r := range rows.([]any) {
		row := r.(*ordjson.Object)
		rec, _ := row.Get("recipient")
		pane, _ := rec.(*ordjson.Object).Get("pane")
		state, _ := row.Get("state")
		out[fmt.Sprint(pane)] = fmt.Sprint(state)
	}
	return out
}

func deliveries(t *testing.T, s *store.Store, id string) []*ordjson.Object {
	t.Helper()
	returnsObj, err := ReadReturns(s, id)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := returnsObj.Get("deliveries")
	var out []*ordjson.Object
	for _, d := range list.([]any) {
		out = append(out, d.(*ordjson.Object))
	}
	return out
}

func TestPassBusySessionCostsOneObservation(t *testing.T) {
	l := newPassLab(t)
	panes := map[string]any{}
	for i := 0; i < 12; i++ {
		pane := fmt.Sprintf("w%d:p1", i)
		id := l.worker("lab", pane)
		panes[pane] = l.pane("working", l.worktree(id))
	}
	l.session("lab", map[string]any{"panes": panes})
	result, _ := l.pump(0)
	calls := l.calls()
	if len(calls) != 1 || countCalls(calls, "list") != 1 {
		t.Fatalf("calls = %v, want exactly one agent list", calls)
	}
	for pane, state := range states(result) {
		if state != "not-delivered" {
			t.Fatalf("%s state = %s, want not-delivered", pane, state)
		}
	}
	fanout, _ := result.Get("fanout")
	if calls, _ := fanout.(*ordjson.Object).Get("herdr_calls"); fmt.Sprint(calls) != "1" {
		t.Fatalf("fanout = %v", fanout)
	}
}

func TestPassIdleWorkerIsReobservedThenPrompted(t *testing.T) {
	l := newPassLab(t)
	id := l.worker("lab", "w1:p1")
	l.session("lab", map[string]any{"panes": map[string]any{"w1:p1": l.pane("idle", l.worktree(id))}})
	result, _ := l.pump(0)
	if got := states(result)["w1:p1"]; got != "submitted" {
		t.Fatalf("state = %s (%v)", got, result)
	}
	calls := l.calls()
	if len(calls) != 3 || calls[0][2] != "list" || calls[1][2] != "get" || calls[2][2] != "prompt" {
		t.Fatalf("calls = %v, want list, get, prompt", calls)
	}
}

func TestPassHungObservationTripsTheSessionWithinBudget(t *testing.T) {
	l := newPassLab(t)
	panes := map[string]any{}
	var ids []string
	for i := 0; i < 4; i++ {
		pane := fmt.Sprintf("w%d:p1", i)
		ids = append(ids, l.worker("lab", pane))
		panes[pane] = l.pane("idle", l.worktree(ids[i]))
	}
	l.session("lab", map[string]any{"panes": panes, "list_hang": 30})
	result, elapsed := l.pump(6 * time.Second)
	if elapsed > 6*time.Second {
		t.Fatalf("pass took %s, budget 6s", elapsed)
	}
	if n := countCalls(l.calls(), "list"); n != 1 || len(l.calls()) != 1 {
		t.Fatalf("calls = %v, want one hung agent list and nothing else", l.calls())
	}
	for pane, state := range states(result) {
		if state != "not-delivered" {
			t.Fatalf("%s = %s, want not-delivered", pane, state)
		}
	}
	for _, id := range ids {
		if d := deliveries(t, l.s, id); len(d) != 1 {
			t.Fatalf("%s deliveries = %v, want one not-delivered record", id, d)
		}
	}
}

func TestPassHungPromptTripsTheSession(t *testing.T) {
	l := newPassLab(t)
	panes := map[string]any{}
	for i := 0; i < 3; i++ {
		pane := fmt.Sprintf("w%d:p1", i)
		id := l.worker("lab", pane)
		panes[pane] = l.pane("idle", l.worktree(id))
	}
	l.session("lab", map[string]any{"panes": panes, "prompt_hang": 30})
	result, elapsed := l.pump(10 * time.Second)
	if elapsed > 10*time.Second {
		t.Fatalf("pass took %s, budget 10s", elapsed)
	}
	if n := countCalls(l.calls(), "prompt"); n != 1 {
		t.Fatalf("prompts = %d, want 1: %v", n, l.calls())
	}
	counts := map[string]int{}
	for _, state := range states(result) {
		counts[state]++
	}
	if counts["uncertain"] != 1 || counts["not-delivered"] != 2 {
		t.Fatalf("states = %v, want one uncertain and two not-delivered", states(result))
	}
}

func TestPassHealthySessionIsNotStarvedByAHungOne(t *testing.T) {
	l := newPassLab(t)
	hung := l.worker("hung", "w1:p1")
	healthy := l.worker("lab", "w2:p1")
	l.session("hung", map[string]any{"panes": map[string]any{"w1:p1": l.pane("idle", l.worktree(hung))}, "list_hang": 30})
	l.session("lab", map[string]any{"panes": map[string]any{"w2:p1": l.pane("idle", l.worktree(healthy))}})
	submitted := false
	for i := 0; i < 2 && !submitted; i++ {
		result, _ := l.pump(6 * time.Second)
		submitted = states(result)["w2:p1"] == "submitted"
		if got := states(result)["w1:p1"]; got != "not-delivered" && got != "quiet" && got != "deferred" {
			t.Fatalf("hung recipient = %s", got)
		}
	}
	if !submitted {
		t.Fatal("the healthy session's idle recipient was never prompted in two passes")
	}
}

func TestPassBudgetTooSmallDefersButStillPresentsInline(t *testing.T) {
	l := newPassLab(t)
	id := l.worker("lab", "w1:p1")
	l.session("lab", map[string]any{"panes": map[string]any{"w1:p1": l.pane("idle", l.worktree(id))}})
	// An open question owed to the calling coordinator: presented inline, no Herdr call needed.
	task, _ := l.s.ReadTask(id)
	open := ordjson.NewObject()
	open.Set("id", "q2")
	open.Set("status", "open")
	open.Set("created_at", "2026-09-24T00:02:00+00:00")
	questions, _ := task.Get("questions")
	task.Set("questions", append(questions.([]any), open))
	if err := l.s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	result, _ := l.pump(time.Second)
	got := states(result)
	if got["w1:p1"] != "deferred" || got["w-root:p1"] != "submitted" {
		t.Fatalf("states = %v, want the worker deferred and the inline listing submitted", got)
	}
	if len(l.calls()) != 0 {
		t.Fatalf("calls = %v, want none", l.calls())
	}
	fanout, _ := result.Get("fanout")
	if deferred, _ := fanout.(*ordjson.Object).Get("deferred"); fmt.Sprint(deferred) != "1" {
		t.Fatalf("deferred = %v", deferred)
	}
	for _, d := range deliveries(t, l.s, id) {
		if obligations, _ := d.Get("obligations"); strings.Contains(fmt.Sprint(obligations), "answer:") {
			t.Fatalf("deferred answer has a delivery record: %v", d)
		}
	}
}

func TestPassFairnessReachesADeferredRecipientNext(t *testing.T) {
	l := newPassLab(t)
	ObserveTimeout, PromptTimeout = time.Second, 2*time.Second
	a := l.worker("lab", "w1:p1")
	b := l.worker("lab", "w2:p1")
	l.session("lab", map[string]any{"prompt_hang": 1, "panes": map[string]any{
		"w1:p1": l.pane("idle", l.worktree(a)), "w2:p1": l.pane("idle", l.worktree(b))}})
	first, _ := l.pump(5 * time.Second)
	one := states(first)
	var done, waiting string
	for pane, state := range one {
		switch state {
		case "submitted":
			done = pane
		case "deferred":
			waiting = pane
		}
	}
	if done == "" || waiting == "" {
		t.Fatalf("first pass = %v, want one submitted and one deferred", one)
	}
	second, _ := l.pump(5 * time.Second)
	if got := states(second)[waiting]; got != "submitted" {
		t.Fatalf("second pass = %v, want %s submitted first", states(second), waiting)
	}
}

func TestPassNeverPromptsARecipientReboundAfterTheSnapshot(t *testing.T) {
	l := newPassLab(t)
	id := l.worker("lab", "w1:p1")
	l.session("lab", map[string]any{"panes": map[string]any{
		"w1:p1": l.pane("idle", l.worktree(id)), "w1:p9": l.pane("idle", l.worktree(id))}})
	stale, err := l.s.AllTasks()
	if err != nil {
		t.Fatal(err)
	}
	task, _ := l.s.ReadTask(id)
	task.Set("pane", "w1:p9")
	if err := l.s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	result, err := Pump(l.s, PumpOpts{RuntimeRoot: l.root, SumctlPath: "sumctl", Ctx: l.ctx, Inline: true, Snapshot: stale})
	if err != nil {
		t.Fatal(err)
	}
	if countCalls(l.calls(), "prompt") != 0 {
		t.Fatalf("calls = %v, want no prompt to the old pane", l.calls())
	}
	if got := states(result)["w1:p1"]; got != "quiet" {
		t.Fatalf("stale route state = %s", got)
	}
	if d := deliveries(t, l.s, id); len(d) != 0 {
		t.Fatalf("deliveries = %v, want none for the stale route", d)
	}
}

func TestPassNeverStampsAnObligationClosedAfterTheSnapshot(t *testing.T) {
	l := newPassLab(t)
	id := l.worker("lab", "w1:p1")
	task, _ := l.s.ReadTask(id)
	question := ordjson.NewObject()
	question.Set("id", "q1")
	question.Set("status", "open")
	question.Set("created_at", "2026-09-24T00:00:00+00:00")
	task.Set("questions", []any{question})
	if err := l.s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	stale, _ := l.s.AllTasks()
	question.Set("status", "applied")
	task.Set("questions", []any{question})
	if err := l.s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := Pump(l.s, PumpOpts{RuntimeRoot: l.root, SumctlPath: "sumctl", Ctx: l.ctx, Inline: true, Snapshot: stale}); err != nil {
		t.Fatal(err)
	}
	if d := deliveries(t, l.s, id); len(d) != 0 {
		t.Fatalf("deliveries = %v, want nothing stamped for a closed question", d)
	}
}

func TestPassDefersWhileAnotherPassHoldsTheDeliveryLock(t *testing.T) {
	l := newPassLab(t)
	id := l.worker("lab", "w1:p1")
	l.session("lab", map[string]any{"panes": map[string]any{"w1:p1": l.pane("idle", l.worktree(id))}})
	// An older release's pass holds the compatibility lock exclusively (a second Store stands in for its process).
	legacy, err := store.Open(l.s.Home)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := legacy.DeliveryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	result, elapsed := l.pump(time.Second)
	if elapsed > 3*time.Second {
		t.Fatalf("pass waited %s for a held lock", elapsed)
	}
	if got := states(result)["w1:p1"]; got != "deferred" {
		t.Fatalf("state = %s", got)
	}
	if len(l.calls()) != 0 {
		t.Fatalf("calls = %v", l.calls())
	}
}

// A pass records every attempt in the returns sidecar only: the task records it delivers for are byte-identical
// afterwards, and the legacy `notice` view is derived from the sidecar for each outcome.
func TestPassRecordsAttemptsOnlyInTheReturnsSidecar(t *testing.T) {
	l := newPassLab(t)
	submitted := l.worker("lab", "w1:p1")
	busy := l.worker("lab", "w2:p1")
	timedOut := l.worker("slow", "w3:p1")
	// The root owes itself an open question from the submitted task: presented inline to the caller.
	task, err := l.s.ReadTask(submitted)
	if err != nil {
		t.Fatal(err)
	}
	questions, _ := task.Get("questions")
	open := ordjson.NewObject()
	open.Set("id", "q2")
	open.Set("status", "open")
	open.Set("created_at", "2026-09-24T00:02:00+00:00")
	task.Set("questions", append(questions.([]any), open))
	task.Set("notice", nil)
	if err := l.s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	l.session("lab", map[string]any{"panes": map[string]any{
		"w1:p1": l.pane("idle", l.worktree(submitted)),
		"w2:p1": l.pane("working", l.worktree(busy)),
	}})
	l.session("slow", map[string]any{"panes": map[string]any{"w3:p1": l.pane("idle", l.worktree(timedOut))}, "prompt_hang": 3})
	before := map[string][]byte{}
	for _, id := range []string{submitted, busy, timedOut} {
		raw, err := os.ReadFile(filepath.Join(l.s.Tasks, id, "task.json"))
		if err != nil {
			t.Fatal(err)
		}
		before[id] = raw
	}
	result, _ := l.pump(0)
	got := states(result)
	if got["w1:p1"] != "submitted" || got["w2:p1"] != "not-delivered" || got["w3:p1"] != "uncertain" || got["w-root:p1"] != "submitted" {
		t.Fatalf("states = %v", got)
	}
	want := map[string]string{submitted: "submitted-not-acknowledged", busy: "pending", timedOut: "uncertain"}
	for id, raw := range before {
		after, err := os.ReadFile(filepath.Join(l.s.Tasks, id, "task.json"))
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(raw) {
			t.Fatalf("%s task.json was rewritten by the pass:\nbefore %s\nafter  %s", id, raw, after)
		}
		if len(deliveries(t, l.s, id)) == 0 {
			t.Fatalf("%s: no delivery recorded in the sidecar", id)
		}
		task, err := l.s.ReadTask(id)
		if err != nil {
			t.Fatal(err)
		}
		notice, _ := NoticeOf(l.s, task).(*ordjson.Object)
		if notice == nil {
			t.Fatalf("%s: no derived notice", id)
		}
		if status, _ := notice.Get("status"); status != want[id] {
			t.Fatalf("%s notice status = %v, want %s (%v)", id, status, want[id], notice)
		}
	}
}
