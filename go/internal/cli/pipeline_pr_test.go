package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prLab is the gate lab with a fake GitHub behind it and a task that has already been verified and linted, which is
// what a coordinator has in hand when the only thing left is the PR.
func prLab(t *testing.T) gateLab {
	t.Helper()
	requirePython(t)
	lab := newGateLab(t)
	lab.ghRoot = filepath.Join(lab.home, "fake-gh")
	t.Setenv("SUM_GH_BIN", filepath.Join(repoRoot(t), "tests", "fixtures", "gh_attach.py"))
	t.Setenv("FAKE_GH_ROOT", lab.ghRoot)
	lab.writeGitHub(t, nil)
	comparison := writeEvidenceRun(t, filepath.Join(lab.clone, "evidence"), lab.baseSHA(t), lab.candidate)
	lab.writeTask(t, "reported", append([]string{
		fmt.Sprintf(`{"schema": 1, "id": "e-hand", "kind": "handoff", "source": "worker",
"at": "2026-01-01T00:00:00+00:00", "candidate": %q, "handoff": {"artifacts": [%q],
"changes": "Taught the delivery pipeline to open the PR.", "limitations": "Review is still a separate pane."}}`,
			lab.candidate, comparison),
	}, publicationRecords(lab.candidate)...)...)
	return lab
}

func (lab gateLab) baseSHA(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(gitIn(t, lab.clone, "rev-parse", "HEAD~1"))
}

