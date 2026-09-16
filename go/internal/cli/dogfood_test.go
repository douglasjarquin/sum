package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPolicyToPRDogfoodFromDispatch(t *testing.T) {
	v := newVerifyLab(t)
	policy := asMap(v.taskFile()["verification_policy"])
	if asString(policy["status"]) != "standardized" || asString(policy["contract_sha256"]) == "" {
		t.Fatalf("dispatch policy = %v, want a standardized snapshot", policy)
	}
	sha := v.commit("NOTES.md", "dogfood note\n")
	worker := v.workerRun()
	workerID := asString(worker["run_id"])
	if workerID == "" {
		t.Fatalf("worker run_id missing: %v", worker)
	}
	handoff := filepath.Join(v.base, "handoff.json")
	payload, err := json.Marshal(map[string]any{
		"outcome":   "completed",
		"candidate": sha,
		"files":     []string{"NOTES.md"},
		"verification": map[string]any{
			"run_id": workerID,
			"path":   asString(worker["_path"]),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(handoff, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	v.ctlPane(asString(v.taskFile()["pane"]), true, "report", v.taskID, "--text", "worker finished", "--handoff", handoff)

	executed := v.ctl(true, "verify", v.taskID, "--candidate", sha, "--execute")
	coordID := asString(v.evidence(executed)["run_id"])
	if coordID == "" || coordID == workerID {
		t.Fatalf("coordinator run_id %q worker %q, want two distinct runs", coordID, workerID)
	}

	review := v.ctlPane("w-review:p1", true, "review", v.taskID, "--verdict", "approve", "--candidate", sha, "--policy-reviewed", "--text", "independent review of the candidate")
	if asString(asMap(review["evidence"])["source"]) == "worker" {
		t.Fatalf("review source = %v, want a non-worker reviewer", review["evidence"])
	}

	doc := v.ctl(true, "pipeline", "document", v.taskID)
	if asString(asMap(doc["evidence"])["result"]) == "fail" {
		t.Fatalf("document = %v, want a non-fail audit", doc["evidence"])
	}

	shown := v.ctl(true, "pipeline", "show", v.taskID)
	rows := map[string]string{}
	for _, item := range asSlice(shown["rows"]) {
		row := asMap(item)
		rows[asString(row["stage"])] = asString(row["status"])
	}
	if rows["review"] != "pass" {
		t.Fatalf("review stage = %q in %v, want pass", rows["review"], shown["rows"])
	}
	if rows["test"] != "pass" {
		t.Fatalf("test stage = %q in %v, want pass", rows["test"], shown["rows"])
	}
	if rows["document"] != "pass" && rows["document"] != "skipped" {
		t.Fatalf("document stage = %q in %v, want pass or skipped", rows["document"], shown["rows"])
	}

	reloaded := v.ctl(true, "show", v.taskID)
	if asString(asMap(reloaded["verification_policy"])["contract_sha256"]) != asString(policy["contract_sha256"]) {
		t.Fatalf("in-flight policy drifted: %v", reloaded["verification_policy"])
	}

	ghRoot := filepath.Join(v.base, "fake-gh")
	if err := os.MkdirAll(ghRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	state, err := json.Marshal(map[string]any{
		"version": "2.100.0", "repository": "douglasjarquin/project", "visibility": "PUBLIC",
		"viewer_permission": "WRITE", "head_sha": sha, "next_number": 8,
		"checks": []any{map[string]any{"name": "verify", "conclusion": "SUCCESS", "status": "COMPLETED"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ghRoot, "github.json"), state, 0o600); err != nil {
		t.Fatal(err)
	}

	pushed := v.ctl(false, "pipeline", "push", v.taskID)
	if errText := asString(pushed["error"]); errText != "" && !strings.Contains(errText, "Review") && !strings.Contains(errText, "Lint") && !strings.Contains(errText, "Rebase") && !strings.Contains(errText, "origin") && !strings.Contains(errText, "fast-forward") {
		t.Fatalf("push error = %q", errText)
	}
	if asString(pushed["error"]) == "" {
		pr := v.ctl(true, "pipeline", "pr", v.taskID)
		if asString(asMap(pr["pr"])["url"]) == "" && asString(pr["url"]) == "" {
			t.Fatalf("pr = %v, want a GitHub identity", pr)
		}
	}
}
