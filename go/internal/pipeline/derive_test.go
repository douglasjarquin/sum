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

func TestDerive_verificationReviewAndPRRenderTheExpectedTable(t *testing.T) {
	task := taskFrom(t, `{"schema": 1, "id": "t-aaaaaaaaaaaa", "brief": "do the thing",
"base_sha": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "repository": "/tmp/project", "kind": "ship",
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
"candidate": "cccccccccccccccccccccccccccccccccccccccc", "result": "pass", "summary": "Passed"}]}`)

	record := Derive(task)

	want := "| Stage | Status | Result |\n" +
		"|---|:---:|---|\n" +
		"| Intent | ✅ | Approved brief recorded |\n" +
		"| Rebase | ⏳ | Not run in this release |\n" +
		"| Review | ✅ | Passed after 1 remediation pass |\n" +
		"| Test | ✅ | Passed (`mise run verify`, run 20260906T010203Z-abcd) |\n" +
		"| Document | ✅ | Passed |\n" +
		"| Lint | ⏳ | Not run in this release |\n" +
		"| Push | ⏳ | Not run in this release |\n" +
		"| PR | ✅ | Open: https://github.com/douglasjarquin/project/pull/7 |\n" +
		"| CI | ⏳ | Not run in this release |\n"
	if got := record.Table(); got != want {
		t.Fatalf("table =\n%s\nwant\n%s", got, want)
	}
	if got := record.Candidate; got != candidateSHA {
		t.Fatalf("candidate = %s, want %s", got, candidateSHA)
	}
	if got := record.Get(StageTest).Evidence; len(got) != 1 || got[0] != "e-3" {
		t.Fatalf("test row evidence = %v, want [e-3]", got)
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
