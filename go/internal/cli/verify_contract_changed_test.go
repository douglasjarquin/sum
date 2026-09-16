package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func weakenTheContract(t *testing.T, v *verifyLab) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(v.worktree, "VERIFY.md"))
	if err != nil {
		t.Fatal(err)
	}
	v.commit("VERIFY.md", string(body)+"\nThe candidate rewrote this section.\n")
}

func (v *verifyLab) reportWithWorkerRun(t *testing.T, candidate string) {
	t.Helper()
	run := v.workerRun()
	handoff := filepath.Join(v.base, "handoff.json")
	body, err := json.Marshal(map[string]any{
		"outcome": "done", "candidate": candidate, "next_action": "review",
		"verification": map[string]any{
			"run_id": run["run_id"], "outcome": run["outcome"], "record": run["_path"], "candidate": candidate,
			"certifies": run["certifies"], "requires_root_review": run["requires_root_review"],
			"contract_sha256": asMap(run["contract"])["sha256"],
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(handoff, body, 0o644); err != nil {
		t.Fatal(err)
	}
	v.ctl(true, "report", v.taskID, "--text", "candidate ready", "--handoff", handoff)
}

func TestVerifyFlagsAContractThatMovedSinceDispatch(t *testing.T) {
	v := newVerifyLabWith(t, nil)
	dispatched := asString(asMap(v.taskFile()["verification_policy"])["contract_sha256"])
	if dispatched == "" {
		t.Fatal("dispatch recorded no contract hash to compare against")
	}
	weakenTheContract(t, v)
	sha := v.commit("NOTES.md", "notes\n")
	v.report(t, sha)

	ev := v.evidence(v.ctl(true, "verify", v.taskID, "--candidate", sha, "--execute"))

	if ev["contract_changed_since_dispatch"] != true {
		t.Fatalf("contract_changed_since_dispatch = %v, want true after the candidate edited VERIFY.md", ev["contract_changed_since_dispatch"])
	}
	if asString(ev["contract_sha256"]) == dispatched {
		t.Fatalf("contract_sha256 = %v, want a hash different from the dispatched %v", ev["contract_sha256"], dispatched)
	}
	if ev["requires_root_review"] != true {
		t.Fatalf("requires_root_review = %v, want true", ev["requires_root_review"])
	}
}

func TestVerifyDoesNotFlagAnUntouchedContract(t *testing.T) {
	v := newVerifyLabWith(t, nil)
	dispatched := asString(asMap(v.taskFile()["verification_policy"])["contract_sha256"])
	sha := v.commit("NOTES.md", "notes\n")
	v.report(t, sha)

	ev := v.evidence(v.ctl(true, "verify", v.taskID, "--candidate", sha, "--execute"))

	if ev["contract_changed_since_dispatch"] != false {
		t.Fatalf("contract_changed_since_dispatch = %v, want false", ev["contract_changed_since_dispatch"])
	}
	if asString(ev["contract_sha256"]) != dispatched {
		t.Fatalf("contract_sha256 = %v, want the dispatched %v", ev["contract_sha256"], dispatched)
	}
}

func TestVerifyFlagsAFeatureMapChangedSinceDispatch(t *testing.T) {
	v := newVerifyLab(t)
	mapPath := filepath.Join(v.worktree, "docs", "features", "cli.md")
	body, err := os.ReadFile(mapPath)
	if err != nil {
		t.Fatal(err)
	}
	sha := v.commit("docs/features/cli.md", string(body)+"\nA changed policy note.\n")
	v.report(t, sha)

	ev := v.evidence(v.ctl(true, "verify", v.taskID, "--candidate", sha, "--execute"))
	if ev["policy_changed_since_dispatch"] != true {
		t.Fatalf("policy_changed_since_dispatch = %v, want true after the feature map changed", ev["policy_changed_since_dispatch"])
	}
	if !strings.Contains(asString(ev["policy_change_reason"]), "feature map") {
		t.Fatalf("policy_change_reason = %v, want a feature-map explanation", ev["policy_change_reason"])
	}
	if ev["requires_root_review"] != true {
		t.Fatalf("requires_root_review = %v, want true after the feature map changed", ev["requires_root_review"])
	}
}

func TestBriefListFlagsEvidenceProducedUnderAMovedContract(t *testing.T) {
	v := newVerifyLabWith(t, nil)
	weakenTheContract(t, v)
	sha := v.commit("NOTES.md", "notes\n")
	v.reportWithWorkerRun(t, sha)

	evidence := asMap(v.ctl(true, "brief", "list", v.taskID)["report_evidence"])

	if evidence["contract_changed_since_dispatch"] != true {
		t.Fatalf("contract_changed_since_dispatch = %v, want true", evidence["contract_changed_since_dispatch"])
	}
	if evidence["verification_policy_changed_since"] != true {
		t.Fatalf("verification_policy_changed_since = %v, want true for evidence produced under a moved contract", evidence["verification_policy_changed_since"])
	}
}

func TestBriefListLeavesEvidenceUnflaggedUnderTheDispatchedContract(t *testing.T) {
	v := newVerifyLabWith(t, nil)
	sha := v.commit("NOTES.md", "notes\n")
	v.reportWithWorkerRun(t, sha)

	evidence := asMap(v.ctl(true, "brief", "list", v.taskID)["report_evidence"])

	if evidence["contract_changed_since_dispatch"] != false {
		t.Fatalf("contract_changed_since_dispatch = %v, want false", evidence["contract_changed_since_dispatch"])
	}
	if evidence["verification_policy_changed_since"] != false {
		t.Fatalf("verification_policy_changed_since = %v, want false", evidence["verification_policy_changed_since"])
	}
}
