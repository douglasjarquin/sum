package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// check is one row of the fake gh's scenario file, in the shape `gh pr checks --json` reports.
func check(name, bucket string, required bool) map[string]any {
	return map[string]any{"name": name, "bucket": bucket, "state": strings.ToUpper(bucket), "required": required,
		"link": "https://github.com/douglasjarquin/project/actions/runs/1/job/" + name, "workflow": "verify"}
}

func ciLab(t *testing.T, checks ...map[string]any) publishLab {
	t.Helper()
	requirePython(t)
	lab := newPublishLab(t, map[string]any{"checks": toAnyList(checks)})
	lab.writeSettings(t, `{"schema": 1, "evidence": {"auto_publish": false}}`)
	return lab
}

func toAnyList(checks []map[string]any) []any {
	out := make([]any, len(checks))
	for i, c := range checks {
		out[i] = c
	}
	return out
}

// ciObservation is the row `pr reconcile` and `pipeline ci` both report.
func ciObservation(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	row, _ := result["ci"].(map[string]any)
	if row == nil {
		t.Fatalf("no ci row in %v", result)
	}
	return row
}

// ciCounts reads one bucket out of the observation's counts, whatever number shape the JSON decoder produced.
func ciCounts(t *testing.T, row map[string]any, bucket string) string {
	t.Helper()
	counts, _ := row["counts"].(map[string]any)
	if counts == nil {
		t.Fatalf("ci row has no counts: %v", row)
	}
	return fmt.Sprint(counts[bucket])
}

func runPipelineCI(t *testing.T, lab publishLab, extra ...string) map[string]any {
	t.Helper()
	stdout, stderr, err := runPRCLI(t, lab.home, append([]string{"pipeline", "ci", lab.taskID}, extra...)...)
	if err != nil {
		t.Fatalf("pipeline ci: %v stderr=%s", err, stderr)
	}
	return decodeObject(t, stdout)
}

func ciRowOfRecord(t *testing.T, lab publishLab) map[string]any {
	t.Helper()
	return pipelineRow(t, lab.pipelineRecord(t), "ci")
}

func TestPipelineCI_reconcileObservesThePassingChecksAndPublishesTheRow(t *testing.T) {
	lab := ciLab(t, check("build", "pass", true), check("test", "pass", true), check("docs", "pass", false))

	row := ciObservation(t, reconcile(t, lab))

	if row["outcome"] != "pass" || row["scope"] != "required" || row["required_only"] != true {
		t.Fatalf("ci observation = %v", row)
	}
	if ciCounts(t, row, "pass") != "2" || ciCounts(t, row, "total") != "2" {
		t.Fatalf("counts = %v, want only the two required checks", row["counts"])
	}
	recorded := ciRowOfRecord(t, lab)
	if recorded["status"] != "pass" || !strings.HasPrefix(recorded["result"].(string), "Passed 2/2 required checks (observed ") {
		t.Fatalf("derived CI row = %v", recorded)
	}
	publishPipeline(t, lab)
	if body := lab.prBody(t); !strings.Contains(body, "| CI | ✅ | Passed 2/2 required checks (observed ") {
		t.Fatalf("PR body has no passing CI row:\n%s", body)
	}
}

func TestPipelineCI_aFailingCheckFailsTheRowAndLinksIt(t *testing.T) {
	lab := ciLab(t, check("build", "pass", true), check("test", "fail", true))

	row := ciObservation(t, reconcile(t, lab))

	if row["outcome"] != "fail" {
		t.Fatalf("ci observation = %v", row)
	}
	recorded := ciRowOfRecord(t, lab)
	result, _ := recorded["result"].(string)
	if recorded["status"] != "fail" || !strings.Contains(result, "Failed: [test](https://github.com/douglasjarquin/project/actions/runs/1/job/test)") {
		t.Fatalf("derived CI row = %v", recorded)
	}
	if !strings.Contains(result, "1 of 2 required checks passed (observed ") {
		t.Fatalf("the failing row does not carry the counts: %q", result)
	}
	publishPipeline(t, lab)
	if body := lab.prBody(t); !strings.Contains(body, "| CI | ❌ | Failed: [test](") {
		t.Fatalf("PR body has no failing CI row:\n%s", body)
	}
}

func TestPipelineCI_aPendingCheckLeavesTheRowPending(t *testing.T) {
	lab := ciLab(t, check("build", "pass", true), check("test", "pending", true), check("lint", "skipping", true))

	row := ciObservation(t, reconcile(t, lab))

	if row["outcome"] != "pending" {
		t.Fatalf("ci observation = %v", row)
	}
	recorded := ciRowOfRecord(t, lab)
	if recorded["status"] != "pending" || !strings.HasPrefix(recorded["result"].(string), "1 pending, 1 passed of 3 required checks (observed ") {
		t.Fatalf("derived CI row = %v", recorded)
	}
}

func TestPipelineCI_aPRWithNoChecksIsNotDeclared(t *testing.T) {
	lab := ciLab(t)

	row := ciObservation(t, reconcile(t, lab))

	if row["outcome"] != "not-declared" {
		t.Fatalf("ci observation = %v", row)
	}
	recorded := ciRowOfRecord(t, lab)
	if recorded["status"] != "not_declared" || recorded["mark"] != "➖" ||
		!strings.HasPrefix(recorded["result"].(string), "No checks reported for this PR (observed ") {
		t.Fatalf("derived CI row = %v", recorded)
	}
}

