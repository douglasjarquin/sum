package execution

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/maintenance"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/prepare"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func Reconcile(s *store.Store, ctx *ordjson.Object, runtimeRoot, taskID, attempt string) (*ordjson.Object, error) {
	task, err := reconcileCreation(s, ctx, runtimeRoot, taskID, attempt)
	if err != nil {
		return nil, err
	}
	endpoint := workerEndpoint(task)
	if endpoint.Validate() != nil || endpoint.Attempt != attempt {
		return task, nil
	}
	observe, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := launch.ReconcileVisibility(observe, s, endpoint, prepare.LocateWorker(attempt)); err != nil {
		return nil, err
	}
	return s.ReadTask(taskID)
}

func reconcileCreation(s *store.Store, ctx *ordjson.Object, runtimeRoot, taskID, attempt string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	if err = s.CheckMachine(task); err != nil {
		return nil, err
	}
	if err = maintenance.Refuse(task, "Execution reconcile"); err != nil {
		return nil, err
	}
	actor, err := app.ObserveTaskActor(s, ctx)
	if err != nil {
		return nil, err
	}
	if err = app.CheckTaskRole(s, task, actor, "coordinator"); err != nil {
		return nil, err
	}
	worker, err := reservations.Worker(task)
	if err != nil {
		return nil, err
	}
	if field(worker, "id") != attempt {
		return nil, fmt.Errorf("exact current worker attempt required")
	}
	if endpoint := workerEndpoint(task); endpoint.Validate() == nil && endpoint.Attempt == attempt {
		return task, nil
	}
	native := asObject(field(worker, "native_launch"))
	if native == nil || field(native, "opening_delivery") != nil {
		return nil, fmt.Errorf("split creation has no unreconciled original intent")
	}
	var params hcclient.RunParams
	var template returnsTemplateForReconcile
	if err = decodeNative(field(native, "intent"), &params); err != nil {
		return nil, err
	}
	if err = decodeNative(field(native, "native_template"), &template); err != nil {
		return nil, err
	}
	instance, err := s.Instance()
	if err != nil {
		return nil, err
	}
	if params.Labels["sum.installation"] != instance || params.Labels["sum.task"] != taskID || params.Labels["sum.attempt"] != attempt || params.Labels["sum.role"] != "worker" || params.Cwd != field(task, "worktree") {
		return nil, fmt.Errorf("creation intent does not belong to this worker attempt and checkout")
	}
	before, _ := ordjson.MarshalCompact(native)
	if params.Env["HC_SOCKET"] == "" || params.Env["SUM_CORE_ID"] == "" || params.Env["SUM_SESSION"] == "" || params.Env["SUM_TASK"] != taskID || params.Env["SUM_ATTEMPT"] != attempt || params.Env["SUM_ROLE"] != "worker" {
		return nil, fmt.Errorf("saved worker creation route is incomplete or mismatched")
	}
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	client := hcclient.Client{Socket: params.Env["HC_SOCKET"], CoreID: params.Env["SUM_CORE_ID"]}
	observe, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	terminal, err := launch.DiscoverWorkerCreation(observe, &client, params, template.Spec, asString(field(native, "claimed_at")))
	if err != nil {
		return nil, err
	}
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, Socket: params.Env["HC_SOCKET"], CoreID: params.Env["SUM_CORE_ID"], Session: params.Env["SUM_SESSION"], Address: terminal.Address, CreatedMS: terminal.CreatedMS, Task: taskID, Role: "worker", Attempt: attempt, BindingGeneration: 1, Cwd: params.Cwd}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	w, err := reservations.Worker(current)
	if err != nil {
		return nil, err
	}
	if field(w, "id") != attempt || field(asObject(field(w, "native_launch")), "opening_delivery") != nil || workerEndpoint(current).Attempt == attempt {
		return nil, fmt.Errorf("creation intent changed during reconciliation")
	}
	if err = maintenance.Refuse(current, "Execution reconcile"); err != nil {
		return nil, err
	}
	if err = app.CheckTaskRole(s, current, actor, "coordinator"); err != nil {
		return nil, err
	}
	n := asObject(field(w, "native_launch"))
	after, _ := ordjson.MarshalCompact(n)
	if !bytes.Equal(before, after) {
		return nil, fmt.Errorf("creation intent changed during discovery")
	}
	n.Set("effect", "created")
	n.Set("endpoint", endpoint.Object())
	if template.Spec.Schema == 2 {
		first := ordjson.NewObject()
		first.Set("sent", false)
		n.Set("first_message", first)
	} else {
		current.Set("status", "needs-attention")
		current.Set("error", prepare.LaunchUncertainPrefix+": original terminal reconciled; historical opening delivery remains unknown. Inspect the existing terminal without replay. Confirm only after its worker-init receipt, or park only after proven original-process exit.")
	}
	evidence := (incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: *terminal.PID, Foreground: terminal.Foreground}).Record(store.Now())
	n.Set("incarnation", evidence)
	registration, err := s.Register(endpoint, "worker", taskID, evidence)
	if err != nil {
		return nil, err
	}
	registration.Set("native_template", field(n, "native_template"))
	if err = ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(endpoint)+".json"), registration); err != nil {
		return nil, err
	}
	current.Set("worker_endpoint", endpoint.Object())
	if err = s.SaveTask(current); err != nil {
		return nil, err
	}
	return current, nil
}

type returnsTemplateForReconcile struct {
	Spec launch.HCTemplate `json:"spec"`
}
