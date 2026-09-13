package updatecmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

var errTestInterrupt = errors.New("test interrupt")

const stableLauncher = `#!/usr/bin/env bash
set -euo pipefail
ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
RUNTIME="$ROOT"
if [[ -L "$ROOT/.local/current" ]]; then
  RUNTIME=$(cd -- "$ROOT/.local/current" 2>/dev/null && pwd -P) || { echo "sumctl: $ROOT/.local/current points to a missing runtime" >&2; exit 1; }
fi
BINARY="$RUNTIME/.local/bin/sumctl"
if [[ ! -x "$BINARY" ]]; then
  BINARY="$RUNTIME/.local/bin/sumctl-go"
fi
if [[ ! -x "$BINARY" ]]; then
  echo "sumctl: staged native binary is missing; run mise run setup or stage a release." >&2
  exit 1
fi
export SUM_INSTALL_ROOT="$ROOT"
exec "$BINARY" "$@"
`

func TestApply_interruptBeforeSelection_recordsPendingWithoutSelecting(t *testing.T) {
	lab := newActivationLab(t)
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", lab.newSHA), workingHelper(""))
	afterPendingWrite = func() error { return errTestInterrupt }

	_, err := Apply(lab.store, lab.ctx, lab.newSHA, true)
	if !errors.Is(err, errTestInterrupt) {
		t.Fatalf("apply interrupt: %v", err)
	}
	if hasCurrent(t, lab.root) {
		t.Fatalf("selection changed before the interrupt: %s", currentSHA(t, lab.root))
	}
	generation := pendingGeneration(t, lab.root)
	if generation == "" {
		t.Fatal("pending activation was not recorded")
	}
	taskBefore := readTaskBrief(t, lab)

	view, recErr := independentRecover(t, lab, generation)
	if recErr != nil {
		t.Fatalf("recover: %v", recErr)
	}
	if changed, _ := view.Get("changed"); changed == true {
		t.Fatalf("recover mutated selection\n%s", dump(view))
	}
	if hasCurrent(t, lab.root) {
		t.Fatalf("recover selected a release: %s", currentSHA(t, lab.root))
	}
	if pendingGeneration(t, lab.root) != "" {
		t.Fatal("recover left pending activation")
	}
	if _, again := independentRecover(t, lab, generation); again == nil {
		t.Fatal("repeat recover succeeded and might mutate twice")
	}
	if hasCurrent(t, lab.root) {
		t.Fatal("repeat recover changed selection")
	}
	assertTaskBrief(t, lab, taskBefore)
}

func TestApply_interruptAfterSelection_independentRecoverRestoresOnce(t *testing.T) {
	lab := newActivationLab(t)
	selectWorkingRelease(t, lab, lab.newSHA)
	previous := currentSHA(t, lab.root)
	next := stageNextRelease(t, lab)
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", next), workingHelper(""))
	afterSelect = func() error { return errTestInterrupt }

	_, err := Apply(lab.store, lab.ctx, next, true)
	if !errors.Is(err, errTestInterrupt) {
		t.Fatalf("apply interrupt: %v", err)
	}
	if got := currentSHA(t, lab.root); got != next {
		t.Fatalf("interrupted selection = %s, want candidate %s", got, next)
	}
	generation := pendingGeneration(t, lab.root)
	if generation == "" {
		t.Fatal("pending activation was not recorded")
	}
	taskBefore := readTaskBrief(t, lab)

	view, recErr := independentRecover(t, lab, generation)
	if recErr != nil {
		t.Fatalf("recover: %v", recErr)
	}
	if got := currentSHA(t, lab.root); got != previous {
		t.Fatalf("recovered SHA = %s, want %s\n%s", got, previous, dump(view))
	}
	if pendingGeneration(t, lab.root) != "" {
		t.Fatal("recover left pending activation")
	}
	if _, again := independentRecover(t, lab, generation); again == nil {
		if got := currentSHA(t, lab.root); got != previous {
			t.Fatalf("repeat recover mutated selection to %s", got)
		}
	}
	if got := currentSHA(t, lab.root); got != previous {
		t.Fatalf("repeat recover left SHA %s, want %s", got, previous)
	}
	assertTaskBrief(t, lab, taskBefore)
}

