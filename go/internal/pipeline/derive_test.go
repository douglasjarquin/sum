package pipeline

import (
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

const candidateSHA = "cccccccccccccccccccccccccccccccccccccccc"

func taskFrom(t *testing.T, raw string) *ordjson.Object {
	t.Helper()
	value, err := ordjson.Decode([]byte(raw))
	if err != nil {
		t.Fatalf("decode task fixture: %v", err)
	}
	task, isObject := value.(*ordjson.Object)
	if !isObject {
		t.Fatal("task fixture is not an object")
	}
	return task
}

func TestDerive_taskWithNothingRecordedIsPendingExceptTheMissingBrief(t *testing.T) {
	record := Derive(taskFrom(t, `{"schema": 1, "id": "t-aaaaaaaaaaaa", "evidence": []}`))

	for _, row := range record.Rows {
		want := Pending
		if row.Stage == StageIntent {
			want = Fail
		}
		if row.Status != want {
			t.Fatalf("stage %s = %s, want %s", row.Stage, row.Status, want)
		}
	}
}

func TestDerive_noBriefFailsIntent(t *testing.T) {
	record := Derive(taskFrom(t, `{"schema": 1, "id": "t-aaaaaaaaaaaa", "brief": "", "evidence": []}`))

	row := record.Get(StageIntent)
	if row.Status != Fail || row.Result != "No approved brief" {
		t.Fatalf("intent row = %+v", row)
	}
}

func TestDerive_everyGateThatRanRendersTheExpectedTable(t *testing.T) {
	task := taskFrom(t, `{"schema": 1, "id": "t-aaaaaaaaaaaa", "brief": "do the thing",
"base_sha": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "repository": "/tmp/project", "kind": "ship",
"branch": "sum/t-aaaaaaaaaaaa", "status": "reported",
"repairs": {"schema": 1, "default_allowance": 2, "consumed": 0, "operations": [], "grants": []},
"report": {"text": "done", "candidate": "cccccccccccccccccccccccccccccccccccccccc"},
"pr": {"identity": {"number": 7, "url": "https://github.com/douglasjarquin/project/pull/7"}, "state": "open", "findings": [],
"observed_at": "2026-01-02T00:00:00+00:00"},
"evidence": [
{"schema": 1, "id": "e-1", "kind": "review", "source": "reviewer", "at": "2026-01-01T01:00:00+00:00",
"candidate": "dddddddddddddddddddddddddddddddddddddddd", "verdict": "changes-requested", "text": "the counter is off by one"},
{"schema": 1, "id": "e-2", "kind": "review", "source": "reviewer", "at": "2026-01-01T02:00:00+00:00",
"candidate": "cccccccccccccccccccccccccccccccccccccccc", "verdict": "approve", "text": "fixed; approved"},
{"schema": 1, "id": "e-3", "kind": "verification", "source": "coordinator", "at": "2026-01-01T03:00:00+00:00",
"candidate": "cccccccccccccccccccccccccccccccccccccccc", "result": "pass", "run_id": "20260906T010203Z-abcd",
"certifies": "cccccccccccccccccccccccccccccccccccccccc", "requires_root_review": false},
{"schema": 1, "id": "e-4", "kind": "documentation", "source": "coordinator", "at": "2026-01-01T04:00:00+00:00",
"candidate": "cccccccccccccccccccccccccccccccccccccccc", "result": "pass", "summary": "Passed"},
{"schema": 1, "id": "e-5", "kind": "rebase", "source": "coordinator", "at": "2026-01-01T05:00:00+00:00",
"candidate": "cccccccccccccccccccccccccccccccccccccccc", "outcome": "up-to-date", "base_branch": "main",
"behind": 0, "conflicts": [], "summary": "Up to date with main"},
{"schema": 1, "id": "e-6", "kind": "lint", "source": "coordinator", "at": "2026-01-01T06:00:00+00:00",
"candidate": "cccccccccccccccccccccccccccccccccccccccc", "outcome": "pass", "command": "mise run lint", "exit": 0,
"summary": "Passed (\u0060mise run lint\u0060)"},
{"schema": 1, "id": "e-7", "kind": "push", "source": "coordinator", "at": "2026-01-01T07:00:00+00:00",
"candidate": "cccccccccccccccccccccccccccccccccccccccc", "outcome": "pushed", "branch": "sum/t-aaaaaaaaaaaa",
"remote_sha": "cccccccccccccccccccccccccccccccccccccccc", "summary": "Pushed ccccccc to origin/sum/t-aaaaaaaaaaaa"}]}`)

	record := Derive(task)

	want := "| Stage | Status | Result |\n" +
		"|---|:---:|---|\n" +
		"| Intent | \u2705 | Approved brief recorded |\n" +
		"| Rebase | \u2705 | Up to date with main |\n" +
		"| Review | \u2705 | Passed after 1 remediation pass |\n" +
		"| Test | \u2705 | Passed (`mise run verify`, run 20260906T010203Z-abcd) |\n" +
		"| Document | \u2705 | Passed |\n" +
		"| Lint | \u2705 | Passed (`mise run lint`) |\n" +
		"| Push | \u2705 | Pushed ccccccc to origin/sum/t-aaaaaaaaaaaa |\n" +
		"| PR | \u2705 | Open: https://github.com/douglasjarquin/project/pull/7 |\n" +
		"| CI | \u23f3 | Not observed; `pr reconcile` or `pipeline ci` reads the checks |\n"
	if got := record.Table(); got != want {
		t.Fatalf("table =\n%s\nwant\n%s", got, want)
	}
	if got := record.Candidate; got != candidateSHA {
		t.Fatalf("candidate = %s, want %s", got, candidateSHA)
	}
	if got := record.Get(StageTest).Evidence; len(got) != 1 || got[0] != "e-3" {
		t.Fatalf("test row evidence = %v, want [e-3]", got)
	}
	if got := Next(record); got != "CI is pending: Not observed; `pr reconcile` or `pipeline ci` reads the checks. Do: run `sumctl pipeline ci TASK_ID` to read the checks again; sum observes them, it never watches them." {
		t.Fatalf("next = %q, want the CI gate", got)
	}
	if got := FirstUnsettled(record); got != "ci-pending" {
		t.Fatalf("first unsettled = %q, want ci-pending", got)
	}
}

func TestDerive_gatesThatHaveNotRunSayWhichCommandObservesThem(t *testing.T) {
	record := Derive(taskFrom(t, `{"schema": 1, "id": "t-aaaaaaaaaaaa", "brief": "b",
"report": {"candidate": "cccccccccccccccccccccccccccccccccccccccc"}, "evidence": []}`))

	for stage, want := range map[Stage]string{
		StageRebase: "Not observed; run `pipeline rebase`",
		StageLint:   "Not observed; run `pipeline lint`",
		StagePush:   "Not observed; run `pipeline push`",
	} {
		row := record.Get(stage)
		if row.Status != Pending || row.Result != want {
			t.Fatalf("%s row = %+v, want pending with %q", stage, row, want)
		}
	}
}

func TestDerive_aReconciledPRHeadProvesTheCandidateReachedOrigin(t *testing.T) {
	record := Derive(taskFrom(t, `{"schema": 1, "id": "t-aaaaaaaaaaaa", "brief": "b",
"report": {"candidate": "cccccccccccccccccccccccccccccccccccccccc"},
"pr": {"identity": {"number": 7, "url": "https://github.com/o/r/pull/7",
"head_sha": "cccccccccccccccccccccccccccccccccccccccc"}, "state": "open", "findings": [],
"observed_at": "2026-01-02T00:00:00+00:00"}, "evidence": []}`))

	row := record.Get(StagePush)
	if row.Status != Pass || row.Result != "On origin: the reconciled PR head is this candidate" {
		t.Fatalf("push row = %+v, want a pass derived from the reconciled PR", row)
	}
}

func TestDerive_reviewOnlyCommentsStaysPendingAndRepairsCountAsRemediation(t *testing.T) {
	task := taskFrom(t, `{"schema": 1, "id": "t-aaaaaaaaaaaa", "brief": "b",
"report": {"candidate": "cccccccccccccccccccccccccccccccccccccccc"},
"repairs": {"schema": 1, "default_allowance": 2, "consumed": 2, "operations": [], "grants": []},
"evidence": [{"schema": 1, "id": "e-1", "kind": "review", "source": "reviewer", "at": "2026-01-01T01:00:00+00:00",
"candidate": "cccccccccccccccccccccccccccccccccccccccc", "verdict": "comment", "text": "a thought"}]}`)

	row := Derive(task).Get(StageReview)
	if row.Status != Pending || row.Result != "Comments only; no verdict on this candidate" {
		t.Fatalf("review row = %+v", row)
	}
	if got := RemediationPasses(task); got != 2 {
		t.Fatalf("remediation passes = %d, want 2 from the consumed repairs", got)
	}
}

func TestDerive_blockedVerificationAndFindingOnThePR(t *testing.T) {
	task := taskFrom(t, `{"schema": 1, "id": "t-aaaaaaaaaaaa", "brief": "b",
"report": {"candidate": "cccccccccccccccccccccccccccccccccccccccc"},
"pr": {"identity": {"number": 7, "url": "https://github.com/douglasjarquin/project/pull/7"}, "state": "open",
"findings": ["PR head aaaa is not the recorded candidate cccc"]},
"evidence": [{"schema": 1, "id": "e-1", "kind": "verification", "source": "coordinator", "at": "2026-01-01T01:00:00+00:00",
"candidate": "cccccccccccccccccccccccccccccccccccccccc", "result": "inconclusive", "run_id": "20260906T010203Z-abcd",
"blocked_reason": "the checkout was dirty", "certifies": null, "requires_root_review": true}]}`)

	record := Derive(task)

	test := record.Get(StageTest)
	wantTest := "Blocked (`mise run verify`, run 20260906T010203Z-abcd): the checkout was dirty; the run certifies no candidate, requires root review"
	if test.Status != Blocked || test.Result != wantTest {
		t.Fatalf("test row = %+v\nwant result %q", test, wantTest)
	}
	pr := record.Get(StagePR)
	if pr.Status != Blocked || pr.Result != "PR head aaaa is not the recorded candidate cccc" {
		t.Fatalf("pr row = %+v", pr)
	}
}

func TestDerive_mergedAndClosedPRRows(t *testing.T) {
	merged := Derive(taskFrom(t, `{"schema": 1, "id": "t-a", "brief": "b",
"pr": {"identity": {"number": 7, "url": "https://github.com/o/r/pull/7"}, "state": "merged", "findings": []}}`)).Get(StagePR)
	if merged.Status != Pass || merged.Result != "Merged" {
		t.Fatalf("merged pr row = %+v", merged)
	}
	closed := Derive(taskFrom(t, `{"schema": 1, "id": "t-a", "brief": "b",
"pr": {"identity": {"number": 7, "url": "https://github.com/o/r/pull/7"}, "state": "closed", "findings": []}}`)).Get(StagePR)
	if closed.Status != Fail {
		t.Fatalf("closed pr row = %+v", closed)
	}
}

func TestTable_foldsLineBreaksAndEscapesPipes(t *testing.T) {
	record := New("t-a", "")
	record.Set(Row{Stage: StageIntent, Status: Pass, Result: "one | two\nthree"})

	want := "| Intent | ✅ | one \\| two three |"
	if got := record.Table(); !strings.Contains(got, want) {
		t.Fatalf("table =\n%s\nwant a row %q", got, want)
	}
}
