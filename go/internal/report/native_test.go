package report

import (
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func nativeReportLab(t *testing.T) (*store.Store, *ordjson.Object, store.Endpoint, *hctest.Fixture) {
	t.Helper()
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
	fixture := hctest.New(t)
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: fixture.CoreID, Session: "sum-test-report", Socket: fixture.Socket, Address: "t1", CreatedMS: 1, Task: "t-aaaaaaaaaaaa", Role: "worker", Attempt: "x-aaaaaaaaaaaa", BindingGeneration: 1}
	pid := uint32(100)
	foreground := &hcclient.Foreground{PID: 101, Argv: []string{"codex"}}
	fixture.SetTerminal(hcclient.Terminal{Address: "t1", CreatedMS: 1, State: "live", PID: &pid, Foreground: foreground})
	evidence := (incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: pid, Foreground: foreground}).Record(store.Now())
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: endpoint.Native(), Kind: "worker-init", Attempt: endpoint.Attempt})
	registration, err := s.Register(endpoint, "worker", endpoint.Task, evidence)
	if err != nil {
		t.Fatal(err)
	}
	registration.Set("native_receipt", receipt)
	if err = ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(endpoint)+".json"), registration); err != nil {
		t.Fatal(err)
	}
	attempt, err := reservations.NewAttempt("worker", endpoint.Object(), "/tmp/report-work", store.Now(), "running", nil)
	if err != nil {
		t.Fatal(err)
	}
	attempt.Set("id", endpoint.Attempt)
	attempt.Set("incarnation", evidence)
	attempt.Set("native_receipt", receipt)
	task := ordjson.NewObject()
	task.Set("schema", store.Schema)
	task.Set("id", endpoint.Task)
	task.Set("status", "running")
	task.Set("execution", reservations.NewExecution(attempt))
	task.Set("worker_endpoint", endpoint.Object())
	task.Set("evidence", []any{})
	task.Set("questions", []any{})
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	return s, task, endpoint, fixture
}
func TestReportRequiresCurrentWorkerAndPreservesNativeEvidence(t *testing.T) {
	s, _, endpoint, fixture := nativeReportLab(t)
	// Notification may remain pending without a coordinator route; the report is
	// durable before delivery and still requires current worker authority.
	if _, err := Run(s, endpoint.Task, "work completed", nil, endpoint.Object(), returns.PumpOpts{}); err != nil {
		t.Fatal(err)
	}
	saved, err := s.ReadTask(endpoint.Task)
	if err != nil {
		t.Fatal(err)
	}
	records, _ := saved.Get("evidence")
	rows, _ := records.([]any)
	if len(rows) != 1 {
		t.Fatalf("report not persisted: %#v", records)
	}
	record := rows[0].(*ordjson.Object)
	raw, _ := record.Get("endpoint")
	got := store.EndpointFromContext(raw.(*ordjson.Object))
	if !got.Native().SameAuthority(endpoint.Native()) {
		t.Fatalf("report lost native identity: %#v", got)
	}
	if source, _ := record.Get("source"); source != "worker" {
		t.Fatal("worker claim source lost")
	}
	fixture.AssertNoForce(t)
}
func TestReleasedOrReplacedWorkerCannotReportOrMutateHistory(t *testing.T) {
	for _, change := range []string{"released", "foreground", "core", "historical", "cleanup", "history-maintenance"} {
		t.Run(change, func(t *testing.T) {
			s, task, endpoint, fixture := nativeReportLab(t)
			switch change {
			case "cleanup":
				cleanup := ordjson.NewObject()
				cleanup.Set("intent", "active")
				task.Set("cleanup", cleanup)
				if err := s.SaveTask(task); err != nil {
					t.Fatal(err)
				}
			case "history-maintenance":
				maintenance := ordjson.NewObject()
				maintenance.Set("state", "claimed")
				task.Set("terminal_maintenance", maintenance)
				if err := s.SaveTask(task); err != nil {
					t.Fatal(err)
				}
			case "released":
				if _, err := reservations.Transition(task, endpoint.Attempt, "released", store.Now(), nil, nil); err != nil {
					t.Fatal(err)
				}
				if err := s.SaveTask(task); err != nil {
					t.Fatal(err)
				}
			case "foreground":
				fixture.SetForeground(endpoint.Address, hcclient.Foreground{PID: 202, Argv: []string{"codex"}})
			case "core":
				endpoint.CoreID = "wrong-core"
			case "historical":
				endpoint = store.Endpoint{Machine: endpoint.Machine, Session: "old", Pane: "old-pane", Task: endpoint.Task}
			}
			before, err := s.ReadTask("t-aaaaaaaaaaaa")
			if err != nil {
				t.Fatal(err)
			}
			rawBefore, _ := ordjson.MarshalCompact(before)
			if _, err := Run(s, "t-aaaaaaaaaaaa", "stale report", nil, endpoint.Object(), returns.PumpOpts{}); err == nil {
				t.Fatal("stale report accepted")
			}
			after, err := s.ReadTask("t-aaaaaaaaaaaa")
			if err != nil {
				t.Fatal(err)
			}
			rawAfter, _ := ordjson.MarshalCompact(after)
			if string(rawBefore) != string(rawAfter) {
				t.Fatal("rejected report mutated sealed task history")
			}
			fixture.AssertNoForce(t)
		})
	}
}
