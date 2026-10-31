package returns

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func uncertainAppliedLab(t *testing.T) (*nativeLab, *ordjson.Object, *ordjson.Object, string) {
	t.Helper()
	l := newNativeLab(t)
	task, e := l.task("answered")
	qs, _ := routeValue(task, "questions").([]any)
	q := qs[0].(*ordjson.Object)
	q.Set("answer", "Keep the exact input.")
	if err := l.s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	l.f.QueueFault("terminal.send", hctest.Fault{LoseResponse: true})
	l.pump("worker")
	history, err := ReadReturns(l.s, taskIDOf(task))
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := routeValue(history, "deliveries").([]any)
	row := rows[len(rows)-1].(*ordjson.Object)
	if routeValue(row, "state") != "uncertain" {
		t.Fatal("effect was not uncertain")
	}
	q.Set("application_receipt", ApplicationReceipt(task, "answer:q1", "answer-applied", e))
	q.Set("status", "applied")
	q.Set("applied_at", store.Now())
	if err = l.s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	return l, task, q, routeValue(row, "id").(string)
}
func taskIDOf(task *ordjson.Object) string { return routeValue(task, "id").(string) }
func TestDeliveryReconciliationRefusesUnprovedOrChangedInputs(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*nativeLab, *ordjson.Object, *ordjson.Object, *string)
	}{
		{"missing receipt", func(_ *nativeLab, _ *ordjson.Object, q *ordjson.Object, _ *string) { q.Delete("application_receipt") }},
		{"unknown delivery", func(_ *nativeLab, _ *ordjson.Object, _ *ordjson.Object, id *string) { *id = "d-unknown" }},
		{"wrong native creation", func(_ *nativeLab, _ *ordjson.Object, q *ordjson.Object, _ *string) {
			r, _ := routeValue(q, "application_receipt").(*ordjson.Object)
			e, _ := routeValue(r, "endpoint").(*ordjson.Object)
			e.Set("created_ms", jsonInt(999))
		}},
		{"wrong binding generation", func(_ *nativeLab, _ *ordjson.Object, q *ordjson.Object, _ *string) {
			r, _ := routeValue(q, "application_receipt").(*ordjson.Object)
			e, _ := routeValue(r, "endpoint").(*ordjson.Object)
			e.Set("binding_generation", jsonInt(99))
		}},
		{"wrong receipt obligation", func(_ *nativeLab, _ *ordjson.Object, q *ordjson.Object, _ *string) {
			r, _ := routeValue(q, "application_receipt").(*ordjson.Object)
			r.Set("obligation", "answer:other")
		}},
		{"wrong receipt revision", func(_ *nativeLab, _ *ordjson.Object, q *ordjson.Object, _ *string) {
			r, _ := routeValue(q, "application_receipt").(*ordjson.Object)
			r.Set("revision", strings.Repeat("a", 64))
		}},
		{"changed semantic input", func(_ *nativeLab, _ *ordjson.Object, q *ordjson.Object, _ *string) {
			q.Set("answer", "A later different answer.")
		}},
		{"not applied", func(_ *nativeLab, _ *ordjson.Object, q *ordjson.Object, _ *string) { q.Set("status", "answered") }},
		{"receipt before effect", func(_ *nativeLab, _ *ordjson.Object, q *ordjson.Object, _ *string) {
			r, _ := routeValue(q, "application_receipt").(*ordjson.Object)
			r.Set("at", "2001-01-01T00:00:00Z")
		}},
		{"missing historical coverage", func(l *nativeLab, task *ordjson.Object, _ *ordjson.Object, _ *string) {
			h, _ := ReadReturns(l.s, taskIDOf(task))
			rows, _ := routeValue(h, "deliveries").([]any)
			rows[len(rows)-1].(*ordjson.Object).Delete("coverage")
			if err := Write(l.s, h); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, task, q, id := uncertainAppliedLab(t)
			tc.mutate(l, task, q, &id)
			if err := l.s.SaveTask(task); err != nil {
				t.Fatal(err)
			}
			inputs := len(l.f.Inputs("t1"))
			if _, err := ReconcileDelivery(l.s, l.owner.Object(), taskIDOf(task), id); err == nil {
				t.Fatal("unsafe recovery succeeded")
			}
			if len(l.f.Inputs("t1")) != inputs {
				t.Fatal("recovery sent input")
			}
			dir, _ := l.s.TaskPath(taskIDOf(task))
			if _, err := os.Stat(filepath.Join(dir, ResolutionFile)); !os.IsNotExist(err) {
				t.Fatal("refusal persisted reconciliation")
			}
		})
	}
}
func TestDeliveryReconciliationRetainsDiagnosticAndAllowsClaimRecovery(t *testing.T) {
	l, task, _, id := uncertainAppliedLab(t)
	task.Set("cleanup", func() *ordjson.Object { o := ordjson.NewObject(); o.Set("intent", "claimed"); return o }())
	if err := l.s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	dir, _ := l.s.TaskPath(taskIDOf(task))
	history := filepath.Join(dir, File)
	before, _ := os.ReadFile(history)
	if err := PendingDeliveryEffects(l.s, task); err == nil {
		t.Fatal("effect not blocked")
	}
	if _, err := ReconcileDelivery(l.s, l.owner.Object(), taskIDOf(task), id); err != nil {
		t.Fatal(err)
	}
	if err := PendingDeliveryEffects(l.s, task); err != nil {
		t.Fatal(err)
	}
	proof := filepath.Join(dir, ResolutionFile)
	first, _ := os.ReadFile(proof)
	if result, err := ReconcileDelivery(l.s, l.owner.Object(), taskIDOf(task), id); err != nil || routeValue(result, "result") != "repeated" {
		t.Fatalf("repeat %v %v", result, err)
	}
	second, _ := os.ReadFile(proof)
	after, _ := os.ReadFile(history)
	if !bytes.Equal(before, after) || !bytes.Equal(first, second) {
		t.Fatal("recovery rewrote old history or repeated resolution")
	}
	h, _ := ReadReturns(l.s, taskIDOf(task))
	rows, _ := routeValue(h, "deliveries").([]any)
	rows[len(rows)-1].(*ordjson.Object).Set("native_error", "changed later diagnostic")
	if err := Write(l.s, h); err != nil {
		t.Fatal(err)
	}
	if err := PendingDeliveryEffects(l.s, task); err == nil {
		t.Fatal("stale original row digest accepted")
	}
}
func TestDeliveryReconciliationObservesBeforeStateLockAndRechecksTask(t *testing.T) {
	l, task, _, id := uncertainAppliedLab(t)
	taskID := taskIDOf(task)
	entered, release := l.f.HoldNext("terminal.get", l.owner.Address)
	defer release()
	done := make(chan error, 1)
	go func() { _, err := ReconcileDelivery(l.s, l.owner.Object(), taskID, id); done <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("native authority observation not reached")
	}
	lockDone := make(chan error, 1)
	go func() {
		unlock, err := l.s.Lock()
		if err == nil {
			parent, _ := routeValue(task, "parent").(*ordjson.Object)
			parent.Set("binding_generation", jsonInt(2))
			err = l.s.SaveTask(task)
			unlock()
		}
		lockDone <- err
	}()
	select {
	case err := <-lockDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("state lock held during native observation")
	}
	release()
	if err := <-done; err == nil {
		t.Fatal("concurrent parent binding change was ignored")
	}
}