func TestPipelineCI_withoutRequiredChecksEveryCheckIsTheScopeAndTheRowSaysSo(t *testing.T) {
	lab := ciLab(t, check("build", "pass", false), check("test", "pass", false))

	row := ciObservation(t, reconcile(t, lab))

	if row["outcome"] != "pass" || row["scope"] != "all" || row["required_only"] != false {
		t.Fatalf("ci observation = %v", row)
	}
	result, _ := ciRowOfRecord(t, lab)["result"].(string)
	if !strings.HasPrefix(result, "Passed 2/2 checks (observed ") || !strings.HasSuffix(result, "; GitHub reports no required checks") {
		t.Fatalf("the row does not say the scope is every check: %q", result)
	}
}

func TestPipelineCI_onDemandObservationRepublishesWhenARowMoved(t *testing.T) {
	lab := ciLab(t, check("build", "pending", true))
	reconcile(t, lab)
	publishPipeline(t, lab)
	if body := lab.prBody(t); !strings.Contains(body, "| CI | ⏳ | 1 pending, 0 passed of 1 required checks") {
		t.Fatalf("PR body has no pending CI row:\n%s", body)
	}
	lab.writeSettings(t, `{"schema": 1, "evidence": {"auto_publish": true}}`)
	lab.setChecks(t, check("build", "pass", true))

	result := runPipelineCI(t, lab)

	if ciObservation(t, result)["outcome"] != "pass" {
		t.Fatalf("pipeline ci did not re-observe: %v", result)
	}
	publication, _ := result["publication"].(map[string]any)
	if publication == nil || publication["outcome"] != "published" {
		t.Fatalf("publication = %v, want published", publication)
	}
	body := lab.prBody(t)
	if !strings.Contains(body, "| CI | ✅ | Passed 1/1 required checks") {
		t.Fatalf("the republished body has no passing CI row:\n%s", body)
	}
	if strings.Count(body, pipelineMarker) != 1 {
		t.Fatalf("the republish duplicated the block:\n%s", body)
	}
}

func TestPipelineCI_republishingTheSameObservationIsUnchangedAndEditsNothing(t *testing.T) {
	lab := ciLab(t, check("build", "pass", true))
	reconcile(t, lab)
	lab.writeSettings(t, `{"schema": 1, "evidence": {"auto_publish": true}}`)

	result := runPipelineCI(t, lab, "--no-publish")

	if result["publication"] != nil {
		t.Fatalf("--no-publish published anyway: %v", result["publication"])
	}
	if publishPipeline(t, lab)["outcome"] != "published" {
		t.Fatal("the first publish of a fresh observation should have edited the body")
	}
	before := lab.prBody(t)
	again := publishPipeline(t, lab)
	if again["outcome"] != "unchanged" {
		t.Fatalf("second publish = %v, want unchanged", again)
	}
	if lab.prBody(t) != before {
		t.Fatal("an unchanged republish edited the PR body")
	}
}

func TestPipelineCI_anObservationOfAnEarlierHeadIsIgnoredAfterANewPush(t *testing.T) {
	lab := ciLab(t, check("build", "pass", true))
	reconcile(t, lab)
	if ciRowOfRecord(t, lab)["status"] != "pass" {
		t.Fatal("the first observation did not pass")
	}

	lab.setPRHead(t, "dddddddddddddddddddddddddddddddddddddddd")
	if _, stderr, err := runPRCLI(t, lab.home, "pipeline", "refresh", lab.taskID); err != nil {
		t.Fatalf("pipeline refresh: %v stderr=%s", err, stderr)
	}

	recorded := ciRowOfRecord(t, lab)
	if recorded["status"] != "pending" || recorded["result"] != "Not observed; `pr reconcile` or `pipeline ci` reads the checks" {
		t.Fatalf("derived CI row = %v, want the earlier head's pass ignored", recorded)
	}
}

func TestPipelineCI_fallsBackToTheRollupWhenGhCannotAnswerPrChecksJSON(t *testing.T) {
	lab := ciLab(t, check("build", "pass", true), check("docs", "pass", false))
	lab.setState(t, "checks_json", false)

	row := ciObservation(t, reconcile(t, lab))

	if row["outcome"] != "pass" || row["scope"] != "required" {
		t.Fatalf("ci observation = %v, want the rollup's required checks", row)
	}
	if ciCounts(t, row, "total") != "1" {
		t.Fatalf("counts = %v, want only the required check", row["counts"])
	}
}

func TestPipelineCI_refusesWithoutAReconciledPR(t *testing.T) {
	lab := ciLab(t, check("build", "pass", true))

	_, _, err := runPRCLI(t, lab.home, "pipeline", "ci", lab.taskID)

	if err == nil || !strings.Contains(err.Error(), "pr reconcile") {
		t.Fatalf("err = %v, want a refusal naming pr reconcile", err)
	}
}

func (lab publishLab) setChecks(t *testing.T, checks ...map[string]any) {
	t.Helper()
	lab.setState(t, "checks", toAnyList(checks))
}

// setState edits one scenario key of the fake GitHub, leaving the PR body the previous publications wrote.
func (lab publishLab) setState(t *testing.T, key string, value any) {
	t.Helper()
	path := filepath.Join(lab.ghRoot, "github.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	state[key] = value
	next, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, next, 0o600); err != nil {
		t.Fatal(err)
	}
}

// setPRHead moves the PR identity sum recorded, which is what a new push to the branch looks like to derive.
func (lab publishLab) setPRHead(t *testing.T, head string) {
	t.Helper()
	path := filepath.Join(lab.home, "tasks", lab.taskID, "task.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var task map[string]any
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatal(err)
	}
	pr, _ := task["pr"].(map[string]any)
	identity, _ := pr["identity"].(map[string]any)
	identity["head_sha"] = head
	next, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, next, 0o600); err != nil {
		t.Fatal(err)
	}
}
