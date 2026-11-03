package cli

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/updatecmd"
)

func TestBootstrapReconcilesStoppedWorkersWithoutNativeCoordinator(t *testing.T) {
	clearFallbackIdentity(t)
	root, s, f, sha, env := bootstrapInstallation(t, buildNativeBaseHelper(t))
	tasks := bootstrapStoppedWorkers(t, root, s, f)
	first := tasks[0]
	worktree, _ := first.Get("worktree")
	survivor := exec.Command("sleep", "60")
	survivor.Dir = worktree.(string)
	if err := survivor.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if survivor.ProcessState == nil {
			_ = survivor.Process.Kill()
			_ = survivor.Wait()
		}
	})
	before, _ := os.ReadFile(filepath.Join(s.Tasks, "t-38545efe903c/task.json"))
	view, err := updatecmd.Bootstrap(s, sha, true, false, "")
	if err != nil {
		t.Fatal(err)
	}
	blocker, _ := view.Get("blocker")
	if !strings.Contains(asString(blocker), "process") {
		t.Fatalf("surviving repro process was not a blocker: %v", blocker)
	}
	if _, err := updatecmd.Bootstrap(s, sha, true, true, ""); err == nil {
		t.Fatal("bootstrap approved while a checkout process survived")
	}
	after, _ := os.ReadFile(filepath.Join(s.Tasks, "t-38545efe903c/task.json"))
	if string(before) != string(after) {
		t.Fatal("blocked bootstrap changed a task")
	}
	if _, err := os.Stat(filepath.Join(root, ".local/bootstrap.json")); !os.IsNotExist(err) {
		t.Fatal("blocked bootstrap wrote an operation")
	}
	_ = survivor.Process.Kill()
	_ = survivor.Wait()
	view, err = updatecmd.Bootstrap(s, sha, true, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if blocker, _ := view.Get("blocker"); blocker != nil {
		t.Fatalf("fully stopped workers remained blockers: %v", blocker)
	}
	inspection, _ := view.Get("inspection")
	releases, _ := inspection.(*ordjson.Object).Get("reconciliation")
	if len(releases.([]any)) != 2 {
		t.Fatalf("inspect did not preview both exact worker releases: %v", releases)
	}
	after, _ = os.ReadFile(filepath.Join(s.Tasks, "t-38545efe903c/task.json"))
	if string(before) != string(after) {
		t.Fatal("inspect persisted its proposed reconciliation")
	}
	for _, mode := range []string{"unsettled launch", "archived task", "preparing task", "live worker"} {
		current, err := s.ReadTask("t-38545efe903c")
		if err != nil {
			t.Fatal(err)
		}
		attempt, _ := reservations.Worker(current)
		if mode == "unsettled launch" {
			raw, _ := attempt.Get("native_launch")
			raw.(*ordjson.Object).Set("effect", "uncertain")
		}
		if mode == "archived task" {
			current.Set("status", "archived")
		}
		if mode == "preparing task" {
			current.Set("status", "preparing")
			current.Set("prepare_operation_id", "unresolved-preparation")
		}
		if mode == "live worker" {
			f.SetTerminal(hcclient.Terminal{Address: "t5", CreatedMS: 1, Cwd: worktree.(string), State: "live", Status: &hcclient.Status{State: "done"}})
		}
		if err := s.SaveTask(current); err != nil {
			t.Fatal(err)
		}
		if _, err := updatecmd.Bootstrap(s, sha, true, true, ""); err == nil {
			t.Fatalf("bootstrap released %s", mode)
		}
		unchanged, _ := s.ReadTask("t-38545efe903c")
		actual, _ := reservations.Worker(unchanged)
		if state, _ := actual.Get("state"); state != "running" {
			t.Fatalf("refusal changed %s reservation", mode)
		}
		if err := s.SaveTask(first); err != nil {
			t.Fatal(err)
		}
		finished, exit := uint64(2), int32(0)
		f.SetTerminal(hcclient.Terminal{Address: "t5", CreatedMS: 1, Cwd: worktree.(string), State: "finished", FinishedMS: &finished, ExitCode: &exit})
	}
	worker, _ := reservations.Worker(first)
	id, _ := worker.Get("id")
	cmd := exec.Command(filepath.Join(root, "bin/sumctl"), "execution", "park", "t-38545efe903c", "--attempt", id.(string))
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("old ordinary conversation unexpectedly parked work: %s", out)
	}
	result := bootstrapDriver(t, root, sha, env, "")
	claim, _ := result.Get("claim")
	if asString(claim) == "" {
		t.Fatal("bootstrap did not return its logical claim")
	}
	for _, task := range tasks {
		id, _ := task.Get("id")
		current, err := s.ReadTask(id.(string))
		if err != nil {
			t.Fatal(err)
		}
		attempt, err := reservations.Worker(current)
		if err != nil {
			t.Fatal(err)
		}
		state, _ := attempt.Get("state")
		if state != "released" {
			t.Fatalf("stopped worker was not reconciled: %v", state)
		}
		if status, _ := current.Get("status"); status != "waiting" {
			t.Fatalf("stopped work was marked accepted or lost: %v", status)
		}
		if generation, _ := attempt.Get("generation"); generation != json.Number("6") {
			t.Fatalf("worker was released more than once: %v", generation)
		}
		if questions, _ := current.Get("questions"); len(questions.([]any)) != 1 {
			t.Fatal("reconciliation erased an unresolved question")
		}
	}
}

