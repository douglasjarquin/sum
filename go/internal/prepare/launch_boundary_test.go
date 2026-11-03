package prepare

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/hcboot"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

type nativeBoundaryLab struct {
	state   *store.Store
	actor   *ordjson.Object
	task    *ordjson.Object
	worker  *ordjson.Object
	runtime string
	core    *hctest.Fixture
	route   hcboot.Route
}

func newNativeBoundaryLab(t *testing.T, fresh bool) nativeBoundaryLab {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "launch boundary", false)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := app.RequireClaim(s, session.Token())
	if err != nil {
		t.Fatal(err)
	}
	core := hctest.New(t)
	core.DefaultRoute(t)
	route, err := hcboot.SelectedRoute(s)
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		route.CoreID = core.CoreID
		value, _ := ordjson.FromValue(route)
		if err = ordjson.WriteFile(filepath.Join(s.Root, "runtime-route.json"), value); err != nil {
			t.Fatal(err)
		}
	}
	if err = core.LoadTemplatesFrom(route.ConfigHome); err != nil {
		t.Fatal(err)
	}
	runtime := t.TempDir()
	binary := filepath.Join(runtime, ".local/bin/codex")
	if err = os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(binary, []byte("#!/bin/sh\necho 'codex-cli 0.166.0'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	options := ordjson.NewObject()
	options.Set("codex_home", t.TempDir())
	settings := ordjson.NewObject()
	settings.Set("schema", json.Number("1"))
	settings.Set("template", options)
	if err = ordjson.WriteFile(filepath.Join(s.Home, "settings.json"), settings); err != nil {
		t.Fatal(err)
	}
	checkout := t.TempDir()
	worker, err := reservations.NewAttempt("worker", session.Owner(), checkout, store.Now(), "held", nil)
	if err != nil {
		t.Fatal(err)
	}
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", json.Number("2"))
	task.Set("id", "t-0123456789ab")
	task.Set("machine", host.ID)
	task.Set("status", "prepared")
	task.Set("worktree", checkout)
	task.Set("execution", reservations.NewExecution(worker))
	task.Set("evidence", []any{})
	task.Set("owner", session.Owner())
	task.Set("parent", nil)
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	return nativeBoundaryLab{s, actor, task, worker, runtime, core, route}
}

func (l nativeBoundaryLab) start() (*ordjson.Object, error) {
	unlock, err := l.state.Lock()
	if err != nil {
		return nil, err
	}
	spec := ordjson.NewObject()
	spec.Set("harness", "codex")
	return startNative(l.state, l.actor, l.runtime, asString(policyField(l.task, "id")), filepath.Join(asString(policyField(l.task, "worktree")), "brief.md"), l.task, l.worker, spec, nil, unlock)
}

func TestFirstWorkerLaunchPreparesDefaultRouteBeforeTemplate(t *testing.T) {
	l := newNativeBoundaryLab(t, true)
	for _, path := range []string{filepath.Join(l.route.ConfigHome, "herdr-core/templates"), filepath.Join(l.state.Root, "runtime-route.json")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("fresh fixture already has %s: %v", path, err)
		}
	}
	out, err := l.start()
	if err != nil {
		t.Fatal(err)
	}
	if store.EndpointFromContext(asObject(policyField(out, "worker_endpoint"))).Address == "" {
		t.Fatal("first launch did not save its endpoint")
	}
	route, err := hcboot.SelectedRoute(l.state)
	if err != nil || route.CoreID != l.core.CoreID {
		t.Fatalf("route: %+v %v", route, err)
	}
	if len(l.core.Terminals()) != 1 {
		t.Fatal("first launch did not create exactly one worker")
	}
}

