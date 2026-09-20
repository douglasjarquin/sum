---
name: adversarial-review
description: Independent review of a verified software candidate in a fresh context. Use after Verify, before opening or updating the PR.
---

# adversarial-review

## When to use it

Software Deliver after Verify is about to open or update a PR.
The default is a fresh adversarial-review, or an equivalent independent reviewer, in a context that did not write the candidate.
The worker Bot cannot review its own candidate.

## Required inputs and access

The task brief, `/workspace/sum/tasks/<id>/report.md`, and `/workspace/sum/tasks/<id>/verification.md`.
A reviewer Bot or fresh context that is not the worker and not the coordinator doing the delivery.
The candidate diff.

## Sequence of work

1. Confirm verification.md is the coordinator's run for this candidate.
2. Hand the brief, report, verification, and diff to a fresh independent reviewer.
3. Record what that reviewer found under the task.
4. If this review is skipped, say so.
5. Do not merge.

## How to validate the result

The reviewer is not the worker who produced the candidate.
The review happened before the PR was opened or updated, or the skip was said out loud.

## What to return

The independent review, or an explicit statement that adversarial-review was skipped.
The user still merges.

## What requires approval

Skipping adversarial-review.
Merge.
Waiving a finding that would change the candidate.
