package prepare

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/hcboot"
	"github.com/douglasjarquin/sum/go/internal/hctest"
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
	route, err := hcboot.SelectedRoute(s, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if fresh {
		if err = os.MkdirAll(route.RuntimeDir, 0700); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(route.RuntimeDir) })
		if err = os.Symlink(core.Socket, route.Socket); err != nil {
			t.Fatal(err)
		}
	} else {
		route.Socket, route.CoreID = core.Socket, core.CoreID
		route.ConfigHome, route.StateHome, route.RuntimeDir = t.TempDir(), t.TempDir(), ""
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
	for _, path := range []string{l.route.ConfigHome, filepath.Join(l.state.Root, "runtime-route.json")} {
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
	route, err := hcboot.SelectedRoute(l.state, "", "", "")
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
}