func TestApply_candidatePostCheckFailure_restoresAndChecksPrevious(t *testing.T) {
	lab := newActivationLab(t)
	priorLog := filepath.Join(lab.home, "prior-helper.log")
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", lab.newSHA), workingHelper(priorLog))
	if _, err := Apply(lab.store, lab.ctx, lab.newSHA, true); err != nil {
		t.Fatalf("setup apply: %v", err)
	}
	previous := currentSHA(t, lab.root)
	beforeLog, err := os.ReadFile(priorLog)
	if err != nil {
		t.Fatal(err)
	}
	next := stageNextRelease(t, lab)

	_, applyErr := Apply(lab.store, lab.ctx, next, true)
	if applyErr == nil {
		t.Fatal("apply of a candidate without a native helper succeeded")
	}
	msg := applyErr.Error()
	if !strings.Contains(msg, "candidate entrypoint check failed") {
		t.Fatalf("candidate failure not reported: %v", applyErr)
	}
	if !strings.Contains(msg, "restored and verified") {
		t.Fatalf("verified restoration not reported: %v", applyErr)
	}
	if got := currentSHA(t, lab.root); got != previous {
		t.Fatalf("selection = %s, want restored %s", got, previous)
	}
	afterLog, err := os.ReadFile(priorLog)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterLog) == string(beforeLog) {
		t.Fatal("restored entrypoint was not checked")
	}
	if pendingGeneration(t, lab.root) != "" {
		t.Fatal("verified restoration left pending activation")
	}
	assertTaskBrief(t, lab, "keep me")
}

func TestApply_failedCompensation_isExplicitFailure(t *testing.T) {
	lab := newActivationLab(t)
	selectWorkingRelease(t, lab, lab.newSHA)
	previous := currentSHA(t, lab.root)
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", previous), "#!/bin/sh\necho broken-prior >&2\nexit 1\n")
	next := stageNextRelease(t, lab)

	_, applyErr := Apply(lab.store, lab.ctx, next, true)
	if applyErr == nil {
		t.Fatal("apply succeeded after both candidate and prior helpers failed")
	}
	msg := applyErr.Error()
	if strings.Contains(msg, "was restored") && !strings.Contains(strings.ToLower(msg), "failed") {
		t.Fatalf("claimed restoration without a failure: %v", applyErr)
	}
	if strings.Contains(msg, "restored and verified") {
		t.Fatalf("claimed verified restoration: %v", applyErr)
	}
	if !strings.Contains(msg, "Recovery also failed") && !strings.Contains(msg, "entrypoint check failed") {
		t.Fatalf("restoration failure not named: %v", applyErr)
	}
	if pendingGeneration(t, lab.root) == "" {
		t.Fatal("failed compensation cleared pending evidence")
	}
	assertTaskBrief(t, lab, "keep me")
}

func TestRecover_nonStartingCandidateEntrypoint(t *testing.T) {
	lab := newActivationLab(t)
	selectWorkingRelease(t, lab, lab.newSHA)
	previous := currentSHA(t, lab.root)
	next := stageNextRelease(t, lab)
	afterSelect = func() error { return errTestInterrupt }

	_, err := Apply(lab.store, lab.ctx, next, true)
	if !errors.Is(err, errTestInterrupt) {
		t.Fatalf("apply interrupt: %v", err)
	}
	if got := currentSHA(t, lab.root); got != next {
		t.Fatalf("selection = %s, want broken candidate %s", got, next)
	}
	generation := pendingGeneration(t, lab.root)
	if generation == "" {
		t.Fatal("pending activation was not recorded")
	}

	view, recErr := independentRecover(t, lab, generation)
	if recErr != nil {
		t.Fatalf("recover via known-good runtime: %v", recErr)
	}
	if got := currentSHA(t, lab.root); got != previous {
		t.Fatalf("recovered SHA = %s, want %s\n%s", got, previous, dump(view))
	}
	assertTaskBrief(t, lab, "keep me")
}

