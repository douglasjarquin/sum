package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	pipelineMarker    = "<!-- sum-pipeline:start -->"
	pipelineMarkerEnd = "<!-- sum-pipeline:end -->"
)

func (lab publishLab) setPRBody(t *testing.T, body string) {
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
	pr, _ := state["pr"].(map[string]any)
	pr["body"] = body
	next, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, next, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (lab publishLab) pipelineRecord(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(lab.home, "tasks", lab.taskID, "pipeline.json"))
	if err != nil {
		t.Fatalf("pipeline.json: %v", err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	return record
}

func pipelineRow(t *testing.T, record map[string]any, stage string) map[string]any {
	t.Helper()
	rows, _ := record["rows"].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["stage"] == stage {
			return row
		}
	}
	t.Fatalf("pipeline record has no %s row: %v", stage, record)
	return nil
}

func publishPipeline(t *testing.T, lab publishLab, extra ...string) map[string]any {
	t.Helper()
	args := append([]string{"pipeline", "publish", lab.taskID}, extra...)
	stdout, stderr, err := runPRCLI(t, lab.home, args...)
	if err != nil {
		t.Fatalf("pipeline publish: %v stderr=%s", err, stderr)
	}
	result := decodeObject(t, stdout)
	publication, _ := result["publication"].(map[string]any)
	if publication == nil {
		t.Fatalf("pipeline publish printed no publication:\n%s", stdout)
	}
	return publication
}

func TestPipelinePublish_writesOneBlockBesideTheEvidenceBlockAndKeepsTheProse(t *testing.T) {
	requirePython(t)
	lab := newPublishLab(t, map[string]any{})
	lab.writeSettings(t, `{"schema": 1, "evidence": {"auto_publish": false}}`)
	reconcile(t, lab)
	if _, stderr, err := runPRCLI(t, lab.home, "pr", "evidence", lab.taskID); err != nil {
		t.Fatalf("pr evidence: %v stderr=%s", err, stderr)
	}

	publication := publishPipeline(t, lab)

	if publication["outcome"] != "published" {
		t.Fatalf("publication = %v, want published", publication)
	}
	body := lab.prBody(t)
	if strings.Count(body, pipelineMarker) != 1 || strings.Count(body, pipelineMarkerEnd) != 1 {
		t.Fatalf("PR body does not carry exactly one pipeline block:\n%s", body)
	}
	if strings.Count(body, blockMarker) != 1 {
		t.Fatalf("the evidence block was disturbed:\n%s", body)
	}
	if !strings.Contains(body, prProse) {
		t.Fatalf("PR body lost the human prose:\n%s", body)
	}
	for _, want := range []string{"## Pipeline", "| Stage | Status | Result |", "| Intent | ✅ | Approved brief recorded |", "| CI | ⏳ | Not run in this release |"} {
		if !strings.Contains(body, want) {
			t.Fatalf("PR body is missing %q:\n%s", want, body)
		}
	}
	rows := lab.pipelinePublications(t)
	if len(rows) != 1 || rows[0]["outcome"] != "published" || rows[0]["trigger"] != "manual" {
		t.Fatalf("pipeline publication records = %v", rows)
	}
	if _, err := os.Stat(filepath.Join(lab.home, "tasks", lab.taskID, "publish", "pipeline-receipts.json")); err != nil {
		t.Fatalf("pipeline receipts were not written: %v", err)
	}
}

func TestPipelinePublish_secondPublishIsUnchanged(t *testing.T) {
	requirePython(t)
	lab := newPublishLab(t, map[string]any{})
	lab.writeSettings(t, `{"schema": 1, "evidence": {"auto_publish": false}}`)
	reconcile(t, lab)
	publishPipeline(t, lab)
	before := lab.prBody(t)

	publication := publishPipeline(t, lab)

	if publication["outcome"] != "unchanged" {
		t.Fatalf("second publish = %v, want unchanged", publication)
	}
	if got := lab.prBody(t); got != before {
		t.Fatalf("second publish edited the body:\n%s", got)
	}
}

func TestPipelinePublish_handEditedBlockIsRefusedUntilItIsReplaced(t *testing.T) {
	requirePython(t)
	lab := newPublishLab(t, map[string]any{})
	lab.writeSettings(t, `{"schema": 1, "evidence": {"auto_publish": false}}`)
	reconcile(t, lab)
	publishPipeline(t, lab)
	edited := strings.Replace(lab.prBody(t), pipelineMarker, pipelineMarker+"\na reviewer typed this inside the block", 1)
	lab.setPRBody(t, edited)

	refused := publishPipeline(t, lab)

	if refused["outcome"] != "refused" {
		t.Fatalf("publish over a hand-edited block = %v, want refused", refused)
	}
	if got := lab.prBody(t); got != edited {
		t.Fatalf("a refused publish edited the body:\n%s", got)
	}

	taken := publishPipeline(t, lab, "--replace-foreign-block")

	if taken["outcome"] != "published" {
		t.Fatalf("publish with --replace-foreign-block = %v, want published", taken)
	}
	body := lab.prBody(t)
	if strings.Contains(body, "a reviewer typed this inside the block") {
		t.Fatalf("the foreign block survived the takeover:\n%s", body)
	}
	if !strings.Contains(body, prProse) {
		t.Fatalf("the takeover lost the human prose outside the block:\n%s", body)
	}
}

func TestPipelinePublish_dryRunEditsNothing(t *testing.T) {
	requirePython(t)
	lab := newPublishLab(t, map[string]any{})
	lab.writeSettings(t, `{"schema": 1, "evidence": {"auto_publish": false}}`)
	reconcile(t, lab)
	before := lab.prBody(t)

	publication := publishPipeline(t, lab, "--dry-run")

	if publication["outcome"] != "planned" {
		t.Fatalf("dry run = %v, want planned", publication)
	}
	if got := lab.prBody(t); got != before {
		t.Fatalf("a dry run edited the body:\n%s", got)
	}
}

func TestPRReconcile_publishesTheEvidenceAndPipelineBlocksIntoAnOpenPR(t *testing.T) {
	requirePython(t)
	lab := newPublishLab(t, map[string]any{})

	result := reconcile(t, lab)

	publication, _ := result["pipeline_publication"].(map[string]any)
	if publication == nil || publication["outcome"] != "published" {
		t.Fatalf("pipeline_publication = %v, want published", result["pipeline_publication"])
	}
	body := lab.prBody(t)
	if strings.Count(body, pipelineMarker) != 1 || strings.Count(body, blockMarker) != 1 {
		t.Fatalf("PR body does not carry both blocks exactly once:\n%s", body)
	}
	if !strings.Contains(body, prProse) {
		t.Fatalf("PR body lost the human prose:\n%s", body)
	}
	if rows := lab.pipelinePublications(t); len(rows) != 1 || rows[0]["trigger"] != "reconcile" {
		t.Fatalf("pipeline publication records = %v, want one reconcile record", rows)
	}
}

func TestPRReconcile_publishesNoPipelineBlockWhenAutoPublishIsOff(t *testing.T) {
	requirePython(t)
	lab := newPublishLab(t, map[string]any{})
	lab.writeSettings(t, `{"schema": 1, "evidence": {"auto_publish": false}}`)

	result := reconcile(t, lab)

	if result["pipeline_publication"] != nil {
		t.Fatalf("pipeline_publication = %v, want nothing published", result["pipeline_publication"])
	}
	if strings.Contains(lab.prBody(t), pipelineMarker) {
		t.Fatalf("the PR body was edited:\n%s", lab.prBody(t))
	}
}

func TestPipelinePublish_beforeReconcileSaysToRecordThePRFirst(t *testing.T) {
	lab := newPublishLab(t, map[string]any{})

	_, _, err := runPRCLI(t, lab.home, "pipeline", "publish", lab.taskID)

	if err == nil {
		t.Fatal("publishing before pr reconcile should fail")
	}
	if !strings.Contains(err.Error(), "pr reconcile") {
		t.Fatalf("error = %v, want it to name `pr reconcile`", err)
	}
	if strings.Contains(lab.prBody(t), pipelineMarker) {
		t.Fatalf("a refused publish edited the body:\n%s", lab.prBody(t))
	}
}

func TestPipelineShow_reportsEveryStageBeforeAnythingHasRun(t *testing.T) {
	lab := newPublishLab(t, map[string]any{})

	stdout, stderr, err := runPRCLI(t, lab.home, "pipeline", "show", lab.taskID)
	if err != nil {
		t.Fatalf("pipeline show: %v stderr=%s", err, stderr)
	}

	result := decodeObject(t, stdout)
	rows, _ := result["rows"].([]any)
	if len(rows) != 9 {
		t.Fatalf("pipeline show printed %d rows, want 9:\n%s", len(rows), stdout)
	}
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["status"] != "pending" {
			t.Fatalf("stage %v = %v, want pending before the record exists", row["stage"], row["status"])
		}
	}
}

