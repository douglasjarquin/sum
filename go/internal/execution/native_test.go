package execution

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestNativeParkRequiresTerminalAndProcessDeath(t *testing.T) {
	fixture := hctest.New(t)
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	machine, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	checkout, _ := filepath.EvalSymlinks(t.TempDir())
	lsof := filepath.Join(root, "lsof")
	if err = os.WriteFile(lsof, []byte(fmt.Sprintf("#!/bin/sh\nprintf 'p%d\\nn/\\n'\n", os.Getpid())), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_LSOF_BIN", lsof)
	session, err := s.ClaimCoordinator("", "maintenance conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := app.RequireClaim(s, session.Token())
	if err != nil {
		t.Fatal(err)
	}
	process := exec.Command("sleep", "60")
	if err = process.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	defer func() {
		if !stopped {
			process.Process.Kill()
			process.Wait()
		}
	}()
	attempt, err := reservations.NewAttempt("worker", session.Owner(), checkout, store.Now(), "running", nil)
	if err != nil {
		t.Fatal(err)
	}
	id := asString(field(attempt, "id"))
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: machine.ID, CoreID: fixture.CoreID, Session: "sum-test", Socket: fixture.Socket, Address: "t2", Role: "worker", Task: "t-abcdef123456", Attempt: id, BindingGeneration: 1, CreatedMS: 2, Cwd: checkout}
	pid := uint32(process.Process.Pid)
	foreground := &hcclient.Foreground{PID: int32(pid), Name: "sleep", Argv: []string{"sleep", "60"}, Cwd: checkout}
	terminal := hcclient.Terminal{Address: "t2", Cwd: checkout, State: "live", CreatedMS: 2, PID: &pid, Foreground: foreground, Status: &hcclient.Status{State: "done"}}
	fixture.SetTerminal(terminal)
	bound := incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: pid, Foreground: foreground}
	if _, err = s.Register(endpoint, "worker", endpoint.Task, bound.Record(store.Now())); err != nil {
		t.Fatal(err)
	}
	native := ordjson.NewObject()
	native.Set("incarnation", bound.Record(store.Now()))
	attempt.Set("native_launch", native)
	task := ordjson.NewObject()
	task.Set("schema", json.Number("2"))
	task.Set("id", endpoint.Task)
	task.Set("machine", machine.ID)
	task.Set("status", "running")
	task.Set("worktree", checkout)
	task.Set("owner", session.Owner())
	task.Set("parent", nil)
	task.Set("worker_endpoint", endpoint.Object())
	task.Set("execution", reservations.NewExecution(attempt))
	task.Set("questions", []any{"keep"})
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err = Park(s, ctx, root, endpoint.Task, id); err == nil {
		t.Fatal("Done terminal released capacity")
	}
	terminal.State = "finished"
	finished, exit := uint64(3), int32(0)
	terminal.FinishedMS = &finished
	terminal.ExitCode = &exit
	fixture.SetTerminal(terminal)
	task, err = s.ReadTask(endpoint.Task)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err = reservations.Worker(task)
	if err != nil {
		t.Fatal(err)
	}
	proof := ObserveStop(s, root, task, attempt)
	if proof.Outcome == OutcomeStopped {
		t.Fatal("finished terminal with live process released capacity")
	}
	process.Process.Kill()
	process.Wait()
	stopped = true
	if _, err = Park(s, ctx, root, endpoint.Task, id); err != nil {
		t.Fatal(err)
	}
	current, err := s.ReadTask(endpoint.Task)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := reservations.Worker(current)
	if err != nil {
		t.Fatal(err)
	}
	if field(worker, "state") != "released" {
		t.Fatal("dead owned process did not release capacity")
	}
	if rows, _ := field(current, "questions").([]any); len(rows) != 1 {
		t.Fatal("park erased questions")
	}
}