func TestRecover_wrongCoordinatorPreservesEvidence(t *testing.T) {
	lab := newActivationLab(t)
	selectWorkingRelease(t, lab, lab.newSHA)
	next := stageNextRelease(t, lab)
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", next), workingHelper(""))
	afterSelect = func() error { return errTestInterrupt }
	if _, err := Apply(lab.store, lab.ctx, next, true); !errors.Is(err, errTestInterrupt) {
		t.Fatalf("apply interrupt: %v", err)
	}
	generation := pendingGeneration(t, lab.root)
	journal := readActivationBytes(t, lab.root)
	selected := currentSHA(t, lab.root)

	other := ordjson.NewObject()
	other.Set("session", "sum-test")
	other.Set("pane", "w-other:p9")
	other.Set("machine", strField(lab.ctx, "machine"))
	other.Set("cwd", lab.root)
	_, err := Recover(lab.store, other, generation)
	if err == nil {
		t.Fatal("recover from a non-coordinator pane succeeded")
	}
	if !strings.Contains(err.Error(), "coordinator") {
		t.Fatalf("wrong-coordinator error: %v", err)
	}
	assertJournalUnchanged(t, lab.root, journal)
	if got := currentSHA(t, lab.root); got != selected {
		t.Fatalf("selection mutated to %s", got)
	}
	assertTaskBrief(t, lab, "keep me")
}

func TestRecover_staleGenerationPreservesEvidence(t *testing.T) {
	lab := newActivationLab(t)
	selectWorkingRelease(t, lab, lab.newSHA)
	next := stageNextRelease(t, lab)
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", next), workingHelper(""))
	afterSelect = func() error { return errTestInterrupt }
	if _, err := Apply(lab.store, lab.ctx, next, true); !errors.Is(err, errTestInterrupt) {
		t.Fatalf("apply interrupt: %v", err)
	}
	journal := readActivationBytes(t, lab.root)
	selected := currentSHA(t, lab.root)

	_, err := independentRecover(t, lab, "ffffffffffffffffffffffffffffffff")
	if err == nil {
		t.Fatal("stale generation recovered")
	}
	if !strings.Contains(err.Error(), "stale") && !strings.Contains(err.Error(), "ffffffffffffffffffffffffffffffff") {
		t.Fatalf("stale generation error: %v", err)
	}
	assertJournalUnchanged(t, lab.root, journal)
	if got := currentSHA(t, lab.root); got != selected {
		t.Fatalf("selection mutated to %s", got)
	}
	assertTaskBrief(t, lab, "keep me")
}

func TestRecover_conflictingSelectionPreservesEvidence(t *testing.T) {
	lab := newActivationLab(t)
	selectWorkingRelease(t, lab, lab.newSHA)
	next := stageNextRelease(t, lab)
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", next), workingHelper(""))
	afterSelect = func() error { return errTestInterrupt }
	if _, err := Apply(lab.store, lab.ctx, next, true); !errors.Is(err, errTestInterrupt) {
		t.Fatalf("apply interrupt: %v", err)
	}
	generation := pendingGeneration(t, lab.root)
	if _, err := SelectDefault(lab.root, ""); err != nil {
		t.Fatal(err)
	}
	journal := readActivationBytes(t, lab.root)

	_, err := independentRecover(t, lab, generation)
	if err == nil {
		t.Fatal("conflicting selection recovered")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "neither") && !strings.Contains(strings.ToLower(err.Error()), "match") {
		t.Fatalf("conflicting selection error: %v", err)
	}
	assertJournalUnchanged(t, lab.root, journal)
	if hasCurrent(t, lab.root) {
		t.Fatal("recover mutated a conflicting selection")
	}
	assertTaskBrief(t, lab, "keep me")
}

