package execution

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestWorkerCreationReconcilesSavedRouteAfterTransfer(t *testing.T) {
	l := newRedeliveryLab(t)
	task, err := l.s.ReadTask(l.endpoint.Task)
	if err != nil {
		t.Fatal(err)
	}
	worker, _ := reservations.Worker(task)
	native := asObject(field(worker, "native_launch"))
	var params hcclient.RunParams
	if err = decodeNative(field(native, "intent"), &params); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sleep", "60")
	command.Dir = l.endpoint.Cwd
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { command.Process.Kill(); command.Wait() }()
	argv, err := proc.ProcessArgv(command.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := exec.LookPath(argv[0])
	if err != nil {
		t.Fatal(err)
	}
	spec := launch.HCTemplate{Schema: 2, Name: params.Argv[0], Binary: binary, Argv: argv, Submit: "tab"}
	qualified, _ := ordjson.FromValue(returnsTemplateForReconcile{Spec: spec})
	native.Set("native_template", qualified)
	terminal := l.fixture.Terminals()[0]
	for _, candidate := range l.fixture.Terminals() {
		if candidate.Address == l.endpoint.Address {
			terminal = candidate
		}
	}
	terminal.Argv = []string{"sh", "-c", spec.Command()}
	terminal.Template = &spec.Name
	terminal.Submit = &spec.Submit
	pid := uint32(command.Process.Pid)
	terminal.PID = &pid
	terminal.Foreground = &hcclient.Foreground{PID: int32(pid), Argv: argv, Cwd: params.Cwd}
	l.fixture.SetTerminal(terminal)
	// Model a crash after creation but before saving the reply. The original
	// creation intent is retained byte-for-byte and no opening can be replayed.
	task.Delete("worker_endpoint")
	native.Delete("endpoint")
	native.Delete("opening_delivery")
	native.Delete("first_message")
	native.Set("effect", "uncertain")
	native.Set("claimed_at", time.UnixMilli(int64(terminal.CreatedMS)).UTC().Format(time.RFC3339Nano))
	if err = l.s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if err = ordjson.WriteFile(filepath.Join(l.s.Root, "runtime-route.json"), ordjson.NewObject()); err != nil {
		t.Fatal(err)
	}
	next, err := store.Open(l.s.Root)
	if err != nil {
		t.Fatal(err)
	}
	transferred, err := next.ClaimCoordinator("", "successor", true)
	if err != nil {
		t.Fatal(err)
	}
	before := len(l.fixture.Calls())
	if _, err = Reconcile(l.s, l.ctx, l.runtime, l.endpoint.Task, l.endpoint.Attempt); err == nil {
		t.Fatal("stale claim reconciled")
	}
	if len(l.fixture.Calls()) != before {
		t.Fatal("stale claim touched HC")
	}
	ctx, err := app.RequireClaim(next, transferred.Token())
	if err != nil {
		t.Fatal(err)
	}
	out, err := Reconcile(next, ctx, l.runtime, l.endpoint.Task, l.endpoint.Attempt)
	if err != nil {
		t.Fatal(err)
	}
	if !workerEndpoint(out).Native().SameAuthority(l.endpoint.Native()) {
		t.Fatal("reconciled a different endpoint")
	}
	if _, err = Reconcile(next, ctx, l.runtime, l.endpoint.Task, l.endpoint.Attempt); err != nil {
		t.Fatal(err)
	}
	if len(l.fixture.Terminals()) != 1 {
		t.Fatal("reconciliation created another worker")
	}
}
