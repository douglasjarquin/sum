package returns

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func pendingConsumptionLab(t *testing.T) (*nativeLab, *ordjson.Object, string, string) {
	t.Helper()
	l := newNativeLab(t)
	task, _ := l.task("report")
	l.f.QueueFault("terminal.send", hctest.Fault{LoseResponse: true})
	l.pump("parent")
	w, status := ReadWake(l.s, [3]string{l.owner.Machine, l.owner.CoreID, l.owner.Address})
	if status.State != WakeOK {
		t.Fatal(status)
	}
	b, err := BuildBoundary(l.s, w, []WakeCovered{{Task: taskIDOf(task), ID: "report:r1", Revision: "r1", InputRevision: ObligationRevision(task, "report:r1")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := b.Token()
	if err != nil {
		t.Fatal(err)
	}
	return l, task, w.Episode.Delivery, token
}
func TestConsumptionRetentionFailurePreservesDurableWakeUntilRetry(t *testing.T) {
	l, task, id, token := pendingConsumptionLab(t)
	dir, _ := l.s.TaskPath(taskIDOf(task))
	blocker := filepath.Join(dir, ConsumptionDirectory)
	if err := os.WriteFile(blocker, []byte("blocked archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Consume(l.s, l.owner.Object(), token); err == nil {
		t.Fatal("archive failure was hidden")
	}
	endpoint := [3]string{l.owner.Machine, l.owner.CoreID, l.owner.Address}
	w, status := ReadWake(l.s, endpoint)
	if status.State != WakeOK || w.Episode.Phase != WakeConsumed {
		t.Fatalf("actual committed consume lost: %+v %v", w, status)
	}
	original, err := os.ReadFile(l.s.WakePath(endpoint))
	if err != nil {
		t.Fatal(err)
	}
	history, err := os.ReadFile(filepath.Join(dir, File))
	if err != nil {
		t.Fatal(err)
	}
	inputs := len(l.f.Inputs(l.owner.Address))
	w.Receipts = []WakeReceipt{}
	if err = WriteWake(l.s, w); err == nil {
		t.Fatal("wake discarded receipt while archive was unavailable")
	}
	after, _ := os.ReadFile(l.s.WakePath(endpoint))
	if !bytes.Equal(original, after) {
		t.Fatal("failed retention overwrote original wake")
	}
	if err = os.Rename(blocker, blocker+".retired"); err != nil {
		t.Fatal(err)
	}
	if err = WriteWake(l.s, w); err != nil {
		t.Fatal(err)
	}
	if _, err = ReconcileDelivery(l.s, l.owner.Object(), taskIDOf(task), id); err != nil {
		t.Fatalf("first recovery after retention retry: %v", err)
	}
	afterHistory, _ := os.ReadFile(filepath.Join(dir, File))
	if !bytes.Equal(history, afterHistory) || len(l.f.Inputs(l.owner.Address)) != inputs {
		t.Fatal("retention recovery changed send history or input")
	}
}
func TestConsumptionRetentionNeverArchivesFailedConsumeCommit(t *testing.T) {
	l, task, id, token := pendingConsumptionLab(t)
	endpoint := [3]string{l.owner.Machine, l.owner.CoreID, l.owner.Address}
	path := l.s.WakePath(endpoint)
	prior, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	previousSync := wakeSyncDir
	defer func() { wakeSyncDir = previousSync }()
	wakeSyncDir = func(string) error {
		if err := os.Rename(path, path+".uncommitted"); err != nil {
			return err
		}
		if err := os.WriteFile(path, prior, 0600); err != nil {
			return err
		}
		return fmt.Errorf("injected consumed wake write failure")
	}
	if _, err = Consume(l.s, l.owner.Object(), token); err == nil {
		t.Fatal("failed consume commit was reported successful")
	}
	wakeSyncDir = previousSync
	dir, _ := l.s.TaskPath(taskIDOf(task))
	if _, err = os.Stat(filepath.Join(dir, ConsumptionDirectory)); !os.IsNotExist(err) {
		t.Fatal("uncommitted consume produced durable evidence")
	}
	if _, err = ReconcileDelivery(l.s, l.owner.Object(), taskIDOf(task), id); err == nil {
		t.Fatal("failed consume supplied reconciliation proof")
	}
}
func TestConsumptionRetentionRejectsCorruptArchivedEvidence(t *testing.T) {
	l, task, id, _ := consumedDeliveryLab(t)
	row, err := deliveryByID(l.s, taskIDOf(task), id)
	if err != nil {
		t.Fatal(err)
	}
	proofs, err := retainedConsumptions(l.s, taskIDOf(task), row)
	if err != nil || len(proofs) != 1 {
		t.Fatalf("retained proofs: %v %v", proofs, err)
	}
	path, err := consumptionPath(l.s, taskIDOf(task), *proofs[0].Consumption)
	if err != nil {
		t.Fatal(err)
	}
	value, err := readConsumption(path)
	if err != nil {
		t.Fatal(err)
	}
	value.Receipt.Generation++
	object, _ := ordjson.FromValue(value)
	if err = ordjson.WriteFile(path, object); err != nil {
		t.Fatal(err)
	}
	if _, err = ReconcileDelivery(l.s, l.owner.Object(), taskIDOf(task), id); err == nil {
		t.Fatal("corrupt retained consumed evidence accepted")
	}
}

func TestConsumptionRetainsReviewBeforeFirstRecoveryAfterLaterEpisodes(t *testing.T) {
	l, original, id, _ := consumedDeliveryLab(t, "review")
	endpoint := [3]string{l.owner.Machine, l.owner.CoreID, l.owner.Address}
	for i := 0; i < 21; i++ {
		task, _ := l.task("report")
		l.f.SetStatus(l.owner.Address, "idle")
		l.pump("parent")
		w, status := ReadWake(l.s, endpoint)
		if status.State != WakeOK {
			t.Fatal(status)
		}
		boundary, err := BuildBoundary(l.s, w, []WakeCovered{{Task: taskIDOf(task), ID: "report:r1", Revision: "r1", InputRevision: ObligationRevision(task, "report:r1")}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		token, err := boundary.Token()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = Consume(l.s, l.owner.Object(), token); err != nil {
			t.Fatal(err)
		}
	}
	w, status := ReadWake(l.s, endpoint)
	if status.State != WakeOK {
		t.Fatal(status)
	}
	for _, receipt := range w.Receipts {
		if receipt.Delivery == id {
			t.Fatal("review receipt still in rolling wake history")
		}
	}
	if _, err := ReconcileDelivery(l.s, l.owner.Object(), taskIDOf(original), id); err != nil {
		t.Fatalf("first review recovery after eviction: %v", err)
	}
	if err := PendingDeliveryEffects(l.s, original); err != nil {
		t.Fatal(err)
	}
	open, _ := OpenObligations(l.s, original)
	if len(open) != 1 {
		t.Fatal("retention waived the review obligation")
	}
}
