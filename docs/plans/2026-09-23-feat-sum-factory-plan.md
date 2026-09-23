---
title: Sum software factory - Plan
type: feat
date: 2026-09-23
artifact_contract: ce-unified-plan/v1
product_contract_source: worker-brief-t-320d0fe38221
execution: code
origin: task t-320d0fe38221
---

# Sum software factory - Plan

## Goal Capsule

- **Objective:** Sum can run a per-project software factory: pick the next ready GitHub issue, claim it, dispatch one worker that uses `/lfg` plus mandatory before/after evidence, run the existing delivery pipeline, and either merge at high confidence or leave a human gate.
- **Means:** Four `sum-factory*` skills plus a synchronous `sumctl factory` helper that records lane state and does the GitHub query without an LLM.
- **Authority:** The approved task brief, the standing factory merge authorization in `.sum/preferences.md`, and the NiceBaaS / ILoveThatPhoto intake files as constraints on any eventual run.
- **Stop:** Do not start a live NiceBaaS lane from this checkout.
  Do not merge, open a PR, touch live `.sum/` state, raise capacity, or close issue #124.
- **Execution profile:** Skills, helper, tests, and maps on this task branch.
  The user runs the factory against NiceBaaS after this lands.
- **Who finishes:** This worker lands the branch.
  The coordinator verifies.
  The user decides whether to run it.

## Product Contract

### Summary

A factory is one sequential lane per enrolled GitHub project (limit overwritable).
The coordinator still routes and never implements.
Workers still get their own task checkouts.
There is still no sum daemon.

### Problem Frame

Sum today is one finished task at a time.
The user wants unattended sequential issue throughput for enrolled projects, with a claim that other agents can see, `/lfg` implementation, the sum pipeline table plus red/green evidence, and merge only when confidence is mechanically high.

### Key Decisions

- The idle loop is `sumctl factory tick`, a one-shot helper, never a coordinator sleep and never a background process.
  (session-settled: chosen over a dedicated long-lived lane pane that implements, and over coordinator polling: workers must not dispatch, and the coordinator contract forbids poll loops.)
  Governs R6, R7.
