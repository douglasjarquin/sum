package refreshcmd

import (
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launchtest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
	"path/filepath"
	"testing"
)

func TestNativeRefreshClaimRejectsReplacementAndNeverReplaysUncertainty(t *testing.T) {
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
	e := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Session: "lab", Socket: f.Socket, Address: "t1", Role: "worker", Task: "t-000000000001", Attempt: "a1", BindingGeneration: 1, CreatedMS: 100, Cwd: t.TempDir()}
	pid := uint32(100)
	spec, config, options := launchtest.QualifyCodex(t, f, e.Cwd)
	submit := "tab"
	terminal := hcclient.Terminal{Address: e.Address, State: "live", CreatedMS: e.CreatedMS, Cwd: e.Cwd, PID: &pid, Foreground: &hcclient.Foreground{PID: 100, Name: "codex"}, Template: &spec.Name, Submit: &submit, Argv: spec.Argv, Status: &hcclient.Status{State: "idle"}}
	f.SetTerminal(terminal)
	evidence := incarnation.NativeEvidence{Endpoint: e.Native(), PID: pid, Foreground: terminal.Foreground}.Record(store.Now())
	registration, err := s.Register(e, "worker", e.Task, evidence)
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: e.Native(), Kind: "worker-init", Attempt: e.Attempt})
	registration.Set("native_receipt", receipt)
	qualified, _ := ordjson.FromValue(returns.NativeTemplate{Spec: spec, ConfigHome: config, Options: options})
	registration.Set("native_template", qualified)
	if err = ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(e)+".json"), registration); err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", jsonNumber(2))
	task.Set("id", e.Task)
	task.Set("status", "running")
	task.Set("machine", e.Machine)
	task.Set("worktree", e.Cwd)
	task.Set("worker_endpoint", e.Object())
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	ledger, err := versions.ReadVersions(s, task)
	if err != nil {
		t.Fatal(err)
	}
	ledger.Set("requested", "r1")
	if err = versions.WriteVersions(s, ledger); err != nil {
		t.Fatal(err)
	}
	record := occupantRecord{role: "worker", revision: "r1", value: evidence, ok: true}
	attempt := func() *ordjson.Object {
		return attemptDelivery(s, newSnapshots(""), e.Object(), e.Cwd, "read requested revision r1", host, record, task)
	}
	for _, kind := range []string{"not_found", "unavailable"} {
		f.QueueFault("terminal.get", hctest.Fault{RefuseKind: kind})
		want := "pending-busy"
		if kind == "not_found" {
			want = versions.RefreshUnreachable
		}
		if state, _ := attempt().Get("state"); state != want {
			t.Fatalf("%s refresh state %v, want %s", kind, state, want)
		}
	}
	f.SetStatus(e.Address, "working")
	if state, _ := attempt().Get("state"); state != "pending-busy" {
		t.Fatalf("busy refresh state %v", state)
	}
	f.SetStatus(e.Address, "idle")
	f.SetForeground(e.Address, hcclient.Foreground{PID: 222, Name: "shell"})
	if state, _ := attempt().Get("state"); state != "pending-unreachable" {
		t.Fatalf("replaced refresh state %v", state)
	}
	if len(f.Inputs(e.Address)) != 0 {
		t.Fatal("replacement got input")
	}
	f.SetForeground(e.Address, *terminal.Foreground)
	f.QueueFault("terminal.send", hctest.Fault{LoseResponse: true})
	row := attempt()
	if effect, _ := row.Get("delivery_effect"); effect != "uncertain" {
		t.Fatalf("lost response %v", row)
	}
	f.SetStatus(e.Address, "idle")
	attempt()
	if len(f.Inputs(e.Address)) != 1 {
		t.Fatal("uncertain refresh repeated")
	}
	ledger, err = versions.ReadVersions(s, task)
	if err != nil {
		t.Fatal(err)
	}
	if active, _ := ledger.Get("active"); active == "r1" {
		t.Fatal("submission adopted a revision")
	}

	ledger.Set("requested", "r2")
	if err = versions.WriteVersions(s, ledger); err != nil {
		t.Fatal(err)
	}
	record.revision = "r2"
	record.current = func() (any, error) { ledger.Set("requested", "r3"); return evidence, versions.WriteVersions(s, ledger) }
	row = attempt()
	if state, _ := row.Get("state"); state != "pending-unreachable" {
		t.Fatalf("changed request state %v", row)
	}
	if len(f.Inputs(e.Address)) != 1 {
		t.Fatal("stale requested revision got input")
	}
}