func TestRecover_missingEvidencePreservesHistory(t *testing.T) {
	lab := newActivationLab(t)
	selectWorkingRelease(t, lab, lab.newSHA)
	next := stageNextRelease(t, lab)
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", next), workingHelper(""))
	afterSelect = func() error { return errTestInterrupt }
	if _, err := Apply(lab.store, lab.ctx, next, true); !errors.Is(err, errTestInterrupt) {
		t.Fatalf("apply interrupt: %v", err)
	}
	generation := pendingGeneration(t, lab.root)
	state := activationState(t, lab.root)
	pending := asObject(func() any { v, _ := state.Get("pending"); return v }())
	if pending == nil {
		t.Fatal("pending activation was not recorded")
	}
	recovery := asObject(func() any { v, _ := pending.Get("recovery"); return v }())
	if recovery == nil {
		t.Fatal("recovery evidence was not recorded")
	}
	recovery.Set("sha256", strings.Repeat("0", 64))
	pending.Set("recovery", recovery)
	state.Set("pending", pending)
	if err := ordjson.WriteFile(filepath.Join(lab.root, ".local", "activation.json"), state); err != nil {
		t.Fatal(err)
	}
	journal := readActivationBytes(t, lab.root)
	selected := currentSHA(t, lab.root)

	_, err := independentRecover(t, lab, generation)
	if err == nil {
		t.Fatal("recover succeeded without matching evidence")
	}
	assertJournalUnchanged(t, lab.root, journal)
	if got := currentSHA(t, lab.root); got != selected {
		t.Fatalf("selection mutated to %s", got)
	}
	assertTaskBrief(t, lab, "keep me")
}

func TestApply_refusesWhileActivationPending(t *testing.T) {
	lab := newActivationLab(t)
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", lab.newSHA), workingHelper(""))
	afterPendingWrite = func() error { return errTestInterrupt }
	if _, err := Apply(lab.store, lab.ctx, lab.newSHA, true); !errors.Is(err, errTestInterrupt) {
		t.Fatalf("apply interrupt: %v", err)
	}
	generation := pendingGeneration(t, lab.root)
	if generation == "" {
		t.Fatal("pending activation was not recorded")
	}

	_, err := Apply(lab.store, lab.ctx, lab.newSHA, true)
	if err == nil {
		t.Fatal("apply succeeded while activation is pending")
	}
	if !strings.Contains(err.Error(), generation) {
		t.Fatalf("pending refusal omitted generation: %v", err)
	}
	if hasCurrent(t, lab.root) {
		t.Fatal("refused apply changed selection")
	}
}

func TestRecover_wrongInstancePreservesEvidence(t *testing.T) {
	lab := newActivationLab(t)
	selectWorkingRelease(t, lab, lab.newSHA)
	next := stageNextRelease(t, lab)
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", next), workingHelper(""))
	afterSelect = func() error { return errTestInterrupt }
	if _, err := Apply(lab.store, lab.ctx, next, true); !errors.Is(err, errTestInterrupt) {
		t.Fatalf("apply interrupt: %v", err)
	}
	generation := pendingGeneration(t, lab.root)
	state := activationState(t, lab.root)
	state.Set("instance", "other-instance")
	if err := ordjson.WriteFile(filepath.Join(lab.root, ".local", "activation.json"), state); err != nil {
		t.Fatal(err)
	}
	journal := readActivationBytes(t, lab.root)
	selected := currentSHA(t, lab.root)

	_, err := independentRecover(t, lab, generation)
	if err == nil {
		t.Fatal("recover succeeded against another instance's journal")
	}
	assertJournalUnchanged(t, lab.root, journal)
	if got := currentSHA(t, lab.root); got != selected {
		t.Fatalf("selection mutated to %s", got)
	}
	assertTaskBrief(t, lab, "keep me")
}

func newActivationLab(t *testing.T) *applyLab {
	t.Helper()
	lab := newApplyLab(t, applyLabOpts{})
	writeFile(t, filepath.Join(lab.root, "bin", "sumctl"), stableLauncher)
	if err := os.Chmod(filepath.Join(lab.root, "bin", "sumctl"), 0o755); err != nil {
		t.Fatal(err)
	}
	plantNativeHelper(t, lab.root, workingHelper(filepath.Join(lab.home, "checkout-helper.log")))
	plantTask(t, lab.home)
	t.Cleanup(func() {
		afterPendingWrite = nil
		afterSelect = nil
	})
	return lab
}

