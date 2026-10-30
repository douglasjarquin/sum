package returns

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launchtest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeSubmissionUsesGuardAndKeepsEffectUncertain(t *testing.T) {
	f := hctest.New(t)
	pid := uint32(101)
	f.SetTerminal(hcclient.Terminal{Address: "t1", State: "live", CreatedMS: 10, PID: &pid, Foreground: &hcclient.Foreground{PID: 101, Name: "codex"}, Status: &hcclient.Status{State: "idle"}})
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: "m", CoreID: f.CoreID, Session: "lab", Socket: f.Socket, Address: "t1", Role: "worker", Task: "task", Attempt: "a1", BindingGeneration: 1, CreatedMS: 10}
	r, err := ObserveNative(context.Background(), endpoint, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Send(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	for _, call := range f.Calls() {
		if call.Method == "terminal.send" {
			if call.Params["if"] != "idle" {
				t.Fatalf("unguarded input: %#v", call.Params)
			}
			if _, ok := call.Params["force"]; ok {
				t.Fatal("force supplied")
			}
		}
	}
	f.SetStatus("t1", "idle")
	r, err = ObserveNative(context.Background(), endpoint, "")
	if err != nil {
		t.Fatal(err)
	}
	f.QueueFault("terminal.send", hctest.Fault{LoseResponse: true})
	_, err = r.Send(context.Background(), "second")
	state, _ := promptFailure(err)
	if state != "uncertain" {
		t.Fatalf("lost response state %s: %v", state, err)
	}
	if len(f.Inputs("t1")) != 2 {
		t.Fatal("send was replayed or lost")
	}
}

func TestNativeObservationDefersUnsafeStatusAndReplacement(t *testing.T) {
	for _, status := range []string{"working", "blocked", "unknown", "done"} {
		t.Run(status, func(t *testing.T) {
			f := hctest.New(t)
			f.SetTerminal(hcclient.Terminal{Address: "t1", State: "live", CreatedMS: 10, Status: &hcclient.Status{State: status}})
			e := store.Endpoint{Schema: 2, Backend: "hc", Machine: "m", CoreID: f.CoreID, Session: "lab", Socket: f.Socket, Address: "t1", Role: "worker", Attempt: "a", BindingGeneration: 1, CreatedMS: 10}
			r, err := ObserveNative(context.Background(), e, "")
			if err == nil && status != "done" {
				t.Fatal("unsafe status admitted")
			}
			if err == nil {
				if _, err = r.Send(context.Background(), "hello"); err == nil {
					t.Fatal("unqualified Done input admitted")
				}
			}
			if len(f.Inputs("t1")) != 0 {
				t.Fatal("unsafe input delivered")
			}
		})
	}
}

// The fixture exercises the real Pump ledger boundary over the native socket.
type nativeLab struct {
	t     *testing.T
	f     *hctest.Fixture
	s     *store.Store
	owner store.Endpoint
	next  int
}

func newNativeLab(t *testing.T) *nativeLab {
	t.Helper()
	f := hctest.New(t)
	physical, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(physical, "home"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	l := &nativeLab{t: t, f: f, s: s}
	l.owner = store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Session: "lab", Socket: f.Socket, Address: "t0", Role: "coordinator", Attempt: "coord-1", BindingGeneration: 1, CreatedMS: 100, Cwd: t.TempDir()}
	record := l.bind(l.owner)
	record.Set("wake_protocol", jsonInt(contract.WakeProtocol))
	if err = ordjson.WriteFile(filepath.Join(s.Home, "context.json"), record); err != nil {
		t.Fatal(err)
	}
	return l
}
func (l *nativeLab) bind(e store.Endpoint) *ordjson.Object {
	l.t.Helper()
	pid := uint32(100 + e.CreatedMS)
	terminal := hcclient.Terminal{Address: e.Address, State: "live", CreatedMS: e.CreatedMS, Cwd: e.Cwd, PID: &pid, Foreground: &hcclient.Foreground{PID: int32(pid), Name: "codex", Argv: []string{"codex", "--no-daemon"}, Cwd: e.Cwd}, Status: &hcclient.Status{State: "idle"}}
	spec, config, options := launchtest.QualifyCodex(l.t, l.f, e.Cwd)
	terminal.Template = &spec.Name
	submit := "tab"
	terminal.Submit = &submit
	terminal.Argv = spec.Argv
	l.f.SetTerminal(terminal)
	evidence := incarnation.NativeEvidence{Endpoint: e.Native(), PID: pid, Foreground: terminal.Foreground}.Record(store.Now())
	var task any
	if e.Task != "" {
		task = e.Task
	}
	record, err := l.s.Register(e, e.Role, task, evidence)
	if err != nil {
		l.t.Fatal(err)
	}
	kind := map[string]string{"worker": "worker-init", "coordinator": "coordinator-init"}[e.Role]
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: e.Native(), Kind: kind, Attempt: e.Attempt})
	record.Set("native_receipt", receipt)
	templateRecord, _ := ordjson.FromValue(NativeTemplate{Spec: spec, ConfigHome: config, Options: options})
	record.Set("native_template", templateRecord)
	if err = ordjson.WriteFile(filepath.Join(l.s.Sessions, store.RegistrationKey(e)+".json"), record); err != nil {
		l.t.Fatal(err)
	}
	return record
}
func (l *nativeLab) task(kind string) (*ordjson.Object, store.Endpoint) {
	l.t.Helper()
	l.next++
	id := fmt.Sprintf("t-%012x", l.next)
	e := l.owner
	e.Role = "worker"
	e.Task = id
	e.Address = fmt.Sprintf("t%d", l.next)
	e.Attempt = fmt.Sprintf("worker-%d", l.next)
	e.CreatedMS = uint64(100 + l.next)
	e.Cwd = l.t.TempDir()
	l.bind(e)
	task := ordjson.NewObject()
	task.Set("schema", jsonInt(2))
	task.Set("id", id)
	task.Set("status", "running")
	task.Set("machine", e.Machine)
	task.Set("worktree", e.Cwd)
	task.Set("worker_endpoint", e.Object())
	task.Set("parent", l.owner.Object())
	if kind == "report" {
		item := ordjson.NewObject()
		item.Set("id", "r1")
		item.Set("kind", "report")
		item.Set("at", store.Now())
		task.Set("evidence", []any{item})
	} else {
		q := ordjson.NewObject()
		q.Set("id", "q1")
		q.Set("status", kind)
		q.Set("key", "k1")
		q.Set("created_at", store.Now())
		q.Set("answered_at", store.Now())
		task.Set("questions", []any{q})
	}
	if err := l.s.SaveTask(task); err != nil {
		l.t.Fatal(err)
	}
	return task, e
}
func (l *nativeLab) pump(recipient string) *ordjson.Object {
	l.t.Helper()
	result, err := Pump(l.s, PumpOpts{Recipient: recipient, SumctlPath: "/sumctl", RuntimeRoot: "/not-live", RetryStalled: true})
	if err != nil {
		l.t.Fatal(err)
	}
	return result
}