func TestBootstrapWorkerReconciliationRecovery(t *testing.T) {
	clearFallbackIdentity(t)
	old := buildNativeBaseHelper(t)
	for _, mode := range []string{"partial release", "changed generation"} {
		t.Run(mode, func(t *testing.T) {
			root, s, f, sha, env := bootstrapInstallation(t, old)
			tasks := bootstrapStoppedWorkers(t, root, s, f)
			interrupt := errors.New("interrupted worker reconciliation")
			updatecmd.TestBootstrapBoundary = func(phase string) error {
				if mode == "partial release" && phase == "reconciled:t-38545efe903c" {
					return interrupt
				}
				if mode == "changed generation" && phase == "prepared" {
					current, err := s.ReadTask("t-38545efe903c")
					if err != nil {
						return err
					}
					if _, err := reservations.Transition(current, "x-abc9d0bcbb95", "running", store.Now(), nil, nil); err != nil {
						return err
					}
					if err := s.SaveTask(current); err != nil {
						return err
					}
					return interrupt
				}
				return nil
			}
			t.Cleanup(func() { updatecmd.TestBootstrapBoundary = nil })
			if _, err := updatecmd.Bootstrap(s, sha, true, true, ""); !errors.Is(err, interrupt) {
				t.Fatalf("did not interrupt at reconciliation: %v", err)
			}
			updatecmd.TestBootstrapBoundary = nil
			raw, err := ordjson.ReadFile(filepath.Join(root, ".local/bootstrap.json"))
			if err != nil {
				t.Fatal(err)
			}
			op, _ := raw.(*ordjson.Object).Get("operation")
			pointer, err := ordjson.ReadFile(filepath.Join(s.Root, "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			if schema, _ := pointer.(*ordjson.Object).Get("schema"); schema != json.Number("2") {
				t.Fatal("interrupted reconciliation prematurely fenced the old writer")
			}
			first, _ := s.ReadTask("t-38545efe903c")
			second, _ := s.ReadTask("t-bfc3d2e38890")
			a, _ := reservations.Worker(first)
			b, _ := reservations.Worker(second)
			if mode == "changed generation" {
				if _, err := updatecmd.Bootstrap(s, sha, true, true, op.(string)); err == nil || !strings.Contains(err.Error(), "plan changed") {
					t.Fatalf("accepted stale release generations: %v", err)
				}
				if state, _ := a.Get("state"); state != "running" {
					t.Fatal("stale recovery released a worker")
				}
				return
			}
			if state, _ := a.Get("state"); state != "released" {
				t.Fatal("first release was not durably saved")
			}
			if state, _ := b.Get("state"); state != "running" {
				t.Fatal("second release did not remain pending")
			}
			checkout, _ := first.Get("worktree")
			survivor := exec.Command("sleep", "60")
			survivor.Dir = checkout.(string)
			if err := survivor.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if survivor.ProcessState == nil {
					_ = survivor.Process.Kill()
					_ = survivor.Wait()
				}
			})
			if _, err := updatecmd.Bootstrap(s, sha, true, true, op.(string)); err == nil || !strings.Contains(err.Error(), "process") {
				t.Fatalf("recovery trusted a cached release over a survivor: %v", err)
			}
			pending, _ := s.ReadTask("t-bfc3d2e38890")
			pendingWorker, _ := reservations.Worker(pending)
			if state, _ := pendingWorker.Get("state"); state != "running" {
				t.Fatal("blocked recovery committed the remaining release")
			}
			_ = survivor.Process.Kill()
			_ = survivor.Wait()
			bootstrapDriver(t, root, sha, env, op.(string))
			for _, task := range tasks {
				id, _ := task.Get("id")
				current, _ := s.ReadTask(id.(string))
				attempt, _ := reservations.Worker(current)
				if generation, _ := attempt.Get("generation"); generation != json.Number("6") {
					t.Fatalf("replayed worker release: %v", generation)
				}
				if state, _ := attempt.Get("state"); state != "released" {
					t.Fatal("pending worker did not release during recovery")
				}
			}
		})
	}
}

