package hookstatus

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestObserverWaitsForForegroundWithoutAdoptingChangedProgram(t *testing.T) {
	cases := map[string]func(*hcclient.Terminal){
		"delayed foreground":  nil,
		"changed program pid": func(v *hcclient.Terminal) { pid := uint32(999); v.PID = &pid },
		"changed creation":    func(v *hcclient.Terminal) { v.CreatedMS++ },
		"changed argv":        func(v *hcclient.Terminal) { v.Argv = []string{"different-program"} },
		"changed attempt":     func(v *hcclient.Terminal) { v.Labels["sum.attempt"] = "different-attempt" },
		"changed cwd":         func(v *hcclient.Terminal) { v.Cwd = "/different" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
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
			launched, err := ensureObserver(s, coordinator, "/runtime", "/sumctl", false)
			if err != nil {
				t.Fatal(err)
			}
			endpoint := store.EndpointFromContext(asObject(value(launched, "observer")))
			client := f.Client()
			terminal, err := client.Get(context.Background(), endpoint.Address)
			if err != nil {
				t.Fatal(err)
			}
			pid := uint32(123)
			terminal.PID = &pid
			terminal.Foreground = nil
			f.SetTerminal(terminal)
			observerStore, err := store.Open(s.Root)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- Observe(ctx, observerStore, endpoint, "/runtime", "/sumctl") }()
			await(t, func() bool {
				for _, call := range f.Calls() {
					if call.Method == "events.subscribe" && call.Params["terminal"] == endpoint.Address {
						return true
					}
				}
				return false
			})
			health, err := readHealth(s)
			if err != nil {
				t.Fatal(err)
			}
			if value(health, "observer_receipt") != nil {
				t.Fatal("startup receipt invented before native foreground")
			}
			if change != nil {
				change(&terminal)
				f.SetTerminal(terminal)
			}
			f.SetForeground(endpoint.Address, hcclient.Foreground{PID: 123, Name: "sumctl", Argv: terminal.Argv, Cwd: terminal.Cwd})
			if change == nil {
				if _, err = AwaitReady(context.Background(), s); err != nil {
					t.Fatal(err)
				}
				health, err = readHealth(s)
				if err != nil {
					t.Fatal(err)
				}
				if value(health, "observer_receipt") == nil || value(health, "observer_incarnation") == nil {
					t.Fatal("delayed native foreground did not establish exact receipt")
				}
				cancel()
				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("observer did not stop")
				}
			} else {
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("changed process adopted")
					}
				case <-time.After(time.Second):
					t.Fatal("changed process was not rejected")
				}
				health, err = readHealth(s)
				if err != nil {
					t.Fatal(err)
				}
				if value(health, "observer_receipt") != nil || value(health, "observer_incarnation") != nil {
					t.Fatal("replacement process received startup authority")
				}
			}
		})
	}
}

func TestObserverStartupForegroundWaitIsBounded(t *testing.T) {
	f := hctest.New(t)
	pid := uint32(123)
	terminal := hcclient.Terminal{Address: "t1", State: "live", CreatedMS: 100, PID: &pid, Argv: []string{"sumctl"}, Labels: map[string]string{"sum.attempt": "observer-pending"}}
	f.SetTerminal(terminal)
	client := f.Client()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	observed, err := awaitObserverForeground(ctx, &client, terminal)
	if err == nil || ctx.Err() == nil || observed.Foreground != nil {
		t.Fatalf("unconfirmed foreground adopted: %v %v", observed, err)
	}
}
