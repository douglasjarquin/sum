package execution

import (
	"context"
	"os/exec"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestNativeProgramStoppedIgnoresReleasedAttemptAndRequiresExitAndOriginalPIDDeath(t *testing.T) {
	fixture := hctest.New(t)
	s, err := store.Open(t.TempDir())
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
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: fixture.CoreID, Socket: fixture.Socket, Session: "sum-test", Address: "t2", CreatedMS: 2, Task: "t-abcdef123456", Role: "worker", Attempt: "x-abcdef123456", BindingGeneration: 1, Cwd: t.TempDir()}
	process := exec.Command("sleep", "60")
	if err = process.Start(); err != nil {
		t.Fatal(err)
	}
	alive := true
	defer func() {
		if alive {
			process.Process.Kill()
			process.Wait()
		}
	}()
	pid := uint32(process.Process.Pid)
	fg := &hcclient.Foreground{PID: int32(pid), Name: "sleep", Argv: []string{"sleep", "60"}, Cwd: endpoint.Cwd}
	evidence := incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: pid, Foreground: fg}
	if _, err = s.Register(endpoint, "worker", endpoint.Task, evidence.Record(store.Now())); err != nil {
		t.Fatal(err)
	}
	terminal := hcclient.Terminal{Address: endpoint.Address, CreatedMS: endpoint.CreatedMS, Cwd: endpoint.Cwd, State: "finished", PID: &pid, Foreground: fg}
	fixture.SetTerminal(terminal)
	if proof := NativeProgramStopped(s, endpoint); proof.Outcome == OutcomeStopped {
		t.Fatal("finished without native exit facts was accepted")
	}
	finished, exit := uint64(3), int32(0)
	terminal.FinishedMS = &finished
	terminal.ExitCode = &exit
	fixture.SetTerminal(terminal)
	if proof := NativeProgramStopped(s, endpoint); proof.Outcome == OutcomeStopped {
		t.Fatal("native exit metadata hid surviving original PID")
	}
	process.Process.Kill()
	process.Wait()
	alive = false
	if proof := NativeProgramStopped(s, endpoint); proof.Outcome != OutcomeStopped {
		t.Fatal(proof.Reason)
	}
	for _, tc := range []struct {
		name string
		edit func(*hcclient.Terminal)
	}{
		{"Done live terminal", func(v *hcclient.Terminal) { v.State = "live"; v.Status = &hcclient.Status{State: "done"} }},
		{"reused creation", func(v *hcclient.Terminal) { v.CreatedMS++ }},
		{"kept terminal", func(v *hcclient.Terminal) { v.Keep = true }},
		{"missing exit", func(v *hcclient.Terminal) { v.ExitCode = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := terminal
			tc.edit(&value)
			fixture.SetTerminal(value)
			if proof := NativeProgramStopped(s, endpoint); proof.Outcome == OutcomeStopped {
				t.Fatal("unproven program death was accepted")
			}
		})
	}
	fixture.SetTerminal(terminal)
	client := fixture.Client()
	if _, err = client.Remove(context.Background(), endpoint.Address); err != nil {
		t.Fatal(err)
	}
	if proof := NativeProgramStopped(s, endpoint); proof.Outcome != OutcomeStopped {
		t.Fatal("stable absence with dead original process:", proof.Reason)
	}
	other := endpoint
	other.Machine = "other-machine"
	if proof := NativeProgramStopped(s, other); proof.Outcome == OutcomeStopped {
		t.Fatal("other machine endpoint was accepted")
	}
}
