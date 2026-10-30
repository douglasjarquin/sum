package hookstatus

import (
	"context"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launchtest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func await(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if predicate() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("observed condition not reached")
		case <-tick.C:
		}
	}
}
func TestNativeObserverReconcilesEventsDirtyCommitsAndMissedSnapshots(t *testing.T) {
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
	if err = os.MkdirAll(filepath.Join(s.Home, HookDir), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Home, HookDir, "dirty"), []byte{0}, 0600); err != nil {
		t.Fatal(err)
	}
	e := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Session: "lab", Socket: f.Socket, Address: "t1", Task: "t-000000000001", Role: "worker", Attempt: "a1", BindingGeneration: 1, CreatedMS: 100, Cwd: t.TempDir()}
	spec, config, options := launchtest.QualifyCodex(t, f, e.Cwd)
	instance, err := s.Instance()
	if err != nil {
		t.Fatal(err)
	}
	client := f.Client()
	launched, err := client.Run(context.Background(), hcclient.RunParams{Argv: spec.RunArgv("read the approved worker pointer"), Cwd: e.Cwd, Labels: map[string]string{"sum.installation": instance, "sum.kind": "worker", "sum.task": e.Task, "sum.attempt": e.Attempt}, OutliveParent: true, NoNotify: true, Keep: false, RM: false})
	if err != nil {
		t.Fatal(err)
	}
	terminal := launched.Terminal
	e.Address = terminal.Address
	e.CreatedMS = terminal.CreatedMS
	pid := uint32(123)
	terminal.PID = &pid
	terminal.Foreground = &hcclient.Foreground{PID: 123, Name: "fixture-codex", Argv: spec.Argv, Cwd: e.Cwd}
	f.SetTerminal(terminal)
	if terminal.Labels["sum.installation"] != instance || instance == s.Root {
		t.Fatal("worker labels do not use permanent installation identity")
	}
	f.SetStatus(e.Address, "working")
	record, err := s.Register(e, "worker", e.Task, incarnation.NativeEvidence{Endpoint: e.Native(), PID: pid, Foreground: terminal.Foreground}.Record(store.Now()))
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: e.Native(), Kind: "worker-init", Attempt: e.Attempt})
	record.Set("native_receipt", receipt)
	templateRecord, _ := ordjson.FromValue(returns.NativeTemplate{Spec: spec, ConfigHome: config, Options: options})
	record.Set("native_template", templateRecord)
	if err = ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(e)+".json"), record); err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", jsonInt(2))
	task.Set("id", e.Task)
	task.Set("machine", host.ID)
	task.Set("status", "running")
	task.Set("worktree", e.Cwd)
	task.Set("worker_endpoint", e.Object())
	question := func(id string) *ordjson.Object {
		q := ordjson.NewObject()
		q.Set("id", id)
		q.Set("status", "answered")
		q.Set("created_at", store.Now())
		q.Set("answered_at", store.Now())
		return q
	}
	task.Set("questions", []any{question("q1")})
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	args := []string{"/sumctl", "--home", s.Root, "hook", "observe", "--core-id", f.CoreID, "--session", "lab", "--socket", f.Socket}
	observerPID := uint32(222)
	f.SetTerminal(hcclient.Terminal{Address: "t0", State: "live", CreatedMS: 99, Argv: args, PID: &observerPID, Foreground: &hcclient.Foreground{PID: 222, Name: "sumctl"}})
	scope := store.Endpoint{CoreID: f.CoreID, Session: "lab", Socket: f.Socket, Address: "t0"}
	intent := ordjson.NewObject()
	intent.Set("launch_effect", "submitted")
	intent.Set("core_id", f.CoreID)
	intent.Set("session", "lab")
	intent.Set("socket", f.Socket)
	rawArgs := make([]any, len(args))
	for i, arg := range args {
		rawArgs[i] = arg
	}
	intent.Set("argv", rawArgs)
	if _, err = writeHealth(s, nil, intent); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	observerStore, err := store.Open(s.Home)
	if err != nil {
		t.Fatal(err)
	}
	go func() { done <- Observe(ctx, observerStore, scope, "/runtime", "/sumctl") }()
	subscriptions := func() int {
		n := 0
		for _, call := range f.Calls() {
			if call.Method == "events.subscribe" {
				n++
			}
		}
		return n
	}
	await(t, func() bool {
		select {
		case err := <-done:
			t.Fatalf("observer exited: %v", err)
		default:
		}
		return subscriptions() > 0
	})
	readyView, readyErr := AwaitReady(context.Background(), s)
	if readyErr != nil {
		t.Fatal(readyErr)
	}
	if ready, _ := readyView.Get("ready"); ready != true {
		t.Fatal("startup receipt not confirmed")
	}
	f.SetStatus(e.Address, "idle")
	await(t, func() bool { return len(f.Inputs(e.Address)) == 1 })
	// The native status is already idle before the next obligation exists. The
	// Sum-only commit signal must expose it without another native status event.
	f.SetStatus(e.Address, "idle")
	task.Set("questions", []any{question("q1"), question("q2")})
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if err = returns.MarkDirty(s); err != nil {
		t.Fatal(err)
	}
	await(t, func() bool {
		select {
		case err := <-done:
			t.Fatalf("observer exited before dirty delivery: %v; calls %v", err, f.Calls())
		default:
		}
		return len(f.Inputs(e.Address)) == 2
	})
	f.Miss()
	await(t, func() bool { return subscriptions() > 1 })
	f.DisconnectSubscribers()
	await(t, func() bool { return subscriptions() > 2 })
	if len(f.Inputs(e.Address)) != 2 {
		t.Fatal("reconnect replayed a submitted obligation")
	}
	// Actual native blocked/finished edges persist bounded observations and do not replay.
	f.SetScreen(e.Address, "\x1b[31mPermission needed: allow write?\x1b[0m\x00")
	f.SetStatus(e.Address, "blocked")
	attentionCount := func() int {
		current, err := s.ReadTask(e.Task)
		if err != nil {
			t.Fatal(err)
		}
		return len(asList(value(current, "attention")))
	}
	await(t, func() bool { return attentionCount() == 1 })
	current, _ := s.ReadTask(e.Task)
	blocked := asObject(asList(value(current, "attention"))[0])
	if value(blocked, "kind") != "blocked" || value(blocked, "excerpt") != "Permission needed: allow write?" {
		t.Fatalf("blocked observation = %v", blocked)
	}
	f.Miss()
	await(t, func() bool { return subscriptions() > 3 })
	if attentionCount() != 1 {
		t.Fatal("snapshot replayed attention")
	}
	f.Exit(e.Address, 1)
	await(t, func() bool { return attentionCount() == 2 })
	current, _ = s.ReadTask(e.Task)
	exited := asObject(asList(value(current, "attention"))[1])
	if value(exited, "kind") != "exited" || value(current, "status") != "running" || value(current, "report") != nil {
		t.Fatalf("native exit changed business outcome: %v", current)
	}
	if len(f.Inputs(e.Address)) != 2 {
		t.Fatal("attention sent input to blocked or finished worker")
	}
	if err = settings.SetNativeEventsEnabled(s, false); err != nil {
		t.Fatal(err)
	}
	if err = returns.MarkDirty(s); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("observer did not exit after disabled preference")
	}
	f.AssertNoForce(t)
	if fmt.Sprint(record) == "" {
		t.Fatal("missing binding")
	}
}

