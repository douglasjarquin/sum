package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/machine"
)

// The Rebase row is an observation at one instant. These pin that push and PR fetch the base again right before they
// act, because another coordinator can merge into it in between (#224 was opened already conflicting with main).

func (lab gateLab) coordinatorRecords(t *testing.T, kind string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(lab.home, "tasks", gateTaskID, "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	var task struct {
		Evidence []map[string]any `json:"evidence"`
	}
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	for _, record := range task.Evidence {
		if record["kind"] == kind && record["source"] == "coordinator" {
			rows = append(rows, record)
		}
	}
	return rows
}

// markLocal records this machine on the task, which is what puts it in the status view's maintenance section.
func (lab gateLab) markLocal(t *testing.T) {
	t.Helper()
	host, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lab.home, "tasks", gateTaskID, "task.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	var task map[string]any
	if err := decoder.Decode(&task); err != nil {
		t.Fatal(err)
	}
	task["machine"] = host
	out, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (lab gateLab) originMain(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(gitIn(t, lab.source, "rev-parse", "HEAD"))
}

func TestPipelinePush_refusesWhenTheBaseMovedAfterTheRebaseObservation(t *testing.T) {
	lab := newGateLab(t)
	lab.writeTask(t, "reported", publicationRecords(lab.candidate)...)
	runGate(t, lab, "rebase", gateTaskID)
	if row := lab.row(t, "rebase"); row["status"] != "pass" {
		t.Fatalf("rebase row = %v, want the first observation to pass", row)
	}
	observed := lab.originMain(t)
	lab.advanceOrigin(t, "other.txt", "merged by another coordinator\n")
	moved := lab.originMain(t)

	_, _, err := runPRCLI(t, lab.home, "pipeline", "push", gateTaskID)

	want := fmt.Sprintf("origin/main moved to %s since the last Rebase observation (%s,", moved[:7], observed[:7])
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v\nwant a refusal saying %q", err, want)
	}
	for _, part := range []string{"1 commit(s) behind", "never force-pushed", "--allow-behind"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("error = %v, want it to say %q", err, part)
		}
	}
	if got := lab.remoteSHA(t, gateBranch); got != "" {
		t.Fatalf("origin/%s = %s, want nothing pushed onto a base that moved", gateBranch, got)
	}
	row := lab.row(t, "rebase")
	result, _ := row["result"].(string)
	if row["status"] != "blocked" || !strings.Contains(result, "main at "+moved[:7]+", observed ") {
		t.Fatalf("rebase row = %v, want it re-derived as blocked against %s", row, moved[:7])
	}
	records := lab.coordinatorRecords(t, "rebase")
	if last := records[len(records)-1]; last["trigger"] != "push" || last["base_sha"] != moved {
		t.Fatalf("latest rebase record = %v, want the push's own observation of %s", last, moved)
	}

	runGate(t, lab, "push", gateTaskID, "--allow-behind")

	if got := lab.remoteSHA(t, gateBranch); got != lab.candidate {
		t.Fatalf("origin/%s = %s, want --allow-behind to push a clean but behind candidate", gateBranch, got)
	}
	assertNoLeftovers(t, lab)
}

func TestPipelinePush_aConflictFoundJustBeforePushingRefusesEvenWithAllowBehind(t *testing.T) {
	lab := newGateLab(t)
	lab.writeTask(t, "reported", publicationRecords(lab.candidate)...)
	runGate(t, lab, "rebase", gateTaskID)
	lab.advanceOrigin(t, "app.txt", "someone else's line\n")

	_, _, err := runPRCLI(t, lab.home, "pipeline", "push", gateTaskID, "--allow-behind")

	if err == nil || !strings.Contains(err.Error(), "conflicts with it in: app.txt") || !strings.Contains(err.Error(), "does not waive a conflict") {
		t.Fatalf("error = %v, want a conflict refusal that --allow-behind cannot waive", err)
	}
	if got := lab.remoteSHA(t, gateBranch); got != "" {
		t.Fatalf("origin/%s = %s, want nothing pushed", gateBranch, got)
	}
	if row := lab.row(t, "rebase"); row["status"] != "fail" {
		t.Fatalf("rebase row = %v, want fail", row)
	}
	assertNoLeftovers(t, lab)
}

func TestPipelinePush_anUpToDateBaseCostsOneFreshObservationAndPushes(t *testing.T) {
	lab := newGateLab(t)
	lab.writeTask(t, "reported", publicationRecords(lab.candidate)...)
	runGate(t, lab, "rebase", gateTaskID)

	runGate(t, lab, "push", gateTaskID)

	if got := lab.remoteSHA(t, gateBranch); got != lab.candidate {
		t.Fatalf("origin/%s = %s, want the candidate pushed", gateBranch, got)
	}
	records := lab.coordinatorRecords(t, "rebase")
	if len(records) != 2 {
		t.Fatalf("rebase records = %v, want the explicit observation and exactly one from the push", records)
	}
	fresh := records[1]
	if fresh["trigger"] != "push" || fresh["outcome"] != "up-to-date" || fmt.Sprint(fresh["behind"]) != "0" {
		t.Fatalf("push's rebase record = %v, want one up-to-date observation", fresh)
	}
	if row := lab.row(t, "rebase"); row["status"] != "pass" {
		t.Fatalf("rebase row = %v, want pass", row)
	}
	assertNoLeftovers(t, lab)
}

