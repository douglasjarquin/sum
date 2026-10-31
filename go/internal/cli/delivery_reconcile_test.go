package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/cleanup"
	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
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

func TestDeliveryReconcileLostReportResponse(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "delivery-report", map[string]string{"README.md": "Report lab.\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	id, worker := asString(task["id"]), workerAddress(task)
	d.ctlPane(worker, true, "init")
	d.native.SetStatus("t1", "idle")
	d.native.QueueFault("terminal.send", hctest.Fault{LoseResponse: true})
	d.ctlPane(worker, true, "report", id, "--text", "The recorded candidate is ready for inspection.")
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
		t.Fatalf("no uncertain report effect: %s", before)
	}
	refusal := d.ctl(false, "delivery", "reconcile", id, "--delivery", delivery)
	if !strings.Contains(asString(refusal["error"]), "no matching durable receipt") {
		t.Fatalf("missing receipt accepted: %v", refusal)
	}
	view := d.ctl(true, "wake", "show")
	recipients := asSlice(view["recipients"])
	if len(recipients) != 1 {
		t.Fatalf("wake: %v", view)
	}
	boundary := asString(asMap(recipients[0])["boundary"])
	if boundary == "" {
		t.Fatalf("no boundary: %v", view)
	}
	d.ctl(true, "wake", "consume", "--boundary", boundary)
	s, err := store.Open(d.home)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup.RefusePendingEffects(s, id) == nil {
		t.Fatal("consumption silently rewrote historical uncertainty")
	}
	inputs := len(d.native.Inputs("t1"))
	d.ctl(true, "delivery", "reconcile", id, "--delivery", delivery)
	if err = cleanup.RefusePendingEffects(s, id); err != nil {
		t.Fatal(err)
	}
	inbox := d.ctl(true, "show", id)
	if len(asSlice(asMap(inbox["returns"])["open"])) == 0 {
		t.Fatal("reconciliation waived the report obligation")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || len(d.native.Inputs("t1")) != inputs {
		t.Fatal("report reconciliation changed old delivery or sent input")
	}
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

func TestDeliveryReconcileLostQuestionResponseUsesExactAnswerRead(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "delivery-question", map[string]string{"README.md": "Question lab.\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	id, worker := asString(task["id"]), workerAddress(task)
	d.ctlPane(worker, true, "init")
	d.native.SetStatus("t1", "idle")
	d.native.QueueFault("terminal.send", hctest.Fault{LoseResponse: true})
	q := d.ctlPane(worker, true, "ask", id, "--key", "question-recovery", "--text", "Choose the approach.")
	qid := asString(asMap(q["question"])["id"])
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
		t.Fatalf("no uncertain question send: %s", before)
	}
	d.native.SetStatus(worker, "working")
	d.ctl(true, "answer", id, qid, "--text", "Use this exact approach.")
	before, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(d.home)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup.RefusePendingEffects(s, id) == nil {
		t.Fatal("coordinator answering silently resolved original send")
	}
	inputs := len(d.native.Inputs("t1"))
	d.ctl(true, "delivery", "reconcile", id, "--delivery", delivery)
	if err = cleanup.RefusePendingEffects(s, id); err != nil {
		t.Fatal(err)
	}
	record := d.ctl(true, "show", id)
	if len(asSlice(asMap(record["returns"])["open"])) != 1 {
		t.Fatal("unapplied answer obligation was waived")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || len(d.native.Inputs("t1")) != inputs {
		t.Fatal("question recovery changed original effect or sent input")
	}
}

func TestDeliveryReconcileFirstRecoveryAfterWakeReceiptEviction(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "delivery-retention", map[string]string{"README.md": "Retention lab.\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	id, worker := asString(task["id"]), workerAddress(task)
	d.ctlPane(worker, true, "init")
	d.native.SetStatus("t1", "idle")
	d.native.QueueFault("terminal.send", hctest.Fault{LoseResponse: true})
	d.ctlPane(worker, true, "report", id, "--text", "First uncertain report.")
	path := filepath.Join(d.home, "tasks", id, "returns.json")
	var delivery string
	for _, raw := range asSlice(deliveryRead(t, path)["deliveries"]) {
		row := asMap(raw)
		if row["state"] == "uncertain" {
			delivery = asString(row["id"])
		}
	}
	if delivery == "" {
		t.Fatal("missing uncertain effect")
	}
	consume := func() {
		t.Helper()
		view := d.ctl(true, "wake", "show")
		recipients := asSlice(view["recipients"])
		if len(recipients) != 1 {
			t.Fatalf("wake: %v", view)
		}
		token := asString(asMap(recipients[0])["boundary"])
		if token == "" {
			t.Fatalf("no boundary: %v", view)
		}
		d.ctl(true, "wake", "consume", "--boundary", token)
	}
	consume()
	for i := 0; i < 21; i++ {
		d.native.SetStatus("t1", "idle")
		d.ctlPane(worker, true, "report", id, "--text", fmt.Sprintf("Subsequent report %d.", i))
		consume()
	}
	s, err := store.Open(d.home)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.Owner()
	if err != nil {
		t.Fatal(err)
	}
	endpoint := store.EndpointFromContext(owner)
	w, status := returns.ReadWake(s, [3]string{endpoint.Machine, endpoint.CoreID, endpoint.Address})
	if status.State != returns.WakeOK {
		t.Fatal(status)
	}
	for _, r := range w.Receipts {
		if r.Delivery == delivery {
			t.Fatal("original receipt was not evicted")
		}
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	inputs := len(d.native.Inputs("t1"))
	d.ctl(true, "delivery", "reconcile", id, "--delivery", delivery)
	if err = cleanup.RefusePendingEffects(s, id); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || len(d.native.Inputs("t1")) != inputs {
		t.Fatal("retention recovery changed original effect or sent input")
	}
}
