package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestBindWorkerTerminalRecoversGoneTerminal(t *testing.T) {
	d, task, replacement := nativeRecoveryFixture(t, false)
	id := asString(task["id"])
	worktree := asString(task["worktree"])
	before := nativeGitSnapshot(t, worktree)
	old := asMap(task["worker_endpoint"])
	bound := d.ctl(true, "bind", id, "--worker-terminal", replacement.Address)
	endpoint := asMap(bound["worker_endpoint"])
	if asString(endpoint["address"]) != replacement.Address || asInt(endpoint["binding_generation"]) != 2 {
		t.Fatalf("native binding=%v", endpoint)
	}
	if nativeGitSnapshot(t, worktree) != before {
		t.Fatal("rebind changed Git worktree identity or contents")
	}
	var rebind map[string]any
	for _, raw := range asSlice(bound["evidence"]) {
		row := asMap(raw)
		if asString(row["kind"]) == "native-rebind" {
			rebind = row
		}
	}
	if rebind == nil || asString(asMap(rebind["from"])["address"]) != asString(old["address"]) || asString(asMap(rebind["to"])["address"]) != replacement.Address {
		t.Fatalf("rebind evidence=%v", rebind)
	}
	stopNativeRecoveryTerminal(t, d, replacement.Address)
	attempt := asString(asMap(asMap(bound["execution"])["worker"])["id"])
	parked := d.ctl(true, "execution", "park", id, "--attempt", attempt)
	if parked["released"] != true {
		t.Fatalf("park after rebind=%v", parked)
	}
	resumed := d.ctl(true, "execution", "resume", id, "--attempt", attempt)
	if asString(resumed["worktree"]) != worktree {
		t.Fatalf("resume=%v", resumed)
	}
	d.ctlPane(workerAddress(resumed), true, "init")
	if asString(d.taskRecord(id)["status"]) != "running" {
		t.Fatal("new receipt did not resume task")
	}
	if workerAddress(resumed) == replacement.Address {
		t.Fatal("new attempt reused predecessor process authority")
	}
	if nativeGitSnapshot(t, worktree) != before {
		t.Fatal("resume changed preserved checkout")
	}
}

func TestReportedClosedTerminalRecoversThroughResume(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "reported-terminal-recovery", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	id := asString(task["id"])
	address := workerAddress(task)
	worktree := asString(task["worktree"])
	d.ctlPane(address, true, "init")
	handoff := filepath.Join(d.base, "handoff.json")
	body, _ := json.Marshal(map[string]any{"outcome": "completed", "candidate": task["base_sha"]})
	if err := os.WriteFile(handoff, body, 0600); err != nil {
		t.Fatal(err)
	}
	d.ctlPane(address, true, "report", id, "--text", "Candidate ready.", "--handoff", handoff)
	stopNativeRecoveryTerminal(t, d, address)
	before := nativeGitSnapshot(t, worktree)
	swept := d.ctl(true, "sweep")
	removed := false
	for _, raw := range asSlice(swept["rows"]) {
		row := asMap(raw)
		if asString(row["action"]) == "terminal-remove" && asString(row["state"]) == "closed" {
			removed = true
		}
	}
	if !removed {
		t.Fatalf("reported terminal not removed: %v", swept)
	}
	if nativeGitSnapshot(t, worktree) != before {
		t.Fatal("terminal removal changed the preserved Git checkout")
	}
	saved := d.taskRecord(id)
	attempt := asString(asMap(asMap(saved["execution"])["worker"])["id"])
	resumed := d.ctl(true, "execution", "resume", id, "--attempt", attempt)
	if asString(resumed["worktree"]) != worktree || workerAddress(resumed) == address {
		t.Fatalf("resume did not retain checkout and replace attempt=%v", resumed)
	}
	d.ctlPane(workerAddress(resumed), true, "init")
	current := asString(asMap(asMap(resumed["execution"])["worker"])["id"])
	sent := d.ctl(true, "repair", "send", id, "--attempt", current, "--key", "after-resume", "--text", "Apply the correction.")
	if asString(sent["error"]) != "" {
		t.Fatalf("repair after resume=%v", sent)
	}
	d.ctlPane(workerAddress(resumed), true, "report", id, "--text", "Corrected candidate ready.", "--handoff", handoff)
	stopNativeRecoveryTerminal(t, d, workerAddress(resumed))
	d.ctl(true, "sweep")
	if nativeGitSnapshot(t, worktree) != before {
		t.Fatal("repeated lifecycle changed checkout")
	}
}

func TestBindWorkerTerminalRefusesReplacementWithoutCheckoutIdentity(t *testing.T) {
	d, task, replacement := nativeRecoveryFixture(t, false)
	for _, terminal := range d.native.Terminals() {
		if terminal.Address == replacement.Address {
			terminal.Cwd = filepath.Join(d.base, "unrelated-repository")
			d.native.SetTerminal(terminal)
		}
	}
	before := workerAddress(task)
	refused := d.ctl(false, "bind", asString(task["id"]), "--worker-terminal", replacement.Address)
	if !strings.Contains(asString(refused["error"]), "checkout") {
		t.Fatalf("wrong-checkout replacement=%v", refused)
	}
	if workerAddress(d.taskRecord(asString(task["id"]))) != before {
		t.Fatal("refused rebind changed task endpoint")
	}
}

