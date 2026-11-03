package cli

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
)

func TestResumeLaunchesAtCurrentHEADAfterReportedCommit(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "resume-head", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "prepare", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--model", "pinned-model", "--reasoning", "high", "--approved")
	id, checkout, address := asString(task["id"]), asString(task["worktree"]), workerAddress(task)
	attempt := asString(asMap(asMap(task["execution"])["worker"])["id"])
	if id == "" || checkout == "" || attempt == "" {
		t.Fatalf("dispatch %v", task)
	}

	process := exec.Command("sleep", "60")
	process.Dir = checkout
	if err := process.Start(); err != nil {
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
	d.native.QueueFault("terminal.run", hctest.Fault{RunPID: &pid})
	task = d.ctl(true, "start", id)
	address = workerAddress(task)
	var original hcclient.Terminal
	for _, terminal := range d.native.Terminals() {
		if terminal.Address == address {
			original = terminal
		}
	}
	d.ctlPane(address, true, "init")
	gitIn(t, checkout, "config", "user.email", "test@example.invalid")
	gitIn(t, checkout, "config", "user.name", "sum test")
	gitIn(t, checkout, "commit", "--allow-empty", "-m", "worker candidate")
	head := strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD"))
	if head == asString(task["base_sha"]) {
		t.Fatal("candidate HEAD did not advance")
	}
	d.ctlPane(address, true, "report", id, "--text", "Candidate "+head)
	process.Process.Kill()
	process.Wait()
	alive = false
	finished, exit := uint64(3), int32(0)
	original.State = "finished"
	original.FinishedMS = &finished
	original.ExitCode = &exit
	d.native.SetTerminal(original)
	parked := d.ctl(true, "execution", "park", id, "--attempt", attempt)
	if parked["released"] != true {
		t.Fatalf("park %v", parked)
	}
	resumed := d.ctl(true, "execution", "resume", id, "--attempt", attempt)
	if headAfter := strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD")); headAfter != head {
		t.Fatal("resume reset the reported candidate")
	}
	if asString(resumed["worktree"]) != checkout || asString(resumed["base_sha"]) != asString(task["base_sha"]) {
		t.Fatal("resume changed checkout or approved base")
	}
	if !reflect.DeepEqual(startedArgv(resumed), startedArgv(task)) {
		t.Fatalf("resume changed approved model/argument policy: %v vs %v", startedArgv(resumed), startedArgv(task))
	}
	successor := asMap(asMap(resumed["execution"])["worker"])
	if asString(successor["id"]) == attempt || asString(successor["resumes"]) != attempt {
		t.Fatal("resume reused the released attempt")
	}
	calls := nativeCalls(d, "terminal.run")
	if len(calls) != 2 {
		t.Fatalf("native launches %d want dispatch and successor", len(calls))
	}
	if calls[0].Params["name"] == calls[1].Params["name"] {
		t.Fatal("successor reused native terminal name")
	}
	if asMap(resumed["launch"])["model"] != "pinned-model" || asMap(resumed["launch"])["reasoning"] != "high" {
		t.Fatal("resume changed model or reasoning")
	}
	next := workerAddress(resumed)
	if next == address {
		t.Fatal("resume adopted finished terminal")
	}
	d.ctlPane(next, true, "init")
	final := d.taskRecord(id)
	if asMap(asMap(final["execution"])["worker"])["state"] != "running" {
		t.Fatal("fresh successor receipt did not establish running authority")
	}
}

func TestResumeLostCreationDoesNotReconcileRetiredEndpoint(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "resume-lost", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "prepare", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	id, checkout := asString(task["id"]), asString(task["worktree"])
	attempt := asString(asMap(asMap(task["execution"])["worker"])["id"])
	process := exec.Command("sleep", "60")
	process.Dir = checkout
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Process.Kill(); _ = process.Wait() })
	pid := uint32(process.Process.Pid)
	d.native.QueueFault("terminal.run", hctest.Fault{RunPID: &pid})
	task = d.ctl(true, "start", id)
	retired := task["worker_endpoint"]
	address := workerAddress(task)
	d.ctlPane(address, true, "init")
	if err := process.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = process.Wait()
	d.native.Exit(address, 0)
	d.ctl(true, "execution", "park", id, "--attempt", attempt)
	d.native.QueueFault("terminal.run", hctest.Fault{LoseResponse: true})
	if resumed := d.ctl(false, "execution", "resume", id, "--attempt", attempt); asString(resumed["error"]) == "" {
		t.Fatalf("lost successor reply succeeded: %v", resumed)
	}
	task = d.taskRecord(id)
	successor := asString(asMap(asMap(task["execution"])["worker"])["id"])
	if successor == "" || successor == attempt {
		t.Fatalf("missing successor: %v", task["execution"])
	}
	d.native.Close()
	reconciled := d.ctl(false, "execution", "reconcile", id, "--attempt", successor)
	if failure := asString(reconciled["error"]); !strings.Contains(failure, "dial unix") {
		t.Errorf("unreachable original core did not fail discovery: error=%q endpoint=%v", failure, reconciled["worker_endpoint"])
	}
	if endpoint := d.taskRecord(id)["worker_endpoint"]; endpoint != nil {
		t.Errorf("successor retained retired endpoint: %v", endpoint)
	}
	var historical any
	for _, raw := range asSlice(task["evidence"]) {
		record := asMap(raw)
		if record["kind"] == "execution" && record["successor"] == successor {
			historical = asMap(record["attempt"])["worker_endpoint"]
		}
	}
	if !reflect.DeepEqual(historical, retired) {
		t.Errorf("retired attempt endpoint changed or missing: got %v want %v", historical, retired)
	}
}
