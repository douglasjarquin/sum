---
name: Sum Verify
description: Run a distinct coordinator verification of a worker result. Use when a worker reports a candidate or findings, before treating that report as done.
---

# Verify

## When to use it

A worker wrote the brief's recorded report_destination (default `/workspace/sum/tasks/<id>/report.md`).
Use this before delivery and before telling the user the task is done.
Idle is not done.

## Required inputs and access

The task brief, the worker-owned report at its recorded report_destination, and the actual diff or findings.
The task's acceptance criteria and evidence appropriate to its kind.
For code, the repository's required commands and a frozen checkout separate from the worker's still-writing session.
Identify the exact commit plus hashes/snapshot of any relevant dirty/untracked inputs; preferably commit and freeze all delivery inputs.
For non-code, identify the actual artifact version, primary sources, or external object/receipt.
Do not call a Cursor Cloud Agent.

## Sequence of work

1. Read the brief, the report, and the outstanding questions.
2. Inspect the actual stable candidate or findings and choose verification by task kind:
   - Code: confirm base/branch/diff and frozen inputs; run every required repository check yourself against that candidate.
   - Research/audit: independently inspect important claims and primary sources; record citations and unsupported claims.
   - Document/presentation: open/render the artifact and inspect the requested content, layout, and links.
   - Approved external action: read back the actual resulting object/receipt when supported.
   - Operational inspection: independently check the requested state and retain observations.
3. This is a distinct coordinator run, not a delegated verifier or reading the worker's logs.
   Reading worker logs is not that run.
   Do not call a Cursor Cloud Agent.
   Unavailable required access or evidence means inconclusive, never pass.
   Inspect action-log receipts for duplicate effects or unresolved intents; status-row counts are not duplicate actions.
4. Write `/workspace/sum/tasks/<id>/verification.md` with exact candidate/artifact identity, actual commands/observations, results, and limits.
5. On failure, send only a bounded in-scope repair to the same worker against the task id; never ask a worker to add or reword code comments, other than a directive the code cannot work without, and never ask for test changes absent explicit task authorization.
   Stop after two unsuccessful internal repair iterations and record the blocker.
   Out-of-scope work always requires the actual user's decision before dispatch; no expansion allowance exists.
   Freeze a changed candidate and repeat affected verification and independent review for that new identity.

## How to validate the result

`verification.md` names commands you ran, not commands the worker claimed.
The candidate in that file matches the report's candidate.
A screenshot or passing suite is not a substitute for the run the project itself defines.

## What to return

Pass, fail, or inconclusive, with the verification path.
A fail or inconclusive result stays visible.
Do not relabel it as done.

## What requires approval

Missing required evidence remains inconclusive; waiving a failing required check is reserved to the user and cannot be represented as pass.
Do not invent that decision from the worker's report.