func TestWorkerLaunchExcludesTransferThroughCreationAccounting(t *testing.T) {
	l := newNativeBoundaryLab(t, false)
	entered, release := l.core.HoldReplyNext("terminal.run", "")
	defer release()
	l.core.QueueFault("terminal.run", hctest.Fault{LoseResponse: true})
	launched := make(chan error, 1)
	go func() { _, err := l.start(); launched <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("launch never reached native creation")
	}
	other, err := store.Open(l.state.Root)
	if err != nil {
		t.Fatal(err)
	}
	transferred := make(chan error, 1)
	go func() { _, err := other.ClaimCoordinator("", "replacement", true); transferred <- err }()
	select {
	case err := <-transferred:
		release()
		<-launched
		t.Fatalf("transfer completed before native result accounting: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case err := <-launched:
		if err == nil {
			t.Fatal("lost response accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("launch did not finish")
	}
	select {
	case err := <-transferred:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("transfer stayed blocked after result accounting")
	}
	task, err := l.state.ReadTask(asString(policyField(l.task, "id")))
	if err != nil {
		t.Fatal(err)
	}
	worker, err := reservations.Worker(task)
	if err != nil {
		t.Fatal(err)
	}
	if asString(policyField(worker, "state")) != "uncertain" || len(l.core.Terminals()) != 1 {
		t.Fatal("lost creation was not durably retained as one uncertain attempt")
	}
	for key := range l.core.Terminals()[0].Labels {
		if strings.HasPrefix(key, "herdr.") {
			t.Fatalf("creation published Herdr labels before the reply was recorded: %s", key)
		}
	}
	if visibility := asObject(policyField(asObject(policyField(worker, "native_launch")), "visibility")); asString(policyField(visibility, "state")) != launch.VisibilityPending {
		t.Fatalf("visibility intent is not retained for reconciliation: %v", visibility)
	}
}

func (l nativeBoundaryLab) workerVisibility(t *testing.T) (*ordjson.Object, store.Endpoint) {
	t.Helper()
	task, err := l.state.ReadTask(asString(policyField(l.task, "id")))
	if err != nil {
		t.Fatal(err)
	}
	worker, err := reservations.Worker(task)
	if err != nil {
		t.Fatal(err)
	}
	return asObject(policyField(asObject(policyField(worker, "native_launch")), "visibility")), store.EndpointFromContext(asObject(policyField(task, "worker_endpoint")))
}

func TestWorkerAdoptionFollowsOriginalProofIntoTheInstallationSpace(t *testing.T) {
	l := newNativeBoundaryLab(t, false)
	if _, err := l.start(); err != nil {
		t.Fatal(err)
	}
	instance, _ := l.state.Instance()
	want := launch.VisibilityLabels(instance, asString(policyField(l.task, "id")))
	visibility, ep := l.workerVisibility(t)
	if asString(policyField(visibility, "state")) != launch.VisibilityApplied {
		t.Fatalf("visibility = %v", visibility)
	}
	terminal := l.core.Terminals()[0]
	if terminal.Labels["herdr.space"] != want["herdr.space"] || terminal.Labels["herdr.tab"] != want["herdr.tab"] || terminal.Labels["sum.attempt"] != ep.Attempt {
		t.Fatalf("terminal labels = %v, want %v", terminal.Labels, want)
	}
	var order []string
	for _, call := range l.core.Calls() {
		switch call.Method {
		case "terminal.run":
			if labels, _ := call.Params["labels"].(map[string]any); labels["herdr.space"] != nil {
				t.Fatalf("creation request carried Herdr labels: %v", labels)
			}
			order = append(order, call.Method)
		case "terminal.label", "terminal.send":
			order = append(order, call.Method)
		}
	}
	if strings.Join(order, " ") != "terminal.run terminal.label terminal.send" {
		t.Fatalf("adoption order = %v", order)
	}
	if err := launch.ReconcileVisibility(context.Background(), l.state, ep, LocateWorker(ep.Attempt)); err != nil {
		t.Fatal(err)
	}
	labelCalls := 0
	for _, call := range l.core.Calls() {
		if call.Method == "terminal.label" {
			labelCalls++
		}
	}
	if labelCalls != 1 {
		t.Fatalf("settled visibility was republished: %d label calls", labelCalls)
	}
}

func TestWorkerLostLabelReplyIsReconciledFromTheExactTerminal(t *testing.T) {
	l := newNativeBoundaryLab(t, false)
	l.core.QueueFault("terminal.label", hctest.Fault{LoseResponse: true})
	if _, err := l.start(); err != nil {
		t.Fatal(err)
	}
	visibility, ep := l.workerVisibility(t)
	if asString(policyField(visibility, "state")) != launch.VisibilityUncertain || ep.Address == "" {
		t.Fatalf("lost label reply was not retained as uncertain: %v %+v", visibility, ep)
	}
	if len(l.core.Inputs(ep.Address)) != 1 {
		t.Fatal("an uncertain adoption blocked the opening")
	}
	if err := launch.ReconcileVisibility(context.Background(), l.state, ep, LocateWorker(ep.Attempt)); err != nil {
		t.Fatal(err)
	}
	visibility, _ = l.workerVisibility(t)
	if asString(policyField(visibility, "state")) != launch.VisibilityApplied || len(l.core.Terminals()) != 1 {
		t.Fatalf("reconciliation did not settle the lost reply from the terminal: %v", visibility)
	}
}

func TestRecordsWithoutVisibilityIntentAreNeverLabeled(t *testing.T) {
	l := newNativeBoundaryLab(t, false)
	l.core.QueueFault("terminal.label", hctest.Fault{LoseResponse: true})
	if _, err := l.start(); err != nil {
		t.Fatal(err)
	}
	_, ep := l.workerVisibility(t)
	task, err := l.state.ReadTask(ep.Task)
	if err != nil {
		t.Fatal(err)
	}
	worker, _ := reservations.Worker(task)
	asObject(policyField(worker, "native_launch")).Delete("visibility")
	if err = l.state.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	labels := len(l.core.Calls())
	client := l.core.Client()
	if err = launch.Adoption(context.Background(), l.state, ep, &client, LocateWorker(ep.Attempt))(); err != nil {
		t.Fatal(err)
	}
	if err = launch.ReconcileVisibility(context.Background(), l.state, ep, LocateWorker(ep.Attempt)); err != nil {
		t.Fatal(err)
	}
	for _, call := range l.core.Calls()[labels:] {
		if call.Method == "terminal.label" {
			t.Fatal("a record without visibility intent was labeled")
		}
	}
	visibility, _ := l.workerVisibility(t)
	if visibility != nil {
		t.Fatalf("recovery invented a visibility intent: %v", visibility)
	}
}

func TestWorkerExitBeforeAdoptionSettlesVisibilityAsRefused(t *testing.T) {
	l := newNativeBoundaryLab(t, false)
	l.core.QueueFault("terminal.label", hctest.Fault{LoseResponse: true})
	if _, err := l.start(); err != nil {
		t.Fatal(err)
	}
	_, ep := l.workerVisibility(t)
	l.core.Exit(ep.Address, 0)
	labels := 0
	for _, call := range l.core.Calls() {
		if call.Method == "terminal.label" {
			labels++
		}
	}
	for i := 0; i < 2; i++ {
		if err := launch.ReconcileVisibility(context.Background(), l.state, ep, LocateWorker(ep.Attempt)); err != nil {
			t.Fatalf("reconcile %d of an exited unadopted terminal failed: %v", i, err)
		}
	}
	visibility, _ := l.workerVisibility(t)
	if asString(policyField(visibility, "state")) != launch.VisibilityRefused || asString(policyField(visibility, "detail")) == "" {
		t.Fatalf("exit before adoption was not settled: %v", visibility)
	}
	after := 0
	for _, call := range l.core.Calls() {
		if call.Method == "terminal.label" {
			after++
		}
	}
	if after != labels || len(l.core.Terminals()) != 1 {
		t.Fatal("an exited terminal was relabeled or recreated")
	}
}

func TestWorkerAdoptionRefusesAReplacementShell(t *testing.T) {
	l := newNativeBoundaryLab(t, false)
	l.core.QueueFault("terminal.label", hctest.Fault{LoseResponse: true})
	if _, err := l.start(); err != nil {
		t.Fatal(err)
	}
	_, ep := l.workerVisibility(t)
	inputs := len(l.core.Inputs(ep.Address))
	terminal := l.core.Terminals()[0]
	client := l.core.Client()
	if _, err := client.Label(context.Background(), ep.Address, map[string]string{"herdr.space": "sum-kept"}); err != nil {
		t.Fatal(err)
	}
	l.core.SetForeground(ep.Address, hcclient.Foreground{PID: 777, Name: "sh", Argv: []string{"sh"}, Cwd: terminal.Cwd})
	err := launch.ReconcileVisibility(context.Background(), l.state, ep, LocateWorker(ep.Attempt))
	if err == nil || !strings.Contains(err.Error(), "adoption refused") {
		t.Fatalf("replacement shell was adopted: %v", err)
	}
	visibility, _ := l.workerVisibility(t)
	if asString(policyField(visibility, "state")) == launch.VisibilityApplied {
		t.Fatalf("replacement shell gained visibility authority: %v", visibility)
	}
	if len(l.core.Inputs(ep.Address)) != inputs {
		t.Fatal("replacement shell received input")
	}
}
