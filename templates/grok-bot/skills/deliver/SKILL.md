---
name: deliver
description: Prepare a reviewable PR from a verified candidate. Use after coordinator verification, never to merge.
---

# Deliver

## When to use it

The worker reported a candidate and your distinct verification run is recorded.
Use this to open or update a PR.
Do not use it to merge.

## Required inputs and access

The task brief, report, and `/workspace/sum/tasks/<id>/verification.md`.
GitHub access for the named repository.
The user merges.

## Sequence of work

1. Confirm verification.md is your run for this candidate, not the worker's claim.
2. Arrange an independent review in a fresh context when the stakes warrant it.
   The worker Bot cannot review its own candidate.
   If independent review did not happen, say so.
3. Push the recorded branch without force.
4. Open a PR or adopt the one GitHub already has for that branch.
   Do not open a new PR when the only pull requests for the branch are closed, unless the user says to.
5. Show the user the full PR URL and that the merge decision is still theirs.
6. After the user says it merged, clean up only the worker Bot and files you can prove are finished.
   Leave unfinished work in place.

## How to validate the result

The PR head matches the verified candidate.
verification.md still names that candidate.
The Bot did not merge.

## What to return

The PR URL, what you verified, and that the user still decides the merge.

## What requires approval

Merge.
Force-push.
Deleting unfinished work.
Waiving a failing required check.