func TestBindWorkerTerminalRefusesWhileOldProgramExists(t *testing.T) {
	d, task, replacement := nativeRecoveryFixture(t, true)
	refused := d.ctl(false, "bind", asString(task["id"]), "--worker-terminal", replacement.Address)
	if !strings.Contains(asString(refused["error"]), "live") {
		t.Fatalf("old live program=%v", refused)
	}
	if workerAddress(d.taskRecord(asString(task["id"]))) != workerAddress(task) {
		t.Fatal("live predecessor was replaced")
	}
}

func TestBindWorkerTerminalRefusesMissingReceiptAndReusedProcess(t *testing.T) {
	for _, kind := range []string{"missing receipt", "reused process"} {
		t.Run(kind, func(t *testing.T) {
			d, task, replacement := nativeRecoveryFixture(t, false)
			if kind == "missing receipt" {
				s := nativeTestStore(t, d.home)
				path := filepath.Join(s.Sessions, store.RegistrationKey(replacement)+".json")
				raw, err := ordjson.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				reg := raw.(*ordjson.Object)
				reg.Set("native_receipt", nil)
				if err = ordjson.WriteFile(path, reg); err != nil {
					t.Fatal(err)
				}
			} else {
				for _, terminal := range d.native.Terminals() {
					if terminal.Address == replacement.Address {
						terminal.CreatedMS++
						d.native.SetTerminal(terminal)
					}
				}
			}
			refused := d.ctl(false, "bind", asString(task["id"]), "--worker-terminal", replacement.Address)
			if asString(refused["error"]) == "" {
				t.Fatalf("unproven replacement granted authority=%v", refused)
			}
			if workerAddress(d.taskRecord(asString(task["id"]))) != workerAddress(task) {
				t.Fatal("unproven replacement changed endpoint")
			}
		})
	}
}

func nativeRecoveryFixture(t *testing.T, keepOld bool) (*demoLab, map[string]any, store.Endpoint) {
	t.Helper()
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "terminal-recovery", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	d.ctlPane(workerAddress(task), true, "init")
	if !keepOld {
		stopNativeRecoveryTerminal(t, d, workerAddress(task))
	}
	raw, _ := json.Marshal(task["worker_endpoint"])
	var endpoint store.Endpoint
	if err := json.Unmarshal(raw, &endpoint); err != nil {
		t.Fatal(err)
	}
	endpoint.Address = "t99"
	endpoint.CreatedMS = 99
	endpoint.BindingGeneration++
	pid := uint32(800001)
	fg := &hcclient.Foreground{PID: 800002, Name: "codex", Argv: []string{"codex", "--no-daemon"}, Cwd: asString(task["worktree"])}
	d.native.SetTerminal(hcclient.Terminal{Address: endpoint.Address, CreatedMS: endpoint.CreatedMS, Cwd: fg.Cwd, State: "live", PID: &pid, Foreground: fg, Argv: fg.Argv})
	s := nativeTestStore(t, d.home)
	evidence := incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: pid, Foreground: fg}.Record(store.Now())
	registration, err := s.Register(endpoint, "worker", endpoint.Task, evidence)
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: endpoint.Native(), Kind: "worker-init", Attempt: endpoint.Attempt})
	registration.Set("native_receipt", receipt)
	original, err := s.Registration(store.EndpointFromContext(mustOrdjson(t, task["worker_endpoint"])))
	if err != nil {
		t.Fatal(err)
	}
	if original != nil {
		provenance, _ := original.Get("native_template")
		registration.Set("native_template", provenance)
	}
	if err = ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(endpoint)+".json"), registration); err != nil {
		t.Fatal(err)
	}
	return d, task, endpoint
}

func mustOrdjson(t *testing.T, v any) *ordjson.Object {
	t.Helper()
	o, err := ordjson.FromValue(v)
	if err != nil {
		t.Fatal(err)
	}
	return o
}
func stopNativeRecoveryTerminal(t *testing.T, d *demoLab, address string) {
	t.Helper()
	d.native.Exit(address, 0)
	for _, terminal := range d.native.Terminals() {
		if terminal.Address == address {
			stamp := uint64(1000)
			terminal.FinishedMS = &stamp
			d.native.SetTerminal(terminal)
			return
		}
	}
	t.Fatal("native terminal absent before stop fixture")
}
func nativeGitSnapshot(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", path, "status", "--porcelain=v2", "--branch", "--untracked-files=all").CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	return string(out)
}

func TestBindWorkerTerminalRefusesMismatchedGitRegistration(t *testing.T) {
	d, task, replacement := nativeRecoveryFixture(t, false)
	other := policyProject(t, d.base, "unrelated-registered-checkout", map[string]string{"README.md": "other\n"})
	gitFile := filepath.Join(asString(task["worktree"]), ".git")
	saved, err := os.ReadFile(gitFile)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(gitFile, gitFile+".recorded"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(gitFile, []byte("gitdir: "+filepath.Join(other, ".git")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	refused := d.ctl(false, "bind", asString(task["id"]), "--worker-terminal", replacement.Address)
	if asString(refused["error"]) == "" {
		t.Fatal("replacement inherited mismatched Git checkout identity")
	}
	if workerAddress(d.taskRecord(asString(task["id"]))) != workerAddress(task) {
		t.Fatal("invalid Git registration changed worker binding")
	}
	preserved, err := os.ReadFile(gitFile + ".recorded")
	if err != nil || string(preserved) != string(saved) {
		t.Fatal("refusal destroyed original Git registration", err)
	}
}