func TestNativePumpBusyAnswerThenSubmissionNeedsApplicationReceipt(t *testing.T) {
	l := newNativeLab(t)
	task, e := l.task("answered")
	l.f.SetStatus(e.Address, "working")
	l.pump("worker")
	if len(l.f.Inputs(e.Address)) != 0 {
		t.Fatal("busy worker received input")
	}
	l.f.SetStatus(e.Address, "idle")
	l.pump("worker")
	l.pump("worker")
	if len(l.f.Inputs(e.Address)) != 1 {
		t.Fatal("answer missing or replayed")
	}
	open, err := OpenObligations(l.s, task)
	if err != nil || len(open) != 1 {
		t.Fatalf("native submission closed the answer: %v %v", open, err)
	}
}
func TestNativePumpLostResponseNeverReplays(t *testing.T) {
	l := newNativeLab(t)
	_, e := l.task("answered")
	l.f.QueueFault("terminal.send", hctest.Fault{LoseResponse: true})
	result := l.pump("worker")
	if got := fmt.Sprint(routeValue(routeValue(result, "recipients").([]any)[0].(*ordjson.Object), "state")); got != "uncertain" {
		t.Fatalf("state %s", got)
	}
	l.f.SetStatus(e.Address, "idle")
	l.pump("worker")
	if len(l.f.Inputs(e.Address)) != 1 {
		t.Fatal("uncertain input replayed")
	}
}
func TestNativePumpCoalescesRoutineWakeAndPreservesUrgentDecision(t *testing.T) {
	l := newNativeLab(t)
	for i := 0; i < 12; i++ {
		l.task("report")
	}
	l.pump("parent")
	l.f.SetStatus(l.owner.Address, "idle")
	l.pump("parent")
	if len(l.f.Inputs(l.owner.Address)) != 1 {
		t.Fatal("routine wake storm")
	}
	l.task("open")
	l.pump("parent")
	if len(l.f.Inputs(l.owner.Address)) != 2 {
		t.Fatal("urgent question suppressed")
	}
	l.f.SetStatus(l.owner.Address, "idle")
	l.pump("parent")
	if len(l.f.Inputs(l.owner.Address)) != 2 {
		t.Fatal("decision repeated")
	}
}
func TestNativePumpRejectsForegroundReplacement(t *testing.T) {
	l := newNativeLab(t)
	_, e := l.task("answered")
	l.f.SetForeground(e.Address, hcclient.Foreground{PID: 999, Name: "shell"})
	l.pump("worker")
	if len(l.f.Inputs(e.Address)) != 0 {
		t.Fatal("replacement inherited worker authority")
	}
}

