package updatecmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestQuiescentUpdateBlocksEachRunningAttemptWithoutChangingSelection(t *testing.T) {
	lab := nativeApplyLab(t)
	task := ordjson.NewObject()
	task.Set("schema", json.Number("2"))
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("status", "working")
	session, _ := lab.store.CoordinatorSession()
	worker, err := reservations.NewAttempt("worker", session.Owner(), nil, store.Now(), "running", nil)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := reservations.NewAttempt("verifier", session.Owner(), nil, store.Now(), "running", nil)
	if err != nil {
		t.Fatal(err)
	}
	execution := reservations.NewExecution(worker)
	execution.Set("verifiers", []any{verifier})
	task.Set("execution", execution)
	if err = lab.store.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	view, err := Compatibility(lab.store, lab.root, filepath.Join(lab.root, ".local/releases", lab.newSHA), DefaultRuntime(lab.root))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := view.Get("blocking")
	blocking := raw.([]any)
	if len(blocking) != 2 {
		t.Fatalf("two running attempts: %s", dump(view))
	}
	if _, err = Apply(lab.store, lab.ctx, lab.newSHA, true); err == nil {
		t.Fatal("running attempts selected runtime")
	}
	if hasCurrent(t, lab.root) {
		t.Fatal("running attempts changed selection")
	}
	worker.Set("state", "released")
	verifier.Set("state", "released")
	task.Set("questions", []any{ordjson.NewObject()})
	task.Set("answers", []any{ordjson.NewObject()})
	if err = lab.store.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(lab.store, lab.ctx, lab.newSHA, true); err != nil {
		t.Fatalf("settled attempts with open ledger items: %v", err)
	}
	if currentSHA(t, lab.root) != lab.newSHA || git(t, lab.root, "rev-parse", "HEAD") != lab.newSHA {
		t.Fatal("instructions and selection disagree")
	}
}

func TestLogicalOwnerFloorSurvivesInterruptedRetirement(t *testing.T) {
	for _, claimExists := range []bool{true, false} {
		t.Run(map[bool]string{true: "claim", false: "pointer-only"}[claimExists], func(t *testing.T) {
			lab := nativeApplyLab(t)
			if !claimExists {
				original := filepath.Join(lab.store.Home, "coordinator.json")
				if err := os.Rename(original, original+".interrupted-test"); err != nil {
					t.Fatal(err)
				}
			}
			target := filepath.Join(lab.root, ".local/releases", lab.newSHA)
			offered := contract.BuildRelease()
			offered.Supports.OwnerSchema = nil
			offered.Supports.StateSchema = []int{2}
			raw, _ := json.Marshal(offered)
			plantNativeHelper(t, target, "#!/bin/sh\nprintf '%s\\n' '"+string(raw)+"'\n")
			manifestValue, err := ordjson.ReadFile(filepath.Join(target, "release.json"))
			if err != nil {
				t.Fatal(err)
			}
			manifest := asObject(manifestValue)
			supports, _ := ordjson.FromValue(offered.Supports)
			manifest.Set("supports", supports)
			if err = ordjson.WriteFile(filepath.Join(target, "release.json"), manifest); err != nil {
				t.Fatal(err)
			}

			view, err := Compatibility(lab.store, lab.root, target, DefaultRuntime(lab.root))
			if err != nil {
				t.Fatal(err)
			}
			if ok, _ := view.Get("ok"); ok != false {
				t.Fatal("old target accepted logical floor")
			}
			if !strings.Contains(dump(view), "state.json") || !strings.Contains(dump(view), "owner_schema") {
				t.Fatalf("missing floor provenance: %s", dump(view))
			}
			if hasCurrent(t, lab.root) {
				t.Fatal("compatibility changed selection")
			}
		})
	}
}

func TestUpdateFinalActivationRejectsTransferredClaim(t *testing.T) {
	lab := nativeApplyLab(t)
	stale := lab.ctx
	session, err := lab.store.ClaimCoordinator("", "replacement", true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.RequireClaim(lab.store, session.Token())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(lab.store, stale, lab.newSHA, true); err == nil {
		t.Fatal("stale claim selected runtime")
	}
	if hasCurrent(t, lab.root) {
		t.Fatal("stale claim changed selection")
	}
}

func TestQuiescentActivationExcludesConcurrentDeliveryPass(t *testing.T) {
	lab := newRollbackLab(t)
	selectWorkingRelease(t, lab, lab.oldSHA)
	selectWorkingRelease(t, lab, lab.newSHA)
	old := deliveryLockWait
	deliveryLockWait = 300 * time.Millisecond
	t.Cleanup(func() { deliveryLockWait = old })
	pass, err := store.Open(lab.home)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := pass.DeliveryShared(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	started := time.Now()
	_, err = Rollback(lab.store, lab.ctx, "")
	if err == nil || !strings.Contains(err.Error(), "delivery pass") {
		t.Fatalf("rollback during a delivery pass = %v, want a refusal naming the pass", err)
	}
	if elapsed := time.Since(started); elapsed < 250*time.Millisecond || elapsed > 5*time.Second {
		t.Fatalf("rollback waited %s for the pass", elapsed)
	}
	if got := currentSHA(t, lab.root); got != lab.newSHA {
		t.Fatalf("selection changed to %s", got)
	}
	unlock()
	if _, err := Rollback(lab.store, lab.ctx, ""); err != nil {
		t.Fatalf("rollback after the pass released the lock: %v", err)
	}
	// While the switch holds the lock exclusively, a pass's shared acquisition waits: shown through the hook that
	// runs between the pending record and the selection.
	selectWorkingRelease(t, lab, lab.newSHA)
	afterPendingWrite = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		if _, err := pass.DeliveryShared(ctx); err == nil {
			return fmt.Errorf("a delivery pass took the shared lock during the switch")
		}
		return nil
	}
	t.Cleanup(func() { afterPendingWrite = nil })
	if _, err := Rollback(lab.store, lab.ctx, ""); err != nil {
		t.Fatalf("rollback with the exclusive lock held: %v", err)
	}
}

func TestHistoricalSidecarsNeverBlockOrChangeDuringUpdate(t *testing.T) {
	lab := nativeApplyLab(t)
	path := filepath.Join(lab.store.Home, "deliver", "old.wake.json")
	original := []byte(`{"phase":"uncertain","untouched":true}`)
	writeFile(t, path, string(original))
	view, err := Compatibility(lab.store, lab.root, filepath.Join(lab.root, ".local/releases", lab.newSHA), DefaultRuntime(lab.root))
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := view.Get("ok"); ok != true {
		t.Fatalf("historical sidecar blocked: %s", dump(view))
	}
	if !strings.Contains(dump(view), "old.wake.json") {
		t.Fatalf("missing historical path: %s", dump(view))
	}
	if _, err = Apply(lab.store, lab.ctx, lab.newSHA, true); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("historical sidecar changed")
	}
}
