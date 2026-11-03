package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/cleanup"
	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/updatecmd"
)

func TestDeliveryReconcileLostAnswerResponse(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "delivery-recovery", map[string]string{"README.md": "Recovery lab.\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	id, worker := asString(task["id"]), workerAddress(task)
	d.ctlPane(worker, true, "init")
	d.native.SetStatus("t1", "working")
	q := d.ctlPane(worker, true, "ask", id, "--key", "recovery", "--text", "Which approach?")
	qid := asString(asMap(q["question"])["id"])
	if qid == "" {
		qid = asString(q["id"])
	}
	d.native.QueueFault("terminal.send", hctest.Fault{LoseResponse: true})
	d.ctl(true, "answer", id, qid, "--text", "Use the recorded approach.")
	path := filepath.Join(d.home, "tasks", id, "returns.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var delivery string
	for _, raw := range asSlice(deliveryRead(t, path)["deliveries"]) {
		row := asMap(raw)
		if row["state"] == "uncertain" {
			delivery = asString(row["id"])
		}
	}
	if delivery == "" {
		t.Fatalf("no uncertain effect: %s", before)
	}
	s, err := store.Open(d.home)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup.RefusePendingEffects(s, id) == nil {
		t.Fatal("uncertain effect did not block cleanup")
	}
	taskPath := filepath.Join(d.home, "tasks", id, "task.json")
	taskBefore, _ := os.ReadFile(taskPath)
	d.ctl(false, "resolve", id, qid)
	taskAfter, _ := os.ReadFile(taskPath)
	if !bytes.Equal(taskBefore, taskAfter) {
		t.Fatal("coordinator forged an application receipt")
	}
	d.ctlPane(worker, true, "resolve", id, qid)
	if cleanup.RefusePendingEffects(s, id) == nil {
		t.Fatal("application silently reconciled transport uncertainty")
	}
	currentTask, err := s.ReadTask(id)
	if err != nil {
		t.Fatal(err)
	}
	reviewer := ordjson.NewObject()
	prompt := ordjson.NewObject()
	prompt.Set("state", "uncertain")
	reviewer.Set("prompt", prompt)
	currentTask.Set("reviewer", reviewer)
	if err = s.SaveTask(currentTask); err != nil {
		t.Fatal(err)
	}
	candidate := deliveryCandidate(t, d)
	if !strings.Contains(deliveryActivationBlocks(t, s, candidate), "notification effects") {
		t.Fatal("activation omitted uncertain delivery")
	}
	inputs := len(d.native.Inputs(worker))
	d.ctl(true, "delivery", "reconcile", id, "--delivery", delivery)
	if err := cleanup.RefusePendingEffects(s, id); err != nil {
		t.Fatalf("receipt-backed reconciliation did not unblock cleanup: %v", err)
	}
	d.ctl(true, "delivery", "reconcile", id, "--delivery", delivery)
	blocks := deliveryActivationBlocks(t, s, candidate)
	if strings.Contains(blocks, "notification effects") {
		t.Fatal("validated delivery still blocks activation")
	}
	if !strings.Contains(blocks, "reviewer prompt is unresolved") {
		t.Fatal("delivery reconciliation waived another activation gate")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || len(d.native.Inputs(worker)) != inputs {
		t.Fatal("reconciliation rewrote old effect or sent input")
	}
}

func deliveryRead(t *testing.T, path string) map[string]any {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	if e = json.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	return v
}

func deliveryCandidate(t *testing.T, d *demoLab) string {
	t.Helper()
	root := policyProject(t, d.base, "recovery-candidate", map[string]string{"README.md": "Candidate.\n"})
	hctest.InstallRuntime(t, root)
	payload, err := json.Marshal(contract.BuildRelease())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".local", "bin", "sumctl")
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("#!/bin/sh\ncat <<'CONTRACT'\n"+string(payload)+"\nCONTRACT\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return root
}
func deliveryActivationBlocks(t *testing.T, s *store.Store, candidate string) string {
	t.Helper()
	current := ordjson.NewObject()
	current.Set("kind", "checkout")
	current.Set("path", candidate)
	view, err := updatecmd.Compatibility(s, candidate, candidate, current)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := view.Get("blocking")
	b, err := ordjson.MarshalCompact(raw)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParentDirectedResultsAreCollectedWithoutNativeDelivery(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "pull-results", map[string]string{"README": "base\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	id, address := asString(task["id"]), workerAddress(task)
	d.ctlPane(address, true, "init")
	calls := len(d.native.Calls())
	d.ctlPane(address, true, "ask", id, "--text", "Which approach?")
	d.ctlPane(address, true, "report", id, "--text", "Ready for inspection.")
	for _, call := range d.native.Calls()[calls:] {
		if call.Method == "terminal.send" {
			t.Fatalf("parent-directed result sent native input: %v", call)
		}
	}
	for range 2 {
		view := d.ctl(true, "collect", "--budget", "100ms")
		if !collectedKind(view, "question") || !collectedKind(view, "report") {
			t.Fatalf("reading settled results: %v", view)
		}
	}
}
