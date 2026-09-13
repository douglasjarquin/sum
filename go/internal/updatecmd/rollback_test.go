package updatecmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRollback_plainSelectsRecordedPreviousNotCheckout(t *testing.T) {
	lab := newRollbackLab(t)
	selectWorkingRelease(t, lab, lab.oldSHA)
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", lab.newSHA), workingHelper(""))
	if _, err := Apply(lab.store, lab.ctx, lab.newSHA, true); err != nil {
		t.Fatalf("apply B: %v", err)
	}
	if git(t, lab.root, "rev-parse", "HEAD") != lab.newSHA {
		t.Fatalf("checkout HEAD = %s, want B %s", git(t, lab.root, "rev-parse", "HEAD"), lab.newSHA)
	}
	if got := currentSHA(t, lab.root); got != lab.newSHA {
		t.Fatalf("runtime = %s, want B %s", got, lab.newSHA)
	}
	taskBefore := readTaskBrief(t, lab)

	view, err := Rollback(lab.store, lab.ctx, "")
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := currentSHA(t, lab.root); got != lab.oldSHA {
		t.Fatalf("plain rollback selected %s, want previous release A %s\n%s", got, lab.oldSHA, dump(view))
	}
	rt := DefaultRuntime(lab.root)
	if strField(rt, "kind") != "release" {
		t.Fatalf("kind = %q, want release (not checkout)\n%s", strField(rt, "kind"), dump(view))
	}
	if git(t, lab.root, "rev-parse", "HEAD") != lab.newSHA {
		t.Fatalf("rollback moved checkout HEAD to %s, want B %s", git(t, lab.root, "rev-parse", "HEAD"), lab.newSHA)
	}
	assertTaskBrief(t, lab, taskBefore)
}

func TestRollback_missingHistoryRefusesWithoutCheckoutFallback(t *testing.T) {
	lab := newRollbackLab(t)
	_, err := Rollback(lab.store, lab.ctx, "")
	if err == nil {
		t.Fatal("plain rollback with no previous known-good succeeded")
	}
	if !strings.Contains(err.Error(), "No recorded previous known-good selection") {
		t.Fatalf("refusal = %v, want missing-history diagnostic", err)
	}
	if hasCurrent(t, lab.root) {
		t.Fatalf("missing history changed selection to %s", currentSHA(t, lab.root))
	}
}

func TestRollback_stagedUnapprovedReleaseRefused(t *testing.T) {
	lab := newRollbackLab(t)
	selectWorkingRelease(t, lab, lab.oldSHA)
	previous := currentSHA(t, lab.root)
	knownGood := recordedKnownGood(t, lab.root)
	writeFile(t, filepath.Join(lab.root, "unmerged.md"), "unmerged\n")
	git(t, lab.root, "add", "unmerged.md")
	git(t, lab.root, "commit", "-m", "unmerged local candidate")
	unmerged := git(t, lab.root, "rev-parse", "HEAD")
	buildCompatibleRelease(t, filepath.Join(lab.root, ".local", "releases"), unmerged)
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", unmerged), workingHelper(""))

	_, err := Rollback(lab.store, lab.ctx, unmerged)
	if err == nil {
		t.Fatal("staged-but-unapproved rollback succeeded")
	}
	if got := currentSHA(t, lab.root); got != previous {
		t.Fatalf("selection = %s, want unchanged %s", got, previous)
	}
	if got := recordedKnownGood(t, lab.root); got != knownGood {
		t.Fatalf("known-good = %s, want unchanged %s", got, knownGood)
	}
}

func TestRollback_approvedExplicitReleaseSucceeds(t *testing.T) {
	lab := newRollbackLab(t)
	selectWorkingRelease(t, lab, lab.oldSHA)
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", lab.newSHA), workingHelper(""))
	if _, err := Apply(lab.store, lab.ctx, lab.newSHA, true); err != nil {
		t.Fatalf("apply B: %v", err)
	}

	view, err := Rollback(lab.store, lab.ctx, lab.oldSHA)
	if err != nil {
		t.Fatalf("rollback --to A: %v", err)
	}
	if got := currentSHA(t, lab.root); got != lab.oldSHA {
		t.Fatalf("selected %s, want A %s\n%s", got, lab.oldSHA, dump(view))
	}
}

