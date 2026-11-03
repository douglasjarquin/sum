package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/proc"
)

func originalProcessFault(t *testing.T, binary, cwd string) hctest.Fault {
	t.Helper()
	python := pinnedFixturePython(t)
	var err error
	body := "#!" + python + "\nimport sys,time\nif '--version' in sys.argv:\n print('codex-cli 0.166.0')\nelse:\n while True: time.sleep(1)\n"
	if err = os.WriteFile(binary, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "--no-daemon")
	cmd.Dir = cwd
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	var argv []string
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		argv, err = proc.ProcessArgv(cmd.Process.Pid)
		if err == nil && len(argv) >= 3 && argv[len(argv)-2] == binary {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || len(argv) < 3 {
		t.Fatalf("original argv: %v %v", argv, err)
	}
	pid := uint32(cmd.Process.Pid)
	return hctest.Fault{RunPID: &pid, RunRealtime: true, RunForeground: &hcclient.Foreground{PID: int32(pid), Name: "python3", Argv: argv, Cwd: cwd}}
}

func TestReconcileLostCreationAndDelayedReplyDoesNotDuplicateOpening(t *testing.T) {
	for _, role := range []string{"worker", "reviewer"} {
		t.Run(role, func(t *testing.T) {
			var d *demoLab
			var id string
			if role == "reviewer" {
				d, id = reviewLab(t)
			} else {
				d = newPolicyLab(t)
				repo := policyProject(t, d.base, "lost-creation", map[string]string{"README.md": "x\n"})
				task := d.ctl(true, "prepare", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
				id = asString(task["id"])
			}
			task := d.taskRecord(id)
			fault := originalProcessFault(t, filepath.Join(d.base, "codex"), asString(task["worktree"]))
			d.native.QueueFault("terminal.run", fault)
			entered, release := d.native.HoldReplyNext("terminal.run", "")
			done := make(chan map[string]any, 1)
			go func() {
				if role == "worker" {
					done <- d.ctl(false, "start", id)
				} else {
					done <- d.ctl(false, "reviewer", "launch", id)
				}
			}()
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("creation never reached reply barrier")
			}
			defer release()
			current := d.taskRecord(id)
			var attempt string
			if role == "worker" {
				w, _ := launchedWorker(current)
				attempt = asString(w["id"])
			} else {
				attempt = asString(asMap(current["reviewer"])["attempt"])
			}
			inspected := d.ctl(true, "inspect", id, "--role", role)
			if inspected["launch"] == nil {
				t.Fatal("ambiguous intent unavailable")
			}
			group := "execution"
			if role == "reviewer" {
				group = "reviewer"
			}
			var original hcclient.Terminal
			for _, terminal := range d.native.Terminals() {
				if terminal.Labels["sum.attempt"] == attempt {
					original = terminal
				}
			}
			duplicate := original
			duplicate.Address = "t999"
			d.native.SetTerminal(duplicate)
			refused := d.ctl(false, group, "reconcile", id, "--attempt", attempt)
			if !strings.Contains(asString(refused["error"]), "2 exact-name candidates") {
				t.Fatalf("conflicting creation was accepted: %v", refused)
			}
			d.native.Exit("t999", 0)
			client := d.native.Client()
			if _, err := client.Remove(context.Background(), "t999"); err != nil {
				t.Fatal(err)
			}
			changed := original
			changed.Foreground = &hcclient.Foreground{PID: original.Foreground.PID, Argv: []string{"sh"}, Cwd: original.Cwd}
			d.native.SetTerminal(changed)
			if refused = d.ctl(false, group, "reconcile", id, "--attempt", attempt); asString(refused["error"]) == "" {
				t.Fatal("changed original process was adopted")
			}
			d.native.SetTerminal(original)
			reconciled := d.ctl(true, group, "reconcile", id, "--attempt", attempt)
			_ = reconciled
			d.ctl(true, group, "redeliver", id, "--attempt", attempt)
			current = d.taskRecord(id)
			address := workerAddress(current)
			if role == "reviewer" {
				address = workerAddress(asMap(current["reviewer"]))
			}
			if len(d.native.Inputs(address)) != 1 {
				t.Fatal("recovery did not submit exactly one opening")
			}
			release()
			select {
			case result := <-done:
				if result["error"] != nil {
					t.Fatalf("late reply: %v", result)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("late creation reply hung")
			}
			if len(d.native.Inputs(address)) != 1 {
				t.Fatal("late reply duplicated opening")
			}
			d.ctlPane(address, true, "init")
			current = d.taskRecord(id)
			var receipt map[string]any
			if role == "worker" {
				w, _ := launchedWorker(current)
				receipt = asMap(w["native_receipt"])
			} else {
				receipt = asMap(asMap(current["reviewer"])["native_receipt"])
			}
			if receipt == nil {
				b, _ := json.Marshal(current)
				t.Fatalf("init receipt missing: %s", b)
			}
		})
	}
}
func TestCreatedOpeningSurvivesStartupCommandInterruption(t *testing.T) {
	for _, role := range []string{"worker", "reviewer"} {
		t.Run(role, func(t *testing.T) {
			var d *demoLab
			var id, address string
			var args []string
			if role == "reviewer" {
				d, id = reviewLab(t)
				address = "t3"
				args = []string{"reviewer", "launch", id}
			} else {
				d = newPolicyLab(t)
				repo := policyProject(t, d.base, "interrupt-opening", map[string]string{"README.md": "x\n"})
				task := d.ctl(true, "prepare", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
				id = asString(task["id"])
				address = "t2"
				args = []string{"start", id}
			}
			entered, release := d.native.HoldNext("terminal.get", address)
			defer release()
			command := exec.Command(d.helper, append([]string{"--format", "json", "--home", d.home, "--claim", d.claim}, args...)...)
			command.Env = d.env
			var output bytes.Buffer
			command.Stdout = &output
			command.Stderr = &output
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			stopped := false
			t.Cleanup(func() {
				if !stopped {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			})
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("opening observation never began")
			}
			current := d.taskRecord(id)
			var attempt string
			var native map[string]any
			group := "execution"
			if role == "worker" {
				w, _ := launchedWorker(current)
				attempt = asString(w["id"])
				native = asMap(w["native_launch"])
			} else {
				r := asMap(current["reviewer"])
				attempt = asString(r["attempt"])
				native = asMap(r["native_launch"])
				group = "reviewer"
			}
			if asMap(native["first_message"])["sent"] != false {
				t.Fatal("creation did not atomically preserve unsent opening")
			}
			if err := command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = command.Wait()
			stopped = true
			release()
			d.ctl(true, group, "redeliver", id, "--attempt", attempt)
			if len(d.native.Inputs(address)) != 1 {
				t.Fatal("interrupted startup did not recover exactly once")
			}
			d.ctlPane(address, true, "init")
		})
	}
}

func TestReviewerRecoversCreatedExitBeforeInit(t *testing.T) {
	d, id := reviewLab(t)
	d.native.QueueFault("terminal.send", hctest.Fault{RefuseKind: "blocked"})
	launched := d.ctl(true, "reviewer", "launch", id)
	reviewer := asMap(launched["reviewer"])
	address := workerAddress(reviewer)
	attempt := asString(reviewer["attempt"])
	if asString(d.ctl(false, "reviewer", "recover", id, "--attempt", attempt)["error"]) == "" {
		t.Fatal("live uninitialized reviewer released")
	}
	d.native.Exit(address, 0)
	d.ctl(true, "reviewer", "recover", id, "--attempt", attempt)
	if len(asSlice(d.taskRecord(id)["reviewer_launches"])) != 1 {
		t.Fatal("failed reviewer history lost")
	}
	fresh := d.ctl(true, "reviewer", "launch", id)
	if workerAddress(asMap(fresh["reviewer"])) == address {
		t.Fatal("failed reviewer reused")
	}
}

func TestInspectExactAttemptAfterLostWorkerCreationReply(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "inspect-lost", map[string]string{"README.md": "x\n"})
	d.native.QueueFault("terminal.run", hctest.Fault{LoseResponse: true})
	d.ctl(false, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	task := onlyNativeTask(t, d)
	w, _ := launchedWorker(task)
	id, attempt := asString(task["id"]), asString(w["id"])
	inspected := d.ctl(true, "inspect", id, "--role", "worker", "--attempt", attempt)
	if inspected["launch"] == nil || asMap(inspected["endpoint"])["attempt"] != attempt {
		t.Fatal("lost intent was not inspected")
	}
	if len(nativeCalls(d, "terminal.run")) != 1 || len(nativeCalls(d, "terminal.send")) != 0 {
		t.Fatal("inspection replayed a native effect")
	}
	if asString(d.ctl(false, "inspect", id, "--role", "worker", "--attempt", "stale")["error"]) == "" {
		t.Fatal("stale inspection attempt accepted")
	}
}
