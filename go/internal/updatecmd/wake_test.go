package updatecmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// The wake-protocol gate (#240a, KTD7): a target that does not keep the coordinator wake sidecar is refused while an
// episode is outstanding, uncertain, or unreadable, because that code would prompt over it and could not reconcile it.

func labEndpoint(t *testing.T) [3]string {
	t.Helper()
	host, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	return [3]string{host, "sum-test", "w-parent:p1"}
}

// plantWake writes the lab coordinator's sidecar in phase, bound to the occupant the owner record records.
func plantWake(t *testing.T, lab *applyLab, phase string) *returns.Wake {
	t.Helper()
	owner, err := lab.store.Owner()
	if err != nil || owner == nil {
		t.Fatalf("owner: %v %v", owner, err)
	}
	recorded, _ := owner.Get("incarnation")
	w, err := returns.NewWake(lab.store, labEndpoint(t), returns.IncarnationJSON(recorded))
	if err != nil {
		t.Fatal(err)
	}
	w.Prepare("w-planted", []returns.WakeRef{{Task: "t-aaaaaaaaaaaa", ID: "report:r1"}})
	w.Episode.Phase = phase
	if err := returns.WriteWake(lab.store, w); err != nil {
		t.Fatal(err)
	}
	return w
}

// markWakeCapable adds the wake marker to a staged release's tree and verified file list.
func markWakeCapable(t *testing.T, lab *applyLab, sha string) {
	t.Helper()
	dir := filepath.Join(lab.root, ".local", "releases", sha)
	content := "package returns\n"
	writeFile(t, filepath.Join(dir, wakeMarker), content)
	path := filepath.Join(dir, "release.json")
	raw, err := ordjson.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest := asObject(raw)
	files := asObject(func() any { v, _ := manifest.Get("files"); return v }())
	files.Set(wakeMarker, "sha256:"+sha256Hex([]byte(content)))
	if err := ordjson.WriteFile(path, manifest); err != nil {
		t.Fatal(err)
	}
}

func compatWake(t *testing.T, view *ordjson.Object) *ordjson.Object {
	t.Helper()
	compat := asObject(func() any { v, _ := view.Get("compatibility"); return v }())
	row := asObject(func() any {
		if compat == nil {
			return nil
		}
		v, _ := compat.Get("wake_protocol")
		return v
	}())
	if row == nil {
		t.Fatalf("compatibility has no wake_protocol row\n%s", dump(view))
	}
	return row
}

func TestWakeGate_incapableTargetIsRefusedWhileAnEpisodeIsOutstanding(t *testing.T) {
	for _, phase := range []string{returns.WakeSubmitted, returns.WakeUncertain, returns.WakeIntent, returns.WakeClaimed, returns.WakePrepared} {
		t.Run(phase, func(t *testing.T) {
			lab := newRollbackLab(t)
			selectWorkingRelease(t, lab, lab.oldSHA)
			selectWorkingRelease(t, lab, lab.newSHA)
			plantWake(t, lab, phase)
			_, err := Rollback(lab.store, lab.ctx, "", RefusePreIdentity)
			if err == nil {
				t.Fatal("rollback to a release without the wake protocol succeeded over an outstanding episode")
			}
			msg := err.Error()
			for _, want := range []string{"wake protocol", "w-parent:p1", phase, "sumctl wake reconcile", "sumctl wake consume", wakeMarker} {
				if !strings.Contains(msg, want) {
					t.Fatalf("refusal %q does not name %q", msg, want)
				}
			}
			if strings.Contains(msg, "--allow") {
				t.Fatalf("refusal offers an override: %s", msg)
			}
			if got := currentSHA(t, lab.root); got != lab.newSHA {
				t.Fatalf("selection changed to %s", got)
			}
		})
	}
}