func TestRollback_ambiguousMissingIncompleteIncompatibleRefused(t *testing.T) {
	lab := newRollbackLab(t)
	selectWorkingRelease(t, lab, lab.oldSHA)
	previous := currentSHA(t, lab.root)

	t.Run("missing", func(t *testing.T) {
		_, err := Rollback(lab.store, lab.ctx, "abcdef0")
		if err == nil {
			t.Fatal("missing SHA succeeded")
		}
		if got := currentSHA(t, lab.root); got != previous {
			t.Fatalf("selection mutated to %s", got)
		}
	})

	t.Run("ambiguous", func(t *testing.T) {
		releases := filepath.Join(lab.root, ".local", "releases")
		a := filepath.Join(releases, "deadbeefaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1")
		b := filepath.Join(releases, "deadbeefaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa2")
		if err := os.MkdirAll(a, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(b, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := Rollback(lab.store, lab.ctx, "deadbeef")
		if err == nil {
			t.Fatal("ambiguous SHA succeeded")
		}
		if !strings.Contains(err.Error(), "2 staged releases match") {
			t.Fatalf("refusal = %v, want ambiguous match count", err)
		}
		if got := currentSHA(t, lab.root); got != previous {
			t.Fatalf("selection mutated to %s", got)
		}
	})

	t.Run("incomplete", func(t *testing.T) {
		sha := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		dir := filepath.Join(lab.root, ".local", "releases", sha)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := Rollback(lab.store, lab.ctx, sha)
		if err == nil {
			t.Fatal("incomplete release succeeded")
		}
		if got := currentSHA(t, lab.root); got != previous {
			t.Fatalf("selection mutated to %s", got)
		}
	})

	t.Run("incompatible", func(t *testing.T) {
		next := stageNextRelease(t, lab)
		plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", next), workingHelper(""))
		rewriteReleaseSupports(t, filepath.Join(lab.root, ".local", "releases", next), []int{99}, []int{99})
		_, err := Rollback(lab.store, lab.ctx, next)
		if err == nil {
			t.Fatal("incompatible release succeeded")
		}
		if got := currentSHA(t, lab.root); got != previous {
			t.Fatalf("selection mutated to %s", got)
		}
	})
}

func TestRollback_dirtyCheckoutRefused(t *testing.T) {
	for _, source := range []string{"tracked", "untracked"} {
		t.Run(source, func(t *testing.T) {
			lab := newRollbackLab(t)
			selectWorkingRelease(t, lab, lab.oldSHA)
			previous := currentSHA(t, lab.root)
			if source == "tracked" {
				writeFile(t, filepath.Join(lab.root, "AGENTS.md"), "dirty tracked edit\n")
			} else {
				writeFile(t, filepath.Join(lab.root, "untracked.txt"), "dirty untracked edit\n")
			}
			dirtyBefore := git(t, lab.root, "status", "--porcelain")

			_, err := Rollback(lab.store, lab.ctx, "checkout")
			if err == nil {
				t.Fatal("dirty checkout rollback succeeded")
			}
			if !strings.Contains(strings.ToLower(err.Error()), "clean") {
				t.Fatalf("refusal = %v, want a clean-checkout diagnostic", err)
			}
			if got := currentSHA(t, lab.root); got != previous {
				t.Fatalf("selection mutated to %s", got)
			}
			if git(t, lab.root, "status", "--porcelain") != dirtyBefore {
				t.Fatalf("dirty tree was mutated")
			}
		})
	}
}

func TestRollback_cleanUnapprovedCheckoutRefused(t *testing.T) {
	lab := newRollbackLab(t)
	selectWorkingRelease(t, lab, lab.oldSHA)
	previous := currentSHA(t, lab.root)
	writeFile(t, filepath.Join(lab.root, "local-only.md"), "local\n")
	git(t, lab.root, "add", "local-only.md")
	git(t, lab.root, "commit", "-m", "clean unapproved checkout")
	unapproved := git(t, lab.root, "rev-parse", "HEAD")

	_, err := Rollback(lab.store, lab.ctx, "checkout")
	if err == nil {
		t.Fatal("clean unapproved checkout rollback succeeded")
	}
	if got := currentSHA(t, lab.root); got != previous {
		t.Fatalf("selection mutated to %s", got)
	}
	if git(t, lab.root, "rev-parse", "HEAD") != unapproved {
		t.Fatal("refusal moved checkout HEAD")
	}
}

func TestRollback_cleanApprovedCheckoutUsesTargetEvidence(t *testing.T) {
	lab := newRollbackLab(t)
	selectWorkingRelease(t, lab, lab.oldSHA)
	if git(t, lab.root, "rev-parse", "HEAD") != lab.oldSHA {
		t.Fatalf("HEAD = %s, want approved %s", git(t, lab.root, "rev-parse", "HEAD"), lab.oldSHA)
	}

	view, err := Rollback(lab.store, lab.ctx, "checkout")
	if err != nil {
		t.Fatalf("approved checkout rollback: %v", err)
	}
	rt := DefaultRuntime(lab.root)
	if strField(rt, "kind") != "checkout" {
		t.Fatalf("kind = %q, want checkout\n%s", strField(rt, "kind"), dump(view))
	}
	if strField(rt, "sha") != lab.oldSHA {
		t.Fatalf("checkout sha = %s, want %s", strField(rt, "sha"), lab.oldSHA)
	}
	compat := asObject(func() any { v, _ := view.Get("compatibility"); return v }())
	if compat == nil {
		t.Fatalf("missing compatibility\n%s", dump(view))
	}
	candidate := asObject(func() any { v, _ := compat.Get("candidate"); return v }())
	if candidate == nil || strField(candidate, "sha") != lab.oldSHA {
		t.Fatalf("compatibility did not use checkout target evidence\n%s", dump(view))
	}
}

func TestCompatibility_absentManifestIsNotSavedByCompiledContract(t *testing.T) {
	lab := newApplyLab(t, applyLabOpts{})
	current := DefaultRuntime(lab.root)
	compat, err := Compatibility(lab.store, lab.root, lab.root, current)
	if err != nil {
		t.Fatalf("compatibility: %v", err)
	}
	if ok, _ := compat.Get("ok"); ok == true {
		t.Fatalf("checkout without a manifest was compatible via the executing helper\n%s", dump(compat))
	}
	blocking, _ := compat.Get("blocking")
	text := fmtBlocking(blocking)
	if text == "" {
		t.Fatalf("expected blocking evidence, got %s", dump(compat))
	}
}

func TestRollback_failedActivationRestoresWithoutFalseSuccess(t *testing.T) {
	lab := newRollbackLab(t)
	selectWorkingRelease(t, lab, lab.oldSHA)
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", lab.newSHA), workingHelper(""))
	if _, err := Apply(lab.store, lab.ctx, lab.newSHA, true); err != nil {
		t.Fatalf("apply B: %v", err)
	}
	previous := currentSHA(t, lab.root)
	taskBefore := readTaskBrief(t, lab)
	writeFile(t, filepath.Join(lab.root, "bin", "sumctl"), stableLauncher)
	if err := os.Chmod(filepath.Join(lab.root, "bin", "sumctl"), 0o755); err != nil {
		t.Fatal(err)
	}
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", lab.oldSHA), "#!/bin/sh\necho broken-rollback-target >&2\nexit 1\n")

	_, err := Rollback(lab.store, lab.ctx, "")
	if err == nil {
		t.Fatal("rollback of a failing target succeeded")
	}
	msg := err.Error()
	if strings.Contains(msg, "restored and verified") && !strings.Contains(strings.ToLower(msg), "failed") {
		t.Fatalf("claimed verified restoration without naming the failure: %v", err)
	}
	if got := currentSHA(t, lab.root); got != previous {
		t.Fatalf("selection = %s, want restored B %s", got, previous)
	}
	assertTaskBrief(t, lab, taskBefore)
}

func recordedKnownGood(t *testing.T, root string) string {
	t.Helper()
	state := activationState(t, root)
	known := asObject(func() any { v, _ := state.Get("known_good"); return v }())
	return strField(known, "sha")
}

func rewriteReleaseSupports(t *testing.T, dir string, stateSchema, briefSchema []int) {
	t.Helper()
	path := filepath.Join(dir, "release.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	state := make([]any, 0, len(stateSchema))
	for _, n := range stateSchema {
		state = append(state, n)
	}
	brief := make([]any, 0, len(briefSchema))
	for _, n := range briefSchema {
		brief = append(brief, n)
	}
	doc["supports"] = map[string]any{"state_schema": state, "brief_schema": brief}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}

func fmtBlocking(v any) string {
	list, _ := v.([]any)
	parts := make([]string, 0, len(list))
	for _, item := range list {
		parts = append(parts, fmt.Sprint(item))
	}
	return strings.Join(parts, "; ")
}

func newRollbackLab(t *testing.T) *applyLab {
	t.Helper()
	lab := newApplyLab(t, applyLabOpts{})
	buildCompatibleRelease(t, filepath.Join(lab.root, ".local", "releases"), lab.oldSHA)
	plantNativeHelper(t, lab.root, workingHelper(filepath.Join(lab.home, "checkout-helper.log")))
	plantTask(t, lab.home)
	t.Cleanup(func() {
		afterPendingWrite = nil
		afterSelect = nil
	})
	return lab
}
