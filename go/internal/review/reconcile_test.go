package review

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestReviewerLostReplyReconcilesSavedRouteAfterTransfer(t *testing.T) {
	s, task, ep, f := reviewerLab(t)
	session, err := s.ClaimCoordinator("", "original", false)
	if err != nil {
		t.Fatal(err)
	}
	oldCtx, err := app.RequireClaim(s, session.Token())
	if err != nil {
		t.Fatal(err)
	}
	task.Set("owner", session.Owner())
	task.Set("parent", nil)
	host, _ := s.Machine()
	ep.Machine = host.ID
	task.Set("machine", host.ID)
	command := exec.Command("sleep", "60")
	command.Dir = ep.Cwd
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
	spec := launch.HCTemplate{Schema: 2, Name: "review-recovery", Binary: binary, Argv: argv, Submit: "tab"}
	f.SetTemplates([]hcclient.TemplateInfo{{Name: spec.Name, Command: spec.Command()}})
	f.SetTemplateSubmit(spec.Name, "tab")
	reviewer := ep.Object()
	template, _ := ordjson.FromValue(returns.NativeTemplate{Spec: spec})
	reviewer.Set("native_template", template)
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	pid := uint32(command.Process.Pid)
	instance, _ := s.Instance()
	params := hcclient.RunParams{Argv: []string{spec.Name}, Cwd: ep.Cwd, Name: "exact-reviewer", OutliveParent: true, NoNotify: true, Labels: map[string]string{"sum.installation": instance, "sum.task": ep.Task, "sum.role": "reviewer", "sum.attempt": ep.Attempt}, Env: map[string]string{"HC_SOCKET": ep.Socket, "SUM_CORE_ID": ep.CoreID, "SUM_SESSION": ep.Session, "SUM_TASK": ep.Task, "SUM_ATTEMPT": ep.Attempt, "SUM_ROLE": "reviewer"}}
	f.QueueFault("terminal.run", hctest.Fault{LoseResponse: true, RunPID: &pid, RunForeground: &hcclient.Foreground{PID: int32(pid), Argv: argv, Cwd: ep.Cwd}})
	if _, err = launchPreparedNative(context.Background(), s, task, ep, reviewer, params); err == nil {
		t.Fatal("lost reply accepted")
	}
	saved, _ := s.ReadTask(ep.Task)
	objectField(objectField(saved, "reviewer"), "native_launch").Set("at", time.UnixMilli(int64(f.Terminals()[0].CreatedMS)).UTC().Format(time.RFC3339Nano))
	if err = s.SaveTask(saved); err != nil {
		t.Fatal(err)
	}
	// The workspace route is deliberately unusable; reconciliation must use the sealed intent.
	if err = ordjson.WriteFile(filepath.Join(s.Root, "runtime-route.json"), ordjson.NewObject()); err != nil {
		t.Fatal(err)
	}
	next, err := store.Open(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	transferred, err := next.ClaimCoordinator("", "successor", true)
	if err != nil {
		t.Fatal(err)
	}
	before := len(f.Calls())
	if _, err = Reconcile(s, oldCtx, ep.Task, ep.Attempt); err == nil {
		t.Fatal("stale claim reconciled")
	}
	if len(f.Calls()) != before {
		t.Fatal("stale claim touched HC")
	}
	ctx, err := app.RequireClaim(next, transferred.Token())
	if err != nil {
		t.Fatal(err)
	}
	out, err := Reconcile(next, ctx, ep.Task, ep.Attempt)
	if err != nil {
		t.Fatal(err)
	}
	recovered := store.EndpointFromContext(objectField(out, "reviewer"))
	if recovered.Address == "" || recovered.CoreID != ep.CoreID || recovered.Socket != ep.Socket {
		t.Fatal("lost original endpoint")
	}
	if _, err = Reconcile(next, ctx, ep.Task, ep.Attempt); err != nil {
		t.Fatal(err)
	}
	if len(f.Terminals()) != 1 {
		t.Fatal("reconciliation created another reviewer")
	}
	if labels := f.Terminals()[0].Labels; labels["herdr.space"] != launch.SpaceID(instance) {
		t.Fatalf("reconciled original was not adopted after its proof: %v", labels)
	}
}
