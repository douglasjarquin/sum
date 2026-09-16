---
name: verify
description: Run a distinct coordinator verification of a worker result. Use when a worker reports a candidate or findings, before treating that report as done.
---

# Verify

## When to use it

A worker wrote `/workspace/sum/tasks/<id>/report.md`.
Use this before delivery and before telling the user the task is done.
Idle is not done.

## Required inputs and access

The task brief, the report, and the actual diff or findings.
The repository's own verification commands.
A checkout or Cursor Cloud Agent that is not the worker's still-writing session.

## Sequence of work

1. Read the brief, the report, and the outstanding questions.
2. Confirm the checkout, branch, candidate, and actual diff.
3. Run the repository's own checks yourself against that candidate.
   This is a distinct run from the worker's.
   Reading the worker's logs is not that run.
4. Write `/workspace/sum/tasks/<id>/verification.md` with the commands, exit results, and what you actually saw.
5. If checks fail, send a bounded repair to the same worker against the task id.
   Stop after two coordinator-controlled repairs and bring the budget question to the user.

## How to validate the result

`verification.md` names commands you ran, not commands the worker claimed.
The candidate in that file matches the report's candidate.
A screenshot or passing suite is not a substitute for the run the project itself defines.

## What to return

Pass, fail, or inconclusive, with the verification path.
A fail or inconclusive result stays visible.
Do not relabel it as done.

## What requires approval

Waiving missing evidence or a failing required check needs the user's explicit decision.
Do not invent that decision from the worker's report.