func TestWakeGate_unreadableSidecarRefusesAnIncapableTarget(t *testing.T) {
	lab := newRollbackLab(t)
	selectWorkingRelease(t, lab, lab.oldSHA)
	selectWorkingRelease(t, lab, lab.newSHA)
	path := lab.store.WakePath(labEndpoint(t))
	writeFile(t, path, "{broken")
	_, err := Rollback(lab.store, lab.ctx, "", RefusePreIdentity)
	if err == nil || !strings.Contains(err.Error(), "sumctl wake show") || !strings.Contains(err.Error(), "wake protocol") {
		t.Fatalf("rollback over an unreadable sidecar = %v, want a refusal naming the inspection command", err)
	}
	if got := currentSHA(t, lab.root); got != lab.newSHA {
		t.Fatalf("selection changed to %s", got)
	}
}

func TestWakeGate_capableTargetAndClosedEpisodesPass(t *testing.T) {
	lab := newRollbackLab(t)
	selectWorkingRelease(t, lab, lab.oldSHA)
	selectWorkingRelease(t, lab, lab.newSHA)

	// No sidecar at all: the row is unused and nothing blocks.
	view, err := Rollback(lab.store, lab.ctx, lab.oldSHA, RefusePreIdentity)
	if err != nil {
		t.Fatalf("rollback with no sidecar: %v", err)
	}
	if row := compatWake(t, view); strField(row, "result") != "unused" || row.Len() == 0 {
		t.Fatalf("wake_protocol = %s, want unused", dump(row))
	}
	selectWorkingRelease(t, lab, lab.newSHA)

	// A closed episode is not outstanding: an incapable target is still allowed.
	plantWake(t, lab, returns.WakeNotDelivered)
	if _, err := Rollback(lab.store, lab.ctx, lab.oldSHA, RefusePreIdentity); err != nil {
		t.Fatalf("rollback over a closed episode: %v", err)
	}
	selectWorkingRelease(t, lab, lab.newSHA)

	// A capable target passes over an outstanding episode, with the marker as evidence.
	plantWake(t, lab, returns.WakeSubmitted)
	markWakeCapable(t, lab, lab.oldSHA)
	view, err = Rollback(lab.store, lab.ctx, lab.oldSHA, RefusePreIdentity)
	if err != nil {
		t.Fatalf("rollback to a capable release: %v", err)
	}
	row := compatWake(t, view)
	if strField(row, "result") != "supported" || !strings.Contains(strField(row, "target_evidence"), wakeMarker) {
		t.Fatalf("wake_protocol = %s, want supported from the release files", dump(row))
	}
	if got := currentSHA(t, lab.root); got != lab.oldSHA {
		t.Fatalf("selected %s, want %s", got, lab.oldSHA)
	}
}

const wakeContractJSON = `{"sum_version":"0.1.0","contracts":{"herdr_cli":"0.9.0","mcp":{"server":"herdr-mesh-sum","version":"0.1.0","tools":10}},"supports":{"state_schema":[1],"brief_schema":[1],"machine_identity":[1],"wake_protocol":[1]}}`

// A checkout target's capability is what its prebuilt helper reports, never what HEAD carries.
func TestWakeGate_checkoutHelperDecides(t *testing.T) {
	lab := newRollbackLab(t)
	selectWorkingRelease(t, lab, lab.oldSHA)
	plantWake(t, lab, returns.WakeSubmitted)
	plantNativeHelper(t, lab.root, workingHelper(""))
	_, err := Rollback(lab.store, lab.ctx, "checkout", RefusePreIdentity)
	if err == nil || !strings.Contains(err.Error(), ".local/bin/sumctl does not report supports.wake_protocol") {
		t.Fatalf("checkout rollback onto a helper without the wake protocol = %v, want the helper refusal", err)
	}
	if got := currentSHA(t, lab.root); got != lab.oldSHA {
		t.Fatalf("selection changed to %s", got)
	}
	plantNativeHelper(t, lab.root, contractHelper(wakeContractJSON))
	view, err := Rollback(lab.store, lab.ctx, "checkout", RefusePreIdentity)
	if err != nil {
		t.Fatalf("checkout rollback with a capable helper: %v", err)
	}
	if row := compatWake(t, view); strField(row, "result") != "supported" || !strings.Contains(strField(row, "target_evidence"), "release-contract") {
		t.Fatalf("wake_protocol = %s, want supported from the helper's contract", dump(row))
	}
}