func TestPipelinePush_anUnreachableBaseFailsSafeAsUncertain(t *testing.T) {
	lab := newGateLab(t)
	lab.writeTask(t, "reported", publicationRecords(lab.candidate)...)
	runGate(t, lab, "rebase", gateTaskID)
	gitIn(t, lab.clone, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))

	for _, args := range [][]string{{"pipeline", "push", gateTaskID}, {"pipeline", "push", gateTaskID, "--allow-behind"}} {
		_, _, err := runPRCLI(t, lab.home, args...)
		if err == nil || !strings.Contains(err.Error(), "could not be observed just now") || !strings.Contains(err.Error(), "uncertain, never a pass") {
			t.Fatalf("%v: error = %v, want an uncertain-base refusal", args, err)
		}
	}
	if pushes := lab.coordinatorRecords(t, "push"); len(pushes) != 0 {
		t.Fatalf("push records = %v, want no push attempted", pushes)
	}
	if row := lab.row(t, "rebase"); row["status"] != "blocked" {
		t.Fatalf("rebase row = %v, want blocked rather than the earlier pass", row)
	}
}

func TestPipelinePR_refusesWhenTheBaseMovedAfterThePush(t *testing.T) {
	lab := prLab(t)
	runGate(t, lab, "run", gateTaskID, "--no-pr")
	lab.advanceOrigin(t, "other.txt", "merged by another coordinator\n")
	moved := lab.originMain(t)

	_, _, err := runPRCLI(t, lab.home, "pipeline", "pr", gateTaskID)

	want := fmt.Sprintf("refusing to open the PR: origin/main moved to %s since the last Rebase observation", moved[:7])
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v\nwant %q", err, want)
	}
	if got := lab.creates(t); got != 0 {
		t.Fatalf("gh pr create ran %d times against a moved base, want none", got)
	}

	runGate(t, lab, "pr", gateTaskID, "--allow-behind")

	if got := lab.creates(t); got != 1 {
		t.Fatalf("gh pr create ran %d times with --allow-behind, want one", got)
	}
}

func TestPipelinePR_aConflictingPRBlocksTheRowAndTheRundownNamesTheMerge(t *testing.T) {
	lab := prLab(t)
	lab.writeGitHub(t, map[string]any{"mergeable": "CONFLICTING", "merge_state_status": "DIRTY"})

	runGate(t, lab, "run", gateTaskID)

	row := lab.row(t, "pr")
	result, _ := row["result"].(string)
	advice, _ := row["advice"].(string)
	if row["status"] != "blocked" || !strings.Contains(result, "Conflicts with main per GitHub (mergeable CONFLICTING, mergeStateStatus DIRTY") {
		t.Fatalf("pr row = %v, want it blocked on GitHub's mergeability", row)
	}
	if !strings.Contains(advice, "merges the base branch into the task branch") || !strings.Contains(advice, "never rebased or force-pushed") {
		t.Fatalf("pr advice = %q, want the merge remedy", advice)
	}
	lab.markLocal(t)
	stdout, stderr, err := runPRCLI(t, lab.home, "status")
	if err != nil {
		t.Fatalf("status: %v stderr=%s", err, stderr)
	}
	maintenance, _ := decodeObject(t, stdout)["maintenance"].(map[string]any)
	prs, _ := maintenance["open_prs"].([]any)
	if len(prs) != 1 {
		t.Fatalf("open_prs = %v, want the one PR", maintenance["open_prs"])
	}
	open, _ := prs[0].(map[string]any)
	if blocked, _ := open["blocked"].(string); !strings.Contains(blocked, "mergeable CONFLICTING") || open["fix"] == nil {
		t.Fatalf("open PR row = %v, want the conflict and its fix", open)
	}

	lab.writeGitHub(t, map[string]any{"pr": lab.github(t)["pr"], "creates": 1, "mergeable": "MERGEABLE", "merge_state_status": "CLEAN"})
	observed := runGate(t, lab, "ci", gateTaskID, "--no-publish")

	mergeability, _ := observed["mergeability"].(map[string]any)
	if mergeability["mergeable"] != "MERGEABLE" || mergeability["conflict"] != nil {
		t.Fatalf("pipeline ci mergeability = %v, want the new MERGEABLE answer", observed["mergeability"])
	}
	if row := lab.row(t, "pr"); row["status"] != "pass" {
		t.Fatalf("pr row after the base was merged in = %v, want pass", row)
	}
}