func (lab gateLab) writeGitHub(t *testing.T, overrides map[string]any) {
	t.Helper()
	if err := os.MkdirAll(lab.ghRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	state := map[string]any{
		"version": "2.100.0", "repository": "douglasjarquin/project", "visibility": "PUBLIC",
		"viewer_permission": "WRITE", "head_sha": lab.candidate, "next_number": 7,
	}
	for key, value := range overrides {
		state[key] = value
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lab.ghRoot, "github.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (lab gateLab) github(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(lab.ghRoot, "github.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func (lab gateLab) createdBody(t *testing.T) string {
	t.Helper()
	state := lab.github(t)
	pr, _ := state["pr"].(map[string]any)
	body, _ := pr["body"].(string)
	return body
}

// creates counts what the fake GitHub actually created, which is the only honest answer to "did it create twice".
func (lab gateLab) creates(t *testing.T) int {
	t.Helper()
	count, _ := lab.github(t)["creates"].(float64)
	return int(count)
}

func (lab gateLab) prStep(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	steps, _ := result["steps"].([]any)
	for _, raw := range steps {
		step, _ := raw.(map[string]any)
		if step["stage"] == "pr" {
			return step
		}
	}
	t.Fatalf("run reported no pr step: %v", result["steps"])
	return nil
}

// prRecords are the PR stage's own evidence records, newest last.
func (lab gateLab) prRecords(t *testing.T) []map[string]any {
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
		if record["kind"] == "pr" && record["source"] == "coordinator" {
			rows = append(rows, record)
		}
	}
	return rows
}

func listedPR(number int, state, head, branch string) map[string]any {
	return map[string]any{"number": number, "state": state, "head_sha": head, "head_branch": branch, "base_branch": "main"}
}

func TestPipelineRun_opensThePRReconcilesItAndPublishesBothBlocks(t *testing.T) {
	lab := prLab(t)

	result := runGate(t, lab, "run", gateTaskID)

	if step := lab.prStep(t, result); step["outcome"] != "ran" {
		t.Fatalf("pr step = %v, want it to have run", step)
	}
	pr, _ := result["pr"].(map[string]any)
	wantURL := "https://github.com/douglasjarquin/project/pull/7"
	if pr == nil || pr["url"] != wantURL || pr["state"] != "open" {
		t.Fatalf("run reported pr = %v, want the open %s", pr, wantURL)
	}
	if got := lab.creates(t); got != 1 {
		t.Fatalf("gh pr create ran %d times, want exactly one", got)
	}
	row := lab.row(t, "pr")
	if row["status"] != "pass" || row["result"] != "Open: "+wantURL {
		t.Fatalf("pr row = %v, want a pass naming the URL", row)
	}
	body := lab.createdBody(t)
	for _, marker := range []string{"<!-- sum-pipeline:start -->", "| PR | ✅ | Open: " + wantURL, "<!-- before-and-after:start -->"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("the PR body is missing %q:\n%s", marker, body)
		}
	}
	if result["note"] != "Review is the reviewer pane's; only the user merges." {
		t.Fatalf("note = %v", result["note"])
	}
}

func TestPipelineRun_generatedBodyCarriesTheIntentTheChangesAndTheGates(t *testing.T) {
	lab := prLab(t)

	runGate(t, lab, "run", gateTaskID)

	saved, err := os.ReadFile(filepath.Join(lab.home, "tasks", gateTaskID, "pipeline", "pr", "body.md"))
	if err != nil {
		t.Fatalf("generated body: %v", err)
	}
	want := `## Summary

do the thing

## Changes

Taught the delivery pipeline to open the PR.

## Verification

- Test: Passed (` + "`mise run verify`" + `, run 20260906T010203Z-abcd)
- Lint: This project declares no lint task
- Document: `
	if !strings.HasPrefix(string(saved), want) {
		t.Fatalf("generated body =\n%s\nwant it to start with\n%s", saved, want)
	}
	if !strings.Contains(string(saved), "## Limitations\n\nReview is still a separate pane.\n") {
		t.Fatalf("generated body carries no limitations:\n%s", saved)
	}
}

func TestPipelineRun_asecondRunCreatesNothingAndReportsTheExistingPR(t *testing.T) {
	lab := prLab(t)
	runGate(t, lab, "run", gateTaskID)

	second := runGate(t, lab, "run", gateTaskID)

	if got := lab.creates(t); got != 1 {
		t.Fatalf("gh pr create ran %d times across two runs, want one", got)
	}
	if step := lab.prStep(t, second); step["outcome"] != "ran" {
		t.Fatalf("second pr step = %v", step)
	}
	records := lab.prRecords(t)
	if len(records) != 2 || records[1]["action"] != "existing" {
		t.Fatalf("pr records = %v, want the second to be existing", records)
	}
	if row := lab.row(t, "pr"); row["status"] != "pass" {
		t.Fatalf("pr row after a second run = %v, want it still passing", row)
	}
}

func TestPipelinePR_adoptsAnOpenPRAlreadyOnTheBranchInsteadOfDuplicatingIt(t *testing.T) {
	lab := prLab(t)
	lab.writeGitHub(t, map[string]any{"prs": []any{listedPR(12, "OPEN", lab.candidate, gateBranch)}})
	runGate(t, lab, "run", gateTaskID, "--no-pr")

	result := runGate(t, lab, "pr", gateTaskID)

	if got := lab.creates(t); got != 0 {
		t.Fatalf("gh pr create ran %d times, want none against an existing PR", got)
	}
	pr, _ := result["pr"].(map[string]any)
	if pr["action"] != "existing" || fmt.Sprint(pr["number"]) != "12" {
		t.Fatalf("pr = %v, want the existing #12 adopted", pr)
	}
	row := lab.row(t, "pr")
	if row["status"] != "pass" || row["result"] != "Open: https://github.com/douglasjarquin/project/pull/12" {
		t.Fatalf("pr row = %v, want the adopted PR", row)
	}
}

func TestPipelinePR_refusesAfterOnlyAClosedPRUntilTheCoordinatorAllowsIt(t *testing.T) {
	lab := prLab(t)
	lab.writeGitHub(t, map[string]any{"prs": []any{listedPR(4, "CLOSED", "0000000000000000000000000000000000000000", gateBranch)}})
	runGate(t, lab, "run", gateTaskID, "--no-pr")

	refused := runGate(t, lab, "pr", gateTaskID)

	if got := lab.creates(t); got != 0 {
		t.Fatalf("gh pr create ran %d times after a closed PR, want none", got)
	}
	pr, _ := refused["pr"].(map[string]any)
	if pr["action"] != "refused" {
		t.Fatalf("pr = %v, want a refusal", pr)
	}
	row := lab.row(t, "pr")
	result, _ := row["result"].(string)
	if row["status"] != "blocked" || !strings.Contains(result, "#4 (closed)") {
		t.Fatalf("pr row = %v, want it blocked naming #4", row)
	}

	allowed := runGate(t, lab, "pr", gateTaskID, "--allow-new-after-closed")

	if got := lab.creates(t); got != 1 {
		t.Fatalf("gh pr create ran %d times with --allow-new-after-closed, want one", got)
	}
	created, _ := allowed["pr"].(map[string]any)
	if created["action"] != "created" || fmt.Sprint(created["number"]) != "7" {
		t.Fatalf("pr = %v, want #7 created", created)
	}
	if row := lab.row(t, "pr"); row["status"] != "pass" {
		t.Fatalf("pr row = %v, want a pass once the PR exists", row)
	}
}

// A `gh pr create` that times out after GitHub already made the PR must never make a second one.
func TestPipelinePR_aCreateThatTimesOutAfterCreatingIsTreatedAsCreated(t *testing.T) {
	lab := prLab(t)
	lab.writeGitHub(t, map[string]any{"create": "timeout"})
	runGate(t, lab, "run", gateTaskID, "--no-pr")

	result := runGate(t, lab, "pr", gateTaskID)

	if got := lab.creates(t); got != 1 {
		t.Fatalf("gh pr create ran %d times, want exactly one despite the timeout", got)
	}
	pr, _ := result["pr"].(map[string]any)
	if pr["action"] != "created" || fmt.Sprint(pr["number"]) != "7" {
		t.Fatalf("pr = %v, want #7 recorded as created", pr)
	}
	summary, _ := result["summary"].(string)
	if !strings.Contains(summary, "GitHub did") {
		t.Fatalf("summary = %q, want it to say GitHub reported the PR", summary)
	}
	if row := lab.row(t, "pr"); row["status"] != "pass" {
		t.Fatalf("pr row = %v, want a pass", row)
	}
}

func TestPipelinePR_refusedUntilTheBranchIsOnOrigin(t *testing.T) {
	lab := prLab(t)

	_, _, err := runPRCLI(t, lab.home, "pipeline", "pr", gateTaskID)

	if err == nil || !strings.Contains(err.Error(), "Push gate") {
		t.Fatalf("err = %v, want a refusal naming the Push gate", err)
	}
	if got := lab.creates(t); got != 0 {
		t.Fatalf("gh pr create ran %d times before the push, want none", got)
	}
}