func TestNativeQualifiedDoneUsesOrdinarySendAndRejectsChangedProvenance(t *testing.T) {
	l := newNativeLab(t)
	_, endpoint := l.task("answered")
	l.f.SetStatus(endpoint.Address, "done")
	recipient, err := ObserveNative(context.Background(), endpoint, endpoint.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	record, err := l.s.Registration(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err = recipient.Qualify(record); err != nil {
		t.Fatal(err)
	}
	l.f.SetStatus(endpoint.Address, "working") // qualified Codex queues this raced submission
	if _, err = recipient.Send(context.Background(), "follow up"); err != nil {
		t.Fatal(err)
	}
	for _, call := range l.f.Calls() {
		if call.Method == "terminal.send" {
			if _, ok := call.Params["if"]; ok {
				t.Fatal("Done path used unsupported guard")
			}
		}
	}
	raw, _ := record.Get("native_template")
	data, _ := ordjson.MarshalCompact(raw)
	var template NativeTemplate
	if err = json.Unmarshal(data, &template); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(template.Spec.Binary, []byte("changed binary"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = recipient.Qualify(record); err == nil {
		t.Fatal("changed command provenance accepted")
	}
}

func TestNativePumpDoesNotClaimNewEffectsDuringTerminalMaintenance(t *testing.T) {
	for _, state := range []string{"claimed", "uncertain"} {
		t.Run(state, func(t *testing.T) {
			l := newNativeLab(t)
			task, e := l.task("answered")
			maintenance := ordjson.NewObject()
			maintenance.Set("state", state)
			task.Set("terminal_maintenance", maintenance)
			if err := l.s.SaveTask(task); err != nil {
				t.Fatal(err)
			}
			l.pump("worker")
			if len(l.f.Inputs(e.Address)) != 0 {
				t.Fatal("notification raced terminal maintenance")
			}
			if attempts := deliveries(t, l.s, e.Task); len(attempts) != 0 {
				t.Fatal("maintenance-blocked notification was stamped")
			}
		})
	}
}

func TestNativeInlineReadAllowsWorkingCoordinatorWithoutSending(t *testing.T) {
	l := newNativeLab(t)
	l.task("report")
	l.f.SetStatus(l.owner.Address, "working")
	result, err := Pump(l.s, PumpOpts{Recipient: "parent", Ctx: l.owner.Object(), Inline: true})
	if err != nil {
		t.Fatal(err)
	}
	recipients, _ := result.Get("recipients")
	rows, _ := recipients.([]any)
	if len(rows) != 1 {
		t.Fatalf("recipients = %v", recipients)
	}
	row := rows[0].(*ordjson.Object)
	if via, _ := row.Get("via"); via != "inline" {
		t.Fatalf("working caller read = %v", row)
	}
	if len(l.f.Inputs(l.owner.Address)) != 0 {
		t.Fatal("inline read sent native input")
	}
}