func TestVerifyAndReviewRefreshTheRecordedPipeline(t *testing.T) {
	lab := newPublishLab(t, map[string]any{})

	if _, stderr, err := runPRCLI(t, lab.home, "verify", lab.taskID, "--candidate", evidenceCandidate,
		"--result", "pass", "--text", "ran the contract"); err != nil {
		t.Fatalf("verify: %v stderr=%s", err, stderr)
	}
	if got := pipelineRow(t, lab.pipelineRecord(t), "test")["status"]; got != "pass" {
		t.Fatalf("test stage = %v after verify, want pass", got)
	}

	if _, stderr, err := runPRCLI(t, lab.home, "review", lab.taskID, "--verdict", "approve",
		"--candidate", evidenceCandidate, "--text", "approved"); err != nil {
		t.Fatalf("review: %v stderr=%s", err, stderr)
	}
	record := lab.pipelineRecord(t)
	if got := pipelineRow(t, record, "review")["status"]; got != "pass" {
		t.Fatalf("review stage = %v after review, want pass", got)
	}
	if got := pipelineRow(t, record, "intent")["status"]; got != "pass" {
		t.Fatalf("intent stage = %v, want pass for a task with an approved brief", got)
	}
	if got := pipelineRow(t, record, "rebase")["status"]; got != "pending" {
		t.Fatalf("rebase stage = %v, want pending in this release", got)
	}
}

