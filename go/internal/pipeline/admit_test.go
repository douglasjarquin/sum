package pipeline

import (
	"strings"
	"testing"
)

func readyTaskJSON(review, policyReviewed bool, workerRun, coordinatorRun string, requiresReview bool) string {
	reviewRow := `{"schema": 1, "id": "e-rev", "kind": "review", "source": "reviewer", "at": "2026-01-01T02:00:00+00:00",
"candidate": "cccccccccccccccccccccccccccccccccccccccc", "verdict": "approve", "text": "ok", "policy_reviewed": false}`
	if review && policyReviewed {
		reviewRow = `{"schema": 1, "id": "e-rev", "kind": "review", "source": "reviewer", "at": "2026-01-01T02:00:00+00:00",
"candidate": "cccccccccccccccccccccccccccccccccccccccc", "verdict": "approve", "text": "ok", "policy_reviewed": true}`
	}
	if !review {
		reviewRow = `{"schema": 1, "id": "e-comment", "kind": "review", "source": "reviewer", "at": "2026-01-01T02:00:00+00:00",
"candidate": "cccccccccccccccccccccccccccccccccccccccc", "verdict": "comment", "text": "looking"}`
	}
	worker := ""
	if workerRun != "" {
		worker = `{"schema": 1, "id": "e-w", "kind": "verification", "source": "worker", "at": "2026-01-01T02:30:00+00:00",
"candidate": "cccccccccccccccccccccccccccccccccccccccc", "result": "pass", "run_id": "` + workerRun + `"},`
	}
	return `{"schema": 1, "id": "t-aaaaaaaaaaaa", "brief": "do the thing",
"base_sha": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "repository": "/tmp/project", "kind": "ship",
"branch": "sum/t-aaaaaaaaaaaa", "status": "reported",
"verification_policy": {"status": "standardized"},
"report": {"text": "done", "candidate": "cccccccccccccccccccccccccccccccccccccccc"},
"evidence": [
` + reviewRow + `,
` + worker + `
{"schema": 1, "id": "e-3", "kind": "verification", "source": "coordinator", "at": "2026-01-01T03:00:00+00:00",
"candidate": "cccccccccccccccccccccccccccccccccccccccc", "result": "pass", "run_id": "` + coordinatorRun + `",
"certifies": "cccccccccccccccccccccccccccccccccccccccc", "requires_root_review": ` + boolJSON(requiresReview) + `},
{"schema": 1, "id": "e-4", "kind": "documentation", "source": "coordinator", "at": "2026-01-01T04:00:00+00:00",
"candidate": "cccccccccccccccccccccccccccccccccccccccc", "result": "pass", "summary": "Passed"},
{"schema": 1, "id": "e-5", "kind": "rebase", "source": "coordinator", "at": "2026-01-01T05:00:00+00:00",
"candidate": "cccccccccccccccccccccccccccccccccccccccc", "outcome": "up-to-date", "base_branch": "main",
"behind": 0, "conflicts": [], "summary": "Up to date with main"},
{"schema": 1, "id": "e-6", "kind": "lint", "source": "coordinator", "at": "2026-01-01T06:00:00+00:00",
"candidate": "cccccccccccccccccccccccccccccccccccccccc", "outcome": "pass", "command": "mise run lint", "exit": 0,
"summary": "Passed (` + "`mise run lint`" + `)"}
]}`
}

func boolJSON(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func TestRequirePush_refusesWhenReviewIsMissing(t *testing.T) {
	task := taskFrom(t, readyTaskJSON(false, false, "20260906T010203Z-aaaa", "20260906T010203Z-bbbb", false))
	err := RequirePush(task, PushAdmission{})
	if err == nil || !strings.Contains(err.Error(), "Review") {
		t.Fatalf("err = %v, want a Review refusal", err)
	}
}

func TestRequirePush_passesWhenEveryPrePublicationGatePassed(t *testing.T) {
	task := taskFrom(t, readyTaskJSON(true, false, "20260906T010203Z-aaaa", "20260906T010203Z-bbbb", false))
	if err := RequirePush(task, PushAdmission{}); err != nil {
		t.Fatalf("RequirePush = %v, want nil", err)
	}
}

func TestRequirePush_refusesCopiedWorkerRunID(t *testing.T) {
	task := taskFrom(t, readyTaskJSON(true, false, "20260906T010203Z-same", "20260906T010203Z-same", false))
	err := RequirePush(task, PushAdmission{})
	if err == nil || !strings.Contains(err.Error(), "reused") {
		t.Fatalf("err = %v, want a reused run id refusal", err)
	}
}

func TestRequirePush_requiresPolicyReviewedWhenTheTestAskedForIt(t *testing.T) {
	task := taskFrom(t, readyTaskJSON(true, false, "20260906T010203Z-aaaa", "20260906T010203Z-bbbb", true))
	err := RequirePush(task, PushAdmission{})
	if err == nil || !strings.Contains(err.Error(), "policy-reviewed") {
		t.Fatalf("err = %v, want a policy-reviewed refusal", err)
	}
	ok := taskFrom(t, readyTaskJSON(true, true, "20260906T010203Z-aaaa", "20260906T010203Z-bbbb", true))
	if err := RequirePush(ok, PushAdmission{}); err != nil {
		t.Fatalf("RequirePush with policy-reviewed = %v, want nil", err)
	}
}

func TestRequirePush_allowBehindDoesNotWaiveReview(t *testing.T) {
	task := taskFrom(t, readyTaskJSON(false, false, "20260906T010203Z-aaaa", "20260906T010203Z-bbbb", false))
	err := RequirePush(task, PushAdmission{AllowBehind: true})
	if err == nil || !strings.Contains(err.Error(), "Review") {
		t.Fatalf("err = %v, want Review still required", err)
	}
}

func TestRequirePR_refusesUntilPushPassed(t *testing.T) {
	task := taskFrom(t, readyTaskJSON(true, false, "20260906T010203Z-aaaa", "20260906T010203Z-bbbb", false))
	err := RequirePR(task, PushAdmission{})
	if err == nil || !strings.Contains(err.Error(), "Push gate") {
		t.Fatalf("err = %v, want a Push refusal before PR", err)
	}
}

func TestRequirePush_allowBehindDoesNotWaiveAConflict(t *testing.T) {
	raw := strings.Replace(readyTaskJSON(true, false, "20260906T010203Z-aaaa", "20260906T010203Z-bbbb", false),
		`"outcome": "up-to-date", "base_branch": "main",
"behind": 0, "conflicts": [], "summary": "Up to date with main"}`,
		`"outcome": "conflict", "base_branch": "main",
"behind": 1, "conflicts": ["app.txt"], "summary": "Conflicts with main in: app.txt"}`, 1)
	err := RequirePush(taskFrom(t, raw), PushAdmission{AllowBehind: true})
	if err == nil || !strings.Contains(err.Error(), "does not waive a conflict") {
		t.Fatalf("err = %v, want a conflict --allow-behind cannot waive", err)
	}
}
