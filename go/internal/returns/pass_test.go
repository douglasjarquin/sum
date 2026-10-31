package returns

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crypto/sha256"
	"encoding/hex"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launchtest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"sync"
)

type heldNative struct {
	entered <-chan struct{}
	release func()
}
type passLab struct {
	t          *testing.T
	s          *store.Store
	host, root string
	ctx        *ordjson.Object
	tasks      int
	cores      map[string]*hctest.Fixture
	endpoints  map[string]store.Endpoint
	holds      map[string]heldNative
	callStart  map[string]int
}

var testAliases sync.Map

func newPassLab(t *testing.T) *passLab {
	t.Helper()
	physical, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(physical, "home"))
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Init(); err != nil {
		t.Fatal(err)
	}
	host, err := st.Machine()
	if err != nil {
		t.Fatal(err)
	}
	l := &passLab{t: t, s: st, host: host.ID, root: physical, cores: map[string]*hctest.Fixture{}, endpoints: map[string]store.Endpoint{}, holds: map[string]heldNative{}, callStart: map[string]int{}}
	e := l.nativeEndpoint("lab", "w-root:p1")
	e.Role = "coordinator"
	e.Task = ""
	e.Cwd = physical
	l.endpoints["lab/w-root:p1"] = e
	l.ctx = e.Object()
	owner := l.bind(e)
	if err = ordjson.WriteFile(filepath.Join(st.Home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	old := [2]time.Duration{ObserveTimeout, PromptTimeout}
	ObserveTimeout, PromptTimeout = time.Second, time.Second
	t.Cleanup(func() { ObserveTimeout, PromptTimeout = old[0], old[1] })
	l.resetCalls()
	return l
}
func (l *passLab) core(session string) *hctest.Fixture {
	f := l.cores[session]
	if f == nil {
		f = hctest.New(l.t)
		l.cores[session] = f
	}
	return f
}
func (l *passLab) nativeEndpoint(session, alias string) store.Endpoint {
	if e, ok := l.endpoints[session+"/"+alias]; ok {
		return e
	}
	f := l.core(session)
	sum := sha256.Sum256([]byte(alias))
	address := "t" + hex.EncodeToString(sum[:6])
	e := store.Endpoint{Schema: 2, Backend: "hc", Machine: l.host, CoreID: f.CoreID, Session: session, Socket: f.Socket, Address: address, Role: "worker", Attempt: "attempt-" + address, BindingGeneration: 1, CreatedMS: 100, Cwd: l.root}
	l.endpoints[session+"/"+alias] = e
	testAliases.Store(f.CoreID+"/"+address, alias)
	return e
}
func (l *passLab) bind(e store.Endpoint) *ordjson.Object {
	f := l.core(e.Session)
	pid := uint32(4242)
	spec, config, options := launchtest.QualifyCodex(l.t, f, e.Cwd)
	submit := "tab"
	terminal := hcclient.Terminal{Address: e.Address, State: "live", CreatedMS: e.CreatedMS, Cwd: e.Cwd, PID: &pid, Foreground: &hcclient.Foreground{PID: 4242, Name: "codex", Argv: spec.Argv, Cwd: e.Cwd}, Argv: spec.Argv, Template: &spec.Name, Submit: &submit, Status: &hcclient.Status{State: "idle"}}
	f.SetTerminal(terminal)
	var task any
	if e.Task != "" {
		task = e.Task
	}
	record, err := l.s.Register(e, e.Role, task, incarnation.NativeEvidence{Endpoint: e.Native(), PID: pid, Foreground: terminal.Foreground}.Record(store.Now()))
	if err != nil {
		l.t.Fatal(err)
	}
	kind := map[string]string{"worker": "worker-init", "coordinator": "coordinator-init"}[e.Role]
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: e.Native(), Kind: kind, Attempt: e.Attempt})
	record.Set("native_receipt", receipt)
	qualified, _ := ordjson.FromValue(NativeTemplate{Spec: spec, ConfigHome: config, Options: options})
	record.Set("native_template", qualified)
	if err = ordjson.WriteFile(filepath.Join(l.s.Sessions, store.RegistrationKey(e)+".json"), record); err != nil {
		l.t.Fatal(err)
	}
	return record
}
func (l *passLab) resetCalls() {
	for session, f := range l.cores {
		l.callStart[session] = len(f.Calls())
	}
}
func (l *passLab) session(name string, conf map[string]any) {
	f := l.core(name)
	panes, _ := conf["panes"].(map[string]any)
	existing := map[string]hcclient.Terminal{}
	for _, terminal := range f.Terminals() {
		existing[terminal.Address] = terminal
	}
	for alias, raw := range panes {
		data, _ := raw.(map[string]any)
		e := l.nativeEndpoint(name, alias)
		terminal, ok := existing[e.Address]
		if !ok {
			l.bind(e)
			for _, candidate := range f.Terminals() {
				if candidate.Address == e.Address {
					terminal = candidate
					break
				}
			}
		}
		status, _ := data["agent_status"].(string)
		terminal.Status = &hcclient.Status{State: status}
		if cwd, ok := data["cwd"].(string); ok {
			terminal.Cwd = cwd
		}
		if shell, ok := data["shell_pid"].(int); ok {
			pid := uint32(shell)
			terminal.PID = &pid
			terminal.Foreground = &hcclient.Foreground{PID: int32(pid), Name: "shell"}
		}
		for _, field := range []string{"terminal_id", "get_terminal_id"} {
			if native, ok := data[field].(string); ok && native != "term-"+alias {
				terminal.CreatedMS++
			}
		}
		f.SetTerminal(terminal)
	}
	if hold, _ := conf["hold"].(map[string]any); hold != nil {
		for alias, raw := range hold {
			verb := fmt.Sprint(raw)
			method := map[string]string{"get": "terminal.get", "prompt": "terminal.send"}[verb]
			entered, release := f.HoldNext(method, l.nativeEndpoint(name, alias).Address)
			l.holds[verb+"/"+alias] = heldNative{entered, release}
		}
	}
	if seconds, ok := conf["list_hang"].(int); ok && seconds > 0 {
		f.QueueFault("terminal.list", hctest.Fault{Delay: time.Duration(seconds) * time.Second})
	}
	if seconds, ok := conf["prompt_hang"].(int); ok && seconds > 0 {
		f.QueueFault("terminal.send", hctest.Fault{Delay: time.Duration(seconds) * time.Second})
	}
}
func (l *passLab) worker(session, alias string) string {
	l.t.Helper()
	l.tasks++
	id := fmt.Sprintf("t-%012x", l.tasks)
	worktree := l.worktree(id)
	if err := os.MkdirAll(worktree, 0700); err != nil {
		l.t.Fatal(err)
	}
	e := l.nativeEndpoint(session, alias)
	e.Task = id
	e.Cwd = worktree
	l.endpoints[session+"/"+alias] = e
	l.bind(e)
	task := ordjson.NewObject()
	task.Set("schema", jsonInt(2))
	task.Set("id", id)
	task.Set("status", "running")
	task.Set("machine", l.host)
	task.Set("worktree", worktree)
	task.Set("worker_endpoint", e.Object())
	task.Set("parent", l.ctx)
	q := ordjson.NewObject()
	q.Set("id", "q1")
	q.Set("status", "answered")
	q.Set("created_at", "2026-09-24T00:00:00+00:00")
	q.Set("answered_at", "2026-09-24T00:01:00+00:00")
	task.Set("questions", []any{q})
	if err := l.s.SaveTask(task); err != nil {
		l.t.Fatal(err)
	}
	return id
}
func (l *passLab) pane(status, cwd string) map[string]any {
	return map[string]any{"agent_status": status, "cwd": cwd}
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
func (l *passLab) calls() []hctest.Call {
	out := []hctest.Call{}
	for session, f := range l.cores {
		calls := f.Calls()
		for _, call := range calls[l.callStart[session]:] {
			if call.Method != "hello" && call.Method != "templates.list" {
				out = append(out, call)
			}
		}
	}
	return out
}
func countCalls(calls []hctest.Call, method string) int {
	method = map[string]string{"get": "terminal.get", "list": "terminal.list", "prompt": "terminal.send"}[method]
	n := 0
	for _, call := range calls {
		if call.Method == method {
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
		address, _ := rec.(*ordjson.Object).Get("address")
		core, _ := rec.(*ordjson.Object).Get("core_id")
		pane, _ := testAliases.Load(fmt.Sprint(core) + "/" + fmt.Sprint(address))
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
	if calls, _ := fanout.(*ordjson.Object).Get("native_calls"); fmt.Sprint(calls) != "1" {
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
	if len(calls) != 3 || calls[0].Method != "terminal.list" || calls[1].Method != "terminal.get" || calls[2].Method != "terminal.send" {
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

func TestPassHungPromptDoesNotStarveOtherRecipients(t *testing.T) {
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
	if n := countCalls(l.calls(), "prompt"); n != 3 {
		t.Fatalf("prompts = %d, want three independently serialized recipients: %v", n, l.calls())
	}
	counts := map[string]int{}
	for _, state := range states(result) {
		counts[state]++
	}
	if counts["uncertain"] != 1 || counts["submitted"] != 2 {
		t.Fatalf("states = %v, want one uncertain and two unrelated submissions", states(result))
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
	route := asNativeRoute(task)
	route.Set("address", "t999")
	task.Set("worker_endpoint", route)
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

func TestPassDoesNotWaitForTheDeliveryLockOnFirstVisit(t *testing.T) {
	l := newPassLab(t)
	id := l.worker("lab", "w1:p1")
	l.session("lab", map[string]any{"panes": map[string]any{"w1:p1": l.pane("idle", l.worktree(id))}})
	legacy, err := store.Open(l.s.Home)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := legacy.DeliveryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	result, _ := l.pump(0)
	if got := states(result)["w1:p1"]; got != "deferred" {
		t.Fatalf("state = %s", got)
	}
	var wait int
	if _, err := fmt.Sscan(fanoutField(result, "lock_wait_ms"), &wait); err != nil {
		t.Fatal(err)
	}
	if wait > 100 {
		t.Fatalf("lock_wait_ms = %d, want a single non-blocking attempt", wait)
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
	timedOutTask, err := l.s.ReadTask(timedOut)
	if err != nil {
		t.Fatal(err)
	}
	if errText, _ := NoticeOf(l.s, timedOutTask).(*ordjson.Object).Get("error"); !strings.Contains(fmt.Sprint(errText), "native input may have been submitted") {
		t.Fatalf("uncertain notice error = %v, want the recorded timeout reason", errText)
	}
}

// SetNotice gives a task view the derived notice, keeps a recorded null, and adds no key to a record that never
// carried the field and has no attempt to project.
func TestSetNoticeKeepsTheRecordedKeyShape(t *testing.T) {
	l := newPassLab(t)
	id := l.worker("lab", "w1:p1")
	task, err := l.s.ReadTask(id)
	if err != nil {
		t.Fatal(err)
	}
	view := ordjson.NewObject()
	SetNotice(l.s, task, view)
	if _, has := view.Get("notice"); has {
		t.Fatalf("view of a record without the field gained notice = %v", view)
	}
	task.Set("notice", nil)
	SetNotice(l.s, task, view)
	if notice, has := view.Get("notice"); !has || notice != nil {
		t.Fatalf("recorded null notice = %v (present %v), want a null key", notice, has)
	}
	l.session("lab", map[string]any{"panes": map[string]any{"w1:p1": l.pane("idle", l.worktree(id))}})
	l.pump(0)
	SetNotice(l.s, task, view)
	derived, _ := view.Get("notice")
	if status, _ := derived.(*ordjson.Object).Get("status"); status != "submitted-not-acknowledged" {
		t.Fatalf("derived notice status = %v", status)
	}
}

func asNativeRoute(task *ordjson.Object) *ordjson.Object {
	raw, _ := task.Get("worker_endpoint")
	return cloneObject(raw.(*ordjson.Object))
}

func (l *passLab) replaceOwner() {
	e := l.nativeEndpoint("lab", "w-root:p1")
	e.Attempt += "-next"
	e.BindingGeneration++
	l.endpoints["lab/w-root:p1"] = e
	record := l.bind(e)
	record.Set("wake_protocol", jsonInt(1))
	if err := ordjson.WriteFile(filepath.Join(l.s.Home, "context.json"), record); err != nil {
		l.t.Fatal(err)
	}
	l.ctx = e.Object()
	tasks, err := l.s.AllTasks()
	if err != nil {
		l.t.Fatal(err)
	}
	for _, task := range tasks {
		task.Set("parent", e.Object())
		if err = l.s.SaveTask(task); err != nil {
			l.t.Fatal(err)
		}
	}
}
