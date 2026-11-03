package cli

import (
	"encoding/json"
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
		task.Set("questions", []any{"keep-unresolved-question"})
		if err := s.SaveTask(task); err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, task)
	}
	return tasks
}
