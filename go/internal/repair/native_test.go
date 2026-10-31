package repair

import (
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launchtest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
	"path/filepath"
	"testing"
)

func TestNativeRepairClaimsOnceAndRetainsUncertainCharge(t *testing.T) {
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
	bind := func(e store.Endpoint) {
		t.Helper()
		pid := uint32(e.CreatedMS)
		spec, config, options := launchtest.QualifyCodex(t, f, e.Cwd)
		submit := "tab"
		terminal := hcclient.Terminal{Address: e.Address, State: "live", CreatedMS: e.CreatedMS, Cwd: e.Cwd, PID: &pid, Foreground: &hcclient.Foreground{PID: int32(pid), Name: "fixture-codex", Argv: spec.Argv}, Template: &spec.Name, Submit: &submit, Argv: spec.Argv, Status: &hcclient.Status{State: "idle"}}
		f.SetTerminal(terminal)
		var task any
		if e.Task != "" {
			task = e.Task
		}
		record, err := s.Register(e, e.Role, task, incarnation.NativeEvidence{Endpoint: e.Native(), PID: pid, Foreground: terminal.Foreground}.Record(store.Now()))
		if err != nil {
			t.Fatal(err)
		}
		kind := map[string]string{"worker": "worker-init", "coordinator": "coordinator-init"}[e.Role]
		receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: e.Native(), Kind: kind, Attempt: e.Attempt})
		record.Set("native_receipt", receipt)
		qualification, _ := ordjson.FromValue(returns.NativeTemplate{Spec: spec, ConfigHome: config, Options: options})
		record.Set("native_template", qualification)
		if err = ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(e)+".json"), record); err != nil {
			t.Fatal(err)
		}
		if e.Role == "coordinator" {
			if err = ordjson.WriteFile(filepath.Join(s.Home, "context.json"), record); err != nil {
				t.Fatal(err)
			}
		}
	}
	coordinator := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Session: "lab", Socket: f.Socket, Address: "t0", Role: "coordinator", Attempt: "coord-1", BindingGeneration: 1, CreatedMS: 100, Cwd: t.TempDir()}
	bind(coordinator)
	cwd := t.TempDir()
	attempt, err := reservations.NewAttempt("worker", coordinator.Object(), cwd, store.Now(), "running", nil)
	if err != nil {
		t.Fatal(err)
	}
	attemptID, _ := attempt.Get("id")
	worker := coordinator
	worker.Address = "t1"
	worker.Role = "worker"
	worker.Task = "t-aaaaaaaaaaaa"
	worker.Attempt = attemptID.(string)
	worker.CreatedMS = 101
	worker.Cwd = cwd
	bind(worker)
	task := ordjson.NewObject()
	task.Set("schema", jsonInt(2))
	task.Set("id", worker.Task)
	task.Set("status", "running")
	task.Set("machine", host.ID)
	task.Set("worktree", cwd)
	task.Set("worker_endpoint", worker.Object())
	task.Set("parent", coordinator.Object())
	task.Set("execution", reservations.NewExecution(attempt))
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	args := SendArgs{TaskID: worker.Task, Attempt: worker.Attempt, Key: "repair-native-1", Text: "Apply the approved correction.", Class: ClassExpansion, Reason: "explicit authorized additional correction", RuntimeRoot: "/not-live"}
	f.QueueFault("terminal.send", hctest.Fault{LoseResponse: true})
	if _, err = Send(s, coordinator.Object(), args); err == nil {
		t.Fatal("lost response was not uncertain")
	}
	if len(f.Inputs(worker.Address)) != 1 {
		t.Fatal("repair was not submitted once")
	}
	f.SetStatus(worker.Address, "idle")
	result, err := Send(s, coordinator.Object(), args)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, _ := result.Get("duplicate"); duplicate != true {
		t.Fatal("uncertain repair key was not retained")
	}
	if len(f.Inputs(worker.Address)) != 1 {
		t.Fatal("uncertain repair repeated")
	}
	current, err := s.ReadTask(worker.Task)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := Ledger(current)
	if err != nil {
		t.Fatal(err)
	}
	if consumed, _ := ledger.Get("consumed"); consumed != jsonInt(1) {
		t.Fatalf("uncertain expansion charge %v", consumed)
	}
	operation := asObject(func() any { v, _ := result.Get("operation"); return v }())
	if state, _ := operation.Get("state"); state != "uncertain" {
		t.Fatal("lost repair effect changed state")
	}
}
