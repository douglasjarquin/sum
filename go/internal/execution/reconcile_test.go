package execution

import (
	"os/exec"
	"path/filepath"
	"strings"
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

func unreconciledWorkerLab(t *testing.T) redeliveryLab {
	t.Helper()
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
	t.Cleanup(func() { command.Process.Kill(); command.Wait() })
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
	for key := range terminal.Labels {
		if strings.HasPrefix(key, "herdr.") {
			delete(terminal.Labels, key)
		}
	}
	terminal.Keep = false
	l.fixture.SetTerminal(terminal)
	instance, _ := l.s.Instance()
	pending, _ := ordjson.FromValue(launch.NewVisibility(instance, l.endpoint.Task))
	native.Set("visibility", pending)
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
	return l
}

func TestWorkerCreationReconcilesSavedRouteAfterTransfer(t *testing.T) {
	l := unreconciledWorkerLab(t)
	if err := ordjson.WriteFile(filepath.Join(l.s.Root, "runtime-route.json"), ordjson.NewObject()); err != nil {
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
	instance, _ := l.s.Instance()
	if labels := l.fixture.Terminals()[0].Labels; labels["herdr.space"] != launch.SpaceID(instance) {
		t.Fatalf("reconciled original was not adopted after its proof: %v", labels)
	}
}

func TestWorkerCreationReconcileBindingOwnership(t *testing.T) {
	for _, binding := range []string{"cleared", "foreign", "current", "intent-changed"} {
		t.Run(binding, func(t *testing.T) {
			l := unreconciledWorkerLab(t)
			if binding == "foreign" {
				task, err := l.s.ReadTask(l.endpoint.Task)
				if err != nil {
					t.Fatal(err)
				}
				foreign := l.endpoint
				foreign.Attempt = "x-retired"
				task.Set("worker_endpoint", foreign.Object())
				if err = l.s.SaveTask(task); err != nil {
					t.Fatal(err)
				}
			}
			entered, release := l.fixture.HoldReplyNext("terminal.list", "")
			defer release()
			type result struct {
				task *ordjson.Object
				err  error
			}
			done := make(chan result, 1)
			go func() {
				task, err := Reconcile(l.s, l.ctx, l.runtime, l.endpoint.Task, l.endpoint.Attempt)
				done <- result{task, err}
			}()
			select {
			case <-entered:
			case early := <-done:
				t.Fatalf("reconciliation skipped discovery: endpoint=%v error=%v", workerEndpoint(early.task), early.err)
			case <-time.After(5 * time.Second):
				t.Fatal("discovery did not reach list barrier")
			}
			if binding == "current" || binding == "intent-changed" {
				task, err := l.s.ReadTask(l.endpoint.Task)
				if err != nil {
					t.Fatal(err)
				}
				if binding == "current" {
					task.Set("worker_endpoint", l.endpoint.Object())
				} else {
					worker, _ := reservations.Worker(task)
					asObject(field(worker, "native_launch")).Set("claimed_at", "changed")
				}
				if err = l.s.SaveTask(task); err != nil {
					t.Fatal(err)
				}
			}
			release()
			outcome := <-done
			if binding == "current" || binding == "intent-changed" {
				if outcome.err == nil || !strings.Contains(outcome.err.Error(), "creation intent changed") {
					t.Fatalf("changed creation was persisted: %v", outcome.err)
				}
				return
			}
			if outcome.err != nil {
				t.Fatal(outcome.err)
			}
			if !workerEndpoint(outcome.task).Native().SameAuthority(l.endpoint.Native()) {
				t.Fatal("reconciliation persisted a different endpoint")
			}
			before := len(l.fixture.Calls())
			if _, err := Reconcile(l.s, l.ctx, l.runtime, l.endpoint.Task, l.endpoint.Attempt); err != nil {
				t.Fatal(err)
			}
			if len(l.fixture.Calls()) != before {
				t.Fatal("bound current attempt was not idempotent")
			}
		})
	}
}