func TestNativeEnsureObserverLaunchesDetachedHelperAndPreservesDisablement(t *testing.T) {
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
	e := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Session: "lab", Socket: f.Socket, Address: "t99", Role: "coordinator", Attempt: "coord-1", BindingGeneration: 1, CreatedMS: 99}
	row, err := ensureObserver(s, e, "/runtime", "/sumctl", false)
	if err != nil {
		t.Fatal(err)
	}
	if effect, _ := row.Get("launch_effect"); effect != "submitted" {
		t.Fatalf("launch %v", row)
	}
	runs := 0
	for _, call := range f.Calls() {
		if call.Method == "terminal.run" {
			runs++
			for key, want := range map[string]bool{"outlive_parent": true, "no_notify": true, "keep": false, "rm": false} {
				if call.Params[key] != want {
					t.Fatalf("%s %v", key, call.Params[key])
				}
			}
			if _, ok := call.Params["argv"].([]any); !ok {
				t.Fatal("no structured helper argv")
			}
		}
	}
	if runs != 1 {
		t.Fatalf("runs %d", runs)
	}
	if err = settings.SetNativeEventsEnabled(s, false); err != nil {
		t.Fatal(err)
	}
	row = EnableByDefault(s, e.Object(), "/runtime", "/sumctl", true)
	if enabled, _ := row.Get("enabled"); enabled != false {
		t.Fatal("disabled preference was lost")
	}
	for _, call := range f.Calls() {
		if call.Method == "terminal.run" {
			runs--
		}
	}
	if runs != 0 {
		t.Fatal("disabled preference launched an observer")
	}
}
func TestNativeObserverUncertainLaunchWithoutEndpointBlocksActivation(t *testing.T) {
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
	e := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Session: "lab", Socket: f.Socket, Address: "t99", Role: "coordinator", Attempt: "coord-1", BindingGeneration: 1, CreatedMS: 99}
	f.QueueFault("terminal.run", hctest.Fault{LoseResponse: true})
	if _, err = ensureObserver(s, e, "/runtime", "/sumctl", false); err == nil {
		t.Fatal("lost response was successful")
	}
	if _, err = Compatibility(context.Background(), s); err == nil {
		t.Fatal("activation accepted unresolved observer launch")
	}
	if _, err = ensureObserver(s, e, "/runtime", "/sumctl", false); err == nil {
		t.Fatal("uncertain helper launch repeated")
	}
	n := 0
	for _, call := range f.Calls() {
		if call.Method == "terminal.run" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("runs %d", n)
	}
}

func TestPendingAgeUsesStoreClock(t *testing.T) {
	t.Setenv("SUM_NOW", "2026-01-02T00:00:00Z")
	s, err := store.Open(filepath.Join(t.TempDir(), "home"))
	if err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("id", "t-000000000001")
	task.Set("status", "reported")
	question := ordjson.NewObject()
	question.Set("id", "q1")
	question.Set("status", "open")
	question.Set("created_at", "2026-01-01T00:00:00Z")
	task.Set("questions", []any{question})
	row, err := pendingSummary(s, []*ordjson.Object{task})
	if err != nil {
		t.Fatal(err)
	}
	if age, _ := row.Get("oldest_age_s"); fmt.Sprint(age) != "86400" {
		t.Fatalf("age = %v; summary = %v", age, row)
	}
}