func bootstrapStoppedWorkers(t *testing.T, root string, s *store.Store, f *hctest.Fixture) []*ordjson.Object {
	t.Helper()
	owner, err := s.Owner()
	if err != nil {
		t.Fatal(err)
	}
	var tasks []*ordjson.Object
	for i, id := range []string{"t-38545efe903c", "t-bfc3d2e38890"} {
		checkout := filepath.Join(root, ".sum/checkouts", id)
		if err := os.MkdirAll(checkout, 0700); err != nil {
			t.Fatal(err)
		}
		checkout, _ = filepath.EvalSymlinks(checkout)
		attempt, err := reservations.NewAttempt("worker", owner, checkout, store.Now(), "running", nil)
		if err != nil {
			t.Fatal(err)
		}
		attempt.Set("generation", json.Number("5"))
		attempt.Set("id", []string{"x-abc9d0bcbb95", "x-80f3eb10a213"}[i])
		e := store.EndpointFromContext(owner)
		e.Role, e.Task, e.Address, e.Cwd = "worker", id, []string{"t5", "t6"}[i], checkout
		e.Attempt = []string{"x-abc9d0bcbb95", "x-80f3eb10a213"}[i]
		fg := &hcclient.Foreground{PID: 2147480000, Name: "grok", Argv: []string{"grok"}, Cwd: checkout}
		bound := incarnation.NativeEvidence{Endpoint: e.Native(), PID: 2147480000, Foreground: fg}.Record(store.Now())
		registration, err := s.Register(e, "worker", id, bound)
		if err != nil {
			t.Fatal(err)
		}
		receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: e.Native(), Kind: "worker-init", Attempt: e.Attempt})
		registration.Set("native_receipt", receipt)
		if err := ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(e)+".json"), registration); err != nil {
			t.Fatal(err)
		}
		finished, exit := uint64(2), int32(0)
		f.SetTerminal(hcclient.Terminal{Address: e.Address, CreatedMS: e.CreatedMS, Cwd: checkout, State: "finished", FinishedMS: &finished, ExitCode: &exit})
		launch := ordjson.NewObject()
		launch.Set("effect", "created")
		launch.Set("endpoint", e.Object())
		attempt.Set("native_launch", launch)
		task := ordjson.NewObject()
		task.Set("schema", json.Number("2"))
		task.Set("id", id)
		task.Set("machine", e.Machine)
		task.Set("status", "running")
		task.Set("worktree", checkout)
		task.Set("worker_endpoint", e.Object())
		task.Set("execution", reservations.NewExecution(attempt))
		question := ordjson.NewObject()
		question.Set("id", "q-preserved")
		question.Set("text", "Keep this decision unresolved")
		question.Set("status", "open")
		task.Set("questions", []any{question})
		task.Set("owner", owner)
		task.Set("parent", owner)
		if err := s.SaveTask(task); err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, task)
	}
	return tasks
}
