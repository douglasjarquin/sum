package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hookstatus"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func waitNativeCondition(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if predicate() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("native fixture condition was not observed")
		case <-tick.C:
		}
	}
}

// startNativeObserver executes the actual observer program named by terminal.run.
// The pipe gates process startup until HC can attest that same process identity;
// the application itself writes its startup receipt and subscribes to events.
func startNativeObserver(t *testing.T, d *demoLab) string {
	t.Helper()
	s, openErr := store.Open(d.home)
	if openErr != nil {
		t.Fatal(openErr)
	}
	instance, openErr := s.Instance()
	if openErr != nil {
		t.Fatal(openErr)
	}
	coordinator := nativeCoordinatorTerminal(t, d)
	if coordinator.Labels["sum.installation"] != instance || coordinator.Labels["sum.role"] != "coordinator" {
		t.Fatalf("real coordinator init did not establish scoped event labels: %v", coordinator.Labels)
	}
	configureNativeEventsForTest(t, d.home, true)
	enabled := d.ctl(true, "hook", "enable")
	endpoint := asMap(enabled["observer"])
	address := asString(endpoint["address"])
	if address == "" {
		t.Fatalf("observer launch has no native endpoint: %v", enabled)
	}
	var terminal hcclient.Terminal
	for _, candidate := range d.native.Terminals() {
		if candidate.Address == address {
			terminal = candidate
		}
	}
	if len(terminal.Argv) == 0 {
		t.Fatal("observer has no exact native argv")
	}
	gateRead, gateWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.Create(filepath.Join(d.base, "observer-"+address+".log"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/sh", append([]string{"-c", `IFS= read -r gate; exec "$@"`, "observer-start"}, terminal.Argv...)...)
	command.Env = append(append([]string{}, d.env...), "HC_TERMINAL_ID="+address)
	command.Dir = terminal.Cwd
	command.Stdin = gateRead
	command.Stdout = output
	command.Stderr = output
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var processErr error
	go func() { processErr = command.Wait(); close(done) }()
	t.Cleanup(func() {
		_ = gateRead.Close()
		_ = gateWrite.Close()
		select {
		case <-done:
		default:
			_ = command.Process.Signal(syscall.SIGTERM)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			<-done
			t.Error("native observer did not stop after SIGTERM")
		}
		d.native.Exit(address, 0)
		_ = output.Close()
	})
	pid := uint32(command.Process.Pid)
	terminal.PID = &pid
	terminal.Foreground = &hcclient.Foreground{PID: int32(pid), Name: "sumctl", Argv: terminal.Argv, Cwd: terminal.Cwd}
	d.native.SetTerminal(terminal)
	if _, err = fmt.Fprintln(gateWrite, "start"); err != nil {
		t.Fatal(err)
	}
	_ = gateWrite.Close()
	_ = gateRead.Close()
	s, err = store.Open(d.home)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready, err := hookstatus.AwaitReady(ctx, s)
	if err != nil {
		raw, _ := os.ReadFile(output.Name())
		select {
		case <-done:
			t.Fatalf("observer exited: %v; %s", processErr, raw)
		default:
		}
		t.Fatalf("observer readiness: %v; %s", err, raw)
	}
	if value, _ := ready.Get("running"); value != true {
		t.Fatalf("observer did not confirm its own receipt: %v", ready)
	}
	waitNativeCondition(t, func() bool {
		for _, call := range d.native.Calls() {
			if call.Method == "events.subscribe" {
				return true
			}
		}
		return false
	})
	return address
}

func TestNativeObserverSubprocessOwnsItsReadinessReceipt(t *testing.T) {
	d := newPolicyLab(t)
	address := startNativeObserver(t, d)
	health := readJSON(t, filepath.Join(d.home, "hook", "health.json"))
	receipt := asMap(health["observer_receipt"])
	if receipt["kind"] != "observer-start" || asMap(receipt["endpoint"])["address"] != address {
		t.Fatalf("actual startup receipt: %v", health)
	}
	pid := asMap(health["observer_incarnation"])["pid"]
	if _, err := strconv.ParseUint(fmt.Sprint(pid), 10, 32); err != nil {
		t.Fatalf("observer PID missing: %v", health)
	}
	repo := policyProject(t, d.base, "observer-subprocess", map[string]string{"README": "base\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	worker := workerAddress(task)
	d.ctlPane(worker, true, "init")
	d.native.SetStatus("t1", "working")
	asked := d.ctlPane(worker, true, "ask", asString(task["id"]), "--text", "A question awaiting the coordinator?")
	if asString(asMap(asked["question"])["id"]) == "" {
		t.Fatal("question was not saved")
	}
	d.native.SetStatus("t1", "done")
	waitNativeCondition(t, func() bool { return len(d.native.Inputs("t1")) > 0 })
}