func TestPipelineDocument_recordsTheAuditOfTheCandidatesOwnContract(t *testing.T) {
	requirePython(t)
	lab := newPublishLab(t, map[string]any{})
	base, candidate := writeAuditFixture(t, lab.worktree)
	lab.rewriteTask(t, base, candidate)

	stdout, stderr, err := runPRCLI(t, lab.home, "pipeline", "document", lab.taskID)
	if err != nil {
		t.Fatalf("pipeline document: %v stderr=%s", err, stderr)
	}

	result := decodeObject(t, stdout)
	record, _ := result["evidence"].(map[string]any)
	if record == nil || record["kind"] != "documentation" || record["source"] != "coordinator" {
		t.Fatalf("pipeline document recorded %v, want a coordinator documentation record", result["evidence"])
	}
	if record["result"] != "pass" || record["summary"] != "Passed" {
		t.Fatalf("documentation record = %v, want a clean audit", record)
	}
	if record["audit_id"] == nil || record["audit_id"] == "" {
		t.Fatalf("documentation record names no audit: %v", record)
	}
	if got := pipelineRow(t, lab.pipelineRecord(t), "document")["status"]; got != "pass" {
		t.Fatalf("document stage = %v after the audit, want pass", got)
	}
	if entries, _ := os.ReadDir(filepath.Join(lab.worktree, ".git", "worktrees")); len(entries) != 0 {
		t.Fatalf("the audit checkout was left behind: %v", entries)
	}
}

func TestPipelineDocument_skipsAProjectThatDeclaresNoContract(t *testing.T) {
	requirePython(t)
	lab := newPublishLab(t, map[string]any{})
	base, candidate := writeAuditFixture(t, lab.worktree)
	gitIn(t, lab.worktree, "rm", "-q", "VERIFY.md")
	gitIn(t, lab.worktree, "commit", "-q", "-m", "drop the contract")
	candidate = strings.TrimSpace(gitIn(t, lab.worktree, "rev-parse", "HEAD"))
	lab.rewriteTask(t, base, candidate)

	stdout, stderr, err := runPRCLI(t, lab.home, "pipeline", "document", lab.taskID)
	if err != nil {
		t.Fatalf("pipeline document: %v stderr=%s", err, stderr)
	}

	record, _ := decodeObject(t, stdout)["evidence"].(map[string]any)
	if record["result"] != "skipped" || record["summary"] != "Project declares no VERIFY.md" {
		t.Fatalf("documentation record = %v, want a skipped audit", record)
	}
	if got := pipelineRow(t, lab.pipelineRecord(t), "document")["status"]; got != "skipped" {
		t.Fatalf("document stage = %v, want skipped", got)
	}
}

func (lab publishLab) rewriteTask(t *testing.T, base, candidate string) {
	t.Helper()
	writeTaskFixture(t, lab.home, lab.taskID, fmt.Sprintf(`{"schema": 1, "id": %q, "status": "reported", "repository": %q, "worktree": %q,
"questions": [], "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "ship", "branch": "sum/t-aaaaaaaaaaaa",
"report": {"text": "done", "candidate": %q}, "evidence": []}`, lab.taskID, lab.worktree, lab.worktree, base, candidate))
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// writeAuditFixture is a standardized repository whose candidate changes only a file its feature map references,
// so a correct audit of it is clean.
func writeAuditFixture(t *testing.T, dir string) (base, candidate string) {
	t.Helper()
	files := map[string]string{
		"VERIFY.md": "# Verification contract\n\n```verify\nentrypoint = \"mise run verify\"\n" +
			"feature_maps = \"docs/features/README.md\"\nartifacts = \".artifacts/verification\"\ntimeout_seconds = 600\n\n" +
			"[requires]\ncommands = [\"git\", \"python3\"]\n```\n\nRun `mise run verify` from the repository root. It is the only entrypoint.\n",
		"mise.toml":               "[tasks.verify]\nrun = \"python3 -m unittest discover -s tests\"\n",
		"docs/features/README.md": "# Feature maps\n\n- [Core](core.md)\n",
		"docs/features/core.md": "# Core\n\nThe adder in `src/core.py`.\n\n| ID | Scenario | Driver | Evidence |\n| --- | --- | --- | --- |\n" +
			"| `core.add` | Adding two numbers returns their sum | automated: `tests/test_core.py` | offline suite |\n",
		"src/core.py":        "def add(a, b):\n    return a + b\n",
		"tests/test_core.py": "from src.core import add\n\n\ndef test_add():\n    assert add(1, 2) == 3\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, dir, "init", "-q", ".")
	gitIn(t, dir, "config", "user.email", "verify@example.com")
	gitIn(t, dir, "config", "user.name", "verify")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "base")
	base = strings.TrimSpace(gitIn(t, dir, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(dir, "src", "core.py"), []byte("def add(a, b):\n    return b + a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "commit", "-q", "-am", "candidate")
	return base, strings.TrimSpace(gitIn(t, dir, "rev-parse", "HEAD"))
}
