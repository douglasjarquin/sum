package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withAgentsDoc(repo string) {
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# Agents\nbase\n"), 0o644); err != nil {
		panic(err)
	}
}

func withArchitecturePolicy(repo string) {
	contract := filepath.Join(repo, "VERIFY.md")
	body, err := os.ReadFile(contract)
	if err != nil {
		panic(err)
	}
	updated := strings.Replace(string(body), "artifacts = \".artifacts/verification\"\n", "artifacts = \".artifacts/verification\"\npolicy_files = [\"docs/architecture.md\"]\n", 1)
	if err := os.WriteFile(contract, []byte(updated), 0o644); err != nil {
		panic(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "architecture.md"), []byte("# Architecture\nbase\n"), 0o644); err != nil {
		panic(err)
	}
}

func TestVerifyExecute_agentsMdChangeRequiresPolicyReview(t *testing.T) {
	v := newVerifyLabWith(t, withAgentsDoc)
	sha := v.commit("AGENTS.md", "# Agents\nchanged\n")
	v.report(t, sha)
	ev := v.evidence(v.ctl(true, "verify", v.taskID, "--candidate", sha, "--execute"))
	if ev["requires_root_review"] != true {
		t.Fatalf("requires_root_review = %v, want true after AGENTS.md changed", ev["requires_root_review"])
	}
}

func TestVerifyExecute_droppedPolicyFileStillRequiresReview(t *testing.T) {
	v := newVerifyLabWith(t, withArchitecturePolicy)
	contract := filepath.Join(v.worktree, "VERIFY.md")
	body, err := os.ReadFile(contract)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(contract, []byte(strings.Replace(string(body), "policy_files = [\"docs/architecture.md\"]\n", "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.worktree, "docs", "architecture.md"), []byte("# Architecture\nweakened\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v.git(v.worktree, "add", "-A")
	v.git(v.worktree, "commit", "-m", "drop extra policy file")
	sha := v.git(v.worktree, "rev-parse", "HEAD")
	v.report(t, sha)
	ev := v.evidence(v.ctl(true, "verify", v.taskID, "--candidate", sha, "--execute"))
	if ev["requires_root_review"] != true {
		t.Fatalf("requires_root_review = %v, want true after dropping a base policy path", ev["requires_root_review"])
	}
}

func TestReviewPolicyApproveDoesNotFollowALaterCandidate(t *testing.T) {
	v := newVerifyLabWith(t, withAgentsDoc)
	first := v.commit("AGENTS.md", "# Agents\nfirst\n")
	v.report(t, first)
	v.ctl(true, "verify", v.taskID, "--candidate", first, "--execute")
	v.ctl(true, "review", v.taskID, "--verdict", "approve", "--candidate", first, "--policy-reviewed", "--text", "policy read")
	second := v.commit("NOTES.md", "later\n")
	v.report(t, second)
	v.ctl(true, "verify", v.taskID, "--candidate", second, "--execute")
	shown := v.ctl(true, "show", v.taskID)
	missing := asSlice(asMap(asMap(shown["evidence_view"])["closure"])["missing"])
	found := false
	for _, item := range missing {
		if strings.Contains(asString(item), "policy-reviewed") || strings.Contains(asString(item), "changed verification policy") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("closure.missing = %v, want policy review of the later candidate", missing)
	}
}

func TestPipelinePush_waiverForOneCandidateDoesNotUnlockAnother(t *testing.T) {
	v := newVerifyLabWith(t, requireVisualProof)
	first := v.commit("NOTES.md", "first\n")
	v.report(t, first)
	v.ctl(true, "verify", v.taskID, "--candidate", first, "--execute", "--accept-missing-evidence")
	second := v.commit("NOTES.md", "second\n")
	v.report(t, second)
	v.ctl(true, "verify", v.taskID, "--candidate", second, "--execute")
	refused := v.ctl(false, "pipeline", "push", v.taskID, "--allow-behind", "--allow-missing-evidence")
	if !strings.Contains(asString(refused["error"]), "--accept-missing-evidence") && !strings.Contains(asString(refused["error"]), evidenceScenario) {
		t.Fatalf("push error %v, want a refusal until SHA %s has its own waiver", refused["error"], second)
	}
}
