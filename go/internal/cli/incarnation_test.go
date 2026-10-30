package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/machine"
)

func nativeCoordinatorTerminal(t *testing.T, d *demoLab) hcclient.Terminal {
	t.Helper()
	for _, terminal := range d.native.Terminals() {
		if terminal.Address == "t1" {
			return terminal
		}
	}
	t.Fatal("missing coordinator terminal")
	return hcclient.Terminal{}
}

func TestIncarnation_aRestoredPaneUnderANewServerDoesNotInheritTheCoordinator(t *testing.T) {
	d := newPolicyLab(t)
	before, err := os.ReadFile(filepath.Join(d.home, "context.json"))
	if err != nil {
		t.Fatal(err)
	}
	d.setEnv("SUM_CORE_ID", "replacement-core")
	for _, args := range [][]string{{"init"}, {"init", "--role", "coordinator"}, {"settings", "set", "--global", "3"}} {
		result := d.ctl(false, args...)
		if asString(result["error"]) == "" {
			t.Fatalf("replacement core accepted %v: %v", args, result)
		}
	}
	after, err := os.ReadFile(filepath.Join(d.home, "context.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("replacement core changed durable coordinator identity")
	}
}

func TestIncarnation_theSameOccupantStaysTheCoordinator(t *testing.T) {
	d := newPolicyLab(t)
	for range 2 {
		view := d.ctl(true, "init")
		if view["role"] != "coordinator" || asString(asMap(view["endpoint"])["address"]) != "t1" {
			t.Fatalf("same occupant init: %v", view)
		}
	}
}

func TestIncarnation_anOccupantThatCannotBeObservedGainsNothingAndKeepsItsRecord(t *testing.T) {
	d := newPolicyLab(t)
	before, err := os.ReadFile(filepath.Join(d.home, "context.json"))
	if err != nil {
		t.Fatal(err)
	}
	terminal := nativeCoordinatorTerminal(t, d)
	original := terminal
	terminal.Foreground = nil
	d.native.SetTerminal(terminal)
	for _, args := range [][]string{{"init"}, {"init", "--role", "coordinator", "--reclaim"}, {"settings", "set", "--global", "3"}} {
		if result := d.ctl(false, args...); asString(result["error"]) == "" {
			t.Fatalf("unobservable occupant accepted %v", args)
		}
	}
	after, err := os.ReadFile(filepath.Join(d.home, "context.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("unobservable init changed durable owner")
	}
	d.native.SetTerminal(original)
	if view := d.ctl(true, "init"); view["role"] != "coordinator" {
		t.Fatalf("same process recovered: %v", view)
	}
}

func TestIncarnation_aLegacyCoordinatorRecordNeedsProof(t *testing.T) {
	d := newPolicyLab(t)
	path := filepath.Join(d.home, "context.json")
	owner := readJSON(t, path)
	delete(owner, "native_receipt")
	writeJSONFile(t, path, owner)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if result := d.ctl(false, "init"); !strings.Contains(asString(result["error"]), "receipt") {
		t.Fatalf("unreceipted owner: %v", result)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("init invented a receipt for historical ownership")
	}
}

func TestIncarnation_aRestoredWorkerPaneIsNotTheWorkerUntilTheCoordinatorRebinds(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "worker-identity", map[string]string{"README": "base\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	address, id := workerAddress(task), asString(task["id"])
	d.ctlPane(address, true, "init")
	before, err := os.ReadFile(filepath.Join(d.home, "tasks", id, "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	d.native.SetForeground(address, hcclient.Foreground{PID: 300001, Name: "codex", Argv: []string{"codex"}, Cwd: asString(task["worktree"])})
	if result := d.ctlPane(address, false, "init"); asString(result["error"]) == "" {
		t.Fatalf("replacement worker inherited receipt: %v", result)
	}
	after, err := os.ReadFile(filepath.Join(d.home, "tasks", id, "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("replacement worker rewrote task authority")
	}
	if result := d.ctl(true, "init"); result["role"] != "coordinator" {
		t.Fatal("worker replacement changed coordinator")
	}
}

func TestIncarnation_anUnreachableBackendIsNotIdentityProof(t *testing.T) {
	d := newPolicyLab(t)
	before, err := os.ReadFile(filepath.Join(d.home, "context.json"))
	if err != nil {
		t.Fatal(err)
	}
	d.setEnv("HC_SOCKET", filepath.Join(d.base, "absent.sock"))
	for _, args := range [][]string{{"init"}, {"init", "--role", "coordinator", "--reclaim"}, {"settings", "set", "--global", "3"}} {
		if result := d.ctl(false, args...); asString(result["error"]) == "" {
			t.Fatalf("unreachable socket accepted %v", args)
		}
	}
	after, err := os.ReadFile(filepath.Join(d.home, "context.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("unreachable backend rewrote owner")
	}
}

func TestIncarnation_anUnprovenWorkerPaneNeverClaimsAnUnownedCoordinator(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "unowned", map[string]string{"README": "base\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	address := workerAddress(task)
	owner := filepath.Join(d.home, "context.json")
	if err := os.Rename(owner, owner+".saved"); err != nil {
		t.Fatal(err)
	}
	if result := d.ctlPane(address, false, "init", "--role", "coordinator"); asString(result["error"]) == "" {
		t.Fatalf("worker claimed coordinator: %v", result)
	}
	if _, err := os.Stat(owner); !os.IsNotExist(err) {
		t.Fatal("worker created coordinator authority")
	}
}

func TestIncarnation_aRestoredCoordinatorKeepsItsRoleThroughALaterNewConversation(t *testing.T) {
	d := newPolicyLab(t)
	before := readJSON(t, filepath.Join(d.home, "context.json"))
	for _, state := range []string{"working", "done", "idle"} {
		d.native.SetStatus("t1", state)
		if view := d.ctl(true, "init"); view["role"] != "coordinator" {
			t.Fatalf("status %s changed native identity: %v", state, view)
		}
	}
	after := readJSON(t, filepath.Join(d.home, "context.json"))
	if before["attempt"] != after["attempt"] || before["binding_generation"] != after["binding_generation"] {
		t.Fatal("native turn/status changed receipt-bound authority")
	}
}

func mustMachine(t *testing.T) string {
	t.Helper()
	id, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func jsonString(value string) string { raw, _ := json.Marshal(value); return string(raw) }
func fakePrompts(t *testing.T, home string) []string {
	t.Helper()
	d := nativeLabFor(t, home)
	var out []string
	for _, terminal := range d.native.Terminals() {
		out = append(out, d.native.Inputs(terminal.Address)...)
	}
	return out
}
