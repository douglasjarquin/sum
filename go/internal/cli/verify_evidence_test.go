package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// requireVisualProof makes the fixture project demand of every candidate what a real map row demands: the Evidence
// cell names a screenshot pair, so the scenario is only proven by a comparison for the candidate SHA.
func requireVisualProof(repo string) {
	path := filepath.Join(repo, "docs", "features", "cli.md")
	body, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	row := "| `" + evidenceScenario + "` | The counter shows the click | manual: click it and look | screenshot before/after |\n"
	if err := os.WriteFile(path, append(body, []byte(row)...), 0o644); err != nil {
		panic(err)
	}
}

func TestVerifyExecute_blocksWhenTheCandidateProvesNothingItWasAskedToProve(t *testing.T) {
	v := newVerifyLabWith(t, requireVisualProof)
	sha := v.commit("NOTES.md", "notes\n")
	v.report(t, sha)

	out := v.ctl(true, "verify", v.taskID, "--candidate", sha, "--execute")

	ev := v.evidence(out)
	if asString(ev["result"]) != "blocked" || asString(ev["outcome"]) != "pass" {
		t.Fatalf("result %v outcome %v, want a passing run recorded blocked for its evidence", ev["result"], ev["outcome"])
	}
	want := "missing required evidence for 1 scenario(s): " + evidenceScenario
	if asString(ev["blocked_reason"]) != want {
		t.Fatalf("blocked_reason %q, want %q", ev["blocked_reason"], want)
	}
	missing, _ := ev["evidence_missing"].([]any)
	if len(missing) != 1 {
		t.Fatalf("evidence_missing %v, want the one scenario", ev["evidence_missing"])
	}
	row, _ := missing[0].(map[string]any)
	if asString(row["scenario"]) != evidenceScenario || asString(row["feature"]) != "cli" {
		t.Fatalf("evidence_missing row %v, want the scenario and its feature", row)
	}
	if required, _ := ev["evidence_required"].([]any); len(required) != 1 {
		t.Fatalf("evidence_required %v", ev["evidence_required"])
	}

	if status := asString(v.pipelineRow(t, "test")["status"]); status != "blocked" {
		t.Fatalf("test row status %q, want blocked", status)
	}
	refused := v.ctl(false, "pipeline", "push", v.taskID, "--allow-behind")
	if !strings.Contains(asString(refused["error"]), evidenceScenario) {
		t.Fatalf("push error %v, want a refusal naming the scenario", refused["error"])
	}
}

func TestVerifyExecute_passesOnceTheWorkerHasCapturedTheComparison(t *testing.T) {
	v := newVerifyLabWith(t, requireVisualProof)
	sha := v.commit("NOTES.md", "notes\n")
	v.report(t, sha, writeEvidenceRun(t, filepath.Join(v.worktree, "evidence"), v.baseSHA, sha))

	out := v.ctl(true, "verify", v.taskID, "--candidate", sha, "--execute")

	ev := v.evidence(out)
	if asString(ev["result"]) != "pass" {
		t.Fatalf("result %v (%v), want a pass once the comparison exists", ev["result"], ev["blocked_reason"])
	}
	if missing, _ := ev["evidence_missing"].([]any); len(missing) != 0 {
		t.Fatalf("evidence_missing %v, want none", ev["evidence_missing"])
	}
	present, _ := ev["evidence_present"].([]any)
	if len(present) != 1 || asString(present[0]) != evidenceScenario {
		t.Fatalf("evidence_present %v, want the captured scenario", ev["evidence_present"])
	}
	if status := asString(v.pipelineRow(t, "test")["status"]); status != "pass" {
		t.Fatalf("test row status %q, want pass", status)
	}
}

func TestVerifyExecute_acceptMissingEvidenceRecordsTheWaiverAndUnblocksThePush(t *testing.T) {
	v := newVerifyLabWith(t, requireVisualProof)
	sha := v.commit("NOTES.md", "notes\n")
	v.report(t, sha)

	out := v.ctl(true, "verify", v.taskID, "--candidate", sha, "--execute", "--accept-missing-evidence")

	ev := v.evidence(out)
	if asString(ev["result"]) != "pass" || ev["evidence_waived"] != true {
		t.Fatalf("result %v waived %v, want a recorded waiver", ev["result"], ev["evidence_waived"])
	}
	waivedBy, _ := ev["evidence_waived_by"].(map[string]any)
	if waivedBy == nil || asString(waivedBy["session"]) == "" {
		t.Fatalf("evidence_waived_by %v, want the coordinator endpoint that decided", ev["evidence_waived_by"])
	}
	row := v.pipelineRow(t, "test")
	if asString(row["status"]) != "pass" || !strings.Contains(asString(row["result"]), "evidence waived for "+evidenceScenario) {
		t.Fatalf("test row %v, want a pass that says the evidence was waived", row)
	}
}

// report is the worker's handoff. Its artifacts carry the comparison paths the coordinator publishes from, which is
// also how root verification finds captures its own fresh checkout cannot contain.
func (v *verifyLab) report(t *testing.T, candidate string, artifacts ...string) {
	t.Helper()
	handoff := filepath.Join(v.base, "handoff.json")
	body, err := json.Marshal(map[string]any{
		"outcome": "done", "candidate": candidate, "next_action": "review",
		"artifacts": artifacts,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(handoff, body, 0o644); err != nil {
		t.Fatal(err)
	}
	v.ctl(true, "report", v.taskID, "--text", "candidate ready", "--handoff", handoff)
}

func (v *verifyLab) pipelineRow(t *testing.T, stage string) map[string]any {
	t.Helper()
	return pipelineRow(t, v.ctl(true, "pipeline", "show", v.taskID), stage)
}

func TestPipelineRun_stopsBeforePushWhenTheEvidenceIsMissing(t *testing.T) {
	v := newVerifyLabWith(t, requireVisualProof)
	sha := v.commit("NOTES.md", "notes\n")
	v.report(t, sha)
	v.ctl(true, "review", v.taskID, "--verdict", "approve", "--candidate", sha, "--text", "reads well")

	out := v.ctl(true, "pipeline", "run", v.taskID, "--no-pr")

	steps := map[string]map[string]any{}
	for _, raw := range asSlice(out["steps"]) {
		step, _ := raw.(map[string]any)
		steps[asString(step["stage"])] = step
	}
	if asString(steps["test"]["outcome"]) != "ran" {
		t.Fatalf("test step %v, want the contract to have run", steps["test"])
	}
	if asString(steps["push"]["outcome"]) != "not-run" || asString(steps["push"]["detail"]) != "the test gate is blocked" {
		t.Fatalf("push step %v, want it not run because Test is blocked", steps["push"])
	}
	next := asString(out["next"])
	if !strings.Contains(next, evidenceScenario) || !strings.Contains(next, "--accept-missing-evidence") || !strings.Contains(next, "repair send") {
		t.Fatalf("next = %q, want the scenario and both ways out", next)
	}
}
