package launch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
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
