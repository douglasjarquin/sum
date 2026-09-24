package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/machine"
)

var devinWorkerArgv = []string{"--permission-mode", "dangerous", "--respect-workspace-trust", "false"}

func herdrCalls(t *testing.T, base string) [][]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(base, "fake", "calls.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var row struct {
			Args []string `json:"args"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		out = append(out, row.Args)
	}
	return out
}

func agentStartCalls(t *testing.T, base string) [][]string {
	t.Helper()
	var out [][]string
	for _, call := range herdrCalls(t, base) {
		if len(call) >= 2 && call[0] == "agent" && call[1] == "start" {
			out = append(out, call)
		}
	}
	return out
}

func startedArgv(task map[string]any) []string {
	var out []string
	for _, a := range asSlice(asMap(task["launch"])["started_argv"]) {
		out = append(out, asString(a))
	}
	return out
}

func argsAfterSeparator(call []string) ([]string, bool) {
	for i := len(call) - 1; i >= 0; i-- {
		if call[i] == "--" {
			return call[i+1:], true
		}
	}
	return nil, false
}

func TestDispatchDevinWorkerCarriesTrustGateArgv(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "devin-worker", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "devin", "--approved")
	if argv := startedArgv(task); !reflect.DeepEqual(argv, devinWorkerArgv) {
		t.Fatalf("devin worker started_argv = %v, want %v", argv, devinWorkerArgv)
	}
	starts := agentStartCalls(t, d.base)
	if len(starts) != 1 {
		t.Fatalf("agent start calls = %v, want exactly one", starts)
	}
	passed, ok := argsAfterSeparator(starts[0])
	if !ok || !reflect.DeepEqual(passed, devinWorkerArgv) {
		t.Fatalf("agent start args = %v, want -- followed by %v", starts[0], devinWorkerArgv)
	}
}

func TestDispatchOtherHarnessHasNoDevinArgv(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "codex-worker", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if argv := startedArgv(task); len(argv) != 0 {
		t.Fatalf("codex worker started_argv = %v, want none", argv)
	}
	starts := agentStartCalls(t, d.base)
	if len(starts) != 1 {
		t.Fatalf("agent start calls = %v, want exactly one", starts)
	}
	if _, ok := argsAfterSeparator(starts[0]); ok {
		t.Fatalf("codex agent start args = %v, want no -- separator", starts[0])
	}
}

func TestDispatchRecordsTheStableMachineIdentity(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "identity-worker", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	id, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	attempt, occupant := launchedWorker(task)
	for name, recorded := range map[string]any{"task": task["machine"], "attempt owner": asMap(attempt["owner"])["machine"], "occupant": occupant["machine"]} {
		if recorded != id {
			t.Fatalf("%s machine = %v, want the stable identity %s", name, recorded, id)
		}
	}
}

func launchedWorker(task map[string]any) (attempt, occupant map[string]any) {
	attempt = asMap(asMap(task["execution"])["worker"])
	return attempt, asMap(attempt["occupant"])
}

// settleFakeWorker returns the fake worker pane to idle, like a worker that
// finished its turn and waits for the next instruction.
func settleFakeWorker(t *testing.T, base, pane string) {
	t.Helper()
	path := filepath.Join(base, "fake", "state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	asMap(asMap(state["panes"])[pane])["agent_status"] = "idle"
	out, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchBindsAgentThatStartsForegroundChildren(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "mcp-children", map[string]string{"README.md": "x\n"})
	d.setEnv("FAKE_AGENT_CHILDREN", "2")
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "claude", "--approved")
	attempt, occupant := launchedWorker(task)
	if state := asString(attempt["state"]); state != "running" {
		t.Fatalf("worker attempt state = %q, want running; observations %v", state, attempt["observations"])
	}
	if occupant["pid"] == nil || !reflect.DeepEqual(occupant["argv"], []any{"claude"}) || occupant["shell_pid"] == nil {
		t.Fatalf("occupant = %v, want the claude group leader bound with its shell", occupant)
	}
	settleFakeWorker(t, d.base, asString(task["pane"]))
	sent := d.ctl(false, "repair", "send", asString(task["id"]), "--attempt", asString(attempt["id"]), "--key", "rebase", "--text", "Rebase onto main.")
	if msg := asString(sent["error"]); msg != "" {
		t.Fatalf("repair send to the bound worker failed: %s", msg)
	}
}

func TestDispatchKeepsForegroundWithoutItsLeaderUncertain(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "orphaned-group", map[string]string{"README.md": "x\n"})
	d.setEnv("FAKE_AGENT_CHILDREN", "2")
	d.setEnv("FAKE_AGENT_LEADER_GONE", "1")
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "claude", "--approved")
	attempt, occupant := launchedWorker(task)
	if state := asString(attempt["state"]); state != "uncertain" {
		t.Fatalf("worker attempt state = %q, want uncertain when no group leader is observed", state)
	}
	if occupant["pid"] != nil || occupant["argv"] != nil {
		t.Fatalf("occupant = %v, want no guessed pid", occupant)
	}
	settleFakeWorker(t, d.base, asString(task["pane"]))
	sent := d.ctl(false, "repair", "send", asString(task["id"]), "--attempt", asString(attempt["id"]), "--key", "rebase", "--text", "Rebase onto main.")
	if msg := asString(sent["error"]); !strings.Contains(msg, "exact running worker attempt") {
		t.Fatalf("repair send to an uncertain attempt = %v, want refusal", sent)
	}
}

func TestDispatchRetriesAgentPaneBusy(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "busy-pane", map[string]string{"README.md": "x\n"})
	d.setEnv("FAKE_START_BUSY", "2")
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if status := asString(task["status"]); status != "running" {
		t.Fatalf("task status = %q, want running", status)
	}
	if starts := agentStartCalls(t, d.base); len(starts) != 3 {
		t.Fatalf("agent start calls = %d, want two busy refusals then one success", len(starts))
	}
}

// Dispatch binds the worker registration to the occupant Herdr reports for the task's pane (its terminal and shell),
// so the pane's own init and every later delivery are judged against it.
func TestDispatchRecordsTheWorkerPanesIncarnation(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "incarnation-worker", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	id, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	pane := asString(task["pane"])
	registration := readJSON(t, sessionPath(d.home, id, pane))
	inc := asMap(registration["incarnation"])
	if registration["role"] != "worker" || inc["terminal"] != "term-"+pane || asMap(inc["shell"])["pid"] == nil {
		t.Fatalf("worker registration = %v, want the pane's terminal and shell recorded", registration)
	}
	if worker := d.ctlPane(pane, true, "init"); asString(worker["role"]) != "worker" || asString(asMap(worker["incarnation"])["outcome"]) != "same" {
		t.Fatalf("worker init = %v, want worker judged same", worker)
	}
}