func consumedDeliveryLab(t *testing.T, kinds ...string) (*nativeLab, *ordjson.Object, string, *Wake) {
	t.Helper()
	l := newNativeLab(t)
	task, _ := l.task("report")
	kind := "report"
	if len(kinds) > 0 {
		kind = kinds[0]
	}
	if kind == "review" {
		rows, _ := routeValue(task, "evidence").([]any)
		review := rows[0].(*ordjson.Object)
		review.Set("kind", "review")
		review.Set("candidate", strings.Repeat("c", 40))
		review.Set("verdict", "clean")
		if err := l.s.SaveTask(task); err != nil {
			t.Fatal(err)
		}
	}
	l.f.QueueFault("terminal.send", hctest.Fault{LoseResponse: true})
	l.pump("parent")
	endpoint := [3]string{l.owner.Machine, l.owner.CoreID, l.owner.Address}
	w, status := ReadWake(l.s, endpoint)
	if status.State != WakeOK {
		t.Fatal(status)
	}
	b, err := BuildBoundary(l.s, w, []WakeCovered{{Task: taskIDOf(task), ID: kind + ":r1", Revision: "r1", InputRevision: ObligationRevision(task, kind+":r1")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := b.Token()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Consume(l.s, l.owner.Object(), token); err != nil {
		t.Fatal(err)
	}
	w, status = ReadWake(l.s, endpoint)
	if status.State != WakeOK {
		t.Fatal(status)
	}
	return l, task, w.Episode.Delivery, w
}
func TestDeliveryReconciliationValidatesExactConsumedReceipt(t *testing.T) {
	for _, name := range []string{"matching", "other delivery", "other generation", "other episode", "wrong input revision", "omitted input", "wrong recipient"} {
		t.Run(name, func(t *testing.T) {
			l, task, id, w := consumedDeliveryLab(t)
			r := &w.Receipts[len(w.Receipts)-1]
			switch name {
			case "other delivery":
				r.Delivery = "d-some-later-send"
			case "other generation":
				r.Generation++
			case "other episode":
				r.Boundary.Episode = "w-other"
				r.Fingerprint = r.Boundary.Fingerprint()
			case "wrong input revision":
				r.Accepted[0].InputRevision = strings.Repeat("b", 64)
				r.Boundary.Included = r.Accepted
				r.Fingerprint = r.Boundary.Fingerprint()
			case "omitted input":
				r.Boundary.Omitted = r.Boundary.Included
				r.Boundary.Included = nil
				r.Fingerprint = r.Boundary.Fingerprint()
			case "wrong recipient":
				r.Boundary.Recipient.Address = "t999"
				r.Fingerprint = r.Boundary.Fingerprint()
			}
			if err := WriteWake(l.s, w); err != nil {
				t.Fatal(err)
			}
			if name != "matching" {
				row, err := deliveryByID(l.s, taskIDOf(task), id)
				if err != nil {
					t.Fatal(err)
				}
				if err = validateProofs(l.s, task, row, []deliveryProof{{Consumption: r}}); err == nil {
					t.Fatal("unrelated consumed proof validated")
				}
				proofs, err := retainedConsumptions(l.s, taskIDOf(task), row)
				if err != nil || len(proofs) != 1 {
					t.Fatalf("retained original proof: %v %v", proofs, err)
				}
				path, err := consumptionPath(l.s, taskIDOf(task), *proofs[0].Consumption)
				if err != nil {
					t.Fatal(err)
				}
				archived, err := readConsumption(path)
				if err != nil {
					t.Fatal(err)
				}
				archived.Receipt = *r
				object, _ := ordjson.FromValue(archived)
				if err = ordjson.WriteFile(path, object); err != nil {
					t.Fatal(err)
				}
			}
			_, err := ReconcileDelivery(l.s, l.owner.Object(), taskIDOf(task), id)
			if name == "matching" {
				if err != nil {
					t.Fatal(err)
				}
				if err = PendingDeliveryEffects(l.s, task); err != nil {
					t.Fatal(err)
				}
				w.Receipts = []WakeReceipt{}
				if err = WriteWake(l.s, w); err != nil {
					t.Fatal(err)
				}
				if err = PendingDeliveryEffects(l.s, task); err != nil {
					t.Fatalf("archived consumed receipt lost after wake retention: %v", err)
				}
				open, _ := OpenObligations(l.s, task)
				if len(open) != 1 {
					t.Fatal("report obligation was waived")
				}
			} else if err == nil {
				t.Fatal("unrelated consumed receipt accepted")
			}
		})
	}
}

func TestDeliveryReconciliationRequiresExactConsumedReviewCandidate(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint(changed), func(t *testing.T) {
			l, task, id, _ := consumedDeliveryLab(t, "review")
			if changed {
				rows, _ := routeValue(task, "evidence").([]any)
				rows[0].(*ordjson.Object).Set("candidate", strings.Repeat("d", 40))
				if err := l.s.SaveTask(task); err != nil {
					t.Fatal(err)
				}
			}
			_, err := ReconcileDelivery(l.s, l.owner.Object(), taskIDOf(task), id)
			if changed && err == nil {
				t.Fatal("a consumed verdict for another candidate resolved this delivery")
			}
			if !changed {
				if err != nil {
					t.Fatal(err)
				}
				open, _ := OpenObligations(l.s, task)
				if len(open) != 1 {
					t.Fatal("consumption waived review obligation")
				}
			}
		})
	}
}

func TestDeliveryReconciliationRejectsCorruptSavedResolution(t *testing.T) {
	for _, name := range []string{"row digest", "proof generation", "proof revision", "actor", "duplicate", "unknown waiver"} {
		t.Run(name, func(t *testing.T) {
			l, task, _, id := uncertainAppliedLab(t)
			if _, err := ReconcileDelivery(l.s, l.owner.Object(), taskIDOf(task), id); err != nil {
				t.Fatal(err)
			}
			ledger, err := readResolutions(l.s, taskIDOf(task))
			if err != nil {
				t.Fatal(err)
			}
			r := &ledger.Resolutions[0]
			switch name {
			case "row digest":
				r.Digest = strings.Repeat("f", 64)
			case "proof generation":
				r.Proofs[0].Application.Endpoint.BindingGeneration++
			case "proof revision":
				r.Proofs[0].Application.Revision = strings.Repeat("e", 64)
			case "actor":
				r.Coordinator.Role = "worker"
			case "duplicate":
				ledger.Resolutions = append(ledger.Resolutions, *r)
			}
			object, _ := ordjson.FromValue(ledger)
			if name == "unknown waiver" {
				object.Set("resolved", true)
			}
			dir, _ := l.s.TaskPath(taskIDOf(task))
			if err = ordjson.WriteFile(filepath.Join(dir, ResolutionFile), object); err != nil {
				t.Fatal(err)
			}
			calls := len(l.f.Calls())
			if err = PendingDeliveryEffects(l.s, task); err == nil {
				t.Fatal("corrupt saved resolution waived uncertainty")
			}
			if len(l.f.Calls()) != calls {
				t.Fatal("maintenance validator invoked native I/O")
			}
		})
	}
}
func TestDeliveryReconciliationPreservesHistoricalReceiptAcrossBindingChange(t *testing.T) {
	l, task, _, id := uncertainAppliedLab(t)
	value, _ := routeValue(task, "worker_endpoint").(*ordjson.Object)
	replacement := cloneObject(value)
	replacement.Set("attempt", "worker-new")
	replacement.Set("binding_generation", jsonInt(2))
	replacement.Set("address", "t99")
	replacement.Set("created_ms", jsonInt(999))
	task.Set("worker_endpoint", replacement)
	if err := l.s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileDelivery(l.s, l.owner.Object(), taskIDOf(task), id); err != nil {
		t.Fatal(err)
	}
	if err := PendingDeliveryEffects(l.s, task); err != nil {
		t.Fatal(err)
	}
}
