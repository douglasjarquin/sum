package cli

import (
	"bytes"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectWorkerFromOrdinaryShellUsesSavedAttemptRoute(t *testing.T) {
	for _, state := range []string{"live", "unready", "failed", "reused"} {
		t.Run(state, func(t *testing.T) {
			root, s := ordinaryInitLab(t)
			if err := s.Init(); err != nil {
				t.Fatal(err)
			}
			f := hctest.New(t)
			machine, err := s.Machine()
			if err != nil {
				t.Fatal(err)
			}
			taskID, attempt := "t-aaaaaaaaaaaa", "x-aaaaaaaaaaaa"
			params, _ := ordjson.FromValue(hcclient.RunParams{Env: map[string]string{"HC_SOCKET": f.Socket, "SUM_CORE_ID": f.CoreID, "SUM_SESSION": "saved-session"}})
			native := ordjson.NewObject()
			native.Set("intent", params)
			native.Set("effect", state)
			native.Set("stage", state)
			owner := ordjson.NewObject()
			owner.Set("machine", machine.ID)
			owner.Set("session", "sum-test")
			owner.Set("pane", "p1")
			worker, err := reservations.NewAttempt("worker", owner, t.TempDir(), store.Now(), "held", nil)
			if err != nil {
				t.Fatal(err)
			}
			worker.Set("id", attempt)
			worker.Set("native_launch", native)
			execution := reservations.NewExecution(worker)
			task := ordjson.NewObject()
			task.Set("schema", 2)
			task.Set("id", taskID)
			task.Set("status", "working")
			task.Set("execution", execution)
			parent := ordjson.NewObject()
			parent.Set("core_id", "wrong-parent")
			task.Set("parent", parent)
			if state == "live" || state == "reused" {
				ep := store.Endpoint{Schema: 2, Backend: "hc", Machine: machine.ID, Socket: f.Socket, CoreID: f.CoreID, Session: "saved-session", Address: "t1", CreatedMS: 1, Task: taskID, Role: "worker", Attempt: attempt, BindingGeneration: 1, Cwd: t.TempDir()}
				task.Set("worker_endpoint", ep.Object())
				f.SetTerminal(hcclient.Terminal{Address: "t1", CreatedMS: 1, State: "live", Cwd: ep.Cwd})
				f.SetScreen("t1", strings.Repeat("saved-output", 1500))
				if state == "reused" {
					f.SetTerminal(hcclient.Terminal{Address: "t1", CreatedMS: 2, State: "live", Cwd: ep.Cwd})
				}
			}
			if err = s.SaveTask(task); err != nil {
				t.Fatal(err)
			}
			var out, errors bytes.Buffer
			command := NewRoot(filepath.Join(root, "bin", "sumctl"), &out, &errors)
			command.SetArgs([]string{"--home", s.Root, "--format", "json", "inspect", taskID})
			err = command.Execute()
			if state == "reused" {
				if err == nil || !strings.Contains(err.Error(), "creation changed") {
					t.Fatalf("reused endpoint accepted: %v", err)
				}
				for _, call := range f.Calls() {
					if call.Method == "terminal.read" {
						t.Fatal("read reused terminal")
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("inspect: %v %s", err, errors.String())
			}
			view := decodeCLIMap(t, out.String())
			if asMap(view["route"])["core_id"] != f.CoreID || view["attempt"] != attempt || view["read_only"] != true || view["next"] == nil || view["stage"] != state {
				t.Fatalf("inspection=%v", view)
			}
			if state == "live" {
				if asMap(view["output"])["truncated"] != true {
					t.Fatalf("unbounded output=%v", view)
				}
			} else if len(f.Calls()) != 0 {
				t.Fatalf("unknown endpoint contacted HC: %v", f.Calls())
			}
			for _, call := range f.Calls() {
				if call.Method != "hello" && call.Method != "terminal.get" && call.Method != "terminal.read" {
					t.Fatalf("inspection effect: %v", call)
				}
			}
		})
	}
}

func TestInspectRequiresTaskAndRejectsCoordinatorRole(t *testing.T) {
	root, s := ordinaryInitLab(t)
	for _, args := range [][]string{{"inspect"}, {"inspect", "t-aaaaaaaaaaaa", "--role", "coordinator"}} {
		var out bytes.Buffer
		command := NewRoot(filepath.Join(root, "bin", "sumctl"), &out, &out)
		command.SetArgs(append([]string{"--home", s.Root}, args...))
		if err := command.Execute(); err == nil {
			t.Fatal("obsolete coordinator inspection accepted")
		}
	}
}

func TestInspectRecoveryActionsCarryOnlyTheSuppliedClaim(t *testing.T) {
	for _, role := range []string{"worker", "reviewer"} {
		t.Run(role, func(t *testing.T) {
			root, s := ordinaryInitLab(t)
			session, err := s.ClaimCoordinator("", "original", false)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.ClaimCoordinator("", "replacement", true); err != nil {
				t.Fatal(err)
			}
			taskID, attempt := "t-aaaaaaaaaaaa", "x-aaaaaaaaaaaa"
			record, err := reservations.NewAttempt("worker", session.Owner(), t.TempDir(), store.Now(), "held", nil)
			if err != nil {
				t.Fatal(err)
			}
			record.Set("id", attempt)
			record.Set("attempt", attempt)
			task := ordjson.NewObject()
			task.Set("schema", 2)
			task.Set("id", taskID)
			task.Set("execution", reservations.NewExecution(record))
			task.Set("reviewer", record)
			if err := s.SaveTask(task); err != nil {
				t.Fatal(err)
			}
			for _, supplied := range []string{"", session.Token()} {
				var out bytes.Buffer
				command := NewRoot(filepath.Join(root, "bin", "sumctl"), &out, &out)
				args := []string{"--home", s.Root, "--format", "json", "inspect", taskID, "--role", role}
				if supplied != "" {
					args = append(args, "--claim", supplied)
				}
				command.SetArgs(args)
				if err := command.Execute(); err != nil {
					t.Fatalf("read-only inspection: %v %s", err, out.String())
				}
				view := decodeCLIMap(t, out.String())
				for _, key := range []string{"next", "reconcile", "redeliver_if_proven_unsent", "park_if_proven_stopped", "recover_if_proven_stopped_or_uncreated"} {
					action, ok := view[key].([]any)
					if !ok {
						continue
					}
					claim := ""
					for i := 0; i+1 < len(action); i++ {
						if action[i] == "--claim" {
							claim, _ = action[i+1].(string)
						}
					}
					if claim != supplied {
						t.Fatalf("%s supplied=%q action=%v", key, supplied, action)
					}
				}
			}
		})
	}
}
