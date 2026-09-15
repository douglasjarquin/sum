package pipeline

import "testing"

const bodyTask = `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "reported", "branch": "sum/t-aaaaaaaaaaaa",
"brief": "Publish the delivery pipeline table into the PR\n\nThe coordinator owns publication, so the table belongs in a marked block of the PR body.\n\nA second paragraph nobody asked for.",
"report": {"candidate": "cccccccccccccccccccccccccccccccccccccccc"},
"evidence": [
{"schema": 1, "id": "e-1", "kind": "handoff", "source": "worker", "at": "2026-01-01T00:00:00+00:00", "candidate": "cccccccccccccccccccccccccccccccccccccccc",
 "handoff": {"changes": ["Added the publisher", "Wired it into reconcile"], "limitations": "The block is never published into a closed PR."}},
{"schema": 1, "id": "e-2", "kind": "verification", "source": "coordinator", "at": "2026-01-01T01:00:00+00:00", "candidate": "cccccccccccccccccccccccccccccccccccccccc",
 "result": "pass", "run_id": "20260906T010203Z-abcd", "certifies": "cccccccccccccccccccccccccccccccccccccccc", "requires_root_review": false,
 "not_exercised": ["pipeline.pr-body"]},
{"schema": 1, "id": "e-3", "kind": "lint", "source": "coordinator", "at": "2026-01-01T02:00:00+00:00", "candidate": "cccccccccccccccccccccccccccccccccccccccc",
 "outcome": "pass", "summary": "Passed (` + "`mise run lint`" + `)"},
{"schema": 1, "id": "e-4", "kind": "documentation", "source": "coordinator", "at": "2026-01-01T03:00:00+00:00", "candidate": "cccccccccccccccccccccccccccccccccccccccc",
 "result": "pass", "summary": "Audited: no findings"}]}`

const wantBody = `## Summary

Publish the delivery pipeline table into the PR

## Changes

- Added the publisher
- Wired it into reconcile

## Verification

- Test: Passed (` + "`mise run verify`" + `, run 20260906T010203Z-abcd)
- Not exercised: pipeline.pr-body
- Lint: Passed (` + "`mise run lint`" + `)
- Document: Audited: no findings

## Limitations

The block is never published into a closed PR.
`

func TestRenderBody_writesTheApprovedIntentTheChangesAndWhatTheGatesRecorded(t *testing.T) {
	got := RenderBody(taskFrom(t, bodyTask))

	if got != wantBody {
		t.Fatalf("RenderBody =\n%q\nwant\n%q", got, wantBody)
	}
}

func TestRenderBody_omitsSectionsTheWorkerLeftEmpty(t *testing.T) {
	got := RenderBody(taskFrom(t, `{"schema": 1, "id": "t-aaaaaaaaaaaa", "brief": "Do the thing", "evidence": []}`))

	want := `## Summary

Do the thing

## Verification

- Test: No coordinator verification for this candidate
- Lint: Not observed; run ` + "`pipeline lint`" + `
- Document: No documentation audit for this candidate
`
	if got != want {
		t.Fatalf("RenderBody =\n%q\nwant\n%q", got, want)
	}
}

func TestTitle_isTheBriefsFirstLineAndFallsBackToTheBranch(t *testing.T) {
	if got := Title(taskFrom(t, bodyTask)); got != "Publish the delivery pipeline table into the PR" {
		t.Fatalf("Title = %q", got)
	}
	unnamed := taskFrom(t, `{"schema": 1, "id": "t-aaaaaaaaaaaa", "brief": "", "branch": "sum/t-aaaaaaaaaaaa", "evidence": []}`)
	if got := Title(unnamed); got != "sum/t-aaaaaaaaaaaa" {
		t.Fatalf("Title without a brief = %q, want the branch", got)
	}
}
