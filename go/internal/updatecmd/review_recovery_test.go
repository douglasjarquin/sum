package updatecmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestQuiescentUpdateWaitsForSubmittedReviewer(t *testing.T) {
	lab := nativeApplyLab(t)
	session, err := lab.store.CoordinatorSession()
	if err != nil {
		t.Fatal(err)
	}
	worker, err := reservations.NewAttempt("worker", session.Owner(), nil, store.Now(), "released", nil)
	if err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", json.Number("2"))
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("status", "reported")
	task.Set("execution", reservations.NewExecution(worker))
	reviewer := ordjson.NewObject()
	reviewer.Set("candidate", lab.newSHA)
	launch := ordjson.NewObject()
	launch.Set("state", "created")
	reviewer.Set("native_launch", launch)
	prompt := ordjson.NewObject()
	prompt.Set("state", "submitted")
	prompt.Set("candidate", lab.newSHA)
	reviewer.Set("prompt", prompt)
	task.Set("reviewer", reviewer)
	if err := lab.store.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(lab.store, lab.ctx, lab.newSHA, true); err == nil || !strings.Contains(err.Error(), "reviewer") {
		t.Fatalf("update while reviewer is working = %v, want reviewer refusal", err)
	}
	if hasCurrent(t, lab.root) {
		t.Fatal("working reviewer changed runtime selection")
	}
	prompt.Set("state", "applied")
	receipt := ordjson.NewObject()
	receipt.Set("kind", "review")
	receipt.Set("candidate", lab.newSHA)
	prompt.Set("application_receipt", receipt)
	if err := lab.store.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(lab.store, lab.ctx, lab.newSHA, true); err != nil {
		t.Fatalf("settled reviewer blocked update: %v", err)
	}
}

func TestRollbackRecoveryRestoresCheckoutAfterInterruptedAlignment(t *testing.T) {
	for _, checkoutState := range []string{"clean", "dirty", "unrelated-commit"} {
		t.Run(checkoutState, func(t *testing.T) {
			lab := newRollbackLab(t)
			selectWorkingRelease(t, lab, lab.oldSHA)
			selectWorkingRelease(t, lab, lab.newSHA)
			afterSelect = func() error {
				if alignment := syncInstallationCheckout(lab.root, lab.oldSHA); strField(alignment, "result") != "aligned-rollback" {
					t.Fatalf("rollback alignment: %s", dump(alignment))
				}
				return errTestInterrupt
			}
			t.Cleanup(func() { afterSelect = nil })
			if _, err := Rollback(lab.store, lab.ctx, ""); !errors.Is(err, errTestInterrupt) {
				t.Fatalf("interrupted rollback: %v", err)
			}
			afterSelect = nil
			generation := pendingGeneration(t, lab.root)
			if generation == "" || currentSHA(t, lab.root) != lab.oldSHA || git(t, lab.root, "rev-parse", "HEAD") != lab.oldSHA {
				t.Fatal("fixture did not interrupt after backward checkout alignment")
			}
			if checkoutState == "dirty" {
				path := filepath.Join(lab.root, "local-note.txt")
				writeFile(t, path, "preserve my work\n")
				if _, err := independentRecover(t, lab, generation); err == nil || !strings.Contains(err.Error(), "local changes") {
					t.Fatalf("dirty recovery = %v, want local changes refusal", err)
				}
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "preserve my work\n" || pendingGeneration(t, lab.root) != generation || currentSHA(t, lab.root) != lab.oldSHA || git(t, lab.root, "rev-parse", "HEAD") != lab.oldSHA {
					t.Fatal("dirty recovery changed work, selection, or pending evidence")
				}
				return
			}
			if checkoutState == "unrelated-commit" {
				next := stageNextRelease(t, lab)
				git(t, lab.root, "merge", "--ff-only", next)
				if _, err := independentRecover(t, lab, generation); err == nil || !strings.Contains(err.Error(), "checkout HEAD matches neither") {
					t.Fatalf("recovery after unrelated commit = %v, want recorded endpoint refusal", err)
				}
				if pendingGeneration(t, lab.root) != generation || currentSHA(t, lab.root) != lab.oldSHA || git(t, lab.root, "rev-parse", "HEAD") != next {
					t.Fatal("recovery changed unrelated committed work, selection, or pending evidence")
				}
				return
			}
			if _, err := independentRecover(t, lab, generation); err != nil {
				t.Fatalf("recovery: %v", err)
			}
			if currentSHA(t, lab.root) != lab.newSHA || git(t, lab.root, "rev-parse", "HEAD") != lab.newSHA || pendingGeneration(t, lab.root) != "" {
				t.Fatal("recovery did not restore both runtime and checkout before clearing pending")
			}
		})
	}
}
