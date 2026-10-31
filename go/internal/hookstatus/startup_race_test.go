package hookstatus

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestReplacementObserverStartsBeforeRunReplyAndKeepsItsOwnReceipt(t *testing.T) {
	f := hctest.New(t)
	physical, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(physical, "home"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	coordinator := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Session: "lab", Socket: f.Socket, Address: "t99", Role: "coordinator", Attempt: "coord-1", BindingGeneration: 1, CreatedMS: 99}
	client := f.Client()
	start := func(endpoint store.Endpoint) (context.CancelFunc, <-chan error) {
		t.Helper()
		terminal, err := client.Get(context.Background(), endpoint.Address)
		if err != nil {
			t.Fatal(err)
		}
		pid := uint32(200 + terminal.CreatedMS)
		terminal.PID = &pid
		terminal.Foreground = &hcclient.Foreground{PID: int32(pid), Name: "sumctl", Argv: terminal.Argv, Cwd: terminal.Cwd}
		f.SetTerminal(terminal)
		observerStore, err := store.Open(s.Root)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- Observe(ctx, observerStore, endpoint, "/runtime", "/sumctl") }()
		return cancel, done
	}
	stop := func(cancel context.CancelFunc, done <-chan error) {
		t.Helper()
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("observer did not stop")
		}
	}
	initial, err := ensureObserver(s, coordinator, "/runtime", "/sumctl", false)
	if err != nil {
		t.Fatal(err)
	}
	old := store.EndpointFromContext(asObject(value(initial, "observer")))
	cancelOld, oldDone := start(old)
	if _, err = AwaitReady(context.Background(), s); err != nil {
		cancelOld()
		t.Fatal(err)
	}
	stop(cancelOld, oldDone)
	f.Exit(old.Address, 0)
	entered, release := f.HoldReplyNext("terminal.run", "")
	defer release()
	launched := make(chan error, 1)
	go func() { _, err := ensureObserver(s, coordinator, "/runtime", "/sumctl", false); launched <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("replacement Run effect not reached")
	}
	health, err := readHealth(s)
	if err != nil {
		t.Fatal(err)
	}
	oldProofCleared := value(health, "observer") == nil && value(health, "observer_receipt") == nil && value(health, "observer_incarnation") == nil
	retiredCount := len(asList(value(health, "retired_observers")))
	snapshot, err := client.List(context.Background(), hcclient.ListParams{All: true})
	if err != nil {
		t.Fatal(err)
	}
	var successor store.Endpoint
	for _, terminal := range snapshot.Terminals {
		if terminal.State == "live" && terminal.Address != old.Address {
			successor = coordinator
			successor.Address = terminal.Address
			successor.CreatedMS = terminal.CreatedMS
		}
	}
	if successor.Address == coordinator.Address || successor.Address == "" {
		t.Fatal("no replacement native terminal")
	}
	cancelNew, newDone := start(successor)
	defer cancelNew()
	await(t, func() bool {
		select {
		case err := <-newDone:
			t.Fatalf("replacement helper exited before native Run reply: %v", err)
		default:
		}
		health, err := readHealth(s)
		return err == nil && value(health, "observer_receipt") != nil && value(health, "connected_at") != nil && store.EndpointFromContext(asObject(value(health, "observer"))).Address == successor.Address
	})
	if !oldProofCleared || retiredCount != 1 {
		t.Fatal("replacement did not retire old startup proof before Run")
	}
	before, err := readHealth(s)
	if err != nil {
		t.Fatal(err)
	}
	receiptBefore := value(before, "observer_receipt")
	bound := store.EndpointFromContext(asObject(value(before, "observer")))
	if bound.Attempt == old.Attempt || bound.Attempt != value(before, "launch_attempt") {
		t.Fatal("replacement reused the old launch intent")
	}
	select {
	case <-launched:
		t.Fatal("Run reply was not held while child wrote receipt")
	default:
	}
	release()
	select {
	case err := <-launched:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("parent did not merge Run reply")
	}
	after, err := readHealth(s)
	if err != nil {
		t.Fatal(err)
	}
	if value(after, "degraded") != nil {
		t.Fatalf("late Run reply degraded healthy child: %v", value(after, "degraded"))
	}
	if !reflect.DeepEqual(receiptBefore, value(after, "observer_receipt")) {
		t.Fatal("Run result overwrote early child receipt")
	}
	if _, err = AwaitReady(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	stop(cancelNew, newDone)
}

func TestLateObserverRunReplyCannotOverwriteChangedLaunchIntent(t *testing.T) {
	f := hctest.New(t)
	s, err := store.Open(filepath.Join(t.TempDir(), "home"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	coordinator := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Session: "lab", Socket: f.Socket, Address: "t99", Role: "coordinator", Attempt: "coord-1", BindingGeneration: 1, CreatedMS: 99}
	entered, release := f.HoldReplyNext("terminal.run", "")
	defer release()
	done := make(chan error, 1)
	go func() { _, err := ensureObserver(s, coordinator, "/runtime", "/sumctl", false); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("native Run effect not reached")
	}
	replacement := ordjson.NewObject()
	replacement.Set("launch_attempt", "explicit-reconciliation")
	replacement.Set("launch_effect", "uncertain")
	if _, err = writeHealth(s, nil, replacement); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stale launch result accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("launch reply did not finish")
	}
	health, err := readHealth(s)
	if err != nil {
		t.Fatal(err)
	}
	if value(health, "launch_attempt") != "explicit-reconciliation" || value(health, "launch_effect") != "uncertain" || value(health, "observer") != nil {
		t.Fatal("late reply replaced the current intent")
	}
	runs := 0
	for _, call := range f.Calls() {
		if call.Method == "terminal.run" {
			runs++
		}
	}
	if runs != 1 {
		t.Fatal("unresolved launch was replayed")
	}
}
