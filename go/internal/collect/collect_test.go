package collect

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/helpview"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launchtest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestCollectHelpDescribesBoundedRead(t *testing.T) {
	view, err := helpview.View("", "collect")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ordjson.MarshalTOON(view); err != nil {
		t.Fatal(err)
	}
	if get(view, "budget") == nil {
		t.Fatal("collect help omits budget")
	}
}

func TestCollectOpeningNeedsExplicitRedeliveryUntilWorkerInit(t *testing.T) {
	l := newLab(t)
	worker, _ := reservations.Worker(l.task)
	native := ordjson.NewObject()
	native.Set("effect", "created")
	first := ordjson.NewObject()
	first.Set("sent", false)
	native.Set("first_message", first)
	worker.Set("native_launch", native)
	l.save(t)
	out := l.run(t, 50*time.Millisecond)
	if !hasKind(out, "launch-uncertain") {
		t.Fatal("opening uncertainty disappeared")
	}
	next, _ := get(out, "next").([]any)
	if len(next) < 5 || next[4] != "redeliver" || next[1] != "--home" || next[2] != l.s.Home {
		t.Fatalf("next=%v", next)
	}
	worker.Set("native_receipt", ordjson.NewObject())
	l.save(t)
	out = l.run(t, 50*time.Millisecond)
	if hasKind(out, "launch-uncertain") {
		t.Fatal("initialized worker still requires opening redelivery")
	}
}

type lab struct {
	s        *store.Store
	token    string
	task     *ordjson.Object
	f        *hctest.Fixture
	endpoint store.Endpoint
}

func newLab(t *testing.T) lab {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "collect conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	f := hctest.New(t)
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	e := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Socket: f.Socket, Session: "sum-test-collect", Address: "t1", CreatedMS: 1, Cwd: cwd, Role: "worker", Task: "t-aaaaaaaaaaaa", Attempt: "x-aaaaaaaaaaaa", BindingGeneration: 1}
	pid := uint32(101)
	terminal := hcclient.Terminal{Address: e.Address, CreatedMS: e.CreatedMS, Cwd: cwd, State: "live", PID: &pid, Foreground: &hcclient.Foreground{PID: 102, Name: "codex", Cwd: cwd}, Status: &hcclient.Status{State: "working"}}
	f.SetTerminal(terminal)
	task := ordjson.NewObject()
	task.Set("schema", 2)
	task.Set("id", e.Task)
	task.Set("status", "running")
	task.Set("machine", host.ID)
	task.Set("owner", session.Owner())
	task.Set("parent", nil)
	task.Set("worktree", cwd)
	task.Set("worker_endpoint", e.Object())
	task.Set("questions", []any{})
	task.Set("evidence", []any{})
	worker, err := reservations.NewAttempt("worker", session.Owner(), cwd, store.Now(), "running", nil)
	if err != nil {
		t.Fatal(err)
	}
	worker.Set("id", e.Attempt)
	task.Set("execution", reservations.NewExecution(worker))
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	return lab{s: s, token: session.Token(), task: task, f: f, endpoint: e}
}

