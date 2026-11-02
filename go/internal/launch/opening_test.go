package launch

import (
	"context"
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
			progress := DeliverOpening(ctx, &client, initial, spec, "opening", func(OpeningProgress) error { return nil })
			if progress.State != "not-delivered" || progress.First == nil || progress.First.Sent {
				t.Fatalf("lost proven-unsent outcome: %+v", progress)
			}
			for _, call := range fixture.Calls() {
				if call.Method == "terminal.send" {
					t.Fatal("unready program received input")
				}
			}
			fixture.SetStatus(initial.Address, "idle")
			progress = DeliverOpening(context.Background(), &client, progress.Terminal, spec, "opening", func(OpeningProgress) error { return nil })
			if progress.State != "submitted" || len(fixture.Inputs(initial.Address)) != 1 {
				t.Fatalf("ready recovery failed: %+v", progress)
			}
		})
	}
}
