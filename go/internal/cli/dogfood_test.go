package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPolicyToPRDogfoodFromDispatch(t *testing.T) {
	v := newVerifyLab(t)
	policy := asMap(v.taskFile()["verification_policy"])
	if asString(policy["status"]) != "standardized" || asString(policy["contract_sha256"]) == "" {
		t.Fatalf("dispatch policy = %v, want a standardized snapshot", policy)
	}
	sha := v.commit("NOTES.md", "dogfood note\n")
	v.reportWithWorkerRun(t, sha)
	workerID := ""
	for _, raw := range asSlice(v.taskFile()["evidence"]) {
		row := asMap(raw)
		if asString(row["kind"]) == "verification" && asString(row["source"]) == "worker" {
			workerID = asString(row["run_id"])
		}
	}
	if workerID == "" {
		t.Fatal("worker verification run_id missing from the report")
	}

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

	v.ctl(true, "pipeline", "rebase", v.taskID)
	v.setEnv("SUM_GH_BIN", filepath.Join(v.root, "tests", "fixtures", "gh_attach.py"))
	ghRoot := filepath.Join(v.base, "fake-gh")
	if err := os.MkdirAll(ghRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	state, err := json.Marshal(map[string]any{
		"version": "2.100.0", "repository": "douglasjarquin/project", "visibility": "PUBLIC",
		"viewer_permission": "WRITE", "head_sha": sha, "next_number": 8,
		"checks": []any{check("verify", "pass", true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ghRoot, "github.json"), state, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.home, "settings.json"), []byte(`{"schema": 1, "evidence": {"auto_publish": false}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	pushed := v.ctl(true, "pipeline", "push", v.taskID)
	if asString(asMap(pushed["evidence"])["outcome"]) != "pushed" && asString(asMap(pushed["evidence"])["outcome"]) != "already" {
		t.Fatalf("push = %v, want the candidate on origin", pushed)
	}

	pr := v.ctl(true, "pipeline", "pr", v.taskID)
	identity := asMap(pr["pr"])
	url := asString(identity["url"])
	if url != "https://github.com/douglasjarquin/project/pull/8" {
		t.Fatalf("pr = %v, want GitHub identity https://github.com/douglasjarquin/project/pull/8", pr)
	}

	ci := v.ctl(true, "pipeline", "ci", v.taskID, "--no-publish")
	if asString(asMap(ci["ci"])["outcome"]) != "pass" {
		t.Fatalf("ci = %v, want a passing observation of the required verify check", ci)
	}

	shown = v.ctl(true, "pipeline", "show", v.taskID)
	rows = map[string]string{}
	for _, item := range asSlice(shown["rows"]) {
		row := asMap(item)
		rows[asString(row["stage"])] = asString(row["status"])
	}
	if rows["push"] != "pass" || rows["pr"] != "pass" || rows["ci"] != "pass" {
		t.Fatalf("publication stages = %v, want push, pr, and ci pass", shown["rows"])
	}
}
