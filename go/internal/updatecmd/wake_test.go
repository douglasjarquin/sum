package updatecmd

import (
	"context"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func nativeWakeEndpoint(lab *applyLab) [3]string {
	endpoint := store.EndpointFromContext(lab.ctx)
	return [3]string{endpoint.Machine, endpoint.CoreID, endpoint.Address}
}

func TestNativeWakeProtocolPreservesEveryOutstandingPhase(t *testing.T) {
	for _, phase := range []string{"prepared", "claimed", "intent", "uncertain", "submitted"} {
		t.Run(phase, func(t *testing.T) {
			lab := nativeApplyLab(t)
			owner, err := lab.store.Owner()
			if err != nil {
				t.Fatal(err)
			}
			incarnation, _ := owner.Get("incarnation")
			wake, err := returns.NewWake(lab.store, nativeWakeEndpoint(lab), returns.IncarnationJSON(incarnation))
			if err != nil {
				t.Fatal(err)
			}
			wake.Prepare("native-wake", []returns.WakeRef{{Task: "t-aaaaaaaaaaaa", ID: "report:r1"}})
			wake.Episode.Phase = phase
			if err = returns.WriteWake(lab.store, wake); err != nil {
				t.Fatal(err)
			}
			path := lab.store.WakePath(nativeWakeEndpoint(lab))
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			current := DefaultRuntime(lab.root)
			compatible, err := Compatibility(lab.store, lab.root, lab.root, current)
			if err != nil {
				t.Fatal(err)
			}
			if ok, _ := compatible.Get("ok"); ok != true {
				t.Fatalf("native compatible target: %s", dump(compatible))
			}
			plantNativeHelper(t, lab.root, "#!/bin/sh\nprintf '%s\n' '"+preIdentityContractJSON+"'\n")
			refused, err := Compatibility(lab.store, lab.root, lab.root, current)
			if err != nil {
				t.Fatal(err)
			}
			if ok, _ := refused.Get("ok"); ok != false {
				t.Fatal("serving legacy artifact bypassed wake contract")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("capability check rewrote an unresolved wake")
			}
		})
	}
}

func TestNativeWakeUnreadableOrForeignSidecarIsPreserved(t *testing.T) {
	lab := nativeApplyLab(t)
	path := lab.store.WakePath(nativeWakeEndpoint(lab))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	for _, contents := range []string{"{", "{\"schema\":99}"} {
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Compatibility(lab.store, lab.root, lab.root, DefaultRuntime(lab.root)); err != nil {
			t.Fatal(err)
		}
		after, _ := os.ReadFile(path)
		if string(after) != contents {
			t.Fatal("check changed unreadable sidecar")
		}
	}
}

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
	if _, err := os.Stat(lab.store.WakePath(nativeWakeEndpoint(lab))); !os.IsNotExist(err) {
		t.Fatalf("the update touched the wake sidecar: %v", err)
	}
}
