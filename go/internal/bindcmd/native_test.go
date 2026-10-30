package bindcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/taskcheckout"
)

func TestNativeBindRequiresSuccessorReceiptAndPreservesGit(t *testing.T) {
	fixture := hctest.New(t)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		data, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, data)
		}
		return strings.TrimSpace(string(data))
	}
	git("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("owned\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "README")
	git("commit", "-m", "initial")
	base := git("rev-parse", "HEAD")
	st, err := store.Open(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Init(); err != nil {
		t.Fatal(err)
	}
	machine, err := st.Machine()
	if err != nil {
		t.Fatal(err)
	}
	taskID := "t-0123456789ab"
	intent, err := taskcheckout.Plan(st.Root, repo, taskID, base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = taskcheckout.Create(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	coordinator := store.Endpoint{Schema: 2, Backend: "hc", Machine: machine.ID, CoreID: fixture.CoreID, Session: "sum-test", Socket: fixture.Socket, Address: "t1", CreatedMS: 1, Role: "coordinator", Attempt: "bootstrap", BindingGeneration: 1, Cwd: root}
	register := func(endpoint store.Endpoint, pid uint32, kind string, receipt bool) *ordjson.Object {
		fg := &hcclient.Foreground{PID: int32(pid), Name: "sleep", Argv: []string{"sleep", "60"}, Cwd: endpoint.Cwd}
		bound := incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: pid, Foreground: fg}
		fixture.SetTerminal(hcclient.Terminal{Address: endpoint.Address, CreatedMS: endpoint.CreatedMS, Cwd: endpoint.Cwd, State: "live", PID: &pid, Foreground: fg})
		reg, err := st.Register(endpoint, endpoint.Role, func() any {
			if endpoint.Task != "" {
				return endpoint.Task
			}
			return nil
		}(), bound.Record(store.Now()))
		if err != nil {
			t.Fatal(err)
		}
		if receipt {
			r, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: endpoint.Native(), Kind: kind, Attempt: endpoint.Attempt})
			reg.Set("native_receipt", r)
			if err = ordjson.WriteFile(filepath.Join(st.Sessions, store.RegistrationKey(endpoint)+".json"), reg); err != nil {
				t.Fatal(err)
			}
		}
		return reg
	}
	owner := register(coordinator, 100000, "coordinator-init", true)
	if err = ordjson.WriteFile(filepath.Join(st.Home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	old := coordinator
	old.Address = "t2"
	old.CreatedMS = 2
	old.Role = "worker"
	old.Task = taskID
	old.Attempt = "x-0123456789ab"
	old.Cwd = intent.Path
	dead := exec.Command("sleep", "60")
	if err = dead.Start(); err != nil {
		t.Fatal(err)
	}
	pid := uint32(dead.Process.Pid)
	dead.Process.Kill()
	dead.Wait()
	register(old, pid, "worker-init", true)
	fixture.SetTerminal(hcclient.Terminal{Address: old.Address, CreatedMS: old.CreatedMS, Cwd: old.Cwd, State: "finished"})
	task := ordjson.NewObject()
	task.Set("schema", json.Number("2"))
	task.Set("id", taskID)
	task.Set("machine", machine.ID)
	task.Set("status", "running")
	task.Set("worktree", intent.Path)
	task.Set("worker_endpoint", old.Object())
	task.Set("evidence", []any{})
	obj, _ := ordjson.FromValue(intent)
	task.Set("checkout_intent", obj)
	if err = st.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	live := exec.Command("sleep", "60")
	live.Dir = intent.Path
	if err = live.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { live.Process.Kill(); live.Wait() }()
	next := old
	next.Address = "t3"
	next.CreatedMS = 3
	next.BindingGeneration = 2
	register(next, uint32(live.Process.Pid), "worker-init", false)
	if _, err = Run(st, coordinator.Object(), taskID, next.Address, "", false, returns.PumpOpts{}); err == nil {
		t.Fatal("replacement without receipt acquired worker authority")
	}
	unchanged, err := st.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if !SameEndpoint(object(unchanged, "worker_endpoint"), old.Object()) {
		t.Fatal("refusal changed worker binding")
	}
	register(next, uint32(live.Process.Pid), "worker-init", true)

	if err = settings.SetNativeEventsEnabled(st, false); err != nil {
		t.Fatal(err)
	}
	lsof := filepath.Join(root, "lsof")
	if err = os.WriteFile(lsof, []byte(fmt.Sprintf("#!/bin/sh\nprintf 'p%d\\nn%s\\n'\n", live.Process.Pid, intent.Path)), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_LSOF_BIN", lsof)
	result, err := Run(st, coordinator.Object(), taskID, next.Address, "", false, returns.PumpOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !SameEndpoint(object(result, "worker_endpoint"), next.Object()) {
		t.Fatal("matching receipt did not bind successor")
	}
	if _, err = taskcheckout.Verify(context.Background(), intent, false); err != nil {
		t.Fatal("rebind changed Git identity:", err)
	}
}
