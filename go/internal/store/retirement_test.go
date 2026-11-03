package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func retirementStore(t *testing.T) (*store.Store, *ordjson.Object) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	owner := (store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: "retirement-core", Socket: "/tmp/sum-test-retirement.sock", Session: "sum-test-retirement", Address: "t1", CreatedMS: 1, Role: "coordinator", Attempt: "native-owner", BindingGeneration: 1}).Object()
	if err := ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	// Lock files are synchronization artifacts, not a failed-retirement state change.
	release, err := s.DeliveryLock()
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	release()
	return s, owner
}

func retirementSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[relative] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestNativeRetirementPreservesHistoricalBytes(t *testing.T) {
	s, owner := retirementStore(t)
	task := ordjson.NewObject()
	task.Set("schema", json.Number("1"))
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("status", "archived")
	task.Set("parent", owner)
	task.Set("report", "original report")
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	taskPath, err := s.TaskPath("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	procedure := filepath.Join(taskPath, "procedure", "WORKER.md")
	if err := os.MkdirAll(filepath.Dir(procedure), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(procedure, []byte("# Sealed historical procedure\nPreserve these exact bytes.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	wake := filepath.Join(s.Home, "deliver", "historical.wake.json")
	if err := os.MkdirAll(filepath.Dir(wake), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wake, []byte("{\"historical\":true}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before := retirementSnapshot(t, s.Root)
	session, err := s.RetireNativeCoordinator("", "ordinary conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	if session.RetiredNative == nil {
		t.Fatal("native owner was not retained as retirement evidence")
	}
	after := retirementSnapshot(t, s.Root)
	delete(before, "state.json")
	delete(after, "state.json")
	delete(after, filepath.Join("generations", s.Generation, "coordinator.json"))
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("retirement rewrote historical bytes:\nbefore=%v\nafter=%v", before, after)
	}
	pointer, err := ordjson.ReadFile(filepath.Join(s.Root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if schema, _ := pointer.(*ordjson.Object).Get("schema"); schema != json.Number("3") {
		t.Fatalf("format fence missing: %v", schema)
	}
}

func TestNativeRetirementWaitsForAdmittedDelivery(t *testing.T) {
	s, _ := retirementStore(t)
	sender, err := store.Open(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	release, err := sender.DeliveryShared(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	before := retirementSnapshot(t, s.Root)
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := s.RetireNativeCoordinator("", "ordinary conversation", false)
		done <- err
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("retirement passed an admitted delivery lease: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if after := retirementSnapshot(t, s.Root); !reflect.DeepEqual(before, after) {
		t.Fatal("retirement changed authority while an admitted sender held its lease")
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retirement did not finish after delivery released its lease")
	}
}

func TestNativeRetirementRefusesUnsettledExternalEffects(t *testing.T) {
	for _, effect := range []string{"worker starting", "worker uncertain", "worker launch claimed", "worker redelivery claimed", "reviewer launch in-flight", "reviewer prompt in-flight", "reviewer kickoff in-flight", "terminal maintenance claimed", "archived terminal maintenance claimed", "cleanup effects claimed", "preparation active"} {
		t.Run(effect, func(t *testing.T) {
			s, owner := retirementStore(t)
			worker, err := reservations.NewAttempt("worker", owner, "/tmp/sum-test-checkout", store.Now(), "held", nil)
			if err != nil {
				t.Fatal(err)
			}
			task := ordjson.NewObject()
			task.Set("schema", json.Number("2"))
			task.Set("id", "t-aaaaaaaaaaaa")
			task.Set("status", "prepared")
			task.Set("parent", owner)
			task.Set("execution", reservations.NewExecution(worker))
			record := ordjson.NewObject()
			switch effect {
			case "worker starting":
				worker.Set("state", "starting")
			case "worker uncertain":
				worker.Set("state", "uncertain")
			case "worker launch claimed":
				record.Set("effect", "claimed")
				worker.Set("native_launch", record)
			case "worker redelivery claimed":
				redelivery := ordjson.NewObject()
				redelivery.Set("effect", "claimed")
				record.Set("effect", "created")
				record.Set("redelivery", redelivery)
				worker.Set("native_launch", record)
			case "reviewer launch in-flight", "reviewer prompt in-flight", "reviewer kickoff in-flight":
				pending := ordjson.NewObject()
				pending.Set("state", "in-flight")
				field := strings.Fields(effect)[1]
				if field == "launch" {
					field = "native_launch"
				}
				record.Set(field, pending)
				task.Set("reviewer", record)
			case "terminal maintenance claimed":
				record.Set("state", "claimed")
				task.Set("terminal_maintenance", record)
			case "archived terminal maintenance claimed":
				task.Set("status", "archived")
				record.Set("state", "claimed")
				task.Set("terminal_maintenance", record)
			case "cleanup effects claimed":
				record.Set("state", "removing")
				record.Set("step", "effects-claimed")
				task.Set("cleanup", record)
			case "preparation active":
				task.Set("status", "preparing")
				task.Set("prepare_operation_pid", json.Number("123"))
				task.Set("prepare_operation_id", "preparation-operation")
			}
			if err := s.SaveTask(task); err != nil {
				t.Fatal(err)
			}
			before := retirementSnapshot(t, s.Root)
			if _, err := s.RetireNativeCoordinator("", "ordinary conversation", false); err == nil {
				t.Fatalf("retirement accepted %s", effect)
			}
			after := retirementSnapshot(t, s.Root)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("refused retirement changed state for %s", effect)
			}
		})
	}
}

func TestNativeRetirementKeepsExistingWorkerEndpoint(t *testing.T) {
	s, owner := retirementStore(t)
	worker, err := reservations.NewAttempt("worker", owner, "/tmp/sum-test-checkout", store.Now(), "running", nil)
	if err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", json.Number("2"))
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("status", "running")
	task.Set("parent", owner)
	task.Set("execution", reservations.NewExecution(worker))
	endpoint := (store.Endpoint{Schema: 2, Backend: "hc", Machine: "existing-host", CoreID: "existing-core", Socket: "/tmp/sum-test-existing.sock", Session: "sum-test-existing", Address: "t2", CreatedMS: 2, Task: "t-aaaaaaaaaaaa", Role: "worker", Attempt: "existing-attempt", BindingGeneration: 1}).Object()
	task.Set("worker_endpoint", endpoint)
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	path, err := s.TaskPath("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(path, "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetireNativeCoordinator("", "ordinary conversation", false); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(path, "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("retirement changed existing worker identity or task ownership bytes")
	}
}

func TestNativeRetirementAcceptsReceiptConfirmedWorker(t *testing.T) {
	s, owner := retirementStore(t)
	worker, err := reservations.NewAttempt("worker", owner, "/tmp/sum-test-checkout", store.Now(), "running", nil)
	if err != nil {
		t.Fatal(err)
	}
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	attempt, _ := worker.Get("id")
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: "receipt-confirmed-core", Socket: "/tmp/sum-test-receipt-confirmed.sock", Session: "sum-test-retirement", Address: "t2", CreatedMS: 2, Role: "worker", Task: "t-aaaaaaaaaaaa", Attempt: attempt.(string), BindingGeneration: 1}
	observed := incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: 200, Foreground: &hcclient.Foreground{PID: 201, Name: "codex", Argv: []string{"codex"}}}.Record(store.Now())
	receipt, err := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: endpoint.Native(), Kind: "worker-init", Attempt: endpoint.Attempt})
	if err != nil {
		t.Fatal(err)
	}
	registration, err := s.Register(endpoint, "worker", endpoint.Task, observed)
	if err != nil {
		t.Fatal(err)
	}
	registration.Set("native_receipt", receipt)
	if err := ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(endpoint)+".json"), registration); err != nil {
		t.Fatal(err)
	}
	launch := ordjson.NewObject()
	launch.Set("effect", "claimed")
	worker.Set("native_launch", launch)
	worker.Set("native_receipt", receipt)
	worker.Set("incarnation", observed)
	task := ordjson.NewObject()
	task.Set("schema", json.Number("2"))
	task.Set("id", endpoint.Task)
	task.Set("status", "running")
	task.Set("parent", owner)
	task.Set("execution", reservations.NewExecution(worker))
	task.Set("worker_endpoint", endpoint.Object())
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	before := retirementSnapshot(t, s.Root)
	if _, err := s.RetireNativeCoordinator("", "ordinary conversation", false); err != nil {
		t.Fatalf("receipt-confirmed worker unnecessarily blocked retirement: %v", err)
	}
	after := retirementSnapshot(t, s.Root)
	delete(before, "state.json")
	delete(after, "state.json")
	delete(after, filepath.Join("generations", s.Generation, "coordinator.json"))
	if !reflect.DeepEqual(before, after) {
		t.Fatal("retirement rewrote receipt-confirmed worker identity or historical bytes")
	}
}
