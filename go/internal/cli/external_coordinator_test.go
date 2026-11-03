package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every ctl call starts a fresh helper process with the lab's scrubbed environment.
func TestExternalCoordinatorLifecycle(t *testing.T) {
	d := newPolicyLab(t)
	record := filepath.Join(d.home, "coordinator.json")
	if err := os.Rename(record, record+".fixture"); err != nil {
		t.Fatal(err)
	}
	d.claim = ""
	calls := len(d.native.Calls())
	initialized := d.ctl(true, "init")
	d.claim = asString(initialized["claim"])
	// Read-only status also works before any launch selects a runtime route.
	routePath := filepath.Join(filepath.Dir(filepath.Dir(d.home)), "runtime-route.json")
	if err := os.Rename(routePath, routePath+".saved"); err != nil {
		t.Fatal(err)
	}
	if view := d.ctl(true, "status"); view["runtime_route"] != nil {
		t.Fatalf("fresh route status=%v", view)
	}
	if err := os.Rename(routePath+".saved", routePath); err != nil {
		t.Fatal(err)
	}
	if initialized["role"] != "coordinator" || d.claim == "" || initialized["endpoint"] != nil {
		t.Fatalf("ordinary init: %v", initialized)
	}
	if len(d.native.Calls()) != calls {
		t.Fatal("ordinary init contacted HC")
	}
	repo := policyProject(t, d.base, "external", map[string]string{"README": "base\n"})
	task := d.ctl(true, "prepare", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	id := asString(task["id"])
	started := d.ctl(true, "start", id)
	address := workerAddress(started)
	if address == "" || started["parent"] != nil || asMap(started["owner"])["kind"] != "coordinator-session" {
		t.Fatalf("parentless worker: %v", started)
	}
	d.ctlPane(address, true, "init")
	question := d.ctlPane(address, true, "ask", id, "--key", "scope", "--text", "Keep the scope?")
	qid := asString(asMap(question["question"])["id"])
	for range 2 {
		collected := d.ctl(true, "collect", "--budget", "100ms")
		if !collectedKind(collected, "question") {
			t.Fatalf("question missing or settled by reading: %v", collected)
		}
	}
	d.ctl(true, "answer", id, qid, "--text", "Yes.")
	d.ctlPane(address, true, "resolve", id, qid)
	checkout := asString(started["worktree"])
	if err := os.WriteFile(filepath.Join(checkout, "result.txt"), []byte("approved\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "result.txt"}, {"commit", "-m", "Approved fixture result"}} {
		if out, err := exec.Command("git", append([]string{"-C", checkout}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	out, err := exec.Command("git", "-C", checkout, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	candidate := strings.TrimSpace(string(out))
	d.workerReport(true, id, "--text", "Approved fixture complete.")
	d.independentReview(true, id, "--verdict", "approve", "--candidate", candidate, "--text", "Independent fixture review.")
	if collected := d.ctl(true, "collect", "--budget", "100ms"); !collectedKind(collected, "review") {
		t.Fatalf("parentless verdict missing: %v", collected)
	}
	before := readFileString(t, filepath.Join(d.home, "tasks", id, "task.json"))
	old := d.claim
	transferred := d.ctl(true, "init", "--transfer")
	if asString(transferred["claim"]) == old {
		t.Fatalf("transfer did not fence old conversation: %v", transferred)
	}
	if got := readFileString(t, filepath.Join(d.home, "tasks", id, "task.json")); got != before {
		t.Fatal("transfer rewrote worker/task identity")
	}
	calls = len(d.native.Calls())
	if refused := d.ctl(false, "settings", "set", "--global", "4"); !strings.Contains(asString(refused["error"]), "stale") {
		t.Fatalf("old conversation wrote after transfer: %v", refused)
	}
	if len(d.native.Calls()) != calls {
		t.Fatal("stale claim contacted HC")
	}
	d.claim = asString(transferred["claim"])
	d.ctl(true, "settings", "set", "--global", "4")
}

func collectedKind(view map[string]any, kind string) bool {
	for _, item := range asSlice(view["items"]) {
		if asMap(item)["kind"] == kind {
			return true
		}
	}
	return false
}

// snapshotHome fingerprints files so failed entry can be proven write-free.
func snapshotHome(t *testing.T, home string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s %d %x\n", path, info.Size(), raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}