func selectWorkingRelease(t *testing.T, lab *applyLab, sha string) {
	t.Helper()
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", sha), workingHelper(""))
	if _, err := Apply(lab.store, lab.ctx, sha, true); err != nil {
		t.Fatalf("setup apply %s: %v", sha, err)
	}
}

func stageNextRelease(t *testing.T, lab *applyLab) string {
	t.Helper()
	origin := git(t, lab.root, "remote", "get-url", "origin")
	work := filepath.Join(t.TempDir(), "src")
	git(t, t.TempDir(), "clone", "--quiet", origin, work)
	git(t, work, "config", "user.name", "sum test")
	git(t, work, "config", "user.email", "sum@example.invalid")
	git(t, work, "config", "commit.gpgsign", "false")
	name := fmt.Sprintf("extra-%s.md", filepath.Base(t.Name()))
	writeFile(t, filepath.Join(work, name), name+"\n")
	git(t, work, "add", name)
	git(t, work, "commit", "-m", name)
	sha := git(t, work, "rev-parse", "HEAD")
	git(t, work, "push", "--quiet", "origin", "HEAD:main")
	git(t, lab.root, "fetch", "--quiet", "origin")
	buildCompatibleRelease(t, filepath.Join(lab.root, ".local", "releases"), sha)
	return sha
}

func plantNativeHelper(t *testing.T, runtimeRoot, body string) {
	t.Helper()
	path := filepath.Join(runtimeRoot, ".local", "bin", "sumctl")
	writeFile(t, path, body)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func workingHelper(logPath string) string {
	if logPath == "" {
		return "#!/bin/sh\nexit 0\n"
	}
	return fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %s\nexit 0\n", shellQuote(logPath))
}

func shellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'"'"'`) + "'"
}

func plantTask(t *testing.T, home string) {
	t.Helper()
	writeFile(t, filepath.Join(home, "tasks", "t-aaaaaaaaaaaa", "task.json"), `{
  "schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repo",
  "questions": [{"key": "keep", "text": "still open?"}], "evidence": [], "report": null,
  "notice": null, "attention": [], "brief": "keep me",
  "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "task"
}
`)
}

func independentRecover(t *testing.T, lab *applyLab, generation string) (*ordjson.Object, error) {
	t.Helper()
	st, err := store.Open(lab.home)
	if err != nil {
		t.Fatal(err)
	}
	return Recover(st, lab.ctx, generation)
}

func hasCurrent(t *testing.T, root string) bool {
	t.Helper()
	_, err := os.Lstat(filepath.Join(root, ".local", "current"))
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	t.Fatal(err)
	return false
}

func activationState(t *testing.T, root string) *ordjson.Object {
	t.Helper()
	value, err := ordjson.ReadFile(filepath.Join(root, ".local", "activation.json"))
	if err != nil {
		t.Fatalf("activation.json: %v", err)
	}
	obj, ok := value.(*ordjson.Object)
	if !ok {
		t.Fatal("activation.json is not an object")
	}
	return obj
}

func pendingGeneration(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, ".local", "activation.json")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	pending := asObject(func() any { v, _ := activationState(t, root).Get("pending"); return v }())
	if pending == nil {
		return ""
	}
	return strField(pending, "generation")
}

func readActivationBytes(t *testing.T, root string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".local", "activation.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertJournalUnchanged(t *testing.T, root string, want []byte) {
	t.Helper()
	got := readActivationBytes(t, root)
	if string(got) != string(want) {
		t.Fatalf("activation journal changed\nbefore=%s\nafter=%s", want, got)
	}
}

func readTaskBrief(t *testing.T, lab *applyLab) string {
	t.Helper()
	task, err := lab.store.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	return strField(task, "brief")
}

func assertTaskBrief(t *testing.T, lab *applyLab, want string) {
	t.Helper()
	got := readTaskBrief(t, lab)
	if got != want {
		t.Fatalf("task brief = %q, want %q", got, want)
	}
	questions, err := lab.store.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := questions.Get("questions")
	list, _ := raw.([]any)
	if len(list) != 1 {
		t.Fatalf("task questions mutated: %s", dump(questions))
	}
}


