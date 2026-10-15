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
   Before push or opening a pull request, append `intended` to `/workspace/sum/action-log.md`, then append `done` or `failed` after.
   Do not send, publish, or merge.
2. Default to a fresh independent review with the Compound Engineering review skills before opening or updating the PR.
   The worker Bot cannot review its own candidate.
   Treat any added code comment or edit to an existing code comment in the candidate diff as a blocking finding. This covers line and block comments, docblocks and doc comments, and comment text in fixtures and configuration files. Do not flag comments left unchanged while surrounding code changes. Commit messages, PR descriptions and report text are not code comments. Lines or comments required for a tool or format to work are allowed, including shebangs, repository-required license headers, build tags, lint-disable pragmas, and directives such as `// @ts-expect-error`; a directive may be added only when the code cannot work without it and must include no explanatory prose.
   Treat any added or expanded test, and any deleted, rewritten, skipped or disabled existing test, in the candidate diff as a blocking finding, unless the user explicitly asked for that test in this task. Do not flag a candidate because existing tests still run.
   If that review did not happen, say so if skipped.
   Before `reviewer launch`, append `intended` to `/workspace/sum/action-log.md` with the task and candidate SHA as the target, then append `done` or `failed` after; the same candidate inside six hours is `skipped-duplicate` unless the user asked again.
   When sum launches the reviewer, use the helper's `reviewer launch TASK_ID`: it creates a `reviewer` tab in the task workspace at the task checkout, records that pane as the reviewer endpoint, and prompts it once to review the current candidate (with `--policy-reviewed` guidance when the candidate changed verification policy). Never split the worker pane for a reviewer. Explicit `--harness/--model/--reasoning/--preset` flags win, then a saved `reviewer.preset` supplies its harness and argv, otherwise the coordinator's harness is used. Run it again for a new candidate: a live recorded reviewer pane is prompted instead of a second tab, never duplicated; a reviewer proven gone (recorded closed, `pane_not_found`, or `agent_not_found` with no occupant) gets a fresh `reviewer` tab, a rebind recorded with the `bind --reviewer-pane` stop proof, and the kickoff prompt (`action: relaunched`); a gone worker workspace is refused with `herdr worktree open --path CHECKOUT --no-focus`.
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
