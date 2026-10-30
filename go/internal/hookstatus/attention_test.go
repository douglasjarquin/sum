package hookstatus

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func attentionLab(t *testing.T, role string) (*hctest.Fixture, *store.Store, store.Endpoint, *ordjson.Object) {
	t.Helper()
	f := hctest.New(t)
	s, err := store.Open(filepath.Join(t.TempDir(), "home"))
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
	instance, err := s.Instance()
	if err != nil {
		t.Fatal(err)
	}
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Session: "lab", Socket: f.Socket, Address: "t1", Role: role, Task: "t-000000000001", Attempt: "a1", BindingGeneration: 1, CreatedMS: 100, Cwd: t.TempDir()}
	pid := uint32(123)
	terminal := hcclient.Terminal{Address: endpoint.Address, State: "live", CreatedMS: 100, Cwd: endpoint.Cwd, PID: &pid, Foreground: &hcclient.Foreground{PID: 123, Name: "codex"}, Status: &hcclient.Status{State: "blocked"}, Labels: map[string]string{"sum.installation": instance}}
	f.SetTerminal(terminal)
	f.SetScreen(endpoint.Address, strings.Repeat("x", 2100))
	registration, err := s.Register(endpoint, role, endpoint.Task, incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: pid, Foreground: terminal.Foreground}.Record(store.Now()))
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: endpoint.Native(), Kind: role + "-init", Attempt: endpoint.Attempt})
	registration.Set("native_receipt", receipt)
	if err = ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(endpoint)+".json"), registration); err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", jsonInt(2))
	task.Set("id", endpoint.Task)
	task.Set("status", "running")
	field := "worker_endpoint"
	if role == "reviewer" {
		field = "reviewer"
	}
	task.Set(field, endpoint.Object())
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	return f, s, endpoint, registration
}
func TestNativeReviewerAttentionIsBoundedAndNotAVerdict(t *testing.T) {
	f, s, e, _ := attentionLab(t, "reviewer")
	if err := reconcileAttention(context.Background(), s, e); err != nil {
		t.Fatal(err)
	}
	if err := reconcileAttention(context.Background(), s, e); err != nil {
		t.Fatal(err)
	}
	task, err := s.ReadTask(e.Task)
	if err != nil {
		t.Fatal(err)
	}
	records := asList(value(task, "attention"))
	if len(records) != 1 {
		t.Fatalf("attention count %d", len(records))
	}
	record := asObject(records[0])
	if len([]rune(fmtString(value(record, "excerpt")))) != 2000 || value(record, "role") != "reviewer" || value(task, "verdict") != nil || value(task, "status") != "running" {
		t.Fatalf("reviewer observation = %v", task)
	}
	if len(f.Inputs(e.Address)) != 0 {
		t.Fatal("attention produced native input")
	}
}
func fmtString(v any) string { s, _ := v.(string); return s }
func TestNativeAttentionRefusesMissingReceiptAndCleanupRacingRead(t *testing.T) {
	f, s, e, registration := attentionLab(t, "worker")
	registration.Set("native_receipt", nil)
	path := filepath.Join(s.Sessions, store.RegistrationKey(e)+".json")
	if err := ordjson.WriteFile(path, registration); err != nil {
		t.Fatal(err)
	}
	if err := reconcileAttention(context.Background(), s, e); err != nil {
		t.Fatal(err)
	}
	task, _ := s.ReadTask(e.Task)
	if len(asList(value(task, "attention"))) != 0 {
		t.Fatal("missing receipt adopted a native status")
	}
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: e.Native(), Kind: "worker-init", Attempt: e.Attempt})
	registration.Set("native_receipt", receipt)
	if err := ordjson.WriteFile(path, registration); err != nil {
		t.Fatal(err)
	}
	entered, release := f.HoldNext("terminal.read", e.Address)
	defer release()
	done := make(chan error, 1)
	go func() { done <- reconcileAttention(context.Background(), s, e) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("native read not reached")
	}

	unlock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	task, _ = s.ReadTask(e.Task)
	barrier := ordjson.NewObject()
	barrier.Set("state", "claimed")
	task.Set("terminal_maintenance", barrier)
	err = s.SaveTask(task)
	unlock()
	if err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("read reconciliation did not finish")
	}
	task, _ = s.ReadTask(e.Task)
	if len(asList(value(task, "attention"))) != 0 {
		t.Fatal("cleanup race admitted a new obligation")
	}
}
