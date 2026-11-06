---
name: Sum Deliver
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

1. Confirm verification.md is the coordinator's distinct run for the exact candidate/artifact.
   Stop if cancelled, required checks failed, or verification is inconclusive.
   Use canonical `action-log.md` operation/attempt rules before any approved push, native review message, or PR action.
2. For code, obtain a fresh independent review through a separate native reviewer Bot with no authorship of this candidate.
   The worker Bot cannot review its own candidate.
   Default to available Compound Engineering review skills when compatible with native Grok; no terminal helper is required.
   Record the reviewer native identity, task id, exact base/candidate/snapshot, required checks and results, scope, and saved report path before sending the review brief.
   Require a substantive saved review report and explicit task/candidate-correlated native return to Sum; idle or successful send is not review evidence.
   Use a new operation for a new candidate; reconcile uncertain delivery before retry.
   Treat any added code comment or edit to an existing code comment in the candidate diff as a blocking finding. This covers line and block comments, docblocks and doc comments, and comment text in fixtures and configuration files. Do not flag comments left unchanged while surrounding code changes. Commit messages, PR descriptions and report text are not code comments. Lines or comments required for a tool or format to work are allowed, including shebangs, repository-required license headers, build tags, lint-disable pragmas, and directives such as `// @ts-expect-error`; a directive may be added only when the code cannot work without it and must include no explanatory prose.
   Treat any added or expanded test, and any deleted, rewritten, skipped or disabled existing test, in the candidate diff as a blocking finding, unless the user explicitly asked for that test in this task. Do not flag a candidate because existing tests still run.
   If review could not run, say so if skipped and mark delivery incomplete; do not substitute self-review or green CI.
   Save each finding and disposition against the reviewed candidate.
   A change after review requires a new frozen identity, affected coordinator checks, and follow-up independent native review before delivery.
3. Push only the recorded approved branch without force and confirm the remote head.
4. Open or update the PR for that branch; inspect existing PR state first.
   Do not open a new PR when the branch only has closed PRs without the user's instruction.
   Confirm PR head equals the verified and reviewed candidate.
   Report actual CI state separately from local results; do not merge.
5. For non-code work, independently verify the actual artifact/findings through Sum Verify, then provide the substantive result and accessible artifact/source links.
   Do not fabricate a repository, branch, or PR for research, documents, or operations.
6. Every final response includes the outcome, substantive findings/changed behavior, verification and limitations, relevant artifact/PR links, and remaining decision.
   Repeat the essentials even when previously given in progress messages.
7. After accepted completion, return persistent/pre-existing workers, including Square, to idle.
   Do not delete Bots as task cleanup.
   Preserve referenced evidence and unfinished work; exact cleanup authority is defined by permissions.md.

## How to validate the result

For code, the PR head matches the independently reviewed and verified candidate.
verification.md still names that candidate.
The Bot did not merge.

## What to return

The substantive result, verification/limits, relevant artifact or PR links, and remaining decisions.
The user still decides the merge.

## What requires approval

Apply permissions.md: merges, force-push, and waiving required checks remain user-only.
Cleanup requires exact-path authority and may never destroy unfinished work or the only evidence copy.