- Claim is GitHub-visible: label `sum-claimed` plus a `<!-- sum-factory-claim -->` comment that names hostname, Herdr pane, and task id.
  (session-settled: the user's hostname + pane suggestion, made checkable.)
  Governs R3.
- Ready signal is configured per factory: `label`, `roadmap`, or `project-status`.
  NiceBaaS default is `roadmap` against issue #124 because the repo has no `ready` / `in-progress` labels and this token cannot read GitHub Projects (`read:project` missing).
  Governs R2.
- High-confidence merge is a checkable `factory merge-check` result, not a vibe.
  Only `douglasjarquin/remainder`, `cofactorworks/nicebaas`, and `cofactorworks/ilovethatphoto` may merge.
  Governs R8, R9.
- Evidence is a factory hard gate: a map row that names a screenshot, screencast, or red/green pair blocks merge the same way it blocks the Test gate.
  Governs R5.

### Requirements

- R1. User says "run the factory for owner/repo".
  Coordinator enrolls if needed, then `factory enable`.
- R2. Tick lists the next ready issue in sequential order using the configured ready signal, skipping claimed, gated, skipped, and owner-gated issues.
- R3. Claim writes the label and claim comment, occupies one lane, and records host + pane + optional task id.
- R4. Coordinator dispatches one worker with `sum-factory-work` in the brief.
  The worker uses `/lfg` through an open PR and captures evidence with `.agents/skills/evidence`.
- R5. Coordinator runs independent verify, review, and `pipeline run`.
  The PR carries the pipeline table and the before/after block.
- R6. When no issue is ready, tick records `next_tick_at` (default +5 minutes) and exits.
  It never sleeps.
- R7. The 5-minute wake is external: launchd, cron, or a harness scheduler calling `sumctl factory tick`.
  Worker report / hook idle-done is the event-driven wake.
  The coordinator reads tick output on the next inbox pass and dispatches only when `action` is `dispatch`.
- R8. `factory merge-check` is high-confidence only when: authorized repo, coordinator verify pass on the exact candidate, independent review approve, all nine pipeline gates pass including CI as last read, PR head equals candidate, required evidence comparisons exist with a passing verdict, and CI repair attempts have not exhausted three failures.
- R9. Failed evidence, failed/blocked verification, or CI still red after three repairs leaves a human gate (`sum-gated`) and does not merge.
  The lane frees when the PR is merged, or when the issue is gated and not required for later work (`--continue`).
  `--strict-cleanup` (NiceBaaS / photo intake) keeps the lane occupied until cleanup completes.
- R10. Default lane limit is 1 per project and is overwritable.
  Many projects may each have a factory.
- R11. Skills live at `skills/sum-factory*` with the same projections as the other sum skills.

### Scope Boundaries

In scope: `skills/sum-factory`, `skills/sum-factory-claim`, `skills/sum-factory-work`, `skills/sum-factory-merge`, `sumctl factory`, tests, feature map, dictionary, and coordinator pointers.

Out of scope: starting the NiceBaaS lane, raising capacity, GitHub Projects OAuth changes, a sum daemon, nested coordinators, merging this branch, changing deploy/DNS/credential boundaries.

### Acceptance Examples

- AE1. Covers R6, R7.
  Given an enabled factory and no ready issues, `factory tick` exits `action: idle` with `next_tick_at` five minutes later and starts no agent.
- AE2. Covers R2, R3, R10.
  Given one free lane and a ready unclaimed issue, tick returns `action: dispatch`, claim occupies the lane, and a second tick returns `action: occupied`.
- AE3. Covers R8, R9.
  Given a PR on an unauthorized repo, or missing evidence, merge-check is `human-gate` and `factory merge` refuses.

## Planning Contract

### Technical Design

Durable state is `.sum/factory.json` (schema 1), installation-owned, written only when a factory command runs.
This experiment does not write the live installation file.

`sumctl factory tick` uses the pinned `gh` through `SUM_GH_BIN` / `toolpath.Find`, the same as pipeline CI.
It is read-mostly: it writes `last_tick_at` / `next_tick_at` only.
Cron can run it without a coordinator pane.
`enable`, `claim`, `release`, and `merge` require the coordinator pane.

Ready kinds:

| kind | Sequential source |
| --- | --- |
| `label` | Open issues with the configured label, lowest number first |
| `roadmap` | Issue numbers in order from a parent issue body table (`#N`), first still open |
| `project-status` | GitHub Projects v2 Status option; if `gh` lacks `read:project`, tick reports `blocked` and names `gh auth refresh -s read:project` |

Claim convention (one home, also in `sum-factory-claim`):

```
<!-- sum-factory-claim -->
host: <os hostname>
pane: <HERDR_PANE_ID>
task: <task id or none>
at: <RFC 3339 UTC>
```

Label `sum-claimed` is the cross-agent lock.
Label `sum-gated` marks a human gate.

Factory workers load `sum-factory-work` in addition to `sum-worker`.
They do not dispatch, do not merge, and do not upload evidence.
`/lfg` may open the PR; `pipeline run` adopts it.

Merge authorization is a closed list matching `.sum/preferences.md`.
No flag widens it.

### NiceBaaS first run (user, not this branch)

Intake `#115` / PR `#131` is already closed/merged on GitHub.
Live roadmap `#124` (closed body, keep the issue open per intake) now starts at `#137`.
Dozens of new design-parity issues are open and must not be treated as ready unless the user chooses the `label` signal.
Recommended enable:

```
sumctl factory enable cofactorworks/nicebaas \
  --ready roadmap --roadmap-issue 124 \
  --skip 120 --skip 143 --skip 124 --skip 127 \
  --strict-cleanup --lanes 1
```

First lane after that enable is `#137`, not `#115`.
Confirm before running.

### Per-harness idle tick

Do not leave an agent in `sleep 300`.
Call `sumctl factory tick` from outside the model:

| Harness | Token-cheap wake |
| --- | --- |
| Grok | External scheduler or launchd calling the helper |
| Claude | Stop hook / external cron, not a bash loop in the coordinator |
| Cursor | Same helper from a host cron |
| Devin | Same helper; do not keep a cloud session sleeping |

If tick says `idle`, the model is not started.

### Files

- `skills/sum-factory/SKILL.md` - coordinator start, tick, dispatch, stop
- `skills/sum-factory-claim/SKILL.md` - ready signal and claim
- `skills/sum-factory-work/SKILL.md` - worker `/lfg` + evidence
- `skills/sum-factory-merge/SKILL.md` - merge-check, merge, human gate, lane free
- `go/internal/factory/` - state and GitHub helpers
- `go/internal/cli/factory.go` - command tree
- `docs/features/factory.md` - map
- projections under `.agents/skills` and `.claude/skills`