// Final validation and the switch hold the delivery compatibility lock exclusively, so a pass holding it shared (an
// admission in progress) is never overtaken mid-decision; the switch waits its bound and refuses rather than racing.
func TestWakeGate_activationWaitsForADeliveryPassThenRefuses(t *testing.T) {
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
	_, err = Rollback(lab.store, lab.ctx, "", RefusePreIdentity)
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
	if _, err := Rollback(lab.store, lab.ctx, "", RefusePreIdentity); err != nil {
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
	if _, err := Rollback(lab.store, lab.ctx, "", RefusePreIdentity); err != nil {
		t.Fatalf("rollback with the exclusive lock held: %v", err)
	}
	if _, err := os.Stat(lab.store.WakePath(labEndpoint(t))); !os.IsNotExist(err) {
		t.Fatalf("the update touched the wake sidecar: %v", err)
	}
}

// The way out of the gate is the protocol's own settlement, from the serving runtime: reconcile closes a prepared
// episode, consume closes a submitted one, and the rollback then passes without an override.
func TestWakeGate_rollbackPassesOnceTheEpisodeIsSettled(t *testing.T) {
	t.Run("prepared then reconcile", func(t *testing.T) {
		lab := newRollbackLab(t)
		selectWorkingRelease(t, lab, lab.oldSHA)
		selectWorkingRelease(t, lab, lab.newSHA)
		plantWake(t, lab, returns.WakePrepared)
		if _, err := Rollback(lab.store, lab.ctx, "", RefusePreIdentity); err == nil {
			t.Fatal("rollback passed over a prepared episode")
		}
		view, err := returns.Reconcile(lab.store, lab.ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		if strField(view, "after") != returns.WakeNotSubmitted {
			t.Fatalf("reconcile = %s", dump(view))
		}
		if _, err := Rollback(lab.store, lab.ctx, "", RefusePreIdentity); err != nil {
			t.Fatalf("rollback after reconcile: %v", err)
		}
		if got := currentSHA(t, lab.root); got != lab.oldSHA {
			t.Fatalf("selected %s, want %s", got, lab.oldSHA)
		}
	})
	t.Run("submitted then consume", func(t *testing.T) {
		lab := newRollbackLab(t)
		selectWorkingRelease(t, lab, lab.oldSHA)
		selectWorkingRelease(t, lab, lab.newSHA)
		plantWake(t, lab, returns.WakeSubmitted)
		if _, err := Rollback(lab.store, lab.ctx, "", RefusePreIdentity); err == nil {
			t.Fatal("rollback passed over a submitted episode")
		}
		// Reconcile holds a submitted episode as it is: it is not the way out.
		held, err := returns.Reconcile(lab.store, lab.ctx, "")
		if err != nil || strField(held, "after") != returns.WakeSubmitted || strField(held, "action") != "none" {
			t.Fatalf("reconcile of a submitted episode = %s %v", dump(held), err)
		}
		if _, err := Rollback(lab.store, lab.ctx, "", RefusePreIdentity); err == nil {
			t.Fatal("rollback passed after a no-op reconcile")
		}
		shown, err := returns.Show(lab.store, "")
		if err != nil {
			t.Fatal(err)
		}
		rows, _ := shown.Get("recipients")
		entry := asObject(rows.([]any)[0])
		consumed, err := returns.Consume(lab.store, lab.ctx, strField(entry, "boundary"))
		if err != nil || strField(consumed, "result") != "consumed" {
			t.Fatalf("consume = %s %v", dump(consumed), err)
		}
		view, err := Rollback(lab.store, lab.ctx, "", RefusePreIdentity)
		if err != nil {
			t.Fatalf("rollback after consume: %v", err)
		}
		if row := compatWake(t, view); strField(row, "result") != "unused" {
			t.Fatalf("wake_protocol = %s, want unused once the episode is consumed", dump(row))
		}
		if got := currentSHA(t, lab.root); got != lab.oldSHA {
			t.Fatalf("selected %s, want %s", got, lab.oldSHA)
		}
	})
}