func (l lab) save(t *testing.T) {
	t.Helper()
	if err := l.s.SaveTask(l.task); err != nil {
		t.Fatal(err)
	}
}
func question(id, status string) *ordjson.Object {
	q := ordjson.NewObject()
	q.Set("id", id)
	q.Set("status", status)
	q.Set("key", id)
	q.Set("text", "Continue?")
	q.Set("answer", "yes")
	q.Set("created_at", store.Now())
	q.Set("answered_at", store.Now())
	return q
}
func get(o *ordjson.Object, key string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	return v
}
func items(o *ordjson.Object) []any { v, _ := get(o, "items").([]any); return v }
func hasKind(o *ordjson.Object, kind string) bool {
	for _, v := range items(o) {
		if row, ok := v.(*ordjson.Object); ok && get(row, "kind") == kind {
			return true
		}
	}
	return false
}
func (l lab) run(t *testing.T, budget time.Duration) *ordjson.Object {
	t.Helper()
	out, err := Run(context.Background(), l.s, Options{Claim: l.token, Budget: budget, RuntimeRoot: "/not-live", SumctlPath: "/sumctl"})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCollectQuestionIsImmediateAndReadingSettlesNothing(t *testing.T) {
	l := newLab(t)
	l.task.Set("questions", []any{question("q1", "open")})
	l.save(t)
	path, _ := l.s.TaskPath(l.endpoint.Task)
	before, err := os.ReadFile(filepath.Join(path, "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		out := l.run(t, time.Second)
		if !hasKind(out, "question") || get(out, "deadline_hit") != false {
			t.Fatalf("question collect=%v", out)
		}
	}
	after, _ := os.ReadFile(filepath.Join(path, "task.json"))
	if string(before) != string(after) {
		t.Fatal("reading a question mutated the ledger")
	}
	for _, call := range l.f.Calls() {
		if call.Method == "terminal.wait" || call.Method == "terminal.send" || call.Method == "terminal.run" {
			t.Fatalf("question unnecessarily waited or prompted: %v", call)
		}
	}
	l.task.Set("questions", []any{question("q1", "applied")})
	l.save(t)
	if out := l.run(t, 50*time.Millisecond); hasKind(out, "question") {
		t.Fatal("settled question stayed actionable")
	}
}

func TestCollectWaitsWithinBudgetAndNeverEnsuresHC(t *testing.T) {
	l := newLab(t)
	started := time.Now()
	out := l.run(t, 80*time.Millisecond)
	if get(out, "deadline_hit") != true || len(items(out)) != 0 || time.Since(started) > time.Second {
		t.Fatalf("unbounded collection: %v elapsed=%s", out, time.Since(started))
	}
	waits := 0
	for _, call := range l.f.Calls() {
		if call.Method == "terminal.wait" {
			waits++
		}
		if call.Method == "terminal.run" {
			t.Fatal("collect started a core or terminal")
		}
	}
	if waits == 0 {
		t.Fatal("collect did not wait on recorded worker")
	}
}

func TestCollectSeesReviewAndWorkerFailureDuringWait(t *testing.T) {
	for _, kind := range []string{"review", "worker-failure"} {
		t.Run(kind, func(t *testing.T) {
			l := newLab(t)
			entered, release := l.f.HoldReplyNext("terminal.wait", l.endpoint.Address)
			defer release()
			done := make(chan *ordjson.Object, 1)
			errs := make(chan error, 1)
			go func() {
				out, err := Run(context.Background(), l.s, Options{Claim: l.token, Budget: time.Second, RuntimeRoot: "/not-live", SumctlPath: "/sumctl"})
				if err != nil {
					errs <- err
					return
				}
				done <- out
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("collect did not reach wait")
			}
			if kind == "review" {
				other, err := store.Open(l.s.Root)
				if err != nil {
					t.Fatal(err)
				}
				unlock, err := other.Lock()
				if err != nil {
					t.Fatal(err)
				}
				task, err := other.ReadTask(l.endpoint.Task)
				if err != nil {
					t.Fatal(err)
				}
				review := ordjson.NewObject()
				review.Set("id", "review-1")
				review.Set("kind", "review")
				review.Set("verdict", "pass")
				review.Set("candidate", "candidate")
				review.Set("at", store.Now())
				task.Set("evidence", []any{review})
				if err := other.SaveTask(task); err != nil {
					t.Fatal(err)
				}
				unlock()
			} else {
				l.f.Exit(l.endpoint.Address, 1)
			}
			release()
			select {
			case out := <-done:
				if !hasKind(out, kind) {
					t.Fatalf("missing %s: %v", kind, out)
				}
			case err := <-errs:
				t.Fatal(err)
			case <-time.After(2 * time.Second):
				t.Fatal("collect failed to observe wait result")
			}
		})
	}
}

func TestCollectRejectsStaleClaimBeforeHC(t *testing.T) {
	l := newLab(t)
	other, err := store.Open(l.s.Root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.ClaimCoordinator("", "replacement", true); err != nil {
		t.Fatal(err)
	}
	_, err = Run(context.Background(), l.s, Options{Claim: l.token, Budget: time.Second})
	if !errors.Is(err, store.ErrStaleClaim) {
		t.Fatalf("stale collect=%v", err)
	}
	if len(l.f.Calls()) != 0 {
		t.Fatal("stale collect contacted HC")
	}
}

func TestCollectUnreachableCoreIsBoundedAndActionable(t *testing.T) {
	l := newLab(t)
	e := l.endpoint
	e.Socket = filepath.Join(t.TempDir(), "missing.sock")
	l.task.Set("worker_endpoint", e.Object())
	l.save(t)
	started := time.Now()
	out := l.run(t, 100*time.Millisecond)
	if !hasKind(out, "core-unreachable") || time.Since(started) > time.Second {
		t.Fatalf("unreachable collect=%v", out)
	}
}

func TestCollectDeliversQueuedAnswersOnlyToIdleQualifiedWorker(t *testing.T) {
	l := newLab(t)
	spec, config, options := launchtest.QualifyCodex(t, l.f, l.endpoint.Cwd)
	pid := uint32(101)
	submit := "tab"
	terminal := hcclient.Terminal{Address: l.endpoint.Address, CreatedMS: 1, Cwd: l.endpoint.Cwd, State: "live", PID: &pid, Foreground: &hcclient.Foreground{PID: 102, Name: "codex", Cwd: l.endpoint.Cwd}, Status: &hcclient.Status{State: "working"}, Template: &spec.Name, Submit: &submit, Argv: spec.Argv}
	l.f.SetTerminal(terminal)
	observed := incarnation.NativeEvidence{Endpoint: l.endpoint.Native(), PID: pid, Foreground: terminal.Foreground}.Record(store.Now())
	reg, err := l.s.Register(l.endpoint, "worker", l.endpoint.Task, observed)
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: l.endpoint.Native(), Kind: "worker-init", Attempt: l.endpoint.Attempt})
	reg.Set("native_receipt", receipt)
	provenance, _ := ordjson.FromValue(returns.NativeTemplate{Spec: spec, ConfigHome: config, Options: options})
	reg.Set("native_template", provenance)
	if err := ordjson.WriteFile(filepath.Join(l.s.Sessions, store.RegistrationKey(l.endpoint)+".json"), reg); err != nil {
		t.Fatal(err)
	}
	l.task.Set("questions", []any{question("q1", "answered"), question("q2", "open")})
	l.save(t)
	l.run(t, 15*time.Second)
	if len(l.f.Inputs(l.endpoint.Address)) != 0 {
		t.Fatal("busy worker received an answer")
	}
	l.f.SetStatus(l.endpoint.Address, "idle")
	out := l.run(t, 15*time.Second)
	delivered, _ := get(out, "delivered").([]any)
	if len(delivered) != 1 || len(l.f.Inputs(l.endpoint.Address)) != 1 {
		t.Fatalf("queued answer was not delivered: %v", out)
	}
	l.f.SetStatus(l.endpoint.Address, "idle")
	l.run(t, 15*time.Second)
	if len(l.f.Inputs(l.endpoint.Address)) != 1 {
		t.Fatal("submitted answer replayed")
	}
}
