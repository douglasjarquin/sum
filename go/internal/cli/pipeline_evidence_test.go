package cli

import (
	"fmt"
	"strings"
	"testing"
)

// verificationRecord is what `verify` writes once it has read the run's evidence state: a pass whose required
// before/after comparisons are absent is recorded blocked, and a waiver is the coordinator's own decision on the record.
func verificationRecord(candidate, result string, missing []string, waived bool) string {
	rows := make([]string, 0, len(missing))
	for _, scenario := range missing {
		feature, _, _ := strings.Cut(scenario, ".")
		rows = append(rows, fmt.Sprintf(`{"scenario": %q, "feature": %q}`, scenario, feature))
	}
	waiver := "null"
	if waived {
		waiver = `{"machine": "lab", "session": "sum-lab", "pane": "p-1"}`
	}
	return fmt.Sprintf(`{"schema": 1, "id": "e-1", "kind": "verification", "source": "coordinator",
"at": "2026-01-01T01:00:00+00:00", "candidate": %q, "result": %q, "run_id": "20260906T010203Z-abcd",
"certifies": %q, "requires_root_review": false, "evidence_required": ["counter.click"],
"evidence_present": [], "evidence_missing": [%s], "evidence_waived": %t, "evidence_waived_by": %s}`,
		candidate, result, candidate, strings.Join(rows, ", "), waived, waiver)
}

func TestTestGate_blocksWhenTheCandidatesRequiredEvidenceIsMissing(t *testing.T) {
	lab := newGateLab(t)
	lab.writeTask(t, "reported", verificationRecord(lab.candidate, "blocked", []string{"counter.click"}, false))
	runGate(t, lab, "refresh", gateTaskID)

	row := lab.row(t, "test")
	want := "Passed checks; missing before/after evidence for counter.click (the worker captures with `.agents/skills/evidence/`) (`mise run verify`, run 20260906T010203Z-abcd)"
	if row["status"] != "blocked" || row["result"] != want {
		t.Fatalf("test row = %v\nwant a blocked row reading %q", row, want)
	}
}

func TestPipelinePush_refusedWhileRequiredEvidenceIsMissing(t *testing.T) {
	lab := newGateLab(t)
	lab.writeTask(t, "reported", verificationRecord(lab.candidate, "blocked", []string{"counter.click"}, false))
	runGate(t, lab, "rebase", gateTaskID)

	_, _, err := runPRCLI(t, lab.home, "pipeline", "push", gateTaskID)

	if err == nil {
		t.Fatal("pushing a candidate whose required evidence is missing should be refused")
	}
	if !strings.Contains(err.Error(), "counter.click") || !strings.Contains(err.Error(), ".agents/skills/evidence/") {
		t.Fatalf("error = %v, want it to name the scenario and the capture skill", err)
	}
	if got := lab.remoteSHA(t, gateBranch); got != "" {
		t.Fatalf("origin/%s = %s, want nothing pushed", gateBranch, got)
	}
}

func TestPipelinePush_allowMissingEvidenceStillNeedsTheWaiverOnTheRecord(t *testing.T) {
	lab := newGateLab(t)
	lab.writeTask(t, "reported", verificationRecord(lab.candidate, "blocked", []string{"counter.click"}, false))
	runGate(t, lab, "rebase", gateTaskID)

	_, _, err := runPRCLI(t, lab.home, "pipeline", "push", gateTaskID, "--allow-missing-evidence")

	if err == nil || !strings.Contains(err.Error(), "--accept-missing-evidence") {
		t.Fatalf("error = %v, want the flag refused until the waiver is recorded", err)
	}
	if got := lab.remoteSHA(t, gateBranch); got != "" {
		t.Fatalf("origin/%s = %s, want nothing pushed", gateBranch, got)
	}
}

func TestTestGate_waivedEvidenceReadsAsWaivedAndLetsThePushThrough(t *testing.T) {
	lab := newGateLab(t)
	lab.writeTask(t, "reported", verificationRecord(lab.candidate, "pass", []string{"counter.click"}, true))
	runGate(t, lab, "rebase", gateTaskID)

	row := lab.row(t, "test")
	want := "Passed; evidence waived for counter.click (`mise run verify`, run 20260906T010203Z-abcd)"
	if row["status"] != "pass" || row["result"] != want {
		t.Fatalf("test row = %v\nwant a pass reading %q", row, want)
	}
	runGate(t, lab, "push", gateTaskID)
	if got := lab.remoteSHA(t, gateBranch); got != lab.candidate {
		t.Fatalf("origin/%s = %s, want the waived candidate pushed", gateBranch, got)
	}
}
