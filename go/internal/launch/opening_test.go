package launch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
)

func TestOriginalLaunchCanAcquireForegroundAndSurvivePromotion(t *testing.T) {
	cwd := t.TempDir()
	binary := filepath.Join(cwd, "agent")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	spec := HCTemplate{Name: "qualified", Binary: binary, Argv: []string{binary}, Submit: "enter"}
	pid := uint32(123)
	initial := hcclient.Terminal{Address: "t1", CreatedMS: 9, PID: &pid, Cwd: cwd, Labels: map[string]string{"sum.attempt": "original"}}
	current := initial
	current.State = "live"
	current.Template = &spec.Name
	current.Submit = &spec.Submit
	current.Argv = []string{"sh", "-c", spec.Command()}
	current.Foreground = &hcclient.Foreground{PID: 123, Argv: spec.Argv, Cwd: cwd}
	if err := OriginalProgram(initial, current, spec); err != nil {
		t.Fatal(err)
	}
	current.Keep = true
	if err := OriginalProgram(initial, current, spec); err == nil {
		t.Fatal("promotion without an original foreground observation authorized")
	}
	initial.Foreground = current.Foreground
	if err := OriginalProgram(initial, current, spec); err != nil {
		t.Fatalf("ordinary promotion invalidated the original program: %v", err)
	}
	replacement := uint32(456)
	current.PID = &replacement
	current.Foreground = &hcclient.Foreground{PID: 456, Argv: []string{"sh"}, Cwd: cwd}
	if err := OriginalProgram(initial, current, spec); err == nil {
		t.Fatal("recovery shell inherited launch authority")
	}
}

func TestOpeningWaitsForReadinessWithoutSendingAndCanRecover(t *testing.T) {
	for _, state := range []string{"absent", "blocked"} {
		t.Run(state, func(t *testing.T) {
			fixture := hctest.New(t)
			cwd := t.TempDir()
			binary := filepath.Join(cwd, "agent")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0700); err != nil {
				t.Fatal(err)
			}
			spec := HCTemplate{Name: "qualified", Binary: binary, Argv: []string{binary}, Submit: "enter"}
			pid := uint32(123)
			initial := hcclient.Terminal{Address: "t1", CreatedMS: 9, PID: &pid, Cwd: cwd, State: "live", Template: &spec.Name, Submit: &spec.Submit, Argv: []string{"sh", "-c", spec.Command()}}
			terminal := initial
			terminal.Foreground = &hcclient.Foreground{PID: 123, Argv: spec.Argv, Cwd: cwd}
			if state == "blocked" {
				terminal.Status = &hcclient.Status{State: "blocked"}
			}
			fixture.SetTerminal(terminal)
			client := fixture.Client()
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			progress := DeliverOpening(ctx, &client, initial, spec, "opening", func(OpeningProgress) error { return nil }, nil)
			if progress.State != "not-delivered" || progress.First == nil || progress.First.Sent {
				t.Fatalf("lost proven-unsent outcome: %+v", progress)
			}
			for _, call := range fixture.Calls() {
				if call.Method == "terminal.send" {
					t.Fatal("unready program received input")
				}
			}
			fixture.SetStatus(initial.Address, "idle")
			progress = DeliverOpening(context.Background(), &client, progress.Terminal, spec, "opening", func(OpeningProgress) error { return nil }, nil)
			if progress.State != "submitted" || len(fixture.Inputs(initial.Address)) != 1 {
				t.Fatalf("ready recovery failed: %+v", progress)
			}
		})
	}
}

func readyOriginal(t *testing.T) (*hctest.Fixture, hcclient.Terminal, HCTemplate) {
	t.Helper()
	fixture := hctest.New(t)
	cwd := t.TempDir()
	binary := filepath.Join(cwd, "agent")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	spec := HCTemplate{Name: "qualified", Binary: binary, Argv: []string{binary}, Submit: "enter"}
	pid := uint32(123)
	initial := hcclient.Terminal{Address: "t1", CreatedMS: 9, PID: &pid, Cwd: cwd, State: "live", Template: &spec.Name, Submit: &spec.Submit, Argv: []string{"sh", "-c", spec.Command()}}
	terminal := initial
	terminal.Foreground = &hcclient.Foreground{PID: 123, Argv: spec.Argv, Cwd: cwd}
	terminal.Status = &hcclient.Status{State: "idle"}
	fixture.SetTerminal(terminal)
	return fixture, initial, spec
}

func TestOpeningAdoptsOnceAfterOriginalProofAndBeforeInput(t *testing.T) {
	fixture, initial, spec := readyOriginal(t)
	client := fixture.Client()
	labels := map[string]string{"herdr.space": "sum-space", "herdr.tab": "sum-space-task"}
	adoptions := 0
	var persisted []string
	progress := DeliverOpening(context.Background(), &client, initial, spec, "opening", func(p OpeningProgress) error {
		persisted = append(persisted, p.State)
		return nil
	}, func() error {
		adoptions++
		observed, err := client.Get(context.Background(), initial.Address)
		if err != nil || observed.Foreground == nil {
			t.Fatalf("adoption ran before the original foreground was observed: %+v %v", observed, err)
		}
		_, err = client.Label(context.Background(), observed.Address, labels)
		return err
	})
	if progress.State != "submitted" || adoptions != 1 {
		t.Fatalf("progress=%+v adoptions=%d", progress, adoptions)
	}
	if persisted[0] != "readiness-pending" {
		t.Fatalf("original identity was not persisted before adoption: %v", persisted)
	}
	var order []string
	for _, call := range fixture.Calls() {
		if call.Method == "terminal.label" || call.Method == "terminal.send" {
			order = append(order, call.Method)
		}
	}
	if len(order) != 2 || order[0] != "terminal.label" || order[1] != "terminal.send" {
		t.Fatalf("labels were not published before the opening: %v", order)
	}
	if observed := fixture.Terminals()[0]; observed.Labels["herdr.space"] != "sum-space" || !observed.Keep {
		t.Fatalf("adoption did not label the exact terminal and enable keep: %+v", observed)
	}
}

func TestOpeningStopsBeforeInputWhenAdoptionCannotBePersisted(t *testing.T) {
	fixture, initial, spec := readyOriginal(t)
	client := fixture.Client()
	progress := DeliverOpening(context.Background(), &client, initial, spec, "opening", func(OpeningProgress) error { return nil }, func() error {
		return errors.New("state unavailable")
	})
	if progress.State == "submitted" || len(fixture.Inputs(initial.Address)) != 0 {
		t.Fatalf("opening was sent without a durable adoption record: %+v", progress)
	}
}
