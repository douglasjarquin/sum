package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/machine"
)

func TestOrdinaryCoordinatorIgnoresInheritedNativeIdentity(t *testing.T) {
	d := newPolicyLab(t)
	before := readFileString(t, filepath.Join(d.home, "coordinator.json"))
	for key, value := range map[string]string{"SUM_CORE_ID": "replaced", "HC_SOCKET": filepath.Join(d.base, "absent.sock"), "HC_TERMINAL_ID": "t99"} {
		d.setEnv(key, value)
	}
	for range 2 {
		view := d.ctl(true, "init")
		if view["role"] != "coordinator" || view["endpoint"] != nil || view["claim"] != d.claim {
			t.Fatalf("native identity redirected ordinary entry: %v", view)
		}
	}
	if got := readFileString(t, filepath.Join(d.home, "coordinator.json")); got != before {
		t.Fatal("resuming rewrote logical ownership")
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
	if result := d.ctlPane(address, false, "init"); asString(result["blocker"]) == "" && asString(result["error"]) == "" {
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

func TestIncarnation_anUnprovenWorkerPaneNeverClaimsAnUnownedCoordinator(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "unowned", map[string]string{"README": "base\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	address := workerAddress(task)
	owner := filepath.Join(d.home, "coordinator.json")
	if err := os.Rename(owner, owner+".saved"); err != nil {
		t.Fatal(err)
	}
	if result := d.ctlPane(address, false, "init", "--role", "coordinator"); asString(result["blocker"]) == "" && asString(result["error"]) == "" {
		t.Fatalf("worker claimed coordinator: %v", result)
	}
	if _, err := os.Stat(owner); !os.IsNotExist(err) {
		t.Fatal("worker created coordinator authority")
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
