---
name: sum-finish
description: Resolve required results against the final candidate and deliver only within the user's authority.
---
# Finish

Recover the approved role before finishing.
Maintainers follow [Develop](../sum-develop/SKILL.md) and apply the shared candidate, review, evidence, check-accounting, and delivery requirements below with current supported tools.
They may perform fixes/checks and monitor CI directly or use supported independent helpers; no native HC dispatch or report destination is required merely to finish maintenance.
Explicit bounded helpers retain their assignment and immediate parent.

Read the task note, approved goal, target repository requirements, and actual final diff.
Keep worker claims, independently inspected findings, your own executed checks, and hosted CI observations distinct.
The optional [engineering rubric](../../.agents/skills/verify/references/engineering-principles.md) supports review; it does not replace project rules.

In the native operational workflow, the lead accounts for reports and delivery state; workers perform fixes, substantive review, evidence capture, checks, and CI monitoring.
If any of that work remains, brief the appropriate worker under [Work](../sum-work/SKILL.md) and return to messages.
Invoking a finishing or shipping skill does not transfer operational worker work into the lead session or reclassify a maintainer as an operator.

## Candidate lifecycle

A candidate is the exact inputs under review and check.
Record its state in the task note.

| State | Entry evidence | Legal next move |
| --- | --- | --- |
| `editing` | A writer is changing the inputs | Freeze: record what identifies the snapshot; a commit SHA alone does not identify dirty or untracked content |
| `frozen` | The snapshot identity is recorded and the inputs are held stable | Collect the missing marks |
| `complete` | All three marks hold for this identity | Report completion; the user decides merge |
| `superseded` | The inputs or the published head differ from the identity, or a blocking finding requires a change | Start a new candidate: a fresh worker makes the change and a fresh reviewer inspects it, each given the saved reports and the intervening changes; earlier marks do not carry over |

A frozen candidate collects three marks in any order.

| Mark | Holds when |
| --- | --- |
| `checked` | Every prescribed check has an actual passing result for this identity |
| `reviewed` | A separate reviewer's saved report under [Review](../sum-review/SKILL.md) covers this identity with no blocking finding outstanding |
| `published` | The authorized destination carries this identity and was read back |

Claim completion only for a `complete` candidate:

- Account for every required helper report, question/actual answer/application, check command, exit status, and review finding.
- Require actual results for the repository's required checks and configured CI as applicable; native operator leads collect worker results, while maintainers use their supported execution route.
  Do not require a second full local run merely because of another role.
- Record failures, cancellations, missing results, limitations, and unrun paths truthfully.

A required result that failed, is missing, or was cancelled withholds its mark unless the user explicitly changes that requirement.

## Deliver and clean up

Perform only delivery the user authorized: commit, push, PR creation, publication, merge, and deployment are separate actions.
Preserve unrelated WIP and stage only owned paths.
Only the user merges unless they explicitly granted merge authority for this task.
For a requested PR, use the [PR template](../../templates/pull-request.md), preserving any additional target-repository requirements.
Keep every prescribed check and scenario, including setup, readiness, and teardown, accounted for with its source, status, evidence, and limitations in the verification report or task artifacts.
The PR body contains a compact Pipeline table with Stage, Status, and Result columns: one row per category, never per test or execution instance.
Summarize each category using the template's status rules, retain non-pass categories with short reasons, and link to detailed results when published instead of copying the inventory into the PR.
Label aggregate-derived results as aggregate coverage, never independently observed per-step passes.
Include the reviewed candidate, independent review outcome, findings and their disposition, before/after evidence, and actual CI state.
Publish comparable before/after evidence for changed behavior using the [Evidence procedure](../../.agents/skills/evidence/SKILL.md) or equivalent target tools.
Visual changes need screenshots; CLI/API changes need transcripts or request/response evidence, with provenance and safe redaction.
Use an explicit reason for unavailable baselines or not-applicable visual evidence, never an invented before result.
A local artifact path is not a published artifact; retain local originals and expose the evidence on the PR.
The authorized delivery role publishes; capture and review do not confer write authority.
Read back the PR body and current head after publication; verify the table, review summary, and accessible evidence match the final candidate.
An authorized draft PR may hold `published` before the other marks; it stays a draft and claims no completion.
Never claim local/static proof establishes authenticated native acceptance or production adoption.

For native HC resources, the immediate parent closes each assignment that is [`collected` or `cancelled`](../sum-work/SKILL.md#assignment-lifecycle) and records `closed` in the task note.
Before ending a parent-linked terminal, account for required child work and preserve its results.
Reinspect exact task-owned terminal identities/programs using installed HC help; stop no foreign/shared terminal or core.
Use `hc kill <exact-owned-address>`, confirm the terminal finished, then `hc rm <exact-owned-address>` to remove it.
If identity or cleanup is uncertain, preserve the evidence and reconcile actual state under [Resume](../sum-resume/SKILL.md); do not blindly retry.
Cancellation does not satisfy a required check.
An agent returning to a shell may leave its terminal and children alive.
Retain unfinished branches/worktrees and unresolved evidence; never force-reset or delete them to make the task look clean.
Maintainers clean up only their proven-owned resources through the current environment's supported tools; native HC commands apply only to actual HC terminals.
Use ordinary Git cleanup only after delivery/preservation requirements and user authority are satisfied.

Update the task note with final candidate, results, unresolved decisions, and next action.
Report what changed, how it was checked, what shipped, and what remains; no unattended completion is promised after this conversation ends.
