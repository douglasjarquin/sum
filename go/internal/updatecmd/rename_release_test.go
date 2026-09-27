package updatecmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func TestApply_preRenamePreviousVerifiesPostRenameCandidate(t *testing.T) {
	lab := newApplyLab(t, applyLabOpts{})
	releases := filepath.Join(lab.root, ".local", "releases")
	buildCompatibleReleaseSkills(t, releases, lab.oldSHA, []string{"skills/sum-worker/SKILL.md"})
	selectWorkingRelease(t, lab, lab.oldSHA)
	if got := currentSHA(t, lab.root); got != lab.oldSHA {
		t.Fatalf("previous target = %s, want pre-rename release %s", got, lab.oldSHA)
	}

	view, err := Apply(lab.store, lab.ctx, lab.newSHA, true, RefusePreIdentity)
	if err != nil {
		t.Fatalf("apply post-rename candidate over pre-rename previous: %v", err)
	}
	if got := currentSHA(t, lab.root); got != lab.newSHA {
		t.Fatalf("selected %s, want post-rename candidate %s\n%s", got, lab.newSHA, dump(view))
	}
}

func TestRollback_releaseMissingWorkerSkillRefused(t *testing.T) {
	lab := newRollbackLab(t)
	selectWorkingRelease(t, lab, lab.newSHA)
	selected := currentSHA(t, lab.root)
	replaceReleaseSkills(t, lab, lab.oldSHA, nil)

	_, err := Rollback(lab.store, lab.ctx, lab.oldSHA, RefusePreIdentity)
	if err == nil || !strings.Contains(err.Error(), "release lacks a Sum worker skill resource") {
		t.Fatalf("rollback = %v, want the missing worker-skill refusal", err)
	}
	if got := currentSHA(t, lab.root); got != selected {
		t.Fatalf("selection = %s, want unchanged %s", got, selected)
	}
}

func TestRecover_priorReleaseMissingWorkerSkillRefused(t *testing.T) {
	lab := newActivationLab(t)
	selectWorkingRelease(t, lab, lab.newSHA)
	selected := currentSHA(t, lab.root)
	replaceReleaseSkills(t, lab, lab.oldSHA, nil)

	state := activationState(t, lab.root)
	knownGood := asObject(func() any { v, _ := state.Get("known_good"); return v }())
	generation := "0123456789abcdef0123456789abcdef"
	recovery, err := stageRecovery(lab.store, lab.root, generation, knownGood)
	if err != nil {
		t.Fatal(err)
	}
	knownPath := strField(knownGood, "path")
	from := ordjson.NewObject()
	from.Set("kind", "release")
	from.Set("sha", lab.oldSHA)
	from.Set("path", filepath.Join(filepath.Dir(knownPath), lab.oldSHA))
	pending := ordjson.NewObject()
	pending.Set("generation", generation)
	pending.Set("action", "apply")
	pending.Set("from", from)
	pending.Set("to", knownGood)
	pending.Set("recovery", recovery)
	pending.Set("status", "prepared")
	state.Set("pending", pending)
	if err := writeActivationState(lab.store, lab.root, state); err != nil {
		t.Fatal(err)
	}

	_, err = Recover(lab.store, lab.ctx, generation, RefusePreIdentity)
	if err == nil || !strings.Contains(err.Error(), "release lacks a Sum worker skill resource") {
		t.Fatalf("recover = %v, want the missing worker-skill refusal", err)
	}
	if got := currentSHA(t, lab.root); got != selected {
		t.Fatalf("selection = %s, want unchanged %s", got, selected)
	}
}

func replaceReleaseSkills(t *testing.T, lab *applyLab, sha string, workerSkills []string) {
	t.Helper()
	dir := filepath.Join(lab.root, ".local", "releases", sha)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	buildCompatibleReleaseSkills(t, filepath.Join(lab.root, ".local", "releases"), sha, workerSkills)
}
