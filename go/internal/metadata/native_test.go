package metadata

import (
	"context"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"path/filepath"
	"testing"
)

func TestNativeProjectionUsesNamespacedLabelsAndReceiptBoundProcess(t *testing.T) {
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
	terminal := hcclient.Terminal{Address: e.Address, State: "live", CreatedMS: e.CreatedMS, Cwd: e.Cwd, PID: &pid, Foreground: &hcclient.Foreground{PID: 100, Name: "codex"}, Status: &hcclient.Status{State: "working"}, Labels: map[string]string{"unrelated": "preserved"}}
	f.SetTerminal(terminal)
	record, err := s.Register(e, "worker", e.Task, incarnation.NativeEvidence{Endpoint: e.Native(), PID: pid, Foreground: terminal.Foreground}.Record(store.Now()))
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: e.Native(), Kind: "worker-init", Attempt: e.Attempt})
	record.Set("native_receipt", receipt)
	if err = ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(e)+".json"), record); err != nil {
		t.Fatal(err)
	}
	p := newPass(s, emptyMetadata(), "", false)
	row := p.project(context.Background(), e, map[string]string{"sum_state": "answer-pending"}, nil)
	if get(row, "outcome") != "written" {
		t.Fatalf("projection %v", row)
	}
	client := f.Client()
	native, err := client.Get(context.Background(), e.Address)
	if err != nil {
		t.Fatal(err)
	}
	if native.Labels["sum.state"] != "answer-pending" || native.Labels["unrelated"] != "preserved" {
		t.Fatalf("labels %v", native.Labels)
	}
	calls := len(f.Calls())
	again := p.project(context.Background(), e, map[string]string{"sum_state": "answer-pending"}, asObject(get(row, "record")))
	if get(again, "outcome") != "unchanged" || len(f.Calls()) != calls {
		t.Fatal("unchanged projection contacted runtime")
	}
	f.SetForeground(e.Address, hcclient.Foreground{PID: 222, Name: "shell"})
	row = p.project(context.Background(), e, map[string]string{"sum_state": "running"}, nil)
	if get(row, "outcome") != "failed" {
		t.Fatal("replaced foreground inherited projection authority")
	}
}
